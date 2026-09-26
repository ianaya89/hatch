package main

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type repoInfo struct {
	dir      string
	remote   string
	branch   string
	changed  int
	unpushed int
	stash    bool
	subs     bool
}

func isRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

func (s *scanner) findRepos(dir string, depth int) []string {
	if isRepo(dir) {
		return []string{dir}
	}
	if depth == 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || s.exclude.match(e.Name()) {
			continue
		}
		out = append(out, s.findRepos(filepath.Join(dir, e.Name()), depth-1)...)
	}
	return out
}

func (s *scanner) scanRepos() {
	var repos []string
	extra := append([]string{}, s.cfg.ExtraRepos...)
	if s.cfg.Chezmoi {
		if out, err := exec.Command("chezmoi", "source-path").Output(); err == nil {
			src := strings.TrimSpace(string(out))
			if s.insideHome(src) && isRepo(src) {
				s.chezmoiSource = src
			}
			extra = append(extra, src)
		}
	}
	for _, p := range extra {
		abs := expandPath(s.home, p)
		if s.insideHome(abs) && isRepo(abs) && !s.claimedOrUnder(abs) {
			s.claimed[abs] = true
			repos = append(repos, abs)
		}
	}

	for _, root := range s.cfg.DevRoots {
		root = expandPath(s.home, root)
		entries, err := os.ReadDir(root)
		if err != nil || !s.insideHome(root) {
			continue
		}
		loose := &source{Item: Item{Kind: KindPaths, Path: s.rel(root), Label: "~/" + s.rel(root) + " (loose files)"}}
		loose.ID = "folders:" + loose.Path + ":loose"
		for _, e := range entries {
			abs := filepath.Join(root, e.Name())
			if s.claimedOrUnder(abs) || s.exclude.match(e.Name()) {
				continue
			}
			if e.IsDir() {
				found := s.findRepos(abs, s.cfg.RepoDepth)
				for _, r := range found {
					if !s.claimed[r] {
						s.claimed[r] = true
						repos = append(repos, r)
					}
				}
				if len(found) == 0 {
					s.addPath(KindPaths, abs, s.exclude)
				}
				continue
			}
			if e.Type().IsRegular() {
				fe := fileEntry{rel: s.rel(abs), abs: abs}
				if info, err := e.Info(); err == nil {
					fe.size = info.Size()
				}
				loose.files = append(loose.files, fe)
			}
		}
		s.add(loose)
	}

	srcs := make([]*source, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, dir := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			srcs[i] = s.repoSource(analyzeRepo(dir))
			<-sem
		}()
	}
	wg.Wait()
	for _, src := range srcs {
		s.add(src)
	}
}

func (s *scanner) repoSource(ri repoInfo) *source {
	src := &source{Item: Item{Kind: KindRepos, Path: s.rel(ri.dir)}}
	extras := s.repoExtras(ri.dir)
	if ok, reason := ri.cloneable(); ok {
		src.Clone = &Clone{Remote: ri.remote, Branch: ri.branch, Submodules: ri.subs}
		src.files = extras
		for _, f := range extras {
			src.Size += f.size
			src.Files++
		}
		src.Detail = "clone · " + ri.branch + " · " + shortRemote(ri.remote)
		if len(extras) > 0 {
			src.Detail += " · +" + plural(len(extras), "env file")
		}
	} else {
		src.files = s.repoFiles(ri.dir, extras)
		src.Detail = "copy · " + reason
	}
	return src
}

