# tonys

[![CI](https://github.com/bernardo-cs/tonys-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/bernardo-cs/tonys-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/bernardo-cs/tonys-cli?sort=semver)](https://github.com/bernardo-cs/tonys-cli/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/bernardo-cs/tonys-cli.svg)](https://pkg.go.dev/github.com/bernardo-cs/tonys-cli)
[![Go Report Card](https://goreportcard.com/badge/github.com/bernardo-cs/tonys-cli)](https://goreportcard.com/report/github.com/bernardo-cs/tonys-cli)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

An agent-friendly command-line wrapper around the **TonieCloud** API for
[creative tonies](https://tonies.com). Send audio — local files, stdin, or
YouTube links — to your kids' tonies from scripts and bots, with automatic
format conversion and loudness normalization.

It's a Go port of the Python library
[`alexhartm/tonie_api`](https://github.com/alexhartm/tonie_api), extended with a
real token-caching auth layer, structured errors, full chapter editing, audio
conversion/normalization, a YouTube importer, and a machine-readable command
schema.

> [!NOTE]
> **Unofficial project.** Not affiliated with, endorsed by, or supported by
> Boxine GmbH / tonies. Use your own account, at your own risk. See
> [Disclaimer](#disclaimer).

## Why "agent-friendly"?

- **Non-interactive auth** via env vars, with a cached & auto-refreshed token.
- **`--json` everywhere** — stable JSON on stdout, errors as JSON on stderr.
- **Meaningful exit codes** (`0` ok, `1` error, `2` usage, `3` auth, `4` not-found).
- **`tonys schema`** emits a JSON description of every command/flag for tools to
  introspect — the authoritative, always-current command reference.
- **`tonys raw`** can hit any endpoint, so nothing in the API is out of reach.
- Tonies and households can be referenced by **name or id**.

## Install

### Homebrew (macOS / Linux)

```sh
brew install bernardo-cs/tap/tonys
```

### Linux packages (`.deb` / `.rpm` / `.apk`)

Each release attaches native packages for `amd64` and `arm64`. Download the one
for your distro from the
[latest release](https://github.com/bernardo-cs/tonys-cli/releases/latest) and
install it:

```sh
# Debian / Ubuntu
sudo apt install ./tonys_0.1.0_linux_amd64.deb

# Fedora / RHEL / openSUSE
sudo dnf install ./tonys_0.1.0_linux_amd64.rpm     # or: sudo rpm -i ...

# Alpine
sudo apk add --allow-untrusted ./tonys_0.1.0_linux_amd64.apk
```

The binary installs to `/usr/bin/tonys`. ffmpeg/yt-dlp are listed as optional
(recommended/suggested) dependencies, so installation never pulls them in by
force.

### Prebuilt binary

Download the archive for your OS/arch from the
[latest release](https://github.com/bernardo-cs/tonys-cli/releases/latest),
verify it against `checksums.txt`, then put `tonys` on your `PATH`:

```sh
# macOS arm64 example — adjust the version/OS/arch to taste
VER=0.1.0
curl -fsSLO https://github.com/bernardo-cs/tonys-cli/releases/download/v$VER/tonys_${VER}_darwin_arm64.tar.gz
curl -fsSLO https://github.com/bernardo-cs/tonys-cli/releases/download/v$VER/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing      # verify
tar xzf tonys_${VER}_darwin_arm64.tar.gz
sudo mv tonys /usr/local/bin/
```

### With Go

```sh
go install github.com/bernardo-cs/tonys-cli/cmd/tonys@latest
```

### From source

```sh
git clone https://github.com/bernardo-cs/tonys-cli
cd tonys-cli
make build        # produces ./tonys
# or: make install   # installs to $GOBIN
```

### Optional external tools

The binary is self-contained Go, but two **optional external tools are NOT
bundled** — they must be on `PATH` for the features that use them:

- **ffmpeg** — audio conversion & loudness normalization (`brew install ffmpeg`,
  `apt-get install ffmpeg`, …)
- **yt-dlp** — the `yt` YouTube importer (`brew install yt-dlp`, `pipx install yt-dlp`, …)

Plain uploads of already-accepted formats and every API command work without
either. Run **`tonys doctor`** to check what's present (it prints OS-specific
install hints); for provisioning a box, gate on `tonys doctor --strict`:

```sh
tonys doctor            # human/JSON report of deps, creds, writable paths
tonys doctor --online   # also verify login + API reachability
tonys doctor --strict   # exit non-zero unless every check is ok
```

Override the discovered binaries with `$TONYS_FFMPEG` / `$TONYS_YTDLP`.

## Authentication

The recommended path for bots is environment variables; the token is exchanged
once and cached (mode `0600`) under `~/.cache/tonys/token.json`, then
auto-refreshed.

```sh
export TONIE_USERNAME="you@example.com"
export TONIE_PASSWORD="secret"

tonys auth login      # optional: pre-warm & verify the cache
tonys auth status     # show cached token validity
tonys auth logout     # clear the cache (add --all to clear every account)
```

Credential precedence: `--username/--password` flags → env → optional
`~/.config/tonys/config.json`. Override endpoints with `--api-url` / `--token-url`
or `$TONIE_API_URL` / `$TONIE_TOKEN_URL`. Passwords are never logged or printed,
and only ever sent to the OpenID token endpoint over HTTPS — see
[SECURITY.md](SECURITY.md).

## Quick start

```sh
# Tables by default; add --json for machine output.
tonys household list
tonys tonie list
tonys tonie get "Erna-Tonie"

# Upload an audio file as a new chapter (the main command):
tonys upload "Erna-Tonie" bedtime.mp3
tonys upload "Erna-Tonie" story.m4a --title "Bedtime story" --wait
tonys upload "Erna-Tonie" intro.mp3 --position beginning

# Pipe from stdin:
cat song.mp3 | tonys upload "Erna-Tonie" - --title "Song"
```

## Commands

`tonys schema` is the authoritative, machine-readable reference for every command
and flag. A human summary:

| Command | Purpose |
|---|---|
| `me` | the logged-in user (`GET /me`) |
| `config` | backend config & upload limits (`GET /config`) |
| `auth login\|status\|logout` | manage the token cache |
| `household list` | list households |
| `tonie list\|get\|rename` | list / inspect / rename creative tonies |
| `upload <tonie> <file>` | upload a file (or `-`) as a new chapter |
| `chapter list\|add\|add-existing\|rm\|rename\|move\|sort\|clear` | edit chapters |
| `yt <tonie> <url>` | import a YouTube video/playlist (needs yt-dlp) |
| `loudness measure\|show\|measure-tonie` | loudness tooling (see below) |
| `file create` | low-level: request a presigned upload slot |
| `raw <METHOD> <path>` | call any endpoint directly |
| `doctor` | check dependencies, credentials and writable paths |
| `schema` | machine-readable description of all commands |
| `version` | print version |

Run `tonys help <command>` for full per-command help.

### Global flags

Available on every command:

| Flag | Meaning |
|---|---|
| `--json` | shorthand for `--output json` |
| `-o, --output table\|json\|jsonl` | output format (default `table`) |
| `--username`, `--password` | credentials (override env) |
| `--api-url`, `--token-url` | endpoint overrides |
| `--no-cache` | ignore the token cache for this call |
| `--timeout 3m` | per-request timeout |
| `--quiet` | suppress stderr status lines |
| `--verbose` | extra stderr diagnostics |

### Chapters

Chapters can be referenced by **id**, **1-based number**, or **exact title**:

```sh
tonys chapter list "Erna-Tonie"
tonys chapter rename "Erna-Tonie" 3 "New title"
tonys chapter move "Erna-Tonie" "Old intro" 1
tonys chapter rm "Erna-Tonie" 4 5            # remove chapters 4 and 5
tonys chapter sort "Erna-Tonie" 3 1 2        # reorder
tonys chapter clear "Erna-Tonie"
```

New chapters are added to the end by default. Pass `--position beginning` on
`upload`, `chapter add`, `chapter add-existing`, or `yt` to put new audio at the
front instead.

`file create` plus `chapter add-existing` is the low-level split of an upload:
`file create` asks TonieCloud for a presigned upload slot (returning a `fileId`),
and `chapter add-existing <tonie> <fileId>` attaches an already-uploaded file as
a chapter. Most users just want `upload`, which does both.

## Audio conversion

TonieCloud accepts `aac aif aiff flac mp3 m4a m4b wav oga ogg opus wma`. Anything
else is **auto-converted with ffmpeg** before upload. Controls (on `upload`,
`chapter add`, `yt`):

```sh
--convert auto|always|never          # default auto (convert only unsupported inputs)
--format mp3|opus|ogg|m4a|flac|wav   # target codec when converting (default mp3)
--bitrate 128k
--skip 30s                           # trim this many seconds from the start
--skip-end 10s                       # trim this many seconds from the end
```

```sh
tonys upload "Erna-Tonie" voice-memo.webm   # webm → mp3 automatically
tonys upload "Erna-Tonie" podcast.mp3 --skip 1m30s          # drop intro
tonys upload "Erna-Tonie" podcast.mp3 --skip-end 30s        # drop outro
tonys upload "Erna-Tonie" podcast.mp3 --skip 1m --skip-end 30s  # both
```

`--format` only takes effect when a conversion actually happens. With
`--convert auto` (the default), an already-accepted input is uploaded as-is and
keeps its original format; with `--normalize` on, a re-encode is required, so the
file is encoded to `--format` (default mp3).

`--skip` and `--skip-end` accept any Go duration string (`30s`, `1m30s`, `2m`, …)
and trim that many seconds from the start or end respectively. Both force an ffmpeg
pass even for already-accepted formats. When combined with loudness normalization,
the measure and encode passes both operate on the same trimmed window.

## Loudness normalization

Normalize perceived volume (EBU R128 two-pass `loudnorm`) so chapters don't
jump between quiet and loud. It runs in the same ffmpeg pass as conversion and is
independent of the input bitrate/codec.

```sh
--normalize off|target|match   # default off
--target-lufs -16              # integrated loudness target
```

- **`target`** brings the file to a fixed loudness (default −16 LUFS).
- **`match`** brings the file to the **average loudness of the chapters already
  on the tonie** (as known to the local loudness ledger; see below).

```sh
tonys upload "Erna-Tonie" loud.wav  --normalize target --target-lufs -16
tonys upload "Erna-Tonie" quiet.mp3 --normalize match
```

### The loudness ledger and `match`

TonieCloud does not offer a supported way to download a chapter's audio (the
official API returns only metadata, and neither the website nor the iOS app
expose downloads). So `tonys` keeps a local **loudness ledger** at
`~/.local/share/tonys/loudness.json`: every file you upload through `tonys` is
measured and recorded, and `--normalize match` averages those records. When a
tonie has no known chapter loudness yet, `match` falls back to `--target-lufs`.
If you upload everything through `tonys`, your whole tonie stays at a consistent
level.

```sh
tonys loudness measure song.mp3        # measure a local file's LUFS/true-peak/LRA
tonys loudness show "Erna-Tonie"       # what the ledger already knows for a tonie
tonys loudness measure-tonie "Erna-Tonie"
```

`loudness measure-tonie` additionally makes a **best-effort** attempt to fetch
and measure the tonie's existing cloud chapters through an undocumented endpoint,
caching any it can. This is not guaranteed: tonies-published content can never be
downloaded, and many accounts/plans don't expose the endpoint at all — in those
cases chapters are reported as `not downloadable` and the local ledger remains
the reliable source for `match`.

## YouTube import

```sh
tonys yt "Erna-Tonie" "https://youtu.be/XXXX"
tonys yt "Erna-Tonie" "https://youtube.com/playlist?list=YYYY" \
    --normalize target --limit 10 --title-prefix "Mix: " --wait
```

Each item is downloaded (yt-dlp), run through the same convert/normalize
pipeline, and added as a chapter. Useful flags: `--limit N`, `--reverse`,
`--title-prefix`, `--position`, `--skip`, `--wait` / `--wait-timeout`, and
`--continue-on-error` (default on) which keeps going if one item fails and
reports failures in the summary.

YouTube radio URLs such as `watch?v=...&list=RD...&start_radio=1` can expand to
large auto-generated playlists. `tonys yt` warns when it sees one and asks
whether to import only the current video or the whole radio playlist. Choosing
the current video cleans the URL before download; pass `--allow-radio` to skip
the prompt when you intended to import the radio playlist.

## Using it from an agent / script

```sh
export TONIE_USERNAME=... TONIE_PASSWORD=...

# Discover the interface:
tonys schema | jq .

# Machine output + robust error handling:
if out=$(tonys --json tonie get "Erna-Tonie"); then
    echo "$out" | jq '.chapters | length'
else
    code=$?            # 2 = usage, 3 = auth, 4 = not found, 1 = other
fi
```

Successful output goes to **stdout** as JSON; errors go to **stderr** as
`{"error": "...", "class": "...", "status": N}`. stdout stays clean for parsing.

| Exit code | Class | Meaning |
|---|---|---|
| `0` | — | success |
| `1` | `error` | generic / unexpected error |
| `2` | `usage` | bad flags or arguments (incl. ambiguous references) |
| `3` | `auth` | authentication failure |
| `4` | `not_found` | tonie / household / chapter not found |

## Environment variables

| Var | Meaning |
|---|---|
| `TONIE_USERNAME`, `TONIE_PASSWORD` | credentials (aliases: `TONIES_*`, `TONIE_CLOUD_*`) |
| `TONYS_OUTPUT` | default output format (`table`/`json`/`jsonl`) |
| `TONYS_CONFIG`, `TONYS_CACHE`, `TONYS_LOUDNESS_DB` | explicit state-file locations |
| `TONIE_API_URL`, `TONIE_TOKEN_URL` | endpoint overrides |
| `TONYS_FFMPEG`, `TONYS_YTDLP` | binary overrides (aliases: `TONIE_FFMPEG`, `TONIE_YTDLP`) |
| `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `XDG_DATA_HOME` | base dirs for config / token cache / ledger |

## Development

```sh
make build    # build ./tonys
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt -w .
make cover    # coverage summary
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for conventions, and
[SECURITY.md](SECURITY.md) for the security model and how to report issues.

## Disclaimer

`tonys` is an **unofficial** tool. It is not affiliated with, authorized,
endorsed, or sponsored by Boxine GmbH or tonies. "tonies", "TonieCloud",
"Toniebox", and "Creative-Tonie" are trademarks of their respective owners; they
are used here only to describe what this tool interoperates with.

The tool talks to TonieCloud on your behalf using your own credentials. It relies
on undocumented endpoints that can change or break at any time. Use it at your own
risk and in accordance with tonies' Terms of Service. The software is provided
"as is", without warranty of any kind — see [LICENSE](LICENSE).

## License

[MIT](LICENSE) © 2026 Bernardo Simoes. Ported from the MIT-licensed
[`alexhartm/tonie_api`](https://github.com/alexhartm/tonie_api).
