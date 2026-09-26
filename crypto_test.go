package main

import (
	"bufio"
	"errors"
	"net"
	"testing"
)

func handshakePair(t *testing.T, clientCode, serverCode string) (*secureConn, *secureConn, error, error) {
	t.Helper()
	c, s := net.Pipe()
	type res struct {
		sc  *secureConn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		sc, err := serverHandshake(s, bufio.NewReader(s), serverCode, 7)
		if err != nil {
			s.Close()
		}
		ch <- res{sc, err}
	}()
	cc, cerr := clientHandshake(c, clientCode, 7)
	if cerr != nil {
		c.Close()
	}
	r := <-ch
	return cc, r.sc, cerr, r.err
}

func TestHandshakeAndFrames(t *testing.T) {
	cc, sc, cerr, serr := handshakePair(t, "7-guitar-orbit", "7-guitar-orbit")
	if cerr != nil || serr != nil {
		t.Fatalf("handshake: client=%v server=%v", cerr, serr)
	}
	defer cc.Close()
	defer sc.Close()

	go func() {
		cc.sendJSON(request{Op: "fetch", ID: "x"})
		w := newDataWriter(cc)
		w.Write(make([]byte, chunkSize*2+123))
		w.Close()
	}()
	var req request
	if err := sc.recvJSON(&req); err != nil || req.ID != "x" {
		t.Fatalf("recvJSON: %v %+v", err, req)
	}
	dr := &dataReader{sc: sc}
	n := 0
	buf := make([]byte, 4096)
	for {
		k, err := dr.Read(buf)
		n += k
		if err != nil {
			break
		}
	}
	if n != chunkSize*2+123 || !dr.done {
		t.Fatalf("got %d bytes, done=%v", n, dr.done)
	}
}

func TestHandshakeWrongCode(t *testing.T) {
	_, _, cerr, serr := handshakePair(t, "7-guitar-orbit", "7-guitar-otter")
	if !errors.Is(cerr, errBadCode) || !errors.Is(serr, errBadCode) {
		t.Fatalf("want errBadCode on both sides, got client=%v server=%v", cerr, serr)
	}
}

func TestTamperedFrameRejected(t *testing.T) {
	cc, sc, cerr, serr := handshakePair(t, "7-guitar-orbit", "7-guitar-orbit")
	if cerr != nil || serr != nil {
		t.Fatal(cerr, serr)
	}
	defer cc.Close()
	defer sc.Close()
	go func() {
		cc.sendSeq++
		cc.writeMsg(msgJSON, []byte(`{}`))
	}()
	if _, _, err := sc.readMsg(); err == nil {
		t.Fatal("frame with a skipped nonce was accepted")
	}
}
