package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

// writeToneWAV writes a mono 16-bit 11025 Hz WAV of a two-tone signal, loud
// enough to never count as silence in audio analysis.
func writeToneWAV(t *testing.T, path string, seconds float64) {
	t.Helper()
	const rate = 11025
	n := int(seconds * rate)
	data := make([]byte, 44+2*n)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*n))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], rate)
	binary.LittleEndian.PutUint32(data[28:], rate*2)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*n))
	for i := 0; i < n; i++ {
		ts := float64(i) / rate
		s := 0.4*math.Sin(2*math.Pi*523*ts) + 0.3*math.Sin(2*math.Pi*911*ts)
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(int16(s*32767)))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareForUploadHonorsSkipOnAcceptedFormats(t *testing.T) {
	// Regression: an already-accepted format with no convert/normalize used
	// to be returned as-is, silently dropping --skip/--skip-end.
	conv := audio.NewConverter()
	if !conv.Available() {
		t.Skip("ffmpeg not available")
	}
	var buf bytes.Buffer
	a := &App{Output: "json", Stdout: &buf, Stderr: &buf, Quiet: true}
	path := filepath.Join(t.TempDir(), "in.wav")
	writeToneWAV(t, path, 3)

	spec := processSpec{convert: "auto", normalize: "off", skipSeconds: 1}
	out, cleanup, _, err := a.prepareForUpload(context.Background(), toniecloud.CreativeTonie{}, path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if out == path {
		t.Fatal("--skip was dropped: input returned unprocessed")
	}
}

// buildFS mirrors how runCommand constructs a leaf flag set.
func buildFS(a *App, specs []FlagSpec) *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	a.bindGlobals(fs)
	bindFlags(fs, specs)
	return fs
}

func TestReorderArgs(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, uploadFlags)

	in := []string{"Erna-Tonie", "song.mp3", "--title", "Hi", "--normalize", "target", "--wait"}
	out := reorderArgs(fs, in)
	if err := fs.Parse(out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := fstr(fs, "title"); got != "Hi" {
		t.Errorf("title = %q, want Hi", got)
	}
	if got := fstr(fs, "normalize"); got != "target" {
		t.Errorf("normalize = %q, want target", got)
	}
	if !fbool(fs, "wait") {
		t.Error("wait should be true")
	}
	if pos := fs.Args(); !reflect.DeepEqual(pos, []string{"Erna-Tonie", "song.mp3"}) {
		t.Errorf("positionals = %v, want [Erna-Tonie song.mp3]", pos)
	}
}

func TestReorderArgsStdinDashIsPositional(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, uploadFlags)
	out := reorderArgs(fs, []string{"Erna", "-", "--title", "X"})
	if err := fs.Parse(out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pos := fs.Args(); !reflect.DeepEqual(pos, []string{"Erna", "-"}) {
		t.Errorf("positionals = %v, want [Erna -]", pos)
	}
}

func TestReorderArgsNegativeFlagValue(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, uploadFlags)
	out := reorderArgs(fs, []string{"Erna", "f.wav", "--target-lufs", "-18"})
	if err := fs.Parse(out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := fstr(fs, "target-lufs"); got != "-18" {
		t.Errorf("target-lufs = %q, want -18", got)
	}
}

func TestReorderArgsDoubleDashTerminator(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, uploadFlags)
	// `--` must let a positional starting with `-` through unparsed.
	out := reorderArgs(fs, []string{"--", "-weird-name"})
	if err := fs.Parse(out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pos := fs.Args(); !reflect.DeepEqual(pos, []string{"-weird-name"}) {
		t.Fatalf("positionals = %v, want [-weird-name]", pos)
	}
}

func TestEmptySliceEmitsBrackets(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Output: "json", Stdout: &buf}
	var empty []string
	if err := a.emit(empty, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Fatalf("empty slice JSON = %q, want []", got)
	}
}

