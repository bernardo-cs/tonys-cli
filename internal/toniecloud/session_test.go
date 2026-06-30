package toniecloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func tokenServer(t *testing.T, count *int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*count++
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		switch form.Get("grant_type") {
		case "password":
			if form.Get("username") != "user" || form.Get("password") != "pw" {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":"invalid_grant","error_description":"bad creds"}`)
				return
			}
			io.WriteString(w, `{"access_token":"acc-1","expires_in":3600,"refresh_token":"ref-1","refresh_expires_in":7200}`)
		case "refresh_token":
			if form.Get("refresh_token") != "ref-1" {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"access_token":"acc-2","expires_in":3600,"refresh_token":"ref-2","refresh_expires_in":7200}`)
		default:
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPasswordGrantAndCache(t *testing.T) {
	var count int
	srv := tokenServer(t, &count)
	cache := NewTokenCache("")
	auth := &Authenticator{TokenURL: srv.URL, Username: "user", Password: "pw", HTTP: srv.Client(), Cache: cache}

	tok, err := auth.Token(context.Background())
	if err != nil || tok != "acc-1" {
		t.Fatalf("first token: %q err=%v", tok, err)
	}
	// Second call should be served from cache (no extra request).
	tok2, err := auth.Token(context.Background())
	if err != nil || tok2 != "acc-1" {
		t.Fatalf("cached token: %q err=%v", tok2, err)
	}
	if count != 1 {
		t.Fatalf("expected 1 token request, got %d", count)
	}
}

func TestRefreshWhenExpired(t *testing.T) {
	var count int
	srv := tokenServer(t, &count)
	cache := NewTokenCache("")
	now := time.Now()
	auth := &Authenticator{
		TokenURL: srv.URL, Username: "user", Password: "pw", HTTP: srv.Client(), Cache: cache,
		now: func() time.Time { return now },
	}
	// Seed an expired access token with a valid refresh token.
	cache.Put("user", TokenEntry{
		Username:         "user",
		AccessToken:      "old",
		RefreshToken:     "ref-1",
		ExpiresAt:        now.Add(-time.Minute),
		RefreshExpiresAt: now.Add(time.Hour),
	})
	tok, err := auth.Token(context.Background())
	if err != nil || tok != "acc-2" {
		t.Fatalf("refresh token: %q err=%v", tok, err)
	}
	if count != 1 {
		t.Fatalf("expected 1 refresh request, got %d", count)
	}
}

func TestRefreshPreservesUsername(t *testing.T) {
	var count int
	srv := tokenServer(t, &count)
	cache := NewTokenCache("")
	now := time.Now()
	cache.Put("user", TokenEntry{
		Username:         "user",
		AccessToken:      "old",
		RefreshToken:     "ref-1",
		ExpiresAt:        now.Add(-time.Minute),
		RefreshExpiresAt: now.Add(time.Hour),
	})
	// No username supplied → sole-key reuse + refresh must keep the identity.
	auth := &Authenticator{TokenURL: srv.URL, HTTP: srv.Client(), Cache: cache, now: func() time.Time { return now }}
	if _, err := auth.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	e, _ := cache.Get("user")
	if e.Username != "user" {
		t.Fatalf("username not preserved after refresh: %q", e.Username)
	}
	if e.AccessToken != "acc-2" {
		t.Fatalf("token not refreshed: %q", e.AccessToken)
	}
}

func TestBadCredentials(t *testing.T) {
	var count int
	srv := tokenServer(t, &count)
	auth := &Authenticator{TokenURL: srv.URL, Username: "user", Password: "wrong", HTTP: srv.Client(), Cache: NewTokenCache("")}
	_, err := auth.Token(context.Background())
	if err == nil {
		t.Fatal("expected auth error")
	}
	var apiErr *APIError
	if !IsUnauthorized(err) && apiErr == nil {
		t.Fatalf("expected unauthorized-ish error, got %v", err)
	}
}

func TestNoCredentialsNoCache(t *testing.T) {
	auth := &Authenticator{Cache: NewTokenCache("")}
	_, err := auth.Token(context.Background())
	if err != ErrNotAuthenticated {
		t.Fatalf("expected ErrNotAuthenticated, got %v", err)
	}
}

func TestSoleKeyReuse(t *testing.T) {
	cache := NewTokenCache("")
	cache.Put("only@user", TokenEntry{Username: "only@user", AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)})
	// No username supplied → should reuse the sole cached entry.
	auth := &Authenticator{Cache: cache}
	tok, err := auth.Token(context.Background())
	if err != nil || tok != "tok" {
		t.Fatalf("sole-key reuse: %q err=%v", tok, err)
	}
}
