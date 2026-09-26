package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const smallFile = 8 << 20

type fetchReply struct {
	Files   int   `json:"files"`
	Size    int64 `json:"size"`
	Entries int   `json:"entries"`
}

// manifestEntry lets the puller decide per file before any bytes move, which
// turns a re-run into an incremental sync. T is f(ile), d(ir), l(ink) or
// v(irtual, generated on the fly and always refreshed).
type manifestEntry struct {
	R string `json:"r"`
	S int64  `json:"s,omitempty"`
	M int64  `json:"m,omitempty"`
	T string `json:"t"`
}

func manifestOf(f fileEntry) (manifestEntry, bool) {
	if f.data != nil {
		return manifestEntry{R: f.rel, S: int64(len(f.data)), T: "v"}, true
	}
	fi, err := os.Lstat(f.abs)
	if err != nil {
		return manifestEntry{}, false
	}
	switch {
	case fi.IsDir():
		return manifestEntry{R: f.rel, T: "d"}, true
	case fi.Mode()&fs.ModeSymlink != 0:
		return manifestEntry{R: f.rel, T: "l"}, true
	case fi.Mode().IsRegular():
		return manifestEntry{R: f.rel, S: fi.Size(), M: fi.ModTime().Unix(), T: "f"}, true
	}
	return manifestEntry{}, false
}

func sendSource(sc *secureConn, src *source, req request) (int64, error) {
	var files []fileEntry
	var entries []manifestEntry
	var total int64
	count := 0
	for _, f := range src.filtered(compilePatterns(req.Exclude)) {
		if e, ok := manifestOf(f); ok {
			files = append(files, f)
			entries = append(entries, e)
			if e.T == "f" || e.T == "v" {
				total += e.S
				count++
			}
		}
	}
	if err := sc.sendJSON(fetchReply{Files: count, Size: total, Entries: len(entries)}); err != nil {
		return 0, err
	}
	mw := newDataWriter(sc)
	enc := json.NewEncoder(mw)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return 0, err
		}
	}
	if err := mw.Close(); err != nil {
		return 0, err
	}
	want, err := io.ReadAll(&dataReader{sc: sc})
	if err != nil {
		return 0, err
	}
	if len(want) != (len(entries)+7)/8 {
		return 0, fmt.Errorf("bad want bitmap: %d bytes for %d entries", len(want), len(entries))
	}

	dw := newDataWriter(sc)
	var w io.Writer = dw
	var zw *zstd.Encoder
	if req.Compress {
		zw, err = zstd.NewWriter(dw, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
		if err != nil {
			return 0, err
		}
		w = zw
	}
	tw := tar.NewWriter(w)
	var sent int64
	for i, f := range files {
		if want[i/8]&(1<<(i%8)) == 0 {
			continue
		}
		n, err := writeEntry(tw, f)
		if err != nil {
			sc.sendError(fmt.Errorf("%s: %w", f.rel, err))
			return sent, err
		}
		sent += n
	}
	if err := tw.Close(); err != nil {
		return sent, err
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			return sent, err
		}
	}
	return sent, dw.Close()
}

// writeEntry skips files that vanished or can't be read instead of failing
// the whole item: a live home directory changes while we copy it.
func writeEntry(tw *tar.Writer, f fileEntry) (int64, error) {
	if f.data != nil {
		mode := f.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: f.rel, Mode: mode, Size: int64(len(f.data)), ModTime: time.Now(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			return 0, err
		}
		n, err := tw.Write(f.data)
		return int64(n), err
	}
	fi, err := os.Lstat(f.abs)
	if err != nil {
		return 0, nil
	}
	var link string
	var body io.Reader
	switch {
	case fi.IsDir():
	case fi.Mode()&fs.ModeSymlink != 0:
		if link, err = os.Readlink(f.abs); err != nil {
			return 0, nil
		}
	case fi.Mode().IsRegular():
		fh, err := os.Open(f.abs)
		if err != nil {
			return 0, nil
		}
		defer fh.Close()
		if fi.Size() <= smallFile {
			b, err := io.ReadAll(fh)
			if err != nil {
				return 0, nil
			}
			body = bytes.NewReader(b)
			fi = sizedInfo{fi, int64(len(b))}
		} else {
			body = io.MultiReader(fh, zeroReader{})
		}
	default:
		return 0, nil
	}
	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return 0, nil
	}
	hdr.Name = f.rel
	if fi.IsDir() {
		hdr.Name += "/"
	}
	hdr.Uname, hdr.Gname, hdr.Uid, hdr.Gid = "", "", 0, 0
	hdr.Format = tar.FormatPAX
	if err := tw.WriteHeader(hdr); err != nil {
		return 0, err
	}
	if body == nil {
		return 0, nil
	}
	return io.CopyN(tw, body, hdr.Size)
}

type sizedInfo struct {
	fs.FileInfo
	size int64
}

func (s sizedInfo) Size() int64 { return s.size }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type extractStats struct {
	written  int
	skipped  int
	uptodate int
	failed   int
	firstErr error
	bytes    int64
}

type extractor struct {
	root      *os.Root
	overwrite bool
	prefix    string
	onBytes   func(int64)
	stats     extractStats
}

func safeName(name string) (string, error) {
	clean := path.Clean(strings.TrimSuffix(name, "/"))
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(clean, 0) {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	return clean, nil
}

func (e *extractor) extract(r io.Reader, compressed bool) error {
	if compressed {
		// Async decoding would keep reading the stream after tar hits EOF and race drain().
		zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := safeName(hdr.Name)
		if err != nil {
			return err
		}
		if e.prefix != "" {
			name = path.Join(e.prefix, name)
		}
		if err := e.entry(tr, hdr, filepath.FromSlash(name)); err != nil {
			e.stats.failed++
			if e.stats.firstErr == nil {
				e.stats.firstErr = fmt.Errorf("%s: %w", name, err)
			}
		}
	}
}

func (e *extractor) entry(tr *tar.Reader, hdr *tar.Header, name string) error {
	mode := fs.FileMode(hdr.Mode).Perm()
	_, statErr := e.root.Lstat(name)
	exists := statErr == nil

	switch hdr.Typeflag {
	case tar.TypeDir:
		if exists {
			return nil
		}
		if err := e.root.MkdirAll(name, 0o755); err != nil {
			return err
		}
		return e.root.Chmod(name, mode|0o700)
	case tar.TypeSymlink:
		if exists && !e.overwrite {
			e.stats.skipped++
			return nil
		}
		if err := e.root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if exists {
			if err := e.root.Remove(name); err != nil {
				return err
			}
		}
		e.stats.written++
		return e.root.Symlink(hdr.Linkname, name)
	case tar.TypeReg:
		if exists && !e.overwrite {
			e.stats.skipped++
			e.progress(hdr.Size)
			return nil
		}
		if err := e.root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		f, err := e.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, &countingReader{r: tr, fn: e.progress})
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		e.stats.written++
		if err := e.root.Chmod(name, mode); err != nil {
			return err
		}
		return e.root.Chtimes(name, hdr.ModTime, hdr.ModTime)
	}
	return nil
}

func (e *extractor) progress(n int64) {
	e.stats.bytes += n
	if e.onBytes != nil {
		e.onBytes(n)
	}
}

type countingReader struct {
	r  io.Reader
	fn func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.fn(int64(n))
	}
	return n, err
}
