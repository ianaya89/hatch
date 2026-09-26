package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const stagingDir = ".local/share/hatch/staging"

type session struct {
	sc       *secureConn
	peer     hello
	route    string
	addr     string
	items    []Item
	warnings []string
	hints    []string
}

func connect(ctx context.Context, code, addr string) (*session, error) {
	np, norm, err := parseCode(code)
	if err != nil {
		return nil, err
	}
	var addrs []peerAddr
	if addr != "" {
		route := "manual"
		if h, _, err := net.SplitHostPort(addr); err == nil {
			if ip := net.ParseIP(h); ip != nil {
				route = describeRoute(ip)
			}
		}
		addrs = []peerAddr{{addr: addr, route: route}}
	} else if addrs, err = discover(ctx, np); err != nil {
		return nil, err
	}

	var conn net.Conn
	var chosen peerAddr
	lastErr := errors.New("no address to dial")
	for _, a := range addrs {
		d := net.Dialer{Timeout: 3 * time.Second}
		c, err := d.DialContext(ctx, "tcp", a.addr)
		if err == nil {
			conn, chosen = c, a
			break
		}
		lastErr = err
	}
	if conn == nil {
		return nil, lastErr
	}

	sc, err := clientHandshake(conn, norm, np)
	if err != nil {
		conn.Close()
		return nil, err
	}
	sess := &session{sc: sc, route: chosen.route, addr: chosen.addr}
	if err := sc.sendJSON(hello{Version: protoVersion, Host: hostName(), User: userName(), OS: runtime.GOOS}); err != nil {
		sc.Close()
		return nil, err
	}
	if err := sc.recvJSON(&sess.peer); err != nil {
		sc.Close()
		return nil, err
	}
	if err := sc.sendJSON(request{Op: "inventory"}); err != nil {
		sc.Close()
		return nil, err
	}
	var inv inventoryReply
	if err := sc.recvJSON(&inv); err != nil {
		sc.Close()
		return nil, err
	}
	for _, it := range inv.Items {
		if it.Clone == nil {
			continue
		}
		if _, err := safeName(it.Path); err != nil {
			sc.Close()
			return nil, fmt.Errorf("peer sent %w", err)
		}
	}
	sess.items, sess.warnings, sess.hints = inv.Items, inv.Warnings, inv.Hints
	return sess, nil
}

type pullOptions struct {
	overwrite bool
	update    bool
	compress  string
	jobs      int
	fresh     bool
	excludes  map[string][]string
}

func (s *session) addPaths(paths []string) (addReply, error) {
	var rep addReply
	if err := s.sc.sendJSON(request{Op: "add", Paths: paths}); err != nil {
		return rep, err
	}
	err := s.sc.recvJSON(&rep)
	if err == nil {
		s.items = append(s.items, rep.Items...)
	}
	return rep, err
}

func (s *session) children(id string, exclude []string) (childrenReply, error) {
	var rep childrenReply
	if err := s.sc.sendJSON(request{Op: "children", ID: id, Exclude: exclude}); err != nil {
		return rep, err
	}
	return rep, s.sc.recvJSON(&rep)
}

func (o pullOptions) shouldCompress(route string) bool {
	switch o.compress {
	case "on":
		return true
	case "off":
		return false
	}
	return route != "thunderbolt" && route != "direct link" && route != "loopback"
}

type itemStatus int

const (
	statusPending itemStatus = iota
	statusRunning
	statusDone
	statusFailed
)

type itemState struct {
	status itemStatus
	note   string
	err    error
}

type runState struct {
	mu          sync.Mutex
	state       map[string]*itemState
	log         []string
	phase       string
	current     string
	doneBytes   int64
	totalBytes  int64
	written     int
	skipped     int
	uptodate    int
	cloned      int
	failed      int
	finished    bool
	aborted     error
	staged      bool
	brewfile    string
	packages    string
	hints       []string
	started     time.Time
	finishedAt  time.Time
	byID        map[string]Item
	compressing bool
}

func newRunState(items []Item) *runState {
	st := &runState{state: map[string]*itemState{}, byID: map[string]Item{}, started: time.Now()}
	for _, it := range items {
		st.state[it.ID] = &itemState{}
		st.byID[it.ID] = it
		st.totalBytes += it.Size
	}
	return st
}

func (st *runState) set(id string, status itemStatus, note string, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.state[id]
	s.status, s.note, s.err = status, note, err
	switch status {
	case statusRunning:
		st.current = id
	case statusDone, statusFailed:
		if status == statusFailed {
			st.failed++
		}
		st.log = append(st.log, id)
	}
}

