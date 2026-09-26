package main

import "testing"

func TestWordList(t *testing.T) {
	if len(words) != 256 {
		t.Fatalf("want 256 words, got %d", len(words))
	}
	seen := map[string]bool{}
	for _, w := range words {
		if seen[w] {
			t.Fatalf("duplicate word %q", w)
		}
		seen[w] = true
	}
}

func TestNewCodeRoundTrip(t *testing.T) {
	for range 50 {
		c := newCode()
		np, norm, err := parseCode(c)
		if err != nil {
			t.Fatalf("parse %q: %v", c, err)
		}
		if norm != c || np < 1 || np > maxNameplate {
			t.Fatalf("got %d %q for %q", np, norm, c)
		}
	}
}

func TestParseCodeNormalizes(t *testing.T) {
	np, norm, err := parseCode("  7 Guitar--ORBIT ")
	if err != nil {
		t.Fatal(err)
	}
	if np != 7 || norm != "7-guitar-orbit" {
		t.Fatalf("got %d %q", np, norm)
	}
}

func TestParseCodeRejects(t *testing.T) {
	for _, c := range []string{"", "guitar-orbit", "0-guitar-orbit", "100-guitar-orbit", "7-guitr-orbit", "7-guitar-orbit-extra"} {
		if _, _, err := parseCode(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}
