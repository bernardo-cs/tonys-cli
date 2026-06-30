package toniecloud

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenEntry is one account's cached credentials.
type TokenEntry struct {
	Username         string    `json:"username"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"`
}

// Valid reports whether the access token is still usable (with a safety skew).
func (e TokenEntry) Valid(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && e.ExpiresAt.After(now.Add(clockSkew))
}

// TokenCache is a tiny JSON file ({"entries": {key: entry}}) holding bearer
// tokens, written with 0600 permissions. It is keyed by the normalized
// (lowercased) username so multiple accounts can coexist.
type TokenCache struct {
	Path string

	mu      sync.Mutex
	loaded  bool
	entries map[string]TokenEntry
}

type cacheFile struct {
	Entries map[string]TokenEntry `json:"entries"`
}

// NewTokenCache returns a cache backed by path. A blank path yields an in-memory
// cache that is never persisted.
func NewTokenCache(path string) *TokenCache {
	return &TokenCache{Path: path, entries: map[string]TokenEntry{}}
}

func (c *TokenCache) ensureLoaded() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.entries == nil {
		c.entries = map[string]TokenEntry{}
	}
	if c.Path == "" {
		return
	}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return // missing/unreadable cache is not fatal
	}
	var cf cacheFile
	if json.Unmarshal(data, &cf) == nil && cf.Entries != nil {
		c.entries = cf.Entries
	}
}

// Get returns the entry for key, if present.
func (c *TokenCache) Get(key string) (TokenEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	e, ok := c.entries[key]
	return e, ok
}

// SoleKey returns the only key in the cache, if exactly one exists. This lets
// the CLI reuse a cached token when no username is supplied.
func (c *TokenCache) SoleKey() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	if len(c.entries) != 1 {
		return "", false
	}
	for k := range c.entries {
		return k, true
	}
	return "", false
}

// Entries returns a copy of all cached entries.
func (c *TokenCache) Entries() map[string]TokenEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	out := make(map[string]TokenEntry, len(c.entries))
	for k, v := range c.entries {
		out[k] = v
	}
	return out
}

// Put stores an entry and persists the cache.
func (c *TokenCache) Put(key string, e TokenEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	// Fold in entries another process may have written since we loaded, so a
	// multi-account cache does not lose tokens when invocations overlap.
	c.mergeFromDisk()
	c.entries[key] = e
	c.persist()
}

// Delete removes an entry (all entries if key is empty) and persists.
func (c *TokenCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	if key == "" {
		// Explicit "log out everything": wipe the whole cache.
		c.entries = map[string]TokenEntry{}
	} else {
		// Re-read first so deleting one account doesn't drop other accounts a
		// concurrent process added.
		c.mergeFromDisk()
		delete(c.entries, key)
	}
	c.persist()
}

// mergeFromDisk overlays on-disk entries that are absent from the in-memory map,
// preserving our own (newer) values for any shared keys.
func (c *TokenCache) mergeFromDisk() {
	if c.Path == "" {
		return
	}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return
	}
	var cf cacheFile
	if json.Unmarshal(data, &cf) != nil {
		return
	}
	for k, v := range cf.Entries {
		if _, ok := c.entries[k]; !ok {
			c.entries[k] = v
		}
	}
}

func (c *TokenCache) persist() {
	if c.Path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(cacheFile{Entries: c.entries}, "", "  ")
	if err != nil {
		return
	}
	// Atomic-ish write so a crash can't truncate the cache.
	tmp := c.Path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, c.Path)
}
