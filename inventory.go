package main

import (
	"fmt"
	"sort"
	"strings"
)

type Kind string

const (
	KindSecrets Kind = "secrets"
	KindConfig  Kind = "config"
	KindAgents  Kind = "agents"
	KindPaths   Kind = "folders"
	KindRepos   Kind = "repos"
	KindBrew    Kind = "brew"
)

var kindOrder = []Kind{KindSecrets, KindConfig, KindAgents, KindRepos, KindPaths, KindBrew}

var kindLabel = map[Kind]string{
	KindSecrets: "secrets",
	KindConfig:  "config & state",
	KindAgents:  "agents",
	KindRepos:   "repos",
	KindPaths:   "folders",
	KindBrew:    "packages",
}

func kindIndex(k Kind) int {
	for i, o := range kindOrder {
		if o == k {
			return i
		}
	}
	return len(kindOrder)
}

type Item struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	Label   string `json:"label,omitempty"`
	Path    string `json:"path"`
	Detail  string `json:"detail,omitempty"`
	Size    int64  `json:"size"`
	Files   int    `json:"files"`
	Clone   *Clone `json:"clone,omitempty"`
	Default bool   `json:"default"`
	Warn    string `json:"warn,omitempty"`
}

type Clone struct {
	Remote     string `json:"remote"`
	Branch     string `json:"branch"`
	Submodules bool   `json:"submodules,omitempty"`
}

func (it Item) Title() string {
	if it.Label != "" {
		return it.Label
	}
	return "~/" + it.Path
}

type fileEntry struct {
	rel  string
	abs  string
	data []byte
	mode int64
	size int64
	dir  bool
}

type source struct {
	Item
	files []fileEntry
}

type inventory struct {
	home     string
	sources  []*source
	byID     map[string]*source
	warnings []string
	hints    []string
}

func newInventory(home string, sources []*source, warnings []string) *inventory {
	sort.SliceStable(sources, func(i, j int) bool {
		return kindIndex(sources[i].Kind) < kindIndex(sources[j].Kind)
	})
	inv := &inventory{home: home, sources: sources, byID: map[string]*source{}, warnings: warnings}
	for _, s := range sources {
		inv.byID[s.ID] = s
	}
	return inv
}

func (inv *inventory) items() []Item {
	out := make([]Item, len(inv.sources))
	for i, s := range inv.sources {
		out[i] = s.Item
	}
	return out
}

type kindSummary struct {
	kind   Kind
	items  int
	size   int64
	clones int
}

func summarize(items []Item) []kindSummary {
	var out []kindSummary
	for _, k := range kindOrder {
		ks := kindSummary{kind: k}
		for _, it := range items {
			if it.Kind != k {
				continue
			}
			ks.items++
			ks.size += it.Size
			if it.Clone != nil {
				ks.clones++
			}
		}
		if ks.items > 0 {
			out = append(out, ks)
		}
	}
	return out
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func listPreview(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s +%d", strings.Join(names[:max], ", "), len(names)-max)
}
