package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
)

func TestTrimSpecDefaultsOff(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, ytCommand().Flags)
	if err := fs.Parse(reorderArgs(fs, []string{"tonie", "url"})); err != nil {
		t.Fatal(err)
	}
	spec, err := trimSpecFromFlags(fs)
	if err != nil {
		t.Fatal(err)
	}
	if spec.enabled() {
		t.Fatalf("trim enabled by default: %+v", spec)
	}
}

func TestTrimSpecAutoTrimEnablesBothEnds(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, ytCommand().Flags)
	if err := fs.Parse(reorderArgs(fs, []string{"--auto-trim", "tonie", "url"})); err != nil {
		t.Fatal(err)
	}
	spec, err := trimSpecFromFlags(fs)
	if err != nil {
		t.Fatal(err)
	}
	if spec.intro.mode != "auto" || spec.outro.mode != "auto" {
		t.Fatalf("auto-trim modes = %q/%q, want auto/auto", spec.intro.mode, spec.outro.mode)
	}
}

func TestTrimSpecExplicitSideBeatsAutoTrim(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, ytCommand().Flags)
	if err := fs.Parse(reorderArgs(fs, []string{"--auto-trim", "--trim-intro", "off", "tonie", "url"})); err != nil {
		t.Fatal(err)
	}
	spec, err := trimSpecFromFlags(fs)
	if err != nil {
		t.Fatal(err)
	}
	if spec.intro.mode != "off" || spec.outro.mode != "auto" {
		t.Fatalf("modes = %q/%q, want off/auto", spec.intro.mode, spec.outro.mode)
	}
}

func TestTrimSpecValidation(t *testing.T) {
	a := NewApp()

	fs := buildFS(a, ytCommand().Flags)
	fs.Parse(reorderArgs(fs, []string{"--trim-intro", "bogus", "t", "u"}))
	if _, err := trimSpecFromFlags(fs); err == nil {
		t.Error("expected error for bad --trim-intro mode")
	}

	fs2 := buildFS(a, ytCommand().Flags)
	fs2.Parse(reorderArgs(fs2, []string{"--trim-intro", "common", "--intro-max", "0s", "t", "u"}))
	if _, err := trimSpecFromFlags(fs2); err == nil {
		t.Error("expected error for non-positive --intro-max")
	}

	fs3 := buildFS(a, ytCommand().Flags)
	fs3.Parse(reorderArgs(fs3, []string{"--trim-outro", "common", "--outro-min", "2m", "t", "u"}))
	if _, err := trimSpecFromFlags(fs3); err == nil {
		t.Error("expected error for --outro-min >= --outro-max")
	}
}

func TestTrimSpecBoundsReachOptions(t *testing.T) {
	a := NewApp()
	fs := buildFS(a, ytCommand().Flags)
	if err := fs.Parse(reorderArgs(fs, []string{"--trim-intro", "common", "--intro-max", "30s", "--intro-min", "2s", "t", "u"})); err != nil {
		t.Fatal(err)
	}
	spec, err := trimSpecFromFlags(fs)
	if err != nil {
		t.Fatal(err)
	}
	if spec.intro.opts.MaxSeconds != 30 || spec.intro.opts.MinSeconds != 2 {
		t.Fatalf("intro opts = %+v", spec.intro.opts)
	}
}

func TestResolveTrimMode(t *testing.T) {
	cases := []struct {
		mode  string
		items int
		want  string
	}{
		{"auto", 3, "common"},
		{"auto", 1, "suggest"}, // single item: report only, never cut on a heuristic
		{"common", 1, "common"},
		{"spectral", 5, "spectral"},
		{"off", 5, "off"},
	}
	for _, c := range cases {
		if got := resolveTrimMode(c.mode, c.items); got != c.want {
			t.Errorf("resolveTrimMode(%q, %d) = %q, want %q", c.mode, c.items, got, c.want)
		}
	}
}

func TestDetectTrimCutsSuggestNeverCuts(t *testing.T) {
	conv := audio.NewConverter()
	if !conv.Available() {
		t.Skip("ffmpeg not available")
	}
	var buf bytes.Buffer
	a := &App{Output: "json", Stdout: &buf, Stderr: &buf, Quiet: true}
	path := filepath.Join(t.TempDir(), "song.wav")
	writeToneWAV(t, path, 12)

	side := trimSide{mode: "auto", opts: audio.IntroOptions{MinSeconds: 1, MaxSeconds: 6, MaxShiftSeconds: 2}}
	cuts, _ := a.detectTrimCuts(context.Background(), conv, []string{path}, side, "suggest", "intro")
	for i, c := range cuts {
		if c != 0 {
			t.Fatalf("suggest mode produced a cut: cuts[%d] = %.2f", i, c)
		}
	}
}

func TestTrimDetectUsageErrors(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Output: "json", Stdout: &buf, Stderr: &buf}

	fs := buildFS(a, trimDetectCommand("intro").Flags)
	fs.Parse([]string{})
	if err := runTrimDetect(context.Background(), a, fs, nil, "intro"); !isUsage(err) {
		t.Fatalf("no files: err = %v, want usage error", err)
	}

	fs2 := buildFS(a, trimDetectCommand("outro").Flags)
	fs2.Parse(reorderArgs(fs2, []string{"--mode", "common", "one.mp3"}))
	if err := runTrimDetect(context.Background(), a, fs2, []string{"one.mp3"}, "outro"); !isUsage(err) {
		t.Fatalf("common with one file: err = %v, want usage error", err)
	}

	fs3 := buildFS(a, trimDetectCommand("intro").Flags)
	fs3.Parse(reorderArgs(fs3, []string{"--mode", "bogus", "a.mp3", "b.mp3"}))
	if err := runTrimDetect(context.Background(), a, fs3, []string{"a.mp3", "b.mp3"}, "intro"); !isUsage(err) {
		t.Fatalf("bad mode: err = %v, want usage error", err)
	}
}
