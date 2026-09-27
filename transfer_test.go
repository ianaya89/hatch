package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func tarOf(t *testing.T, entries ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		body := []byte("pwned")
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(body)
		}
	}
	tw.Close()
	return &buf
}

func newTestExtractor(t *testing.T, dir string) *extractor {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return &extractor{root: root}
}

func TestExtractRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	ex := newTestExtractor(t, dir)
	for _, name := range []string{"../evil", "/etc/evil", "a/../../evil"} {
		err := ex.extract(tarOf(t, &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}), false)
		if err == nil {
			t.Errorf("%q: expected error", name)
		}
	}
}

func TestExtractSymlinkCannotEscape(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	ex := newTestExtractor(t, home)
	ex.extract(tarOf(t,
		&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: outside},
		&tar.Header{Name: "link/pwned", Typeflag: tar.TypeReg, Mode: 0o644},
	), false)
	if _, err := os.Stat(filepath.Join(outside, "pwned")); err == nil {
		t.Fatal("wrote through a symlink outside the root")
	}
}

func TestExtractKeepsExisting(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "f"), []byte("mine"), 0o644)
	ex := newTestExtractor(t, home)
	if err := ex.extract(tarOf(t, &tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}), false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "f")); string(b) != "mine" || ex.stats.skipped != 1 {
		t.Fatalf("existing file replaced (%q, skipped=%d)", b, ex.stats.skipped)
	}
	ex.overwrite = true
	ex.extract(tarOf(t, &tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644}), false)
	if b, _ := os.ReadFile(filepath.Join(home, "f")); string(b) != "pwned" {
		t.Fatalf("overwrite ignored: %q", b)
	}
}

func TestExtractTruncatedFileLeavesNothing(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "big.bin", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1000})
	tw.Write([]byte("only a little"))
	ex := newTestExtractor(t, home)
	if err := ex.extract(bytes.NewReader(buf.Bytes()), false); err == nil {
		t.Fatal("truncated stream extracted without error")
	}
	for _, name := range []string{"big.bin", "big.bin" + partSuffix} {
		if _, err := os.Stat(filepath.Join(home, name)); err == nil {
			t.Fatalf("%s left behind after a cut transfer", name)
		}
	}
}
