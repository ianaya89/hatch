package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
		served <- serveSession(sc, inv, "src", t.Logf)
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
