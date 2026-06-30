package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
	"github.com/bernardo-cs/tonys-cli/internal/ytdl"
)

// probeTimeout bounds each external `-version` call so a hung binary can't block
// the whole diagnostic.
const probeTimeout = 15 * time.Second

// checkResult is one diagnostic line.
type checkResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
	Remedy string `json:"remedy,omitempty"`
}

type doctorReport struct {
	Ready  bool          `json:"ready"`
	Checks []checkResult `json:"checks"`
}

func doctorCommand() *Command {
	return &Command{
		Name:    "doctor",
		Summary: "Check the environment and dependencies (ffmpeg, yt-dlp, creds, paths)",
		Long: `Verify everything tonys needs is in place. External tools (ffmpeg for
conversion/normalization, yt-dlp for the YouTube importer) are NOT bundled in the
binary — they must be installed on the system; doctor reports whether they are
found and runnable.

By default only local checks run (no network). Use --online to also verify the
credentials log in and the API is reachable. Use --strict to make any non-ok
check exit non-zero (handy for provisioning scripts):

  tonys doctor --strict || echo "install missing deps"`,
		Flags: []FlagSpec{
			{Name: "online", Usage: "also verify login + API reachability", Bool: true},
			{Name: "strict", Usage: "exit non-zero if any check is not ok", Bool: true},
		},
		Run: doctorRun,
	}
}

func doctorRun(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
	var checks []checkResult
	auth := a.authenticator()

	// --- ffmpeg (conversion & loudness normalization) ---
	conv := audio.NewConverter()
	if !conv.Available() {
		checks = append(checks, checkResult{
			Name:   "ffmpeg",
			Status: "warn",
			Detail: "not found on PATH — required for --convert and --normalize (plain uploads of accepted formats still work)",
			Remedy: installHint("ffmpeg"),
		})
	} else if ver, err := versionWithTimeout(ctx, conv.Version); err != nil {
		checks = append(checks, checkResult{
			Name:   "ffmpeg",
			Status: "fail",
			Detail: fmt.Sprintf("found at %s but failed to run: %v", conv.FFmpegPath, err),
			Remedy: installHint("ffmpeg"),
		})
	} else {
		checks = append(checks, checkResult{Name: "ffmpeg", Status: "ok", Detail: fmt.Sprintf("%s (%s)", ver, conv.FFmpegPath)})
	}

	// --- yt-dlp (YouTube importer) ---
	yt := ytdl.New()
	if !yt.Available() {
		checks = append(checks, checkResult{
			Name:   "yt-dlp",
			Status: "warn",
			Detail: "not found on PATH — required only for the `yt` command",
			Remedy: installHint("yt-dlp"),
		})
	} else if ver, err := versionWithTimeout(ctx, yt.Version); err != nil {
		checks = append(checks, checkResult{
			Name:   "yt-dlp",
			Status: "fail",
			Detail: fmt.Sprintf("found at %s but failed to run: %v", yt.Bin, err),
			Remedy: installHint("yt-dlp"),
		})
	} else {
		checks = append(checks, checkResult{Name: "yt-dlp", Status: "ok", Detail: fmt.Sprintf("yt-dlp %s (%s)", ver, yt.Bin)})
	}

	// --- credentials ---
	switch {
	case a.Username != "" && a.Password != "":
		checks = append(checks, checkResult{Name: "credentials", Status: "ok", Detail: "username and password resolved for " + a.Username})
	case a.Username != "":
		checks = append(checks, checkResult{Name: "credentials", Status: "warn", Detail: "username set but no password", Remedy: "set TONIE_PASSWORD"})
	default:
		checks = append(checks, checkResult{Name: "credentials", Status: "warn", Detail: "no credentials resolved", Remedy: "set TONIE_USERNAME and TONIE_PASSWORD (env), or pass --username/--password"})
	}

	// --- writable state dirs (no directory is created as a side effect) ---
	checks = append(checks, dirCheck("token cache", filepath.Dir(a.CachePath), a.NoCache))
	checks = append(checks, dirCheck("loudness ledger", filepath.Dir(a.loudnessCachePath()), false))

	// --- cached token, if any (resolve the key the way the auth layer does) ---
	if !a.NoCache && auth.Cache != nil {
		if key := cacheKey(a.Username, auth.Cache); key != "" {
			if e, ok := auth.Cache.Get(key); ok && e.AccessToken != "" {
				status, detail := "ok", "cached token present for "+e.Username
				if !e.Valid(time.Now()) {
					status, detail = "warn", "cached token for "+e.Username+" expired (will refresh on next call)"
				}
				checks = append(checks, checkResult{Name: "session token", Status: status, Detail: detail})
			}
		}
	}

	// --- online checks ---
	if fbool(fs, "online") {
		checks = append(checks, a.onlineChecks(ctx, auth)...)
	}

	report := doctorReport{Ready: true, Checks: checks}
	worst := "ok"
	for _, c := range checks {
		if c.Status == "fail" || (c.Status == "warn" && fbool(fs, "strict")) {
			report.Ready = false
		}
		worst = worseStatus(worst, c.Status)
	}

	if err := a.emit(report, func(w io.Writer) {
		tw := table(w)
		fmt.Fprintf(tw, "CHECK\tSTATUS\tDETAIL\n")
		for _, c := range checks {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, symbolFor(c.Status), c.Detail)
		}
		tw.Flush()
		for _, c := range checks {
			if c.Status != "ok" && c.Remedy != "" {
				fmt.Fprintf(w, "  ↳ %s: %s\n", c.Name, c.Remedy)
			}
		}
	}); err != nil {
		return err
	}
	if !report.Ready {
		// Non-zero exit so provisioning scripts can gate on it.
		return fmt.Errorf("doctor: environment not ready (worst status: %s)", worst)
	}
	return nil
}

