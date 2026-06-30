package toniecloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ErrChapterDownloadUnavailable is returned when a chapter's audio cannot be
// downloaded (no available endpoint, or a content-token chapter).
var ErrChapterDownloadUnavailable = errors.New("chapter audio download not available")

// ChapterDownloadURL resolves a temporary URL to a chapter's audio, if the
// backend exposes one. The endpoint used here is discovered empirically; if the
// account/plan does not expose it, ErrChapterDownloadUnavailable is returned.
func (c *Client) ChapterDownloadURL(ctx context.Context, t CreativeTonie, ch Chapter) (string, error) {
	if ch.IsContentToken() {
		return "", fmt.Errorf("%w: chapter %q is tonies-published content", ErrChapterDownloadUnavailable, ch.Title)
	}
	// Candidate endpoints, tried in order. The first that returns JSON with a
	// usable URL wins. Discovered/confirmed against the live API.
	for _, ep := range chapterURLEndpoints(t, ch) {
		raw, err := c.Raw(ctx, http.MethodGet, ep, nil)
		if err != nil {
			continue
		}
		if u := extractURL(raw); u != "" {
			return u, nil
		}
	}
	return "", ErrChapterDownloadUnavailable
}

// DownloadChapter writes a chapter's audio to destPath.
func (c *Client) DownloadChapter(ctx context.Context, t CreativeTonie, ch Chapter, destPath string) error {
	url, err := c.ChapterDownloadURL(ctx, t, ch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Presigned URLs are self-authenticating; others may need the bearer token.
	if c.Auth != nil {
		if tok, terr := c.Auth.Token(ctx); terr == nil {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return newAPIError(http.MethodGet, url, resp.StatusCode, body)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// chapterURLEndpoints lists candidate API paths that may yield a download URL.
func chapterURLEndpoints(t CreativeTonie, ch Chapter) []string {
	base := "households/" + t.HouseholdID + "/creativetonies/" + t.ID + "/chapters/" + ch.ID
	return []string{base, base + "/url", base + "/download"}
}

// extractURL finds the first URL-looking string value in a JSON object.
func extractURL(raw []byte) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"url", "downloadUrl", "fileUrl", "audioUrl", "href"} {
		if v, ok := m[k].(string); ok && strings.HasPrefix(v, "http") {
			return v
		}
	}
	return ""
}