func (st *runState) addBytes(n int64) {
	st.mu.Lock()
	st.doneBytes += n
	st.mu.Unlock()
}

func (st *runState) setPhase(p string) {
	st.mu.Lock()
	st.phase = p
	st.mu.Unlock()
}

func pull(ctx context.Context, sess *session, items []Item, home string, opts pullOptions, st *runState) {
	defer func() {
		st.mu.Lock()
		st.finished = true
		st.finishedAt = time.Now()
		st.current = ""
		st.mu.Unlock()
	}()
	root, err := os.OpenRoot(home)
	if err != nil {
		st.mu.Lock()
		st.aborted = err
		st.mu.Unlock()
		return
	}
	defer root.Close()

	compress := opts.shouldCompress(sess.route)
	st.mu.Lock()
	st.compressing = compress
	st.hints = sess.hints
	st.mu.Unlock()

	var copies, clones []Item
	for _, it := range items {
		if it.Clone != nil {
			clones = append(clones, it)
		} else {
			copies = append(copies, it)
		}
	}

	linkOK := true
	st.setPhase("copy")
	for _, it := range copies {
		if !linkOK || ctx.Err() != nil {
			st.set(it.ID, statusFailed, "", errors.New("connection lost"))
			continue
		}
		st.set(it.ID, statusRunning, "", nil)
		stats, inSync, err := fetch(sess, root, it, "", compress, opts, st.addBytes)
		linkOK = inSync
		st.recordStats(stats)
		if err == nil {
			err = stats.err()
		}
		if err != nil {
			st.set(it.ID, statusFailed, statsNote(stats), err)
			continue
		}
		if it.Kind == KindBrew {
			dest := filepath.Join(home, filepath.FromSlash(it.Path))
			st.mu.Lock()
			if it.ID == "packages" {
				st.packages = dest
			} else {
				st.brewfile = dest
			}
			st.mu.Unlock()
		}
		st.set(it.ID, statusDone, statsNote(stats), nil)
	}

	st.setPhase("clone")
	cloneErr := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(opts.jobs, 1))
	for _, it := range clones {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st.set(it.ID, statusRunning, "cloning", nil)
			note, err := cloneRepo(ctx, home, it)
			mu.Lock()
			cloneErr[it.ID] = err
			mu.Unlock()
			if err == nil && it.Files == 0 {
				st.mu.Lock()
				st.cloned++
				st.mu.Unlock()
				st.set(it.ID, statusDone, note, nil)
			} else if err != nil && it.Files == 0 {
				st.set(it.ID, statusFailed, "", err)
			}
		}()
	}
	wg.Wait()

	st.setPhase("extras")
	for _, it := range clones {
		if it.Files == 0 {
			continue
		}
		cerr := cloneErr[it.ID]
		if !linkOK || ctx.Err() != nil {
			st.set(it.ID, statusFailed, "", errors.Join(cerr, errors.New("env files not fetched: connection lost")))
			continue
		}
		prefix := ""
		if cerr != nil {
			prefix = stagingDir
			st.mu.Lock()
			st.staged = true
			st.mu.Unlock()
		}
		stats, inSync, err := fetch(sess, root, it, prefix, compress, opts, st.addBytes)
		linkOK = inSync
		st.recordStats(stats)
		if err == nil {
			err = stats.err()
		}
		switch {
		case cerr != nil:
			st.set(it.ID, statusFailed, "env files staged", cerr)
		case err != nil:
			st.set(it.ID, statusFailed, "", err)
		default:
			st.mu.Lock()
			st.cloned++
			st.mu.Unlock()
			st.set(it.ID, statusDone, "cloned · "+statsNote(stats), nil)
		}
	}

	if linkOK {
		sess.sc.sendJSON(request{Op: "bye"})
	}
	sess.sc.Close()
}

func (st *runState) recordStats(s extractStats) {
	st.mu.Lock()
	st.written += s.written
	st.skipped += s.skipped
	st.uptodate += s.uptodate
	st.mu.Unlock()
}

func (s extractStats) err() error {
	if s.failed == 0 {
		return nil
	}
	return fmt.Errorf("%d not written, first: %w", s.failed, s.firstErr)
}

