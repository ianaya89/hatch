package main

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	protoVersion = 1
	chunkSize    = 256 << 10

	msgJSON  byte = 'J'
	msgData  byte = 'D'
	msgEnd   byte = 'E'
	msgError byte = 'X'
)

type hello struct {
	Version int    `json:"version"`
	Host    string `json:"host"`
	User    string `json:"user"`
	OS      string `json:"os"`
}

type request struct {
	Op       string `json:"op"`
	ID       string `json:"id,omitempty"`
	Compress bool   `json:"compress,omitempty"`
}

type remoteError struct {
	Error string `json:"error"`
}

func (s *secureConn) sendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.writeMsg(msgJSON, b)
}

func (s *secureConn) sendError(err error) error {
	b, _ := json.Marshal(remoteError{Error: err.Error()})
	return s.writeMsg(msgError, b)
}

func (s *secureConn) recvJSON(v any) error {
	typ, payload, err := s.readMsg()
	if err != nil {
		return err
	}
	switch typ {
	case msgJSON:
		return json.Unmarshal(payload, v)
	case msgError:
		return decodeRemoteError(payload)
	default:
		return fmt.Errorf("unexpected message %q", typ)
	}
}

type peerError struct{ msg string }

func (e *peerError) Error() string { return "remote: " + e.msg }

func decodeRemoteError(payload []byte) error {
	var re remoteError
	if json.Unmarshal(payload, &re) != nil || re.Error == "" {
		return &peerError{msg: "unknown error"}
	}
	return &peerError{msg: re.Error}
}

type dataWriter struct {
	sc  *secureConn
	buf []byte
}

func newDataWriter(sc *secureConn) *dataWriter {
	return &dataWriter{sc: sc, buf: make([]byte, 0, chunkSize)}
}

func (w *dataWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		c := copy(w.buf[len(w.buf):cap(w.buf)], p)
		w.buf = w.buf[:len(w.buf)+c]
		p = p[c:]
		if len(w.buf) == cap(w.buf) {
			if err := w.flush(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (w *dataWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	err := w.sc.writeMsg(msgData, w.buf)
	w.buf = w.buf[:0]
	return err
}

func (w *dataWriter) Close() error {
	if err := w.flush(); err != nil {
		return err
	}
	return w.sc.writeMsg(msgEnd, nil)
}

type dataReader struct {
	sc   *secureConn
	rest []byte
	done bool
	err  error
}

func (r *dataReader) Read(p []byte) (int, error) {
	for len(r.rest) == 0 {
		if r.done {
			return 0, io.EOF
		}
		if r.err != nil {
			return 0, r.err
		}
		typ, payload, err := r.sc.readMsg()
		if err != nil {
			r.err = err
			return 0, err
		}
		switch typ {
		case msgData:
			r.rest = payload
		case msgEnd:
			r.done = true
		case msgError:
			r.err = decodeRemoteError(payload)
		default:
			r.err = fmt.Errorf("unexpected message %q in data stream", typ)
		}
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// drain consumes the stream up to its end marker so the next request starts
// on a frame boundary even if the consumer stopped reading early.
func (r *dataReader) drain() error {
	_, err := io.Copy(io.Discard, r)
	return err
}
