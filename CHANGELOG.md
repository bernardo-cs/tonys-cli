# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Features
- `--skip` / `--skip-end` on `upload`, `chapter add` and `yt` trim a fixed
  duration from the start / end of the audio before upload.
- Automatic intro/outro trimming for YouTube imports: `--trim-intro` and
  `--trim-outro` on `tonys yt` (plus `--auto-trim` for both) detect the intro
  or closing jingle shared by playlist items via cross-file spectral
  fingerprinting — no AI, no network — and cut it per item, including a
  short pause (up to ~3s) between the jingle and the content. Bounds are
  tunable with
  `--intro-max/--intro-min/--outro-max/--outro-min`, and cuts stack on top of
  `--skip`/`--skip-end`.
- New offline commands `tonys intro detect <file>...` and
  `tonys outro detect <file>...` report per-file cut points (and the shared
  jingle length) for local audio, in table or `--json` form.
- Single-file `spectral` mode finds an intro/outro boundary by spectral
  contrast + cohesion. It is a heuristic: `auto` only reports it on single
  videos; cutting requires opting in with `--trim-intro spectral` /
  `--trim-outro spectral`.

### Fixes
- `--skip`/`--skip-end` are no longer silently ignored when the input is
  already in an accepted format and no conversion or normalization was
  requested.
- A start-trim at or beyond the file's duration now fails with a clear
  trim-window error instead of silently encoding (and uploading) an empty
  file.

## [0.1.0] - 2026-06-30

Initial public release.

### Features
- Inspect households and creative tonies; rename tonies.
- Upload audio as chapters from a file or stdin, with full chapter editing
  (add, add-existing, remove, rename, move, sort, clear).
- Automatic ffmpeg conversion of unsupported formats and EBU R128 loudness
  normalization (`target` or `match` against a local loudness ledger).
- YouTube import via yt-dlp, including radio-playlist detection.
- Agent-friendly surface: stable `--json` output, JSON errors with meaningful
  exit codes (`2` usage, `3` auth, `4` not-found), a machine-readable
  `tonys schema`, and a `tonys raw` escape hatch.
- Non-interactive auth with a `0600` token cache and automatic refresh.
- `tonys doctor` environment/dependency diagnostics.
- Distributed via Homebrew, Linux packages (`.deb`/`.rpm`/`.apk`, amd64 &
  arm64), prebuilt archives with checksums, and `go install`.

[Unreleased]: https://github.com/bernardo-cs/tonys-cli/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bernardo-cs/tonys-cli/releases/tag/v0.1.0
