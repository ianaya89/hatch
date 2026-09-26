package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCargoCmds(t *testing.T) {
	out := `bat v0.24.0:
    bat
tool v0.1.0 (https://github.com/me/tool#abc123):
    tool
mine v0.2.0 (/home/me/src/mine):
    mine
`
	got := cargoCmds(out, "/home/me")
	want := []string{
		"cargo install 'bat'",
		"cargo install --git 'https://github.com/me/tool' 'tool'",
		`cargo install --path "$HOME"/'src/mine'`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestGoInstallCmd(t *testing.T) {
	cases := map[string]string{
		"/x/dlv: go1.26\n\tpath\tgithub.com/go-delve/delve/cmd/dlv\n\tmod\tgithub.com/go-delve/delve\tv1.25.0\th1:x\n": "go install 'github.com/go-delve/delve/cmd/dlv@latest'",
		"/x/go: go1.26\n\tpath\tcmd/go\n": "",
		"/x/hatch: go1.26\n\tpath\tgithub.com/ianaya89/hatch\n\tmod\tgithub.com/ianaya89/hatch\t(devel)\t\n": "",
	}
	for in, want := range cases {
		got, ok := goInstallCmd(in)
		if got != want || ok != (want != "") {
			t.Errorf("goInstallCmd = %q %v, want %q", got, ok, want)
		}
	}
}

func TestPipxAndNpmCmds(t *testing.T) {
	pipx := []byte(`{"venvs":{"llm":{"metadata":{"main_package":{"package_or_url":"llm","pip_args":[]}}},
		"obp":{"metadata":{"main_package":{"package_or_url":"/home/me/Development/omron-bp","pip_args":["--force-reinstall","--editable"]}}}}}`)
	want := []string{`pipx install 'llm'`, `pipx install --editable "$HOME"/'Development/omron-bp'`}
	if got := pipxCmds(pipx, "/home/me"); !reflect.DeepEqual(got, want) {
		t.Errorf("pipx: got %q", got)
	}
	npm := []byte(`{"dependencies":{"npm":{},"corepack":{},"wrangler":{},"@anthropic-ai/claude-code":{}}}`)
	want = []string{"npm install -g '@anthropic-ai/claude-code'", "npm install -g 'wrangler'"}
	if got := npmCmds(npm); !reflect.DeepEqual(got, want) {
		t.Errorf("npm: got %q", got)
	}
}

func TestUvCmds(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".local/share/uv/tools/tinybird")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "uv-receipt.toml"), []byte("[tool]\nrequirements = [{ name = \"tinybird\" }]\npython = \"3.11\"\n"), 0o644)
	want := []string{"uv tool install --python '3.11' 'tinybird'"}
	if got := uvCmds(home); !reflect.DeepEqual(got, want) {
		t.Errorf("uv: got %q", got)
	}
}

func TestShq(t *testing.T) {
	if got := shq("it's"); got != `'it'\''s'` {
		t.Errorf("shq = %s", got)
	}
}

func TestInstallTarget(t *testing.T) {
	cases := map[string]string{
		"go install 'golang.org/x/tools/gopls@latest'":      "golang.org/x/tools/gopls@latest",
		`pipx install --editable "$HOME"/'Development/obp'`: "Development/obp",
		"mise install": "install",
	}
	for in, want := range cases {
		if got := installTarget(in); got != want {
			t.Errorf("installTarget(%q) = %q, want %q", in, got, want)
		}
	}
}
