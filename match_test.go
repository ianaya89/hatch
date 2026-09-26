package main

import "testing"

func TestPatterns(t *testing.T) {
	ps := compilePatterns([]string{"node_modules", "*.pyc", "/logs", "/projects/**/*.jsonl", "/logs_*"})
	cases := map[string]bool{
		"node_modules":              true,
		"a/b/node_modules":          true,
		"x.pyc":                     true,
		"deep/x.pyc":                true,
		"logs":                      true,
		"sub/logs":                  false,
		"logs_2.sqlite":             true,
		"projects/p/abc.jsonl":      true,
		"projects/p/s/sub.jsonl":    true,
		"projects/p/memory/MEM.md":  false,
		"history.jsonl":             false,
		"other/projects/p/x.jsonl":  false,
		"node_modules_not_really/x": false,
	}
	for rel, want := range cases {
		if got := ps.match(rel); got != want {
			t.Errorf("match(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestRemotes(t *testing.T) {
	network := []string{"git@github.com:a/b.git", "https://github.com/a/b", "ssh://git@host/x", "gh-alias:a/b"}
	local := []string{"/srv/x.git", "../x", "~/x", "file:///x"}
	for _, r := range network {
		if !isNetworkRemote(r) {
			t.Errorf("%q should be network", r)
		}
	}
	for _, r := range local {
		if isNetworkRemote(r) {
			t.Errorf("%q should be local", r)
		}
	}
	if got := shortRemote("git@github.com:ianaya89/hatch.git"); got != "github.com/ianaya89/hatch" {
		t.Errorf("shortRemote = %q", got)
	}
	if got := shortRemote("https://user@gitlab.com/a/b.git"); got != "gitlab.com/a/b" {
		t.Errorf("shortRemote = %q", got)
	}
}

func TestDoubleStarMatchesZeroDirs(t *testing.T) {
	re := globToRegexp("**/*.pdf")
	for rel, want := range map[string]bool{"a.pdf": true, "x/a.pdf": true, "x/y/a.pdf": true, "a.txt": false} {
		if re.MatchString(rel) != want {
			t.Errorf("**/*.pdf vs %q: want %v", rel, want)
		}
	}
	ps := compilePatterns([]string{"/cache", "*.log"})
	if !ps.matchSelfOrParent("cache/a/b") || !ps.matchSelfOrParent("x/y.log") || ps.matchSelfOrParent("src/cache.go") {
		t.Error("matchSelfOrParent")
	}
}