func TestProcessSpecValidation(t *testing.T) {
	a := NewApp()

	fs := buildFS(a, uploadFlags)
	fs.Parse(reorderArgs(fs, []string{"--convert", "bogus", "t", "f"}))
	if _, err := processSpecFromFlags(fs); err == nil {
		t.Error("expected error for bad --convert")
	}

	fs2 := buildFS(a, uploadFlags)
	fs2.Parse(reorderArgs(fs2, []string{"--normalize", "target", "--target-lufs", "-14", "t", "f"}))
	spec, err := processSpecFromFlags(fs2)
	if err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	if !spec.normalizeOn() || spec.targetLUFS != -14 {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestResolveChapter(t *testing.T) {
	tonie := toniecloud.CreativeTonie{
		Name: "Erna",
		Chapters: []toniecloud.Chapter{
			{ID: "aaa", Title: "Intro"},
			{ID: "bbb", Title: "Story"},
			{ID: "ccc", Title: "Outro"},
		},
	}
	cases := []struct {
		ref     string
		want    int
		wantErr bool
	}{
		{"bbb", 1, false},   // by id
		{"2", 1, false},     // by 1-based index
		{"Outro", 2, false}, // by title
		{"outro", 2, false}, // case-insensitive title
		{"zzz", -1, true},   // missing
		{"9", -1, true},     // out of range
	}
	for _, c := range cases {
		got, err := resolveChapter(tonie, c.ref)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("resolveChapter(%q) = %d, %v; want %d, err=%v", c.ref, got, err, c.want, c.wantErr)
		}
	}
}

func TestDiffNewChapters(t *testing.T) {
	before := []toniecloud.Chapter{{ID: "a"}, {ID: "b"}}
	after := []toniecloud.Chapter{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	got := diffNewChapters(before, after)
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "d" {
		t.Fatalf("diffNewChapters = %+v", got)
	}
}

func TestChapterPositionFromFlags(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, uploadFlags)
	if err := fs.Parse(reorderArgs(fs, []string{"t", "f"})); err != nil {
		t.Fatalf("parse: %v", err)
	}
	pos, err := chapterPositionFromFlags(fs)
	if err != nil || pos != chapterPositionEnd {
		t.Fatalf("default position = %q, %v; want end", pos, err)
	}

	fs2 := buildFS(a, uploadFlags)
	if err := fs2.Parse(reorderArgs(fs2, []string{"--position", "front", "t", "f"})); err != nil {
		t.Fatalf("parse front: %v", err)
	}
	pos, err = chapterPositionFromFlags(fs2)
	if err != nil || pos != chapterPositionBeginning {
		t.Fatalf("front position = %q, %v; want beginning", pos, err)
	}

	fs3 := buildFS(a, uploadFlags)
	if err := fs3.Parse(reorderArgs(fs3, []string{"--position", "middle", "t", "f"})); err != nil {
		t.Fatalf("parse middle: %v", err)
	}
	if _, err := chapterPositionFromFlags(fs3); err == nil {
		t.Fatal("expected invalid position error")
	}
}

func TestChaptersWithNewFirst(t *testing.T) {
	before := []toniecloud.Chapter{{ID: "a"}, {ID: "b"}}
	after := []toniecloud.Chapter{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}

	got, changed := chaptersWithNewFirst(before, after)
	if !changed {
		t.Fatal("expected order change")
	}
	want := []toniecloud.Chapter{{ID: "c"}, {ID: "d"}, {ID: "a"}, {ID: "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chaptersWithNewFirst = %+v, want %+v", got, want)
	}

	got, changed = chaptersWithNewFirst(nil, []toniecloud.Chapter{{ID: "a"}})
	if changed || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("single all-new list = %+v changed=%v, want unchanged", got, changed)
	}
}

func TestFormatSelection(t *testing.T) {
	a := &App{Output: "table"}
	if a.format() != "table" {
		t.Errorf("default format = %q", a.format())
	}
	a.JSON = true
	if a.format() != "json" {
		t.Errorf("--json should force json, got %q", a.format())
	}
	a.JSON = false
	a.Output = "jsonl"
	if a.format() != "jsonl" {
		t.Errorf("jsonl not honored, got %q", a.format())
	}
}

func TestExitCodes(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Error("nil → 0")
	}
	if ExitCode(usageErr("bad")) != 2 {
		t.Error("usage → 2")
	}
	if ExitCode(toniecloud.ErrNotAuthenticated) != 3 {
		t.Error("auth → 3")
	}
	if ExitCode(notFoundErr("x")) != 4 {
		t.Error("notfound → 4")
	}
}

func TestSchemaIsValidAndCoversCommands(t *testing.T) {
	// Every command must have a name and summary so the schema is useful.
	var walk func(c *Command)
	walk = func(c *Command) {
		if c.Name == "" || c.Summary == "" {
			t.Errorf("command missing name/summary: %+v", c)
		}
		for _, s := range c.Sub {
			walk(s)
		}
	}
	for _, c := range rootCommands() {
		walk(c)
	}
}
