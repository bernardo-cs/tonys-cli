package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// loudnessRecord is one chapter's measured integrated loudness.
type loudnessRecord struct {
	ChapterID      string  `json:"chapterId"`
	TonieID        string  `json:"tonieId"`
	Title          string  `json:"title"`
	IntegratedLUFS float64 `json:"integratedLufs"`
	Source         string  `json:"source"` // "upload" | "cloud"
	At             string  `json:"at"`
}

// loudnessCache persists measured chapter loudness keyed by chapter id, so
// `--normalize match` can target the real level of what is already on a tonie
// without re-downloading/re-measuring every time. (The chapter id is stable; the
// upload fileId is not the same value.)
type loudnessCache struct {
	Path string

	mu     sync.Mutex
	loaded bool
	byFile map[string]loudnessRecord
}

type loudnessCacheFile struct {
	Records map[string]loudnessRecord `json:"records"`
}

func newLoudnessCache(path string) *loudnessCache {
	return &loudnessCache{Path: path, byFile: map[string]loudnessRecord{}}
}

func (c *loudnessCache) ensureLoaded() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.byFile == nil {
		c.byFile = map[string]loudnessRecord{}
	}
	if c.Path == "" {
		return
	}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return
	}
	var cf loudnessCacheFile
	if json.Unmarshal(data, &cf) == nil && cf.Records != nil {
		c.byFile = cf.Records
	}
}

func (c *loudnessCache) get(chapterID string) (loudnessRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	r, ok := c.byFile[chapterID]
	return r, ok
}

func (c *loudnessCache) put(r loudnessRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	if r.At == "" {
		r.At = time.Now().UTC().Format(time.RFC3339)
	}
	// Re-read the file and fold in any records written by a concurrent process
	// since we loaded, so writing the whole map back does not drop their entries
	// (last-writer-wins per chapter id is fine; losing other chapters is not).
	c.mergeFromDisk()
	c.byFile[r.ChapterID] = r
	c.persist()
}

// mergeFromDisk overlays on-disk records that are absent from the in-memory map,
// preserving our own (newer) values for any shared keys.
func (c *loudnessCache) mergeFromDisk() {
	if c.Path == "" {
		return
	}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return
	}
	var cf loudnessCacheFile
	if json.Unmarshal(data, &cf) != nil {
		return
	}
	for k, v := range cf.Records {
		if _, ok := c.byFile[k]; !ok {
			c.byFile[k] = v
		}
	}
}

// forTonie returns all records belonging to a tonie.
func (c *loudnessCache) forTonie(tonieID string) []loudnessRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	var out []loudnessRecord
	for _, r := range c.byFile {
		if r.TonieID == tonieID {
			out = append(out, r)
		}
	}
	return out
}

func (c *loudnessCache) persist() {
	if c.Path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(c.Path), 0o700) != nil {
		return
	}
	data, err := json.MarshalIndent(loudnessCacheFile{Records: c.byFile}, "", "  ")
	if err != nil {
		return
	}
	tmp := c.Path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, c.Path)
}

// dataDir is where non-cache persistent state (the loudness ledger) lives.
func dataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "tonys")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "tonys")
}

func (a *App) loudnessCachePath() string {
	if v := os.Getenv("TONYS_LOUDNESS_DB"); v != "" {
		return v
	}
	return filepath.Join(dataDir(), "loudness.json")
}
