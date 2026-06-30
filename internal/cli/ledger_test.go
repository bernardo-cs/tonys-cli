package cli

import (
	"path/filepath"
	"testing"
)

func rec(chapterID, tonieID string, lufs float64) loudnessRecord {
	return loudnessRecord{ChapterID: chapterID, TonieID: tonieID, Title: chapterID, IntegratedLUFS: lufs, Source: "upload"}
}

func TestLoudnessCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loudness.json")
	c := newLoudnessCache(path)
	c.put(rec("c1", "t1", -16.0))
	c.put(rec("c2", "t1", -18.0))
	c.put(rec("c3", "t2", -12.0))

	// Reload from disk and confirm per-tonie grouping.
	c2 := newLoudnessCache(path)
	got, ok := c2.get("c1")
	if !ok || got.IntegratedLUFS != -16.0 {
		t.Fatalf("get(c1) = %+v ok=%v", got, ok)
	}
	if n := len(c2.forTonie("t1")); n != 2 {
		t.Fatalf("forTonie(t1) = %d records, want 2", n)
	}
	if n := len(c2.forTonie("t2")); n != 1 {
		t.Fatalf("forTonie(t2) = %d records, want 1", n)
	}
}

func TestLoudnessCachePutStampsTime(t *testing.T) {
	c := newLoudnessCache(filepath.Join(t.TempDir(), "loudness.json"))
	c.put(rec("c1", "t1", -16.0))
	got, _ := c.get("c1")
	if got.At == "" {
		t.Error("put should stamp the At timestamp")
	}
}

// TestLoudnessCacheMergesConcurrentWrites mirrors the token-cache concurrency
// test: a record written by another process after we loaded must survive our
// own write.
func TestLoudnessCacheMergesConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loudness.json")

	c1 := newLoudnessCache(path)
	c1.get("none") // force load of the (empty) file

	newLoudnessCache(path).put(rec("other", "t1", -20.0))

	c1.put(rec("mine", "t1", -16.0))

	final := newLoudnessCache(path)
	if _, ok := final.get("mine"); !ok {
		t.Error("mine lost")
	}
	if _, ok := final.get("other"); !ok {
		t.Error("other clobbered by concurrent write (merge failed)")
	}
}
