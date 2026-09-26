package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testInventory(t *testing.T) (*inventory, string) {
	t.Helper()
	home := t.TempDir()
	writeFile(t, home, "Documents/a.pdf", "aa", 0o644)
	writeFile(t, home, "Documents/deep/b.pdf", "bbb", 0o644)
	writeFile(t, home, "Documents/notes.txt", "n", 0o644)
	writeFile(t, home, "Documents/node_modules/c.pdf", "c", 0o644)
	cfg := defaultConfig()
	cfg.Brew, cfg.Chezmoi, cfg.Packages = false, false, false
	cfg.DevRoots, cfg.ExtraRepos, cfg.State = nil, nil, nil
	return scanInventory(cfg, home, nil), home
}

func TestAddCustomGlobAndPath(t *testing.T) {
	inv, _ := testInventory(t)
	src, err := inv.addCustom("~/Documents/**/*.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if src.Files != 2 || src.Size != 5 {
		t.Fatalf("glob: %d files %d bytes (node_modules must stay excluded)", src.Files, src.Size)
	}
	if inv.byID[src.ID] == nil {
		t.Fatal("custom item not registered for fetch")
	}
	dir, err := inv.addCustom("~/Documents")
	if err != nil || dir.Files != 3 {
		t.Fatalf("path: %v files=%d", err, dir.Files)
	}
	for _, bad := range []string{"/etc", "~/../x", "~/missing", "~/Documents/*.zip"} {
		if _, err := inv.addCustom(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestChildrenAndExcludes(t *testing.T) {
	inv, _ := testInventory(t)
	src, _ := inv.addCustom("~/Documents")
	rep := src.children(nil)
	var names []string
	for _, c := range rep.Children {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{"deep", "a.pdf", "notes.txt"}) {
		t.Fatalf("children = %v", names)
	}
	if got := src.children(compilePatterns([]string{"*.txt"})); got.Files != 2 {
		t.Fatalf("pattern exclude: %d files", got.Files)
	}
	f := itemFilter{hidden: map[string]bool{"deep": true}}
	if f.size(rep) != 3 {
		t.Fatalf("hidden child size = %d", f.size(rep))
	}
	if got := len(src.filtered(compilePatterns(f.excludes()))); got >= len(src.files) {
		t.Fatal("hidden child not excluded from the file list")
	}
}

func TestSelectionRoundTrip(t *testing.T) {
	home := t.TempDir()
	items := []Item{{ID: "a", Default: true}, {ID: "b", Default: false}, {ID: "c", Default: true}}
	sel := selectionFrom(items, map[string]bool{"a": false, "b": true, "c": true}, []string{"~/x/**"},
		map[string]itemFilter{"c": {patterns: []string{"*.log"}, hidden: map[string]bool{"cache": true}}})
	if err := sel.save(home, "old mac/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/share/hatch/selections/old_mac_1.json")); err != nil {
		t.Fatal("selection file name not sanitized")
	}
	got := loadSelection(home, "old mac/1")
	if got == nil || got.apply(items[0]) || !got.apply(items[1]) || !got.apply(items[2]) {
		t.Fatalf("apply mismatch: %+v", got)
	}
	if ex := got.filters()["c"].excludes(); !reflect.DeepEqual(ex, []string{"*.log", "/cache"}) {
		t.Fatalf("excludes = %v", ex)
	}
	if time.Since(got.Saved) > time.Minute || !reflect.DeepEqual(got.Custom, []string{"~/x/**"}) {
		t.Fatal("custom/saved not persisted")
	}
}
