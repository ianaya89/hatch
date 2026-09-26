package main

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"filippo.io/cpace"
)

const (
	magic            = "HATCH1"
	maxFrame         = 16 << 20
	handshakeTimeout = 15 * time.Second
	confirmClient    = "hatch-confirm-client"
	confirmServer    = "hatch-confirm-server"
)

var errBadCode = errors.New("pairing failed: wrong code")

// secureConn frames AES-256-GCM messages with a per-direction key and a
// counter nonce, so frames can't be replayed, reordered or reflected.
type secureConn struct {
	conn    net.Conn
	br      *bufio.Reader
	send    cipher.AEAD
	recv    cipher.AEAD
	sendSeq uint64
	recvSeq uint64
	wbuf    []byte
	rbuf    []byte
	visual  []byte
	rx, tx  atomic.Int64
}

func pakeContext(nameplate int) *cpace.ContextInfo {
	return cpace.NewContextInfo("hatch-pull", "hatch-serve", fmt.Appendf(nil, "%s/%d", magic, nameplate))
}

func clientHandshake(conn net.Conn, code string, nameplate int) (*secureConn, error) {
	conn.SetDeadline(time.Now().Add(handshakeTimeout))
	defer conn.SetDeadline(time.Time{})

	msgA, st, err := cpace.Start(code, pakeContext(nameplate))
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte(magic)); err != nil {
		return nil, err
	}
	if err := writeRaw(conn, msgA); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 256<<10)
	msgB, err := readRaw(br)
	if err != nil {
		return nil, err
	}
	key, err := st.Finish(msgB)
	if err != nil {
		return nil, err
	}
	sc, err := newSecureConn(conn, br, key, "hatch c2s", "hatch s2c")
	if err != nil {
		return nil, err
	}
	if err := sc.writeMsg(msgJSON, []byte(confirmClient)); err != nil {
		return nil, err
	}
	typ, payload, err := sc.readMsg()
	if err != nil || typ != msgJSON || string(payload) != confirmServer {
		return nil, errBadCode
	}
	return sc, nil
}

func serverHandshake(conn net.Conn, br *bufio.Reader, code string, nameplate int) (*secureConn, error) {
	conn.SetDeadline(time.Now().Add(handshakeTimeout))
	defer conn.SetDeadline(time.Time{})

	hdr := make([]byte, len(magic))
	if _, err := io.ReadFull(br, hdr); err != nil || string(hdr) != magic {
		return nil, errors.New("not a hatch client")
	}
	msgA, err := readRaw(br)
	if err != nil {
		return nil, err
	}
	msgB, key, err := cpace.Exchange(code, pakeContext(nameplate), msgA)
	if err != nil {
		return nil, err
	}
	if err := writeRaw(conn, msgB); err != nil {
		return nil, err
	}
	sc, err := newSecureConn(conn, br, key, "hatch s2c", "hatch c2s")
	if err != nil {
		return nil, err
	}
	typ, payload, err := sc.readMsg()
	if err != nil || typ != msgJSON || string(payload) != confirmClient {
		return nil, errBadCode
	}
	if err := sc.writeMsg(msgJSON, []byte(confirmServer)); err != nil {
		return nil, err
	}
	return sc, nil
}

func newSecureConn(conn net.Conn, br *bufio.Reader, key []byte, sendInfo, recvInfo string) (*secureConn, error) {
	send, err := newAEAD(key, sendInfo)
	if err != nil {
		return nil, err
	}
	recv, err := newAEAD(key, recvInfo)
	if err != nil {
		return nil, err
	}
	vis, err := hkdf.Expand(sha256.New, key, "hatch visual", 32)
	if err != nil {
		return nil, err
	}
	return &secureConn{conn: conn, br: br, send: send, recv: recv, visual: vis}, nil
}

func newAEAD(secret []byte, info string) (cipher.AEAD, error) {
	k, err := hkdf.Expand(sha256.New, secret, info, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(seq uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], seq)
	return n
}

func (s *secureConn) writeMsg(typ byte, payload []byte) error {
	if len(payload)+1 > maxFrame {
		return fmt.Errorf("message too large (%d bytes)", len(payload))
	}
	need := 4 + 1 + len(payload) + s.send.Overhead()
	if cap(s.wbuf) < need {
		s.wbuf = make([]byte, need)
	}
	buf := s.wbuf[:4+1+len(payload)]
	buf[4] = typ
	copy(buf[5:], payload)
	out := s.send.Seal(buf[:4], nonce(s.sendSeq), buf[4:], nil)
	s.sendSeq++
	binary.BigEndian.PutUint32(out[:4], uint32(len(out)-4))
	n, err := s.conn.Write(out)
	s.tx.Add(int64(n))
	return err
}

// readMsg returns a payload that is only valid until the next call.
func (s *secureConn) readMsg() (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(s.br, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame+64 || n < uint32(s.recv.Overhead()+1) {
		return 0, nil, fmt.Errorf("bad frame size %d", n)
	}
	if cap(s.rbuf) < int(n) {
		s.rbuf = make([]byte, n)
	}
	ct := s.rbuf[:n]
	if _, err := io.ReadFull(s.br, ct); err != nil {
		return 0, nil, err
	}
	s.rx.Add(int64(n) + 4)
	pt, err := s.recv.Open(ct[:0], nonce(s.recvSeq), ct, nil)
	if err != nil {
		return 0, nil, errors.New("message authentication failed")
	}
	s.recvSeq++
	return pt[0], pt[1:], nil
}

func (s *secureConn) Close() error { return s.conn.Close() }

func writeRaw(w io.Writer, b []byte) error {
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(b)))
	if _, err := w.Write(append(hdr[:], b...)); err != nil {
		return err
	}
	return nil
}

func readRaw(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(hdr[:]))
	_, err := io.ReadFull(r, b)
	return b, err
}
