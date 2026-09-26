package main

import (
	"strings"
	"testing"
)

var testSeed = []byte("0123456789abcdef0123456789abcdef")

func TestVisualDeterministic(t *testing.T) {
	a, b := newVisual(testSeed), newVisual(testSeed)
	for _, ms := range []int64{0, 1234, 7777, 1790000000123} {
		if a.render(ms) != b.render(ms) {
			t.Fatalf("same seed and time rendered differently at %d", ms)
		}
	}
	if a.caption() != b.caption() {
		t.Fatal("captions differ")
	}
}

func TestVisualSeedsDiffer(t *testing.T) {
	a := newVisual(testSeed)
	b := newVisual([]byte("fedcba9876543210fedcba9876543210"))
	if a.render(0) == b.render(0) {
		t.Fatal("different seeds rendered the same cloud")
	}
}

func TestVisualSpinsAndFits(t *testing.T) {
	v := newVisual(testSeed)
	still, later := stripANSI(v.render(0)), stripANSI(v.render(visPeriod/4))
	if still == later {
		t.Fatal("cloud didn't rotate")
	}
	lines := strings.Split(still, "\n")
	if len(lines) != visRows {
		t.Fatalf("got %d rows, want %d", len(lines), visRows)
	}
	dots := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > visCols+4 {
			t.Fatalf("row too wide: %d", n)
		}
		for _, r := range l {
			if r >= 0x2800 && r <= 0x28FF {
				dots++
			}
		}
	}
	if dots < 40 {
		t.Fatalf("only %d braille cells lit", dots)
	}
}

func TestVisualCoversAllShapes(t *testing.T) {
	seen := map[visShape]bool{}
	for i := range 64 {
		seed := make([]byte, 32)
		seed[0] = byte(i)
		seen[newVisual(seed).shape] = true
	}
	if len(seen) != len(shapeNames) {
		t.Fatalf("only saw %d of %d shapes", len(seen), len(shapeNames))
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestPairedPeersShareVisual(t *testing.T) {
	cc, sc, cerr, serr := handshakePair(t, "7-guitar-orbit", "7-guitar-orbit")
	if cerr != nil || serr != nil {
		t.Fatal(cerr, serr)
	}
	defer cc.Close()
	defer sc.Close()
	if newVisual(cc.visual).render(4242) != newVisual(sc.visual).render(4242) {
		t.Fatal("peers derived different clouds")
	}
}
