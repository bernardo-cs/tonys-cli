package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

// resolveTestApp builds an App whose TonieCloud client points at srv and always
// presents a valid (cached) token, so resolution logic can be tested offline.
func resolveTestApp(srv *httptest.Server) *App {
	cache := toniecloud.NewTokenCache("")
	cache.Put("u", toniecloud.TokenEntry{Username: "u", AccessToken: "test-token", ExpiresAt: time.Now().Add(time.Hour)})
	auth := &toniecloud.Authenticator{Username: "u", Cache: cache}
	a := &App{Output: "json", Stdout: io.Discard, Stderr: io.Discard}
	a.client = toniecloud.New(srv.URL, auth, srv.Client())
	return a
}

func tonieServer(t *testing.T, householdsJSON, toniesJSON string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/households", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, householdsJSON)
	})
	mux.HandleFunc("/households/h1/creativetonies", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, toniesJSON)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestResolveTonie(t *testing.T) {
	srv := tonieServer(t,
		`[{"id":"h1","name":"Home","access":"owner","canLeave":true}]`,
		`[{"id":"t1","householdId":"h1","name":"Erna"},
		  {"id":"t2","householdId":"h1","name":"Erna"},
		  {"id":"t3","householdId":"h1","name":"Bert"}]`)
	a := resolveTestApp(srv)
	ctx := context.Background()

	// id wins, even though "Erna" by name is ambiguous.
	if got, err := a.resolveTonie(ctx, "t1", ""); err != nil || got.ID != "t1" {
		t.Fatalf("by id = %+v, %v; want t1", got, err)
	}

	// unambiguous, case-insensitive name.
	if got, err := a.resolveTonie(ctx, "bert", ""); err != nil || got.ID != "t3" {
		t.Fatalf("by name = %+v, %v; want t3", got, err)
	}

	// ambiguous name → usage error (exit 2), per the documented contract.
	_, err := a.resolveTonie(ctx, "Erna", "")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("ambiguous tonie exit code = %d (err=%v), want 2", ExitCode(err), err)
	}

	// missing → not-found (exit 4).
	_, err = a.resolveTonie(ctx, "Nope", "")
	if err == nil || ExitCode(err) != 4 {
		t.Fatalf("missing tonie exit code = %d (err=%v), want 4", ExitCode(err), err)
	}
}

func TestResolveHouseholdAmbiguous(t *testing.T) {
	srv := tonieServer(t,
		`[{"id":"h1","name":"Home","access":"owner","canLeave":true},
		  {"id":"h2","name":"Home","access":"owner","canLeave":true}]`,
		`[]`)
	a := resolveTestApp(srv)
	ctx := context.Background()

	if got, err := a.resolveHousehold(ctx, "h2"); err != nil || got.ID != "h2" {
		t.Fatalf("by id = %+v, %v; want h2", got, err)
	}

	_, err := a.resolveHousehold(ctx, "Home")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("ambiguous household exit code = %d (err=%v), want 2", ExitCode(err), err)
	}

	_, err = a.resolveHousehold(ctx, "Missing")
	if err == nil || ExitCode(err) != 4 {
		t.Fatalf("missing household exit code = %d (err=%v), want 4", ExitCode(err), err)
	}
}