func (ri repoInfo) cloneable() (bool, string) {
	var why []string
	if ri.changed > 0 {
		why = append(why, strconv.Itoa(ri.changed)+" changed")
	}
	if ri.unpushed > 0 {
		why = append(why, strconv.Itoa(ri.unpushed)+" unpushed")
	}
	if ri.stash {
		why = append(why, "stash")
	}
	switch {
	case ri.remote == "":
		why = append(why, "no remote")
	case !isNetworkRemote(ri.remote):
		why = append(why, "local remote")
	}
	if ri.branch == "" || ri.branch == "(detached)" {
		why = append(why, "detached HEAD")
	}
	if len(why) > 0 {
		return false, strings.Join(why, " · ")
	}
	return true, ""
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func analyzeRepo(dir string) repoInfo {
	ri := repoInfo{dir: dir}
	ri.remote, _ = git(dir, "config", "--get", "remote.origin.url")
	if ri.remote == "" {
		if names, _ := git(dir, "remote"); names != "" {
			first := strings.Fields(names)[0]
			ri.remote, _ = git(dir, "config", "--get", "remote."+first+".url")
		}
	}
	status, err := git(dir, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		ri.changed = -1
	}
	sc := bufio.NewScanner(strings.NewReader(status))
	for sc.Scan() {
		line := sc.Text()
		if b, ok := strings.CutPrefix(line, "# branch.head "); ok {
			ri.branch = b
		} else if line != "" && !strings.HasPrefix(line, "#") {
			ri.changed++
		}
	}
	if ri.remote != "" {
		if n, err := git(dir, "rev-list", "--count", "--branches", "--not", "--remotes"); err == nil {
			ri.unpushed, _ = strconv.Atoi(n)
		}
	}
	_, err = git(dir, "rev-parse", "-q", "--verify", "refs/stash")
	ri.stash = err == nil
	_, err = os.Stat(filepath.Join(dir, ".gitmodules"))
	ri.subs = err == nil
	return ri
}

func isNetworkRemote(r string) bool {
	switch {
	case strings.HasPrefix(r, "file://"):
		return false
	case strings.Contains(r, "://"):
		return true
	case strings.HasPrefix(r, "/"), strings.HasPrefix(r, "."), strings.HasPrefix(r, "~"):
		return false
	default:
		return strings.Contains(r, ":")
	}
}

func shortRemote(r string) string {
	r = strings.TrimSuffix(r, ".git")
	if i := strings.Index(r, "://"); i >= 0 {
		r = r[i+3:]
	} else if at := strings.Index(r, "@"); at >= 0 {
		r = strings.Replace(r[at+1:], ":", "/", 1)
	}
	if at := strings.Index(r, "@"); at >= 0 {
		r = r[at+1:]
	}
	return r
}

// repoFiles copies .git plus whatever git considers part of the worktree
// (tracked + untracked-but-not-ignored), so .gitignore does the pruning.
func (s *scanner) repoFiles(dir string, extras []fileEntry) []fileEntry {
	out := s.walk(filepath.Join(dir, ".git"), nil)
	seen := map[string]bool{}
	for _, f := range out {
		seen[f.abs] = true
	}
	list, err := exec.Command("git", "-C", dir, "ls-files", "-co", "--exclude-standard", "-z").Output()
	if err != nil {
		return append(out, s.walk(dir, s.exclude)...)
	}
	for _, rel := range bytes.Split(list, []byte{0}) {
		if len(rel) == 0 {
			continue
		}
		abs := filepath.Join(dir, string(rel))
		fi, err := os.Lstat(abs)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		switch {
		case fi.IsDir():
			out = append(out, s.walk(abs, s.exclude)...)
		case fi.Mode().IsRegular():
			out = append(out, fileEntry{rel: s.rel(abs), abs: abs, size: fi.Size()})
		case fi.Mode()&fs.ModeSymlink != 0:
			out = append(out, fileEntry{rel: s.rel(abs), abs: abs})
		}
	}
	for _, f := range extras {
		if !seen[f.abs] {
			out = append(out, f)
		}
	}
	return out
}

func (s *scanner) repoExtras(dir string) []fileEntry {
	want := compilePatterns(s.cfg.RepoExtras)
	var out []fileEntry
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && (d.Name() == ".git" || s.exclude.match(d.Name())) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !want.match(d.Name()) {
			return nil
		}
		fe := fileEntry{rel: s.rel(p), abs: p}
		if info, err := d.Info(); err == nil {
			fe.size = info.Size()
		}
		out = append(out, fe)
		return nil
	})
	return out
}
