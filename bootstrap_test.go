package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestBootstrapServesBinary(t *testing.T) {
	bin := []byte("fake-binary")
	sum := sha256.Sum256(bin)
	boot := &bootstrap{bin: bin, sum: hex.EncodeToString(sum[:]), host: "mac.local", port: 7788, platform: "darwin/arm64"}

	c, s := net.Pipe()
	go func() {
		br := bufio.NewReader(s)
		if !isHTTPRequest(br) {
			s.Close()
			return
		}
		boot.serve(s, br, t.Logf)
	}()
	c.Write([]byte("GET /h HTTP/1.1\r\nHost: mac.local\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "fake-binary" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}

	cmd := boot.command("42-tiger-mango")
	for _, want := range []string{"curl -so h mac.local:7788/h", "grep ^" + boot.sum[:bootstrapHashLen] + "&&", "./h pull 42-tiger-mango"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
}

func TestHatchClientIsNotHTTP(t *testing.T) {
	br := bufio.NewReader(strings.NewReader(magic + "xx"))
	if isHTTPRequest(br) {
		t.Fatal("hatch handshake mistaken for HTTP")
	}
}

func TestBootstrapCommandFitsSmallQR(t *testing.T) {
	boot := &bootstrap{sum: strings.Repeat("a", 64), host: "ianaya89-mbpro14.local", port: 7788}
	cmd := boot.command("42-tiger-mango")
	if len(cmd) > 134 {
		t.Fatalf("command is %d bytes; keep it within QR version 6-L (134): %s", len(cmd), cmd)
	}
}
