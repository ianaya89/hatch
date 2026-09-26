package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// hints lists the manual steps hatch can't do: anything backed by the
// Keychain, macOS privacy (TCC) grants, and state owned by apps.
func (s *scanner) hints() []string {
	exists := func(p string) bool {
		_, err := os.Stat(expandPath(s.home, p))
		return err == nil
	}
	app := func(name string) bool {
		_, err := os.Stat(filepath.Join("/Applications", name+".app"))
		return err == nil
	}
	var out []string

	if s.chezmoiSource != "" {
		out = append(out, "chezmoi apply — restores the files chezmoi manages (hatch skipped them)")
	}

	var claude []string
	matches, _ := filepath.Glob(filepath.Join(s.home, ".claude*"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() {
			claude = append(claude, "~/"+s.rel(m))
		}
	}
	if len(claude) == 1 {
		out = append(out, "Claude Code: run /login — credentials live in the Keychain")
	} else if len(claude) > 1 {
		out = append(out, "Claude Code: run /login once per config dir ("+strings.Join(claude, ", ")+") — credentials live in the Keychain")
	}
	if exists(".config/gh") {
		out = append(out, "gh auth status — log in again if the token was in the Keychain")
	}
	if app("1Password") {
		out = append(out, "1Password: sign in to the app, then the op CLI")
	}
	if _, err := exec.LookPath("tailscale"); err == nil || app("Tailscale") {
		out = append(out, "Tailscale: sign in (tailscale up)")
	}
	if exists("Library/Containers/com.wireguard.macos") {
		out = append(out, "WireGuard: tunnels live in the Keychain — export them from the app (Export Tunnels to Zip) and import on the new machine")
	}

	var tcc []string
	for _, c := range []struct{ name, path string }{
		{"Karabiner", ".config/karabiner"}, {"AeroSpace", ".aerospace.toml"}, {"AeroSpace", ".config/aerospace"},
		{"skhd", ".config/skhd"}, {"yabai", ".config/yabai"}, {"Hammerspoon", ".hammerspoon"},
	} {
		if exists(c.path) && !contains(tcc, c.name) {
			tcc = append(tcc, c.name)
		}
	}
	if app("Raycast") {
		tcc = append(tcc, "Raycast")
		out = append(out, "Raycast: Settings → Advanced → Export, then import — not everything lives in ~/.config/raycast")
	}
	if len(tcc) > 0 {
		out = append(out, "grant Accessibility / Input Monitoring in System Settings: "+strings.Join(tcc, ", "))
	}

	if agents := s.userLaunchAgents(); len(agents) > 0 {
		target := agents[0]
		if len(agents) > 1 {
			target = "{" + strings.Join(agents, ",") + "}"
		}
		out = append(out, "load your LaunchAgents once restored: launchctl load ~/Library/LaunchAgents/"+target)
	}
	if exists(".orbstack") || exists(".docker") {
		out = append(out, "containers: images and volumes don't move — docker save / dump the volumes you need")
	}
	return out
}

func (s *scanner) userLaunchAgents() []string {
	dir := filepath.Join(s.home, "Library", "LaunchAgents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	user := strings.ToLower(filepath.Base(s.home))
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".plist") {
			continue
		}
		if s.managed[filepath.Join(dir, name)] || strings.Contains(strings.ToLower(name), user) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
