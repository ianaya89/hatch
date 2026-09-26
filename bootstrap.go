package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// bootstrap lets a machine without hatch fetch this very binary from the
// serving machine. The sha256 travels in the printed command (screen → QR →
// clipboard), never over the network, so a LAN attacker can't swap the binary.
type bootstrap struct {
	bin      []byte
	sum      string
	host     string
	port     int
	platform string
}

func loadBootstrap(port int) (*bootstrap, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	bin, err := os.ReadFile(exe)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bin)
	return &bootstrap{
		bin:      bin,
		sum:      hex.EncodeToString(sum[:]),
		host:     bootstrapHost(),
		port:     port,
		platform: runtime.GOOS + "/" + runtime.GOARCH,
	}, nil
}

// bootstrapHost prefers the Bonjour name: macOS resolves name.local natively,
// on Wi-Fi and on a Thunderbolt Bridge alike, unlike any single IP we'd pick.
func bootstrapHost() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("scutil", "--get", "LocalHostName").Output(); err == nil {
			if h := strings.TrimSpace(string(out)); h != "" {
				return h + ".local"
			}
		}
	}
	if ips := localIPv4s(); len(ips) > 0 {
		return ips[0]
	}
	h, _ := os.Hostname()
	return h
}

func (b *bootstrap) url() string {
	return "http://" + net.JoinHostPort(b.host, strconv.Itoa(b.port)) + "/hatch"
}

func (b *bootstrap) command(code string) string {
	return fmt.Sprintf(`curl -fsSo /tmp/hatch %s && echo "%s  /tmp/hatch" | shasum -a 256 -c -q && chmod +x /tmp/hatch && /tmp/hatch pull %s`,
		b.url(), b.sum, code)
}

func isHTTPRequest(br *bufio.Reader) bool {
	head, err := br.Peek(4)
	return err == nil && (string(head) == "GET " || string(head) == "HEAD")
}

func (b *bootstrap) serve(conn net.Conn, br *bufio.Reader, logf func(string, ...any)) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Minute))
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Request:    req,
		Close:      true,
		Header:     http.Header{"Content-Type": {"text/plain"}},
	}
	var body []byte
	if req.URL.Path == "/hatch" {
		resp.StatusCode = http.StatusOK
		resp.Header.Set("Content-Type", "application/octet-stream")
		body = b.bin
	} else {
		body = []byte("not found\n")
	}
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if resp.Write(conn) == nil && resp.StatusCode == http.StatusOK && req.Method == http.MethodGet {
		logf("%s sent the hatch binary to %s", styleAccent.Render("↓"), conn.RemoteAddr())
	}
}
