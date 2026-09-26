package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

const maxNameplate = 99

// The nameplate is public (advertised over mDNS); only the two words are secret.
func newCode() string {
	np := randInt(maxNameplate) + 1
	return fmt.Sprintf("%d-%s-%s", np, words[randInt(len(words))], words[randInt(len(words))])
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}

var wordSet = func() map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}()

func parseCode(s string) (int, string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == ' ' }), "-")
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return 0, "", fmt.Errorf("code must look like 7-guitar-orbit")
	}
	np, err := strconv.Atoi(parts[0])
	if err != nil || np < 1 || np > maxNameplate {
		return 0, "", fmt.Errorf("code must start with a number between 1 and %d", maxNameplate)
	}
	for _, w := range parts[1:] {
		if !wordSet[w] {
			return 0, "", fmt.Errorf("unknown word %q in code", w)
		}
	}
	return np, s, nil
}
