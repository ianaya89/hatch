# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-09-26

### Added

- `hatch serve` scans the machine and waits for one pull, advertised over mDNS with a one-time code.
- `hatch pull` pairs with the code (CPace PAKE), then transfers over AES-256-GCM frames with zstd.
- Scanners: secrets, agent configs (transcripts skipped), `~/.config` + home dotfiles not managed by chezmoi, state (atuin, zoxide, nb), dev folders, repos, Brewfile.
- Clean, pushed repos are re-cloned from their remote; repos with local work copy `.git` plus what `git ls-files` reports; env files (`.env*`, `.envrc`, `.dev.vars`) always travel.
- TUI checklist with per-group folding, live progress and a summary with next steps; `--yes` for a non-interactive pull.
- Route detection prefers Thunderbolt Bridge / link-local, disables compression there; `--addr` for Tailscale or networks without mDNS.
- `hatch scan` previews the inventory; `hatch config` prints or writes the config.
- `~/bin`, `~/.local/bin` and `~/Library/Fonts` in the default state paths.
- Generated `packages.sh` reinstalling mise/cargo/go/npm/pipx/uv tools, shipped next to the Brewfile.
- Summary lists detected manual steps (Keychain logins, WireGuard, TCC grants, LaunchAgents, containers).
- `caffeinate` keeps both Macs awake while serve/pull run.
- Bootstrap: `serve` serves its own binary on the same port (HTTP is detected by peeking the first bytes) and prints a QR + one-liner that verifies the sha256 before running `hatch pull`.
- Pairing cloud: both peers draw the same spinning 3D braille particle cloud (orb/ring/galaxy/helix, palette, arms, tilt, spin from the session key), rotated by wall-clock time so both screens move in sync; the TUI asks to confirm the match.

[Unreleased]: https://github.com/ianaya89/hatch/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ianaya89/hatch/releases/tag/v0.1.0
