package toniecloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultAPIURL is the TonieCloud REST base URL.
const DefaultAPIURL = "https://api.tonie.cloud/v2"

// UserAgent is sent with every request; overridable by the CLI.
var UserAgent = "tonys-cli"

// Client talks to the TonieCloud REST API. Construct it with New.
type Client struct {
	APIURL string
	HTTP   *http.Client
	Auth   *Authenticator
}

// New builds a Client. A nil httpClient uses http.DefaultClient.
func New(apiURL string, auth *Authenticator, httpClient *http.Client) *Client {
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{APIURL: strings.TrimRight(apiURL, "/"), HTTP: httpClient, Auth: auth}
}

// do issues an authenticated request. body, if non-nil, is JSON-encoded. out, if
// non-nil, receives the decoded JSON response. The raw response bytes are
// returned for callers that want them.
func (c *Client) do(ctx context.Context, method, path string, body, out any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	full := c.APIURL + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, full, reader)
	if err != nil {
		return nil, err
	}

	if c.Auth != nil {
		tok, err := c.Auth.Token(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, full, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode/100 != 2 {
		return respBody, newAPIError(method, full, resp.StatusCode, respBody)
	}
	if out != nil && len(bytes.TrimSpace(respBody)) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return respBody, fmt.Errorf("decode %s %s response: %w", method, full, err)
		}
	}
	return respBody, nil
}

func newAPIError(method, url string, status int, body []byte) *APIError {
	e := &APIError{Method: method, URL: url, Status: status, Body: string(body)}
	// TonieCloud returns {"detail": "..."}; Keycloak {"error_description": "..."}.
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		for _, k := range []string{"detail", "message", "error_description", "error"} {
			if v, ok := m[k].(string); ok && v != "" {
				e.Detail = v
				break
			}
		}
	}
	return e
}

// Me returns the logged-in user (GET /me).
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, "me", nil, &u)
	return u, err
}

// GetConfig returns the backend configuration (GET /config).
func (c *Client) GetConfig(ctx context.Context) (Config, error) {
	var cfg Config
	_, err := c.do(ctx, http.MethodGet, "config", nil, &cfg)
	return cfg, err
}

// Households returns all households of the logged-in user (GET /households).
func (c *Client) Households(ctx context.Context) ([]Household, error) {
	var hs []Household
	_, err := c.do(ctx, http.MethodGet, "households", nil, &hs)
	return hs, err
}

// CreativeToniesByHousehold lists all creative tonies in one household.
func (c *Client) CreativeToniesByHousehold(ctx context.Context, householdID string) ([]CreativeTonie, error) {
	var ts []CreativeTonie
	_, err := c.do(ctx, http.MethodGet, "households/"+householdID+"/creativetonies", nil, &ts)
	return ts, err
}

// CreativeTonies lists every creative tonie across all of the user's households.
func (c *Client) CreativeTonies(ctx context.Context) ([]CreativeTonie, error) {
	households, err := c.Households(ctx)
	if err != nil {
		return nil, err
	}
	var out []CreativeTonie
	for _, h := range households {
		ts, err := c.CreativeToniesByHousehold(ctx, h.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ts...)
	}
	return out, nil
}

// CreativeTonie fetches a single creative tonie
// (GET /households/{hid}/creativetonies/{id}).
func (c *Client) CreativeTonie(ctx context.Context, householdID, tonieID string) (CreativeTonie, error) {
	var t CreativeTonie
	_, err := c.do(ctx, http.MethodGet, "households/"+householdID+"/creativetonies/"+tonieID, nil, &t)
	return t, err
}

// AddChapter appends an already-uploaded file as a new chapter
// (POST /households/{hid}/creativetonies/{id}/chapters).
func (c *Client) AddChapter(ctx context.Context, t CreativeTonie, fileID, title string) error {
	_, err := c.do(ctx, http.MethodPost,
		"households/"+t.HouseholdID+"/creativetonies/"+t.ID+"/chapters",
		map[string]string{"title": title, "file": fileID}, nil)
	return err
}

// SetChapters replaces the chapter list (and order) of a tonie via PATCH. This
// powers sorting, removing and renaming chapters and clearing the tonie.
func (c *Client) SetChapters(ctx context.Context, t CreativeTonie, chapters []Chapter) (CreativeTonie, error) {
	if chapters == nil {
		chapters = []Chapter{}
	}
	var updated CreativeTonie
	_, err := c.do(ctx, http.MethodPatch,
		"households/"+t.HouseholdID+"/creativetonies/"+t.ID,
		map[string]any{"chapters": chapters}, &updated)
	return updated, err
}

// RenameTonie changes a tonie's display name via PATCH.
func (c *Client) RenameTonie(ctx context.Context, t CreativeTonie, name string) (CreativeTonie, error) {
	var updated CreativeTonie
	_, err := c.do(ctx, http.MethodPatch,
		"households/"+t.HouseholdID+"/creativetonies/"+t.ID,
		map[string]any{"name": name}, &updated)
	return updated, err
}

// Patch applies an arbitrary set of fields to a tonie via PATCH.
func (c *Client) Patch(ctx context.Context, t CreativeTonie, fields map[string]any) (CreativeTonie, error) {
	var updated CreativeTonie
	_, err := c.do(ctx, http.MethodPatch,
		"households/"+t.HouseholdID+"/creativetonies/"+t.ID, fields, &updated)
	return updated, err
}

// CreateFileUpload requests a presigned S3 upload slot (POST /file).
func (c *Client) CreateFileUpload(ctx context.Context) (FileUploadRequest, error) {
	var fr FileUploadRequest
	_, err := c.do(ctx, http.MethodPost, "file", map[string]any{}, &fr)
	return fr, err
}

// Raw performs an arbitrary authenticated API call and returns the raw JSON
// response. It is the escape hatch that makes every endpoint reachable, even
// ones without a dedicated method. method is GET/POST/PATCH/etc.; path is
// relative to the API base (leading slash optional); body is optional JSON.
func (c *Client) Raw(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	raw, err := c.do(ctx, strings.ToUpper(method), path, body, nil)
	return json.RawMessage(raw), err
}
