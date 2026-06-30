// Package ytdl wraps the yt-dlp (or youtube-dl) binary to list and download
// audio from videos and playlists.
package ytdl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Item is a single downloadable entry (a video).
type Item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Client wraps a yt-dlp executable.
type Client struct {
	Bin string
}

// New resolves the yt-dlp binary, honoring $TONYS_YTDLP / $TONIE_YTDLP, then
// yt-dlp, then youtube-dl on $PATH.
func New() *Client {
	if p := firstNonEmpty(os.Getenv("TONYS_YTDLP"), os.Getenv("TONIE_YTDLP")); p != "" {
		return &Client{Bin: p}
	}
	for _, name := range []string{"yt-dlp", "youtube-dl"} {
		if p, err := exec.LookPath(name); err == nil {
			return &Client{Bin: p}
		}
	}
	return &Client{}
}

// Available reports whether a yt-dlp binary was found.
func (c *Client) Available() bool { return c.Bin != "" }

// Version returns the yt-dlp version string, confirming the binary executes.
func (c *Client) Version(ctx context.Context) (string, error) {
	if !c.Available() {
		return "", errBinMissing
	}
	out, err := exec.CommandContext(ctx, c.Bin, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// metaJSON is the subset of `yt-dlp -J` output we use.
type metaJSON struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	URL        string      `json:"url"`
	WebpageURL string      `json:"webpage_url"`
	Entries    []*metaJSON `json:"entries"`
}

// List returns the items behind a URL: one for a single video, many for a
// playlist. It uses a flat (metadata-only) extraction for speed.
func (c *Client) List(ctx context.Context, rawURL string) ([]Item, error) {
	if !c.Available() {
		return nil, errBinMissing
	}
	if err := requireHTTPURL(rawURL); err != nil {
		return nil, err
	}
	// "--" terminates option parsing so a URL that begins with "-" can never be
	// interpreted as a yt-dlp flag (e.g. --exec, which would run commands).
	out, err := c.output(ctx, "-J", "--flat-playlist", "--no-warnings", "--", rawURL)
	if err != nil {
		return nil, err
	}
	return parseItems(out, rawURL)
}

// requireHTTPURL rejects anything that is not a plain http/https URL, so neither
// a malformed value nor a non-web scheme reaches yt-dlp.
func requireHTTPURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme in %q (only http and https are allowed)", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid URL %q: missing host", raw)
	}
	return nil
}

// parseItems turns `yt-dlp -J` output into a flat list of items.
func parseItems(out []byte, fallbackURL string) ([]Item, error) {
	var meta metaJSON
	if err := json.Unmarshal(out, &meta); err != nil {
		return nil, fmt.Errorf("parse yt-dlp metadata: %w", err)
	}
	if len(meta.Entries) > 0 {
		var items []Item
		for _, e := range meta.Entries {
			if e == nil {
				continue // deleted/unavailable entry
			}
			it := itemFromMeta(e)
			if it.URL != "" {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("no downloadable items found in playlist")
		}
		return items, nil
	}
	it := itemFromMeta(&meta)
	if it.URL == "" {
		it.URL = fallbackURL
	}
	return []Item{it}, nil
}

func itemFromMeta(m *metaJSON) Item {
	url := firstNonEmpty(m.WebpageURL, m.URL)
	if url == "" && m.ID != "" {
		url = "https://www.youtube.com/watch?v=" + m.ID
	}
	title := m.Title
	if title == "" {
		title = m.ID
	}
	return Item{ID: m.ID, Title: title, URL: url}
}

// DownloadAudio downloads the best audio stream of item and returns the
// resulting file path. Each item is downloaded into its own subdirectory of
// destDir so the produced file is unambiguous even when yt-dlp cannot supply a
// stable id (which previously risked returning a different item's file).
func (c *Client) DownloadAudio(ctx context.Context, item Item, destDir string) (string, error) {
	if !c.Available() {
		return "", errBinMissing
	}
	if err := requireHTTPURL(item.URL); err != nil {
		return "", err
	}
	itemDir, err := os.MkdirTemp(destDir, "item-*")
	if err != nil {
		return "", err
	}
	_, err = c.output(ctx,
		"-f", "bestaudio/best",
		"--no-playlist",
		"--no-warnings",
		"--no-progress",
		"-o", filepath.Join(itemDir, "audio.%(ext)s"),
		"--", item.URL,
	)
	if err != nil {
		return "", err
	}
	produced, err := soleAudioFile(itemDir)
	if err != nil {
		return "", fmt.Errorf("%w (item %q)", err, item.Title)
	}
	return produced, nil
}

// soleAudioFile returns the single non-temporary file produced in dir. It reads
// the directory directly (rather than globbing) so a destDir containing glob
// metacharacters like [ ] * ? does not make the lookup silently miss the file.
func soleAudioFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := e.Name()
		if strings.HasSuffix(base, ".part") || strings.HasSuffix(base, ".ytdl") || strings.HasSuffix(base, ".tmp") {
			continue
		}
		candidates = append(candidates, filepath.Join(dir, base))
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("yt-dlp produced no file")
	case 1:
		return candidates[0], nil
	default:
		// Multiple outputs (rare); pick the largest.
		best, bestSize := candidates[0], int64(-1)
		for _, p := range candidates {
			if fi, statErr := os.Stat(p); statErr == nil && fi.Size() > bestSize {
				best, bestSize = p, fi.Size()
			}
		}
		return best, nil
	}
}

func (c *Client) output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("yt-dlp: %s", lastLine(msg))
	}
	return []byte(stdout.String()), nil
}

var errBinMissing = fmt.Errorf("yt-dlp not found: install it (e.g. `brew install yt-dlp` or `pip install yt-dlp`) or set $TONYS_YTDLP")

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