func statsNote(s extractStats) string {
	note := plural(s.written, "file")
	if s.uptodate > 0 {
		note += fmt.Sprintf(" · %d up to date", s.uptodate)
	}
	if s.skipped > 0 {
		note += fmt.Sprintf(" · %d existed, kept", s.skipped)
	}
	return note
}

// fetch negotiates a manifest first, so unchanged files never cross the
// wire. It reports inSync=false when the stream broke mid-transfer and the
// connection can't carry further requests.
func fetch(sess *session, root *os.Root, it Item, prefix string, compress bool, opts pullOptions, onBytes func(int64)) (extractStats, bool, error) {
	var stats extractStats
	if err := sess.sc.sendJSON(request{Op: "fetch", ID: it.ID, Compress: compress, Exclude: opts.excludes[it.ID]}); err != nil {
		return stats, false, err
	}
	var rep fetchReply
	if err := sess.sc.recvJSON(&rep); err != nil {
		var pe *peerError
		return stats, errors.As(err, &pe), err
	}

	mr := &dataReader{sc: sess.sc}
	sc := bufio.NewScanner(mr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	want := make([]byte, (rep.Entries+7)/8)
	i := 0
	for sc.Scan() {
		var e manifestEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || i >= rep.Entries {
			return stats, false, fmt.Errorf("bad manifest")
		}
		switch decide(root, prefix, e, opts) {
		case wantIt:
			want[i/8] |= 1 << (i % 8)
		case upToDate:
			stats.uptodate++
			onBytes(e.S)
		case keepLocal:
			stats.skipped++
			onBytes(e.S)
		}
		i++
	}
	if err := sc.Err(); err != nil || !mr.done || i != rep.Entries {
		return stats, false, fmt.Errorf("manifest stream broke: %v", err)
	}
	ww := newDataWriter(sess.sc)
	if _, err := ww.Write(want); err != nil {
		return stats, false, err
	}
	if err := ww.Close(); err != nil {
		return stats, false, err
	}

	dr := &dataReader{sc: sess.sc}
	ex := &extractor{root: root, overwrite: true, prefix: prefix, onBytes: onBytes}
	err := ex.extract(dr, compress)
	if derr := dr.drain(); err == nil {
		err = derr
	}
	ex.stats.uptodate, ex.stats.skipped = stats.uptodate, stats.skipped
	return ex.stats, dr.done, err
}

type decision int

const (
	wantIt decision = iota
	upToDate
	keepLocal
	skipDir
)

// decide compares a manifest entry with what's on disk. Same size and mtime
// means up to date; a different local file is kept unless --overwrite, or
// --update and the peer's copy is newer. Hatch-generated files always refresh.
func decide(root *os.Root, prefix string, e manifestEntry, opts pullOptions) decision {
	rel, err := safeName(e.R)
	if err != nil {
		return skipDir
	}
	if prefix != "" {
		rel = path.Join(prefix, rel)
	}
	fi, err := root.Lstat(filepath.FromSlash(rel))
	if err != nil {
		return wantIt
	}
	switch e.T {
	case "d":
		return skipDir
	case "v":
		return wantIt
	case "l":
		if opts.overwrite {
			return wantIt
		}
		return keepLocal
	}
	if fi.Mode().IsRegular() && fi.Size() == e.S && fi.ModTime().Unix() == e.M {
		return upToDate
	}
	if opts.overwrite || (opts.update && e.M > fi.ModTime().Unix()) {
		return wantIt
	}
	return keepLocal
}

func cloneRepo(ctx context.Context, home string, it Item) (string, error) {
	if !isNetworkRemote(it.Clone.Remote) || strings.HasPrefix(it.Clone.Remote, "-") {
		return "", fmt.Errorf("refusing remote %q", it.Clone.Remote)
	}
	dest := filepath.Join(home, filepath.FromSlash(it.Path))
	if fi, err := os.Lstat(dest); err == nil {
		if !fi.IsDir() {
			return "", fmt.Errorf("%s exists and is not a directory", dest)
		}
		if entries, _ := os.ReadDir(dest); len(entries) > 0 {
			return "already there", nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	args := []string{"clone", "--quiet"}
	if it.Clone.Submodules {
		args = append(args, "--recurse-submodules")
	}
	if it.Clone.Branch != "" {
		args = append(args, "--branch", it.Clone.Branch)
	}
	args = append(args, "--", it.Clone.Remote, dest)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New(lastLine(string(out), err))
	}
	return "cloned", nil
}

func lastLine(out string, fallback error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return strings.TrimPrefix(l, "fatal: ")
		}
	}
	return fallback.Error()
}
