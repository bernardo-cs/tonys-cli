# tonys-cli Agent Notes

## Project Overview

`tonys` is an agent-friendly Go CLI for the TonieCloud API. It lets scripts and
bots inspect households/creative tonies, edit chapters, upload audio from files
or stdin, import YouTube audio, and optionally convert, loudness-normalize or
trim audio before upload — including automatic detection of the intro/outro
jingles playlist items share (spectral fingerprinting, no AI).

The CLI is designed for automation:
- stable JSON output via `--json` / `TONYS_OUTPUT=json`
- JSON errors with meaningful exit codes
- non-interactive auth through env vars and a token cache
- `tonys schema` for machine-readable command discovery
- `tonys raw` for direct endpoint access

This project is not affiliated with Boxine / tonies.de.

## Repository Layout

- `cmd/tonys/main.go`: executable entry point, signal handling, error exit.
- `internal/cli`: command tree, flag parsing, output, command implementations,
  loudness ledger, and CLI tests.
- `internal/toniecloud`: TonieCloud API client, auth/session/token cache,
  upload handling, models, and client tests.
- `internal/audio`: ffmpeg-backed conversion, EBU R128 loudness measurement /
  normalization, and spectral fingerprinting with shared intro/outro detection
  (`fingerprint.go`, `intro.go`; pure Go FFT in `fft.go`).
- `internal/ytdl`: yt-dlp integration for YouTube imports.
- `README.md`: user-facing behavior and command reference.
- `Makefile`: common build/test/vet/fmt targets.

## Development Commands

```sh
make build    # go build -ldflags ... -o ./tonys ./cmd/tonys
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt -w .
```

Use `go test ./...` for normal verification. Run `make vet` when changing public
interfaces, command dispatch, auth, HTTP behavior, or error handling.

## Runtime Dependencies

The Go binary is self-contained, but some features shell out to optional tools:
- `ffmpeg`: audio conversion and loudness normalization.
- `yt-dlp`: `tonys yt` imports.

Do not make tests require these binaries unless the test already skips cleanly
when they are absent. Keep dependency discovery compatible with `TONYS_FFMPEG`
and `TONYS_YTDLP`.

## Important Conventions

- Keep command definitions in `internal/cli/commands.go` and specific command
  files under `internal/cli/commands_*.go`.
- `rootCommands()` is authoritative for dispatch, help, and `tonys schema`.
  Update schema-facing metadata when adding or changing commands/flags.
- Preserve JSON stdout and JSON stderr behavior for automation. Human status
  messages belong on stderr and should respect quiet/verbose modes.
- Use the existing structured error helpers and exit-code mapping. Usage errors
  should exit `2`, auth errors `3`, not-found errors `4`.
- Tonies and chapters are intentionally resolvable by user-friendly references
  like name, id, chapter number, or exact title. Avoid breaking those flows.
- Token cache files and credential handling must remain non-interactive and
  suitable for bots. Do not log passwords or tokens.
- Temporary processed audio files must be cleaned up by callers via returned
  cleanup functions.
- Keep TonieCloud API behavior isolated in `internal/toniecloud`; CLI packages
  should use client methods instead of open-coding HTTP calls except for the
  explicit `raw` command path.

## Testing Guidance

Add focused tests near changed behavior:
- CLI parsing/output behavior: `internal/cli/*_test.go`
- API client/session/auth behavior: `internal/toniecloud/*_test.go`
- Audio conversion/loudness behavior: `internal/audio/*_test.go`
- yt-dlp command wrapping: `internal/ytdl/*_test.go`

Prefer deterministic tests with fake HTTP servers, fake command runners, temp
dirs, and explicit env setup. Avoid tests that contact TonieCloud, YouTube, or
other live services.

