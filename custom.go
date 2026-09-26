package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type child struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Files int    `json:"files"`
	Dir   bool   `json:"dir,omitempty"`
}

type childrenReply struct {
	Children []child `json:"children"`
	Size     int64   `json:"size"`
	Files    int     `json:"files"`
}

type addReply struct {
	Items  []Item   `json:"items"`
	Errors []string `json:"errors,omitempty"`
}

// itemRel is a file's path relative to its item root, the frame of
// reference for exclude patterns and for the children listing.
func (src *source) itemRel(rel string) string {
	if src.Path == "" || src.Path == "." {
		return rel
	}
	if r, ok := strings.CutPrefix(rel, src.Path+"/"); ok {
		return r
	}
	return rel
}

func (src *source) filtered(ex patterns) []fileEntry {
	if len(ex) == 0 {
		return src.files
	}
	out := make([]fileEntry, 0, len(src.files))
	for _, f := range src.files {
		if !ex.matchSelfOrParent(src.itemRel(f.rel)) {
			out = append(out, f)
		}
	}
	return out
}

func (src *source) children(ex patterns) childrenReply {
	agg := map[string]*child{}
	var rep childrenReply
	for _, f := range src.filtered(ex) {
		if f.dir {
			continue
		}
		name, rest, nested := strings.Cut(src.itemRel(f.rel), "/")
		c := agg[name]
		if c == nil {
			c = &child{Name: name}
			agg[name] = c
		}
		c.Size += f.size
		c.Files++
		c.Dir = c.Dir || (nested && rest != "")
		rep.Size += f.size
		rep.Files++
	}
	for _, c := range agg {
		rep.Children = append(rep.Children, *c)
	}
	sort.Slice(rep.Children, func(i, j int) bool {
		if rep.Children[i].Size != rep.Children[j].Size {
			return rep.Children[i].Size > rep.Children[j].Size
		}
		return rep.Children[i].Name < rep.Children[j].Name
	})
	return rep
}

func hasGlob(p string) bool { return strings.ContainsAny(p, "*?[") }

// addCustom resolves a path or glob typed on the pulling side. It is confined
// to $HOME and still honours the default excludes and chezmoi-managed files.
func (inv *inventory) addCustom(pattern string) (*source, error) {
	s := inv.scanner
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("empty path")
	}
	abs := expandPath(s.home, pattern)
	if abs != s.home && !s.insideHome(abs) {
		return nil, fmt.Errorf("%s is outside home", pattern)
	}
	id := "custom:" + pattern
	if existing := inv.byID[id]; existing != nil {
		return existing, nil
	}

	var base string
	var files []fileEntry
	if !hasGlob(abs) {
		if _, err := os.Lstat(abs); err != nil {
			return nil, fmt.Errorf("%s: not found", pattern)
		}
		base, files = abs, s.walkCustom(abs, nil)
	} else {
		i := strings.IndexAny(abs, "*?[")
		base = filepath.Dir(abs[:i+1])
		rest, _ := filepath.Rel(base, abs)
		if _, err := os.Stat(base); err != nil {
			return nil, fmt.Errorf("%s: %s not found", pattern, base)
		}
		files = s.walkCustom(base, globToRegexpMatcher(filepath.ToSlash(rest)))
	}
	src := &source{Item: Item{ID: id, Kind: KindCustom, Label: pattern, Path: s.rel(base)}, files: files}
	for _, f := range files {
		if !f.dir {
			src.Size += f.size
			src.Files++
		}
	}
	if src.Files == 0 {
		return nil, fmt.Errorf("%s matched no files", pattern)
	}
	src.Default = true
	src.Detail = plural(src.Files, "file")
	if s.large > 0 && src.Size > s.large {
		src.Warn = "large"
	}
	inv.sources = append(inv.sources, src)
	inv.byID[id] = src
	return src, nil
}

func globToRegexpMatcher(g string) func(string) bool {
	re := globToRegexp(g)
	return re.MatchString
}

// walkCustom ignores claims (the user asked for this path explicitly) but
// keeps the default excludes. With keep set, only matching files are kept.
func (s *scanner) walkCustom(root string, keep func(string) bool) []fileEntry {
	var out []fileEntry
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if p != root && s.exclude.match(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		t := d.Type()
		switch {
		case t.IsDir():
			if keep == nil {
				out = append(out, fileEntry{rel: s.rel(p), abs: p, dir: true})
			}
		case s.managed[p]:
		case keep != nil && !keep(rel):
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
