# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-09-27

### Added

- `hatch pull --dry-run`: negotiate the manifest but transfer and clone nothing; report what would be written, kept and cloned.
- The next-steps checklist starts with "sign in to the App Store" when the received Brewfile has `mas` apps, since `brew bundle` can't install them otherwise.

## [0.2.1] - 2026-09-27

### Fixed

- Inline QR (iTerm2/WezTerm) was a tiny code in a large white square: rsc.io/qr scales the image bounds but not the modules. The PNG is now rasterized at scale, in an 18×9 cell box beside the instructions.
- A file cut short by a dropped connection could land under its real name and then be kept by the next incremental pull as a "local edit". Files are now written to `*.hatch-part` and renamed into place only when complete.

## [0.2.0] - 2026-09-26

### Added

- Choose what to sync: `→` on an item lists its children with sizes and lets you untick them; `-` adds exclude patterns; `+` adds paths or globs (`~/Documents/**/*.pdf`) resolved on the old machine; `/` filters the list.
- Incremental sync: serve sends a manifest first and pull only requests missing or changed files; `--update` takes newer copies, `--overwrite` replaces every differing file.
- The selection (ticks, custom paths, excludes) is remembered per source machine and restored on the next pull; `--fresh` ignores it.
- `serve --no-custom` refuses custom paths from the puller.

### Changed

- Protocol v2: both machines must run the same hatch version (the QR bootstrap guarantees it).
- Compact bootstrap command (96-bit sha256 prefix, fixed port 7788, `/h`) so the QR fits version 6; drawn as an inline image on iTerm2/WezTerm, as compact half-blocks next to the instructions elsewhere.
- `**/` in patterns now also matches zero directories.

## [0.1.1] - 2026-09-26

### Changed

- Release re-runs replace existing artifacts instead of failing; the Homebrew cask is now published by GoReleaser.

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

[Unreleased]: https://github.com/ianaya89/hatch/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/ianaya89/hatch/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/ianaya89/hatch/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/ianaya89/hatch/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/ianaya89/hatch/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/ianaya89/hatch/releases/tag/v0.1.0
