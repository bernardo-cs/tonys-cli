# Security Policy

## Reporting a vulnerability

Please report security issues **privately**, not as a public GitHub issue.

Use [GitHub's private vulnerability reporting](https://github.com/bernardo-cs/tonys-cli/security/advisories/new)
for this repository. You'll get an acknowledgement as soon as possible, and a
fix or mitigation will be coordinated before any public disclosure.

## Security model

`tonys` talks to your own TonieCloud account, so it necessarily handles
credentials. How it treats them:

- **Credentials come from you, never from the binary.** Username/password are
  read from `--username`/`--password`, then the `TONIE_USERNAME` /
  `TONIE_PASSWORD` environment variables (and documented aliases), then an
  optional `~/.config/tonys/config.json`. Nothing is hard-coded.
- **Passwords are never logged or printed.** They are sent only to the OpenID
  token endpoint over HTTPS. They never appear on stdout, in `--json` output, in
  error messages, or in status lines.
- **Tokens are cached with `0600` permissions** under `~/.cache/tonys/token.json`
  (or `$XDG_CACHE_HOME`/`$TONYS_CACHE`), written atomically. The access token is
  refreshed via the OAuth refresh-token grant so credentials are not re-sent on
  every call. Run `tonys auth logout` to clear the cache, or `--no-cache` to skip
  it entirely.
- **stdout is reserved for data.** All secrets and diagnostics go to stderr, so
  piping `tonys --json ... | jq` can never leak a token into a log.
- **External tools** (`ffmpeg`, `yt-dlp`) are invoked with explicit argument
  vectors — never through a shell — and URLs are validated and passed after a
  `--` terminator so they cannot be interpreted as options.

## Things to keep private yourself

- Don't commit `.envrc`, `.env`, `config.json`, `token.json`, or
  `loudness.json` — they are listed in `.gitignore` for this reason.
- Treat the token cache like a password; anyone who can read it can act as you
  until the token expires.

## Supported versions

This is a young project; only the latest release receives security fixes.
