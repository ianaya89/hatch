package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, rel, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func buildFixture(t *testing.T, home string) {
	writeFile(t, home, ".ssh/id_ed25519", "PRIVATE", 0o600)
	os.Chmod(filepath.Join(home, ".ssh"), 0o700)
	writeFile(t, home, ".config/tool/settings.json", `{"a":1}`, 0o644)
	writeFile(t, home, ".config/tool/.cache/big", "cache", 0o644)
	writeFile(t, home, ".claude/settings.json", "{}", 0o644)
	writeFile(t, home, ".claude/projects/p/abc.jsonl", "transcript", 0o644)
	writeFile(t, home, ".claude/projects/p/memory/MEMORY.md", "mem", 0o644)
	writeFile(t, home, ".zsh_history", "ls", 0o600)
	os.Symlink("settings.json", filepath.Join(home, ".config/tool/link.json"))

	wip := filepath.Join(home, "Development/wip")
	writeFile(t, wip, ".gitignore", "node_modules\n.env\n", 0o644)
	writeFile(t, wip, "main.go", "package main", 0o644)
	gitRun(t, home, "init", "-q", "-b", "main", wip)
	gitRun(t, wip, "add", ".")
	gitRun(t, wip, "commit", "-q", "-m", "init")
	writeFile(t, wip, "draft.go", "package main // wip", 0o644)
	writeFile(t, wip, "node_modules/dep/index.js", "x", 0o644)
	writeFile(t, wip, ".env", "SECRET=1", 0o600)

	// Clean repo whose remote can't resolve: it gets a clone item, the clone
	// fails, and its env file must land in staging instead of being lost.
	clean := filepath.Join(home, "Development/clean")
	writeFile(t, clean, ".gitignore", ".env\n", 0o644)
	gitRun(t, home, "init", "-q", "-b", "main", clean)
	gitRun(t, clean, "add", ".")
	gitRun(t, clean, "commit", "-q", "-m", "init")
	gitRun(t, clean, "remote", "add", "origin", "https://hatch.invalid/clean.git")
	gitRun(t, clean, "update-ref", "refs/remotes/origin/main", "HEAD")
	writeFile(t, clean, ".env", "TOKEN=2", 0o600)
}

func TestPullEndToEnd(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	buildFixture(t, src)

	cfg := defaultConfig()
	cfg.Brew, cfg.Chezmoi, cfg.Packages = false, false, false
	cfg.DevRoots, cfg.ExtraRepos = []string{"~/Development"}, nil
	inv := scanInventory(cfg, src, nil)

	byPath := map[string]Item{}
	for _, it := range inv.items() {
		byPath[it.Path] = it
	}
	if it := byPath["Development/clean"]; it.Clone == nil || it.Files != 1 {
		t.Fatalf("clean repo should be a clone item with 1 env file: %+v", it)
	}
	if it := byPath["Development/wip"]; it.Clone != nil {
		t.Fatalf("wip repo should be copied: %+v", it)
	}

	const code = "7-guitar-orbit"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			served <- err
			return
		}
		sc, err := serverHandshake(conn, bufio.NewReader(conn), code, 7)
		if err != nil {
			served <- err
			return
		}
		defer sc.Close()
		served <- serveSession(sc, inv, "src", true, t.Logf)
	}()

	sess, err := connect(context.Background(), code, ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var items []Item
	for _, it := range sess.items {
		if it.Default {
			items = append(items, it)
		}
	}
	if len(sess.sc.visual) != 32 {
		t.Fatalf("session has no visual seed")
	}
	st := newRunState(items)
	pull(context.Background(), sess, items, dst, pullOptions{compress: "on", jobs: 2}, st)
	if err := <-served; err != nil {
		t.Fatalf("server: %v", err)
	}

	mustRead := func(rel, want string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", rel, b, err, want)
		}
	}
	mustMiss := func(rel string) {
		t.Helper()
		if _, err := os.Lstat(filepath.Join(dst, rel)); err == nil {
			t.Errorf("%s should not have been transferred", rel)
		}
	}

	mustRead(".ssh/id_ed25519", "PRIVATE")
	mustRead(".config/tool/settings.json", `{"a":1}`)
	mustRead(".claude/projects/p/memory/MEMORY.md", "mem")
	mustRead(".zsh_history", "ls")
	mustRead("Development/wip/draft.go", "package main // wip")
	mustRead("Development/wip/.env", "SECRET=1")
	mustRead(filepath.Join(stagingDir, "Development/clean/.env"), "TOKEN=2")
	mustMiss(".claude/projects/p/abc.jsonl")
	mustMiss(".config/tool/.cache/big")
	mustMiss("Development/wip/node_modules")

	if fi, err := os.Stat(filepath.Join(dst, ".ssh")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf(".ssh mode = %v, %v", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(filepath.Join(dst, ".ssh/id_ed25519")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, %v", fi.Mode().Perm(), err)
	}
	if target, err := os.Readlink(filepath.Join(dst, ".config/tool/link.json")); err != nil || target != "settings.json" {
		t.Errorf("symlink = %q, %v", target, err)
	}
	if out, err := exec.Command("git", "-C", filepath.Join(dst, "Development/wip"), "log", "--oneline").Output(); err != nil || len(out) == 0 {
		t.Errorf("copied repo history missing: %v", err)
	}
	if st.failed != 1 || !st.staged {
		t.Errorf("want exactly the unreachable clone to fail with staged env, got failed=%d staged=%v", st.failed, st.staged)
	}
}

func pullOnce(t *testing.T, inv *inventory, dst string, opts pullOptions, pick func(Item) bool) *runState {
	t.Helper()
	const code = "9-otter-radar"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			served <- err
			return
		}
		sc, err := serverHandshake(conn, bufio.NewReader(conn), code, 9)
		if err != nil {
			served <- err
			return
		}
		defer sc.Close()
		served <- serveSession(sc, inv, "src", true, t.Logf)
	}()
	sess, err := connect(context.Background(), code, ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var items []Item
	for _, it := range sess.items {
		if pick(it) {
			items = append(items, it)
		}
	}
	st := newRunState(items)
	pull(context.Background(), sess, items, dst, opts, st)
	if err := <-served; err != nil {
		t.Fatalf("server: %v", err)
	}
	return st
}

