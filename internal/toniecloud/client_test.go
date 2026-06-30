package toniecloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newMockServer stands in for both the TonieCloud API and the S3 upload bucket.
func newMockServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/v2/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"uuid":"u-1","email":"a@b.c","firstName":"Jo","locale":"de"}`)
	})
	mux.HandleFunc("/v2/config", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"locales":["de"],"unicodeLocales":["de"],"maxChapters":99,"maxSeconds":5400,"maxBytes":1073741824,"accepts":["mp3","wav"],"stageWarning":false,"paypalClientId":"x","ssoEnabled":true}`)
	})
	mux.HandleFunc("/v2/households", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":"h1","name":"Home","ownerName":"Jo","access":"owner","canLeave":true}]`)
	})
	mux.HandleFunc("/v2/households/h1/creativetonies", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":"t1","householdId":"h1","name":"Erna","imageUrl":"","secondsRemaining":5000,"secondsPresent":400,"chaptersRemaining":98,"chaptersPresent":1,"transcoding":false,"lastUpdate":null,"chapters":[{"id":"c1","title":"One","file":"f_c1","seconds":12.5,"transcoding":false}]}]`)
	})
	mux.HandleFunc("/v2/households/h1/creativetonies/t1", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			io.WriteString(w, `{"id":"t1","householdId":"h1","name":"Erna","imageUrl":"","secondsRemaining":5000,"secondsPresent":400,"chaptersRemaining":98,"chaptersPresent":1,"transcoding":false,"lastUpdate":null,"chapters":[{"id":"c1","title":"One","file":"f_c1","seconds":12.5,"transcoding":false}]}`)
		case http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			var m map[string]any
			json.Unmarshal(body, &m)
			// Echo back name/chapters changes.
			name := "Erna"
			if v, ok := m["name"].(string); ok {
				name = v
			}
			resp := map[string]any{"id": "t1", "householdId": "h1", "name": name, "chapters": m["chapters"]}
			if m["chapters"] == nil {
				resp["chapters"] = []any{}
			}
			json.NewEncoder(w).Encode(resp)
		}
	})
	mux.HandleFunc("/v2/households/h1/creativetonies/t1/chapters", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !json.Valid(body) {
			w.WriteHeader(415)
			return
		}
		w.WriteHeader(200)
		io.WriteString(w, `{}`)
	})
	// POST /file → presigned upload pointing back at this same server.
	mux.HandleFunc("/v2/file", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host + "/s3"
		json.NewEncoder(w).Encode(map[string]any{
			"fileId": "file-123",
			"request": map[string]any{
				"url":    base,
				"fields": map[string]string{"key": "file-123", "policy": "p"},
			},
		})
	})
	mux.HandleFunc("/s3", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(204)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testClient(srv *httptest.Server) *Client {
	auth := &Authenticator{} // no auth needed; we set token manually via RoundTrip
	c := New(srv.URL+"/v2", auth, srv.Client())
	// Replace the authenticator with one that yields a static token.
	c.Auth = staticAuth("test-token")
	return c
}

// staticAuth returns an Authenticator whose Token always yields tok.
func staticAuth(tok string) *Authenticator {
	cache := NewTokenCache("")
	cache.Put("u", TokenEntry{Username: "u", AccessToken: tok, ExpiresAt: farFuture()})
	return &Authenticator{Username: "u", Cache: cache}
}

func farFuture() time.Time { return time.Now().Add(24 * time.Hour) }

func TestMeConfigHouseholds(t *testing.T) {
	srv := newMockServer(t)
	c := testClient(srv)
	ctx := context.Background()

	u, err := c.Me(ctx)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if u.UUID != "u-1" || u.Email != "a@b.c" {
		t.Fatalf("unexpected user: %+v", u)
	}
	if u.Extra["firstName"] != "Jo" {
		t.Fatalf("Extra not preserved: %+v", u.Extra)
	}

	cfg, err := c.GetConfig(ctx)
	if err != nil || cfg.MaxChapters != 99 || len(cfg.Accepts) != 2 {
		t.Fatalf("config: %+v err=%v", cfg, err)
	}

	hs, err := c.Households(ctx)
	if err != nil || len(hs) != 1 || hs[0].ID != "h1" {
		t.Fatalf("households: %+v err=%v", hs, err)
	}
}

func TestCreativeToniesAndChapters(t *testing.T) {
	srv := newMockServer(t)
	c := testClient(srv)
	ctx := context.Background()

	tonies, err := c.CreativeTonies(ctx)
	if err != nil || len(tonies) != 1 {
		t.Fatalf("tonies: %+v err=%v", tonies, err)
	}
	if tonies[0].Chapters[0].Title != "One" {
		t.Fatalf("chapter parse: %+v", tonies[0].Chapters)
	}

	one, err := c.CreativeTonie(ctx, "h1", "t1")
	if err != nil || one.Name != "Erna" {
		t.Fatalf("single tonie: %+v err=%v", one, err)
	}

	updated, err := c.RenameTonie(ctx, one, "NewName")
	if err != nil || updated.Name != "NewName" {
		t.Fatalf("rename: %+v err=%v", updated, err)
	}

	cleared, err := c.SetChapters(ctx, one, nil)
	if err != nil || len(cleared.Chapters) != 0 {
		t.Fatalf("clear: %+v err=%v", cleared, err)
	}
}

func TestUploadFlow(t *testing.T) {
	srv := newMockServer(t)
	c := testClient(srv)
	ctx := context.Background()

	dir := t.TempDir()
	f := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(f, []byte("ID3fake-audio-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	tonie := CreativeTonie{ID: "t1", HouseholdID: "h1"}
	res, err := c.UploadFile(ctx, tonie, f, "")
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if res.FileID != "file-123" {
		t.Fatalf("fileId: %q", res.FileID)
	}
	if res.Title != "song" { // default title from filename
		t.Fatalf("default title: %q", res.Title)
	}
}

func TestAPIErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"detail":"nope"}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, staticAuth("x"), srv.Client())

	_, err := c.Me(context.Background())
	if err == nil || !IsNotFound(err) {
		t.Fatalf("expected not-found error, got %v", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("detail not surfaced: %v", err)
	}
}
