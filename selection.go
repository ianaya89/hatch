package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"time"
)

// savedSelection remembers what the user picked per source machine, so a
// second pull (the final catch-up before wiping the old Mac) starts from the
// same choices and only moves what changed.
type savedSelection struct {
	Saved    time.Time           `json:"saved"`
	Off      []string            `json:"off,omitempty"`
	On       []string            `json:"on,omitempty"`
	Custom   []string            `json:"custom,omitempty"`
	Patterns map[string][]string `json:"patterns,omitempty"`
	Hidden   map[string][]string `json:"hidden,omitempty"`
}

var unsafeHost = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func selectionPath(home, peer string) string {
	return filepath.Join(home, ".local", "share", "hatch", "selections", unsafeHost.ReplaceAllString(peer, "_")+".json")
}

func loadSelection(home, peer string) *savedSelection {
	b, err := os.ReadFile(selectionPath(home, peer))
	if err != nil {
		return nil
	}
	var sel savedSelection
	if json.Unmarshal(b, &sel) != nil {
		return nil
	}
	return &sel
}

func (sel *savedSelection) save(home, peer string) error {
	sel.Saved = time.Now()
	p := selectionPath(home, peer)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sel, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// itemFilter is the per-item exclude state: typed patterns plus children the
// user unticked in the drill-down view.
type itemFilter struct {
	patterns []string
	hidden   map[string]bool
}

func (f itemFilter) excludes() []string {
	out := append([]string{}, f.patterns...)
	names := make([]string, 0, len(f.hidden))
	for n, off := range f.hidden {
		if off {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, "/"+n)
	}
	return out
}

func (f itemFilter) empty() bool { return len(f.excludes()) == 0 }

// size applies the unticked children to a listing already filtered by patterns.
func (f itemFilter) size(rep childrenReply) int64 {
	var n int64
	for _, c := range rep.Children {
		if !f.hidden[c.Name] {
			n += c.Size
		}
	}
	return n
}

func selectionFrom(items []Item, selected map[string]bool, custom []string, filters map[string]itemFilter) *savedSelection {
	sel := &savedSelection{Custom: custom, Patterns: map[string][]string{}, Hidden: map[string][]string{}}
	for _, it := range items {
		on := selected[it.ID]
		switch {
		case it.Default && !on:
			sel.Off = append(sel.Off, it.ID)
		case !it.Default && on:
			sel.On = append(sel.On, it.ID)
		}
	}
	for id, f := range filters {
		if len(f.patterns) > 0 {
			sel.Patterns[id] = f.patterns
		}
		for n, off := range f.hidden {
			if off {
				sel.Hidden[id] = append(sel.Hidden[id], n)
			}
		}
		slices.Sort(sel.Hidden[id])
	}
	return sel
}

func (sel *savedSelection) filters() map[string]itemFilter {
	out := map[string]itemFilter{}
	ids := map[string]bool{}
	for id := range sel.Patterns {
		ids[id] = true
	}
	for id := range sel.Hidden {
		ids[id] = true
	}
	for id := range ids {
		f := itemFilter{patterns: sel.Patterns[id], hidden: map[string]bool{}}
		for _, n := range sel.Hidden[id] {
			f.hidden[n] = true
		}
		out[id] = f
	}
	return out
}

func (sel *savedSelection) apply(it Item) bool {
	if slices.Contains(sel.Off, it.ID) {
		return false
	}
	if slices.Contains(sel.On, it.ID) {
		return true
	}
	return it.Default
}

// restoreSelection re-adds the user's custom paths on the peer and asks for
// filtered sizes, so restored excludes show real numbers in the checklist.
func restoreSelection(sess *session, sel *savedSelection) (map[string]itemFilter, map[string]childrenReply) {
	if sel == nil {
		return map[string]itemFilter{}, map[string]childrenReply{}
	}
	if len(sel.Custom) > 0 {
		sess.addPaths(sel.Custom)
	}
	filters := sel.filters()
	kids := map[string]childrenReply{}
	for id, f := range filters {
		if rep, err := sess.children(id, f.patterns); err == nil {
			kids[id] = rep
		}
	}
	return filters, kids
}
