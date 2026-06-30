# Contributing to tonys

Thanks for your interest in improving `tonys`! Bug reports, feature ideas, and
pull requests are all welcome.

## Getting started

You need [Go](https://go.dev/dl/) (see the version in [`go.mod`](go.mod)). The
optional external tools are only needed if you work on the features that use
them:

- **ffmpeg** — audio conversion & loudness normalization
- **yt-dlp** — the `yt` YouTube importer

```sh
git clone https://github.com/bernardo-cs/tonys-cli
cd tonys-cli
make build        # builds ./tonys
./tonys doctor    # sanity-check your environment
```

## Development workflow

```sh
make test    # go test ./...
make vet     # go vet ./...
make fmt     # gofmt -w .
make cover   # coverage summary
```

Before opening a PR, please make sure:

- `make test` passes and `make vet` is clean.
- `gofmt -l .` reports nothing (run `make fmt`).
- New behavior has tests next to it.

## Conventions

- **Commands** live in `internal/cli/commands.go` and `internal/cli/commands_*.go`.
  `rootCommands()` is the single source of truth for dispatch, help, and
  `tonys schema` — update its metadata when you add or change a command/flag.
- **TonieCloud API** behavior stays in `internal/toniecloud`; CLI code should call
  client methods rather than open-coding HTTP (the `raw` command is the only
  exception).
- **Output discipline:** machine output (`--json`) goes to stdout and must stay
  clean; human status/diagnostics go to stderr and respect `--quiet`/`--verbose`.
- **Exit codes:** usage errors exit `2`, auth `3`, not-found `4`. Use the
  existing `usageErr` / `notFoundErr` helpers so the mapping stays consistent.
- **Never** log or print passwords/tokens.

## Tests must be hermetic

Tests must not contact TonieCloud, YouTube, S3, or any live service, and must not
require ffmpeg/yt-dlp unless they skip cleanly when those are absent. Use
`httptest` servers, fake command runners, temp dirs, and `t.Setenv` for
deterministic, isolated tests.

## Reporting security issues

Please **don't** file public issues for vulnerabilities — see
[SECURITY.md](SECURITY.md).