func TestIncrementalSync(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, src, ".config/tool/a.json", "one", 0o644)
	writeFile(t, src, ".config/tool/b.json", "two", 0o644)
	writeFile(t, src, ".config/tool/debug.log", "noise", 0o644)
	cfg := defaultConfig()
	cfg.Brew, cfg.Chezmoi, cfg.Packages = false, false, false
	cfg.DevRoots, cfg.ExtraRepos, cfg.State = nil, nil, nil
	isTool := func(it Item) bool { return it.Path == ".config/tool" }
	opts := pullOptions{compress: "on", excludes: map[string][]string{"config:.config/tool": {"*.log"}}}

	st := pullOnce(t, scanInventory(cfg, src, nil), dst, opts, isTool)
	if st.written != 2 || st.failed != 0 {
		t.Fatalf("first pull: written=%d failed=%d", st.written, st.failed)
	}
	if _, err := os.Stat(filepath.Join(dst, ".config/tool/debug.log")); err == nil {
		t.Fatal("excluded file was transferred")
	}

	st = pullOnce(t, scanInventory(cfg, src, nil), dst, opts, isTool)
	if st.written != 0 || st.uptodate != 2 {
		t.Fatalf("second pull should move nothing: written=%d uptodate=%d", st.written, st.uptodate)
	}

	writeFile(t, src, ".config/tool/a.json", "one, edited", 0o644)
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(src, ".config/tool/a.json"), later, later)
	st = pullOnce(t, scanInventory(cfg, src, nil), dst, opts, isTool)
	if st.written != 0 || st.skipped != 1 {
		t.Fatalf("changed file must be kept by default: written=%d kept=%d", st.written, st.skipped)
	}
	opts.update = true
	st = pullOnce(t, scanInventory(cfg, src, nil), dst, opts, isTool)
	if st.written != 1 || st.uptodate != 1 {
		t.Fatalf("--update should take the newer copy: written=%d uptodate=%d", st.written, st.uptodate)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, ".config/tool/a.json")); string(b) != "one, edited" {
		t.Fatalf("content = %q", b)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	buildFixture(t, src)
	cfg := defaultConfig()
	cfg.Brew, cfg.Chezmoi, cfg.Packages = false, false, false
	cfg.DevRoots, cfg.ExtraRepos = []string{"~/Development"}, nil
	all := func(it Item) bool { return it.Default }

	st := pullOnce(t, scanInventory(cfg, src, nil), dst, pullOptions{compress: "on", dryRun: true}, all)
	entries, _ := os.ReadDir(dst)
	if len(entries) != 0 {
		t.Fatalf("dry run wrote %d entries into the target", len(entries))
	}
	if st.planned == 0 || st.written != 0 || st.plannedRepo != 1 || st.cloned != 0 {
		t.Fatalf("dry run plan: planned=%d written=%d repos=%d cloned=%d", st.planned, st.written, st.plannedRepo, st.cloned)
	}
	planned := st.planned

	st = pullOnce(t, scanInventory(cfg, src, nil), dst, pullOptions{compress: "on"}, all)
	if st.written != planned {
		t.Fatalf("real pull wrote %d files, dry run promised %d", st.written, planned)
	}
}

func TestAppStoreHintComesBeforeBrewBundle(t *testing.T) {
	dir := t.TempDir()
	bf := filepath.Join(dir, "Brewfile")
	os.WriteFile(bf, []byte("brew \"git\"\nmas \"Xcode\", id: 497799835\n  mas \"Things\", id: 904280696\n"), 0o644)
	st := newRunState(nil)
	st.brewfile, st.masApps, st.finished = bf, countMasApps(bf), true
	if st.masApps != 2 {
		t.Fatalf("countMasApps = %d", st.masApps)
	}
	lines := strings.Join(summaryLines(st, false), "\n")
	store, brew := strings.Index(lines, "App Store"), strings.Index(lines, "brew bundle")
	if store < 0 || brew < 0 || store > brew {
		t.Fatalf("App Store step must precede brew bundle:\n%s", lines)
	}
}
