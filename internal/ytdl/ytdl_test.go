package ytdl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseItemsSingleVideo(t *testing.T) {
	out := []byte(`{"id":"abc123","title":"My Song","webpage_url":"https://youtu.be/abc123"}`)
	items, err := parseItems(out, "https://input")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "My Song" || items[0].URL != "https://youtu.be/abc123" {
		t.Fatalf("items = %+v", items)
	}
}

func TestParseItemsPlaylist(t *testing.T) {
	out := []byte(`{"id":"pl","title":"Mix","entries":[
		{"id":"a","title":"One","url":"https://youtube.com/watch?v=a"},
		null,
		{"id":"b","title":"Two","url":"https://youtube.com/watch?v=b"}
	]}`)
	items, err := parseItems(out, "https://input")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Title != "One" || items[1].ID != "b" {
		t.Fatalf("items = %+v", items)
	}
}

func TestParseItemsReconstructsURL(t *testing.T) {
	out := []byte(`{"id":"xyz","title":"T"}`)
	items, _ := parseItems(out, "https://input")
	if items[0].URL != "https://www.youtube.com/watch?v=xyz" {
		t.Fatalf("url = %q", items[0].URL)
	}
}

func TestRequireHTTPURL(t *testing.T) {
	ok := []string{
		"https://youtu.be/abc",
		"http://example.com/watch?v=1",
		"  https://youtube.com/playlist?list=x  ",
	}
	for _, u := range ok {
		if err := requireHTTPURL(u); err != nil {
			t.Errorf("requireHTTPURL(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{
		"--exec=rm -rf /",     // option-injection attempt
		"-J",                  // bare flag
		"file:///etc/passwd",  // non-web scheme
		"ftp://example.com/x", // non-web scheme
		"not a url at all",    // no scheme/host
		"",                    // empty
	}
	for _, u := range bad {
		if err := requireHTTPURL(u); err == nil {
			t.Errorf("requireHTTPURL(%q) = nil, want error", u)
		}
	}
}

func TestSoleAudioFileMetacharDir(t *testing.T) {
	// A temp dir whose name contains glob metacharacters must not break lookup.
	base := t.TempDir()
	dir := filepath.Join(base, "item-[abc]*")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A partial download must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "audio.mp3.part"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := soleAudioFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("soleAudioFile = %q, want %q", got, want)
	}
}
