# hatch

[![CI](https://github.com/ianaya89/hatch/actions/workflows/ci.yml/badge.svg)](https://github.com/ianaya89/hatch/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ianaya89/hatch?sort=semver)](https://github.com/ianaya89/hatch/releases)
![Go](https://img.shields.io/github/go-mod/go-version/ianaya89/hatch)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Move your dev setup to a new machine, peer to peer over the local network. No cloud, no USB drive, no manually remembering which dotfiles you never put in a dotfiles manager.

## How it works

```sh
# old machine
hatch serve
  code   42-tiger-mango

# new machine
hatch pull 42-tiger-mango
```

1. `hatch serve` scans the old machine and starts listening, advertised over mDNS under a random public **nameplate** (`42`) plus a one-time **code** it prints.
2. `hatch pull <code>` on the new machine resolves the nameplate over mDNS (or dials `--addr host:port` directly), then pairs using the two secret words in the code.
3. Once paired, hatch shows a checklist TUI grouped by category — untick whatever you don't want.
4. Selected items transfer over an encrypted, optionally compressed stream. Repos that are clean and pushed are cloned from their remote instead of copied.
5. hatch prints a summary (files written, repos cloned, anything skipped or failed) and a numbered list of next steps: `brew bundle`, `chezmoi apply`, `packages.sh`, then the manual steps it detected on the old machine — Keychain-backed logins (Claude Code per config dir, gh, 1Password, Tailscale, WireGuard tunnels), Accessibility/Input Monitoring grants (Karabiner, AeroSpace, skhd, Raycast…), LaunchAgents to load, container volumes to dump.

On macOS both sides hold a `caffeinate` assertion while they run, so neither machine idle-sleeps mid-transfer.

### New machine without hatch yet

`hatch serve` also serves its own binary on the same port and prints a QR plus a one-liner:

```sh
curl -fsSo /tmp/hatch http://old-mac.local:49547/hatch && echo "<sha256>  /tmp/hatch" | shasum -a 256 -c -q && chmod +x /tmp/hatch && /tmp/hatch pull 42-tiger-mango
```

Scan the QR with an iPhone, copy the text, and paste it on the new Mac (Universal Clipboard, same Apple ID). The sha256 travels screen → camera → clipboard, never over the network, so a LAN attacker can't swap the binary: a modified download fails `shasum -c` and the `&&` chain stops before it runs. The URL uses the Bonjour name (`name.local`), which macOS resolves on Wi-Fi and over a Thunderbolt Bridge alike. Files fetched with `curl` aren't quarantined, so Gatekeeper doesn't block the unsigned binary. The served binary is the one running `serve`, so both machines must share OS/arch (printed next to the command). Disable with `--no-bootstrap` / `--no-qr`.

### Pairing cloud

After pairing, both machines draw the same spinning 3D particle cloud — a nod to Apple's pairing cloud. Everything about it comes from the session key (HKDF → ChaCha8): the shape (armillary **orb** with 2–4 bands, **ring**, spiral **galaxy** with 2–4 arms, or horizontal double **helix**), the palette (violet, ocean, aurora, ember, sakura, gold, lime, ice), tilt and spin direction, plus every particle position. A caption such as `aurora galaxy · 3 arms · ↻` makes it easy to compare without relying on color.

Particles are drawn with braille sub-pixels (2×4 per cell) and shaded by depth, so near ones glow brighter. The rotation angle is derived from the wall clock, so two NTP-synced machines spin in lockstep and can be compared side by side while moving. `serve` animates while the new machine sits on the checklist; the TUI asks you to confirm the match before continuing. The PAKE already guarantees a shared key cryptographically — the cloud makes it visible, and a machine in the middle would draw a different one.

The code can also be typed as separate words: `hatch pull 42 tiger mango` works the same as `hatch pull 42-tiger-mango`.

## What gets moved

| Category | Default sources | Notes |
| --- | --- | --- |
| `secrets` | `~/.ssh ~/.gnupg ~/.aws ~/.azure ~/.kube ~/.docker/config.json ~/.netrc ~/.npmrc ~/.pypirc ~/.cargo/credentials.toml ~/.gem/credentials ~/.terraform.d/credentials.tfrc.json ~/.config/gcloud ~/.config/gh ~/.config/op` | logs/cache/lock files under these are ignored |
| `agents` | `.claude .claude-* .claude.json .codex .codex-* .gemini .cursor .config/opencode .config/crush` | configs move; transcripts, caches, telemetry and IDE/extension dirs are excluded |
| `config & state` | everything under `~/.config` and home dotfiles not managed by chezmoi, plus `~/.local/share/atuin`, `~/.local/share/zoxide`, fish history, `~/.nb`, `~/.password-store`, `~/bin`, `~/.local/bin`, `~/Library/Fonts` | |
| `repos` | anything found under `~/Development ~/dev ~/code ~/src ~/projects ~/repos ~/work` (depth 3), plus `~/.dotfiles`/`~/dotfiles` and the chezmoi source repo | see below |
| `folders` | non-repo directories and loose files under the dev roots, plus anything you list in `paths` | |
| `packages` | `brew bundle dump` output (formulae, casks, taps, mas, VS Code extensions) as a Brewfile, plus a generated `packages.sh` that reinstalls tools from `mise install`, `cargo install`, `go install`, `npm -g`, `pipx` and `uv tool` | editable/local installs use `"$HOME"/…` so the script survives a different username |

Build junk (`node_modules`, `.venv`, `dist`, `build`, `target`, framework caches, …) and OS junk (`.DS_Store`, `.zcompdump*`, …) are excluded everywhere. Items over 1 GiB are off by default (`large_item_mb`), still listed but unticked.

### Repos: clone when clean, copy when not

- A repo is **cloned from its remote** (0 bytes over the wire) when it has no uncommitted changes, no unpushed commits, no stash, a real network remote (not a local/`file://` one), and isn't on a detached HEAD.
- Otherwise it's **copied**: hatch sends `.git` plus whatever `git ls-files -co --exclude-standard` reports (tracked + untracked-but-not-ignored), so your `.gitignore` prunes `node_modules`, build output, etc. from the transfer for you.
- Env files (`.env`, `.env.*`, `.envrc`, `.dev.vars`) always travel with the repo, cloned or not, since they're gitignored by definition and wouldn't otherwise come along for a clone.
- If a clone fails on the new machine (auth, network, deleted remote…), its env files still land — staged at `~/.local/share/hatch/staging/<repo path>` so nothing is lost. Fix the problem and re-run `hatch pull`; existing files are kept, so it's safe to retry.

### chezmoi

- Files chezmoi manages are skipped by the scanner (dotfiles, `~/.config` entries) — run `chezmoi apply` on the new machine to restore them instead of transferring the rendered output.
- The chezmoi source repo itself (`chezmoi source-path`) is offered as an ordinary repo item, so it clones or copies like any other repo.

## Security

- The code is `<nameplate>-<word>-<word>`, e.g. `42-tiger-mango`. The nameplate (1–99) is public — it's advertised in mDNS TXT records and only used to find the peer. The two words come from a 256-word list and are the actual secret.
- Pairing uses [CPace](https://pkg.go.dev/filippo.io/cpace), a password-authenticated key exchange: both sides derive a shared key from the code without ever putting the code itself on the wire. A wrong guess only gets **one online attempt** — when a peer completes the exchange with the wrong code, `hatch serve` burns the code and exits, and nothing on the wire allows an offline brute-force.
- Once paired, all traffic is AES-256-GCM, with separate HKDF-derived keys per direction and a monotonic counter nonce, so frames can't be replayed, reordered, or reflected back at the sender.
- Extraction on the pulling side happens inside an `os.Root` rooted at `$HOME`, so a malicious or buggy peer can't write outside it via `../` or a symlink trick.
- Existing files are never overwritten — matches are skipped and counted — unless you pass `--overwrite`.
- Repo clones run with `GIT_TERMINAL_PROMPT=0` and, unless you've already set `GIT_SSH_COMMAND`, `ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new`: no interactive prompts, and unseen SSH host keys are trusted on first use (TOFU) instead of rejected. Fine for a fresh machine, but it means a MITM on that very first connection to a given host wouldn't be caught.

## Transport

- Route detection ranks interfaces — Thunderbolt Bridge (`bridge0`) > direct link-local connection > loopback > LAN > Tailscale (`100.64.0.0/10`) > other routed — and the client dials the best address mDNS offered.
- Compression defaults to off on Thunderbolt Bridge and other direct/loopback links (already fast; CPU becomes the bottleneck) and on everywhere else. Override with `--compress auto|on|off`.
- No mDNS, different subnets, or a Tailscale-only path? Skip discovery with `--addr host:port` (`hatch serve` prints candidate `host:port` values, and also use it if you pass `--no-mdns`).

## Installation

| Platform | Recommended |
| --- | --- |
| Linux | install script or prebuilt binary |
| macOS | Homebrew or install script |
| Any (with Go) | `go install` |

### Install script (Linux / macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/ianaya89/hatch/main/install.sh | sh
```

Installs to `~/.local/bin` by default. Override with env vars:

```sh
HATCH_INSTALL_DIR=/usr/local/bin HATCH_VERSION=v0.1.0 \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/ianaya89/hatch/main/install.sh)"
```

### Homebrew (macOS)

```sh
brew install ianaya89/tap/hatch
```

> The Homebrew cask is macOS-only. On Linux, use the install script or a prebuilt binary.

### Prebuilt binary

Grab a tarball for your OS/arch (linux/darwin, amd64/arm64) from the [releases page](https://github.com/ianaya89/hatch/releases), then:

```sh
tar -xzf hatch_*_linux_amd64.tar.gz
install -m 0755 hatch ~/.local/bin/hatch
```

### go install / from source (requires Go 1.26+)

```sh
go install github.com/ianaya89/hatch@latest

# or, for a version-stamped binary:
git clone https://github.com/ianaya89/hatch ~/hatch
cd ~/hatch
make install          # builds with version info into ~/.local/bin/hatch
```

Ensure `~/.local/bin` is on your `PATH` (or set `PREFIX=/usr/local make install`).

## Usage

```sh
hatch serve [--port N] [--no-mdns]          on the old machine: scan and wait for a pull
hatch pull [code] [flags]                   on the new machine: pick items and pull them
hatch scan [--json]                         preview what serve would offer
hatch config [--init]                       print the effective config (or write the default)
hatch version
```

`pull` flags:

| Flag | Default | Description |
| --- | --- | --- |
| `--addr host:port` | — | skip mDNS and dial directly (e.g. a Tailscale IP) |
| `--overwrite` | off | replace files that already exist |
| `--compress mode` | `auto` | `auto` \| `on` \| `off` (`auto`: off on Thunderbolt/direct links) |
| `--jobs N` | `4` | parallel git clones |
| `--yes` | off | no TUI: pull the default selection and print progress |

### TUI keys

Shown on the checklist screen (`hatch pull` without `--yes`):

| Key | Action |
| --- | --- |
| `↑`/`k`, `↓`/`j` | Move |
| `→`/`l`/`tab` | Expand the current group |
| `←`/`h` | Collapse the current group |
| `space`/`x` | Toggle the item, or the whole group from its header |
| `a` / `n` | Select all / none |
| `pgup`/`pgdown` | Page up/down |
| `g`/`home`, `G`/`end` | Jump to top/bottom |
| `enter` | Pull the current selection |
| `q`/`esc` | Quit |

While a pull is running, `q` aborts (existing files are kept, so re-running is safe).

### Non-interactive: `--yes`

```sh
hatch pull 42-tiger-mango --yes
```

Skips the TUI, pulls every item the scanner flagged `default` on the serving side, and prints periodic progress plus a final summary. Useful for scripting or a headless box.

### `hatch scan`

Preview what `hatch serve` would offer on this machine, without opening a port:

```sh
hatch scan            # human-readable, grouped by category
hatch scan --json     # same inventory as JSON, for scripting
```

### `hatch config`

```sh
hatch config          # print the effective config (defaults + your file), with its path
hatch config --init   # write the default config to that path (fails if it already exists)
```

Config lives at `~/.config/hatch/config.toml` (or `$HATCH_CONFIG`) and is TOML. **Any key you set replaces its default outright** — arrays don't merge, so if you set `secrets = [...]`, list everything you want, including whatever defaults you meant to keep.

```toml
# hatch config — any key set here replaces its default.
dev_roots     = ["~/code"]
secret_ignore = ["/logs", "*.bak"]
```

Pattern syntax used by `exclude`, `secret_ignore`, `agent_ignore`, `dotfile_junk`, `repo_extras`:

- no `/` → matches the name anywhere (a basename glob), e.g. `*.lock`
- leading `/` → anchored to the item's own root, e.g. `/logs`
- `**` → any depth, `*` → one path segment, `?` → one character

### Env vars

| Var | Effect |
| --- | --- |
| `HATCH_HOME` | act on this directory instead of `$HOME` (also shifts the default config path) |
| `HATCH_CONFIG` | config file path, overrides the default location |

## Try it locally

Simulate two machines on one box with separate home directories:

```sh
HATCH_HOME=/tmp/a hatch serve
# note the printed code, then in another terminal:
HATCH_HOME=/tmp/b hatch pull <code>
```

## Roadmap

- **v0.2** — a verify step after pull: `brew bundle check`, `gh auth status`, `ssh -T` per host, per-repo `git status`, so you know what still needs attention instead of assuming a finished transfer means a working environment.
- **v0.3** — `hatch mcp`: expose inventory/pull/verify as MCP tools so an agent on the new machine can drive the migration itself.

## Development

```sh
make build      # build ./hatch with version stamped from git
make install    # build into $PREFIX/bin (default ~/.local)
make test       # go test ./...
make vet        # go vet ./...
make fmt        # gofmt -w .
make check      # fmt + vet + test
```

CI (gofmt, vet, build, race tests) runs on every push and PR.

### Releasing

Tag and push; the release workflow runs [GoReleaser](https://goreleaser.com) to build cross-platform binaries, publish a GitHub release, and update the Homebrew cask in [`ianaya89/homebrew-tap`](https://github.com/ianaya89/homebrew-tap):

```sh
git tag v0.1.0
git push origin v0.1.0
```

The binary's `--version` is stamped from the tag. Update [`CHANGELOG.md`](CHANGELOG.md) before tagging a release.

**One-time setup for the Homebrew step** — the default `GITHUB_TOKEN` can't write to the tap repo, so add a Personal Access Token with write access to it (skip this if it's already set from another project sharing the same tap):

```sh
gh secret set HOMEBREW_TAP_TOKEN --repo ianaya89/hatch
```

Without that secret the release still succeeds, but the cask update step fails.

## License

MIT
