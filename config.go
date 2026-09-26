package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	DevRoots     []string `toml:"dev_roots"`
	ExtraRepos   []string `toml:"extra_repos"`
	RepoDepth    int      `toml:"repo_depth"`
	RepoExtras   []string `toml:"repo_extras"`
	Secrets      []string `toml:"secrets"`
	SecretIgnore []string `toml:"secret_ignore"`
	Agents       []string `toml:"agents"`
	AgentIgnore  []string `toml:"agent_ignore"`
	State        []string `toml:"state"`
	Paths        []string `toml:"paths"`
	Exclude      []string `toml:"exclude"`
	DotfileJunk  []string `toml:"dotfile_junk"`
	LargeItemMB  int64    `toml:"large_item_mb"`
	Brew         bool     `toml:"brew"`
	Packages     bool     `toml:"packages"`
	Chezmoi      bool     `toml:"chezmoi"`
}

func defaultConfig() Config {
	return Config{
		DevRoots:   []string{"~/Development", "~/dev", "~/code", "~/src", "~/projects", "~/repos", "~/work"},
		ExtraRepos: []string{"~/.dotfiles", "~/dotfiles"},
		RepoDepth:  3,
		RepoExtras: []string{".env", ".env.*", ".envrc", ".dev.vars"},
		Secrets: []string{
			"~/.ssh", "~/.gnupg", "~/.aws", "~/.azure", "~/.kube", "~/.docker/config.json",
			"~/.netrc", "~/.npmrc", "~/.pypirc", "~/.cargo/credentials.toml", "~/.gem/credentials",
			"~/.terraform.d/credentials.tfrc.json", "~/.config/gcloud", "~/.config/gh", "~/.config/op",
		},
		SecretIgnore: []string{"/logs", "/cache", "/http-cache", "/virtenv", "S.*", "*.lock"},
		Agents: []string{
			".claude", ".claude-*", ".claude.json", ".codex", ".codex-*",
			".gemini", ".cursor", ".config/opencode", ".config/crush",
		},
		AgentIgnore: []string{
			"/projects/**/*.jsonl", "/projects/*/????????-????-????-????-????????????",
			"/file-history", "/shell-snapshots", "/shell_snapshots", "/session-env", "/paste-cache",
			"/backups", "/cache", "/.tmp", "/tmp", "/sessions", "/archived_sessions", "/logs_*",
			"/*.log", "/statsig", "/debug", "/telemetry", "/ide", "/extensions",
		},
		State: []string{
			"~/.local/share/atuin", "~/.local/share/zoxide", "~/.local/share/fish/fish_history",
			"~/.nb", "~/.password-store", "~/bin", "~/.local/bin", "~/Library/Fonts",
		},
		Exclude: []string{
			"node_modules", ".venv", "venv", "__pycache__", ".next", ".nuxt", ".turbo", ".parcel-cache",
			".svelte-kit", ".cache", "dist", "build", "target", ".gradle", ".terraform", "coverage",
			".pytest_cache", ".mypy_cache", ".ruff_cache", "DerivedData", ".DS_Store", "*.pyc",
		},
		DotfileJunk: []string{
			".DS_Store", ".CFUserTextEncoding", ".localized", ".zcompdump*", "*.backup*", ".lesshst",
			".wget-hsts", ".viminfo", ".Trash", "*.swp",
		},
		LargeItemMB: 1024,
		Brew:        true,
		Packages:    true,
		Chezmoi:     true,
	}
}

func homeDir() string {
	if h := os.Getenv("HATCH_HOME"); h != "" {
		abs, err := filepath.Abs(h)
		if err == nil {
			return abs
		}
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func configPath(home string) string {
	if p := os.Getenv("HATCH_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(home, ".config", "hatch", "config.toml")
}

func loadConfig(home string) (Config, error) {
	cfg := defaultConfig()
	path := configPath(home)
	_, err := toml.DecodeFile(path, &cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.RepoDepth < 0 {
		cfg.RepoDepth = 0
	}
	return cfg, nil
}

func encodeConfig(cfg Config) string {
	var b bytes.Buffer
	b.WriteString("# hatch config — any key set here replaces its default.\n")
	b.WriteString("# Paths are relative to $HOME (~/...). Patterns: no slash = match a name anywhere,\n")
	b.WriteString("# leading slash = anchored to the item root, ** = any depth.\n\n")
	toml.NewEncoder(&b).Encode(cfg)
	return b.String()
}

func writeDefaultConfig(home string) (string, error) {
	path := configPath(home)
	if _, err := os.Stat(path); err == nil {
		return path, fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	return path, os.WriteFile(path, []byte(encodeConfig(defaultConfig())), 0o644)
}

func expandPath(home, p string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	default:
		return filepath.Join(home, p)
	}
}
