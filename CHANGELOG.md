# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_Nothing yet._

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
