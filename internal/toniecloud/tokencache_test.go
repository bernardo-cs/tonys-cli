package toniecloud

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func entry(user, tok string) TokenEntry {
	return TokenEntry{Username: user, AccessToken: tok, ExpiresAt: time.Now().Add(time.Hour)}
}

func TestTokenCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	c := NewTokenCache(path)
	c.Put("alice", entry("alice", "tok-a"))

	// A fresh cache at the same path must read the persisted entry back.
	c2 := NewTokenCache(path)
	got, ok := c2.Get("alice")
	if !ok || got.AccessToken != "tok-a" {
		t.Fatalf("round-trip = %+v, ok=%v", got, ok)
	}
}

func TestTokenCachePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file-mode bits not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "token.json")
	NewTokenCache(path).Put("alice", entry("alice", "tok"))

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token cache perms = %o, want 600", perm)
	}
}

func TestTokenCacheDeletePreservesOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	c := NewTokenCache(path)
	c.Put("alice", entry("alice", "a"))
	c.Put("bob", entry("bob", "b"))

	c.Delete("alice")

	c2 := NewTokenCache(path)
	if _, ok := c2.Get("alice"); ok {
		t.Error("alice should be deleted")
	}
	if _, ok := c2.Get("bob"); !ok {
		t.Error("bob should survive deletion of alice")
	}
}

// TestTokenCacheMergesConcurrentWrites simulates two overlapping invocations:
// one loads the (empty) cache, another writes an entry, and the first then
// writes its own — the second's entry must not be clobbered.
func TestTokenCacheMergesConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")

	c1 := NewTokenCache(path)
	if _, ok := c1.Get("none"); ok { // force c1 to load the (empty) file
		t.Fatal("unexpected entry")
	}

	// Another process writes "bob" after c1 has already loaded.
	NewTokenCache(path).Put("bob", entry("bob", "b"))

	// c1 writes "alice"; bob must be preserved via the re-read-and-merge.
	c1.Put("alice", entry("alice", "a"))

	final := NewTokenCache(path)
	if _, ok := final.Get("alice"); !ok {
		t.Error("alice lost")
	}
	if _, ok := final.Get("bob"); !ok {
		t.Error("bob clobbered by concurrent write (merge failed)")
	}
}