// cacheKey resolves the token-cache key exactly as Authenticator.resolveKey does:
// the normalized username, or the sole cached entry when no username is set.
func cacheKey(username string, cache *toniecloud.TokenCache) string {
	if k := normalizeUser(username); k != "" {
		return k
	}
	if only, ok := cache.SoleKey(); ok {
		return only
	}
	return ""
}

// versionWithTimeout runs a `-version` probe under its own deadline so a hung
// binary surfaces as an error instead of blocking doctor forever.
func versionWithTimeout(ctx context.Context, get func(context.Context) (string, error)) (string, error) {
	vctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return get(vctx)
}

// onlineChecks verifies authentication (via the real auth path, which can reuse a
// cached/refresh token without explicit credentials) and API reachability.
func (a *App) onlineChecks(ctx context.Context, auth *toniecloud.Authenticator) []checkResult {
	if _, err := auth.Token(ctx); err != nil {
		if errors.Is(err, toniecloud.ErrNotAuthenticated) {
			return []checkResult{{Name: "api login", Status: "warn", Detail: "skipped: no credentials and no usable cached token", Remedy: "set TONIE_USERNAME and TONIE_PASSWORD"}}
		}
		return []checkResult{{Name: "api login", Status: "fail", Detail: err.Error(), Remedy: "check credentials and network"}}
	}
	out := []checkResult{{Name: "api login", Status: "ok", Detail: "token acquired"}}
	if u, err := a.Client().Me(ctx); err != nil {
		out = append(out, checkResult{Name: "api reachable", Status: "fail", Detail: err.Error()})
	} else {
		out = append(out, checkResult{Name: "api reachable", Status: "ok", Detail: "GET /me ok (" + u.Email + ")"})
	}
	return out
}

// dirCheck reports whether a state directory is (or can become) writable, without
// creating it as a side effect.
func dirCheck(name, dir string, skipped bool) checkResult {
	if skipped {
		return checkResult{Name: name, Status: "ok", Detail: "disabled (--no-cache)"}
	}
	if !filepath.IsAbs(dir) {
		return checkResult{
			Name:   name,
			Status: "warn",
			Detail: "path is not absolute (home dir unresolved): " + dir,
			Remedy: "set $HOME, or an explicit TONYS_CACHE / TONYS_LOUDNESS_DB path",
		}
	}
	info, err := os.Stat(dir)
	switch {
	case err == nil && info.IsDir():
		if probeWritable(dir) {
			return checkResult{Name: name, Status: "ok", Detail: "writable: " + dir}
		}
		return checkResult{Name: name, Status: "fail", Detail: "not writable: " + dir}
	case err == nil:
		return checkResult{Name: name, Status: "fail", Detail: "not a directory: " + dir}
	default:
		// Missing: report based on whether the nearest existing parent is writable.
		if nearestWritable(dir) {
			return checkResult{Name: name, Status: "ok", Detail: "will be created on first use: " + dir}
		}
		return checkResult{Name: name, Status: "warn", Detail: "missing and parent not writable: " + dir}
	}
}

// probeWritable checks dir is writable by creating and removing a temp file.
func probeWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".tonys-doctor-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// nearestWritable walks up to the nearest existing ancestor of dir and reports
// whether a file can be created there (so dir could be made on first use).
func nearestWritable(dir string) bool {
	for d := dir; ; {
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		if info, err := os.Stat(parent); err == nil {
			return info.IsDir() && probeWritable(parent)
		}
		d = parent
	}
}

// installHint returns an OS-appropriate install command for a package.
func installHint(pkg string) string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install " + pkg
	case "windows":
		return "winget install " + pkg + "  (or choco install " + pkg + ")"
	default: // linux & friends
		if pkg == "yt-dlp" {
			return "pipx install yt-dlp  (or apt-get install yt-dlp)"
		}
		return "apt-get install " + pkg + "  (or your distro's equivalent)"
	}
}

func symbolFor(status string) string {
	switch status {
	case "ok":
		return "ok"
	case "warn":
		return "WARN"
	default:
		return "FAIL"
	}
}

// worseStatus returns the more severe of two statuses (ok < warn < fail).
func worseStatus(a, b string) string {
	rank := map[string]int{"ok": 0, "warn": 1, "fail": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
