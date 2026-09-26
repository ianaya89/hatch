package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type scanner struct {
	cfg           Config
	home          string
	exclude       patterns
	claimed       map[string]bool
	managed       map[string]bool
	chezmoiSource string
	large         int64
	progress      func(string)
	sources       []*source
	mu            sync.Mutex
	warnings      []string
}

func scanInventory(cfg Config, home string, progress func(string)) *inventory {
	if progress == nil {
		progress = func(string) {}
	}
	s := &scanner{
		cfg:      cfg,
		home:     home,
		exclude:  compilePatterns(cfg.Exclude),
		claimed:  map[string]bool{},
		managed:  map[string]bool{},
		large:    cfg.LargeItemMB << 20,
		progress: progress,
	}
	if cfg.Chezmoi {
		s.loadChezmoi()
	}
	progress("secrets")
	s.scanList(KindSecrets, cfg.Secrets, compilePatterns(cfg.SecretIgnore))
	progress("agents")
	s.scanAgents()
	progress("state")
	s.scanList(KindConfig, cfg.State, nil)
	progress("config")
	s.scanConfigDir()
	s.scanHomeDotfiles()
	progress("folders")
	s.scanList(KindPaths, cfg.Paths, nil)
	progress("repos")
	s.scanRepos()
	if cfg.Brew {
		progress("brew")
		s.scanBrew()
	}
	if cfg.Packages {
		progress("packages")
		s.scanPackages()
	}
	inv := newInventory(home, s.sources, s.warnings)
	inv.hints = s.hints()
	return inv
}

func (s *scanner) warn(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings = append(s.warnings, fmt.Sprintf(format, args...))
}

func (s *scanner) rel(abs string) string {
	r, err := filepath.Rel(s.home, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

func (s *scanner) insideHome(abs string) bool {
	r := s.rel(abs)
	return r != "." && r != ".." && !strings.HasPrefix(r, "../") && !filepath.IsAbs(r)
}

func (s *scanner) claimedOrUnder(abs string) bool {
	for p := abs; strings.HasPrefix(p, s.home) && p != s.home; p = filepath.Dir(p) {
		if s.claimed[p] {
			return true
		}
	}
	return false
}

func (s *scanner) loadChezmoi() {
	if _, err := exec.LookPath("chezmoi"); err != nil {
		return
	}
	out, err := exec.Command("chezmoi", "managed", "--path-style", "absolute", "--include", "files,symlinks").Output()
	if err != nil {
		return
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if p := strings.TrimSpace(sc.Text()); p != "" {
			s.managed[p] = true
		}
	}
}

func (s *scanner) add(src *source) {
	if src.Clone == nil {
		src.Size, src.Files = 0, 0
		for _, f := range src.files {
			if !f.dir {
				src.Size += f.size
				src.Files++
			}
		}
		if src.Files == 0 {
			return
		}
	}
	src.Default = true
	if s.large > 0 && src.Size > s.large {
		src.Default = false
		src.Warn = "large, off by default"
	}
	if src.ID == "" {
		src.ID = string(src.Kind) + ":" + src.Path
	}
	s.sources = append(s.sources, src)
}

func (s *scanner) addPath(kind Kind, abs string, skip patterns) {
	if !s.insideHome(abs) {
		s.warn("%s is outside home, skipped", abs)
		return
	}
	if s.claimedOrUnder(abs) {
		return
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return
	}
	s.claimed[abs] = true
	src := &source{Item: Item{Kind: kind, Path: s.rel(abs)}, files: s.walk(abs, skip)}
	s.add(src)
	if fi.IsDir() {
		src.Detail = plural(src.Files, "file")
	}
}

func (s *scanner) scanList(kind Kind, paths []string, ignore patterns) {
	skip := append(append(patterns{}, s.exclude...), ignore...)
	for _, p := range paths {
		s.addPath(kind, expandPath(s.home, p), skip)
	}
}

func (s *scanner) scanAgents() {
	skip := append(append(patterns{}, s.exclude...), compilePatterns(s.cfg.AgentIgnore)...)
	for _, g := range s.cfg.Agents {
		matches, _ := filepath.Glob(filepath.Join(s.home, g))
		sort.Strings(matches)
		for _, m := range matches {
			s.addPath(KindAgents, m, skip)
		}
	}
}

func (s *scanner) scanConfigDir() {
	dir := filepath.Join(s.home, ".config")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if s.exclude.match(e.Name()) {
			continue
		}
		s.addPath(KindConfig, filepath.Join(dir, e.Name()), s.exclude)
	}
}

func (s *scanner) scanHomeDotfiles() {
	entries, err := os.ReadDir(s.home)
	if err != nil {
		return
	}
	junk := compilePatterns(s.cfg.DotfileJunk)
	src := &source{Item: Item{ID: "config:~dotfiles", Kind: KindConfig, Label: "home dotfiles"}}
	var names []string
	for _, e := range entries {
		name := e.Name()
		abs := filepath.Join(s.home, name)
		t := e.Type()
		if !strings.HasPrefix(name, ".") || junk.match(name) || s.claimed[abs] || s.managed[abs] {
			continue
		}
		if !t.IsRegular() && t&fs.ModeSymlink == 0 {
			continue
		}
		fe := fileEntry{rel: name, abs: abs}
		if info, err := e.Info(); err == nil && t.IsRegular() {
			fe.size = info.Size()
		}
		src.files = append(src.files, fe)
		names = append(names, name)
	}
	src.Detail = listPreview(names, 4)
	s.add(src)
}

// walk lists everything under root except skipped names, claimed subtrees
// (they belong to another item) and chezmoi-managed files (chezmoi restores them).
func (s *scanner) walk(root string, skip patterns) []fileEntry {
	var out []fileEntry
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p != root {
				s.warn("%s: %v", s.rel(p), err)
			}
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if p != root {
			r, _ := filepath.Rel(root, p)
			if skip.match(filepath.ToSlash(r)) || s.claimed[p] {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		t := d.Type()
		switch {
		case t.IsDir():
			out = append(out, fileEntry{rel: s.rel(p), abs: p, dir: true})
		case s.managed[p]:
		case t.IsRegular():
			fe := fileEntry{rel: s.rel(p), abs: p}
			if info, err := d.Info(); err == nil {
				fe.size = info.Size()
			}
			out = append(out, fe)
		case t&fs.ModeSymlink != 0:
			out = append(out, fileEntry{rel: s.rel(p), abs: p})
		}
		return nil
	})
	return out
}

func (s *scanner) scanBrew() {
	if _, err := exec.LookPath("brew"); err != nil {
		return
	}
	cmd := exec.Command("brew", "bundle", "dump", "--file=-")
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		s.warn("brew bundle dump failed: %v", err)
		return
	}
	counts := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 0 {
			counts[f[0]]++
		}
	}
	var parts []string
	for _, k := range []string{"brew", "cask", "mas", "tap", "vscode"} {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	rel := ".local/share/hatch/Brewfile"
	s.add(&source{
		Item:  Item{ID: "brew", Kind: KindBrew, Label: "Brewfile", Path: rel, Detail: strings.Join(parts, " · ")},
		files: []fileEntry{{rel: rel, data: out, size: int64(len(out))}},
	})
}
