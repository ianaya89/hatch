package main

import (
	"path"
	"regexp"
	"strings"
)

type pattern struct {
	glob string
	re   *regexp.Regexp
}

type patterns []pattern

func compilePatterns(globs []string) patterns {
	var ps patterns
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if !strings.Contains(g, "/") {
			ps = append(ps, pattern{glob: g})
			continue
		}
		ps = append(ps, pattern{re: globToRegexp(strings.TrimPrefix(g, "/"))})
	}
	return ps
}

func globToRegexp(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// match takes a slash path relative to the item root. Name globs are tested
// against the basename, path globs against the whole relative path.
func (ps patterns) match(rel string) bool {
	base := path.Base(rel)
	for _, p := range ps {
		if p.re != nil {
			if p.re.MatchString(rel) {
				return true
			}
		} else if ok, _ := path.Match(p.glob, base); ok {
			return true
		}
	}
	return false
}
