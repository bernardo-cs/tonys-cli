package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

// processFlags are the convert/normalize flags shared by upload, chapter add and
// the youtube importer.
var processFlags = []FlagSpec{
	{Name: "convert", Usage: "convert input: auto|always|never", Default: "auto"},
	{Name: "format", Usage: "target format when converting: mp3|opus|ogg|m4a|flac|wav"},
	{Name: "bitrate", Usage: "target bitrate when converting", Default: "128k"},
	{Name: "normalize", Usage: "loudness normalize: off|target|match", Default: "off"},
	{Name: "target-lufs", Usage: "integrated loudness target (LUFS)", Default: "-16"},
	{Name: "skip", Usage: "skip the first duration of audio (e.g. 30s, 1m30s)"},
	{Name: "skip-end", Usage: "skip the last duration of audio (e.g. 30s, 1m30s)"},
}

// processSpec is the resolved processing configuration.
type processSpec struct {
	convert        string
	format         string
	bitrate        string
	normalize      string
	targetLUFS     float64
	skipSeconds    float64
	skipEndSeconds float64
}

func processSpecFromFlags(fs *flag.FlagSet) (processSpec, error) {
	s := processSpec{
		convert:   orDefault(fstr(fs, "convert"), "auto"),
		format:    fstr(fs, "format"),
		bitrate:   orDefault(fstr(fs, "bitrate"), "128k"),
		normalize: orDefault(fstr(fs, "normalize"), "off"),
	}
	switch s.convert {
	case "auto", "always", "never":
	default:
		return s, usageErr("--convert must be auto, always or never")
	}
	switch s.normalize {
	case "off", "target", "match":
	default:
		return s, usageErr("--normalize must be off, target or match")
	}
	tl := orDefault(fstr(fs, "target-lufs"), "-16")
	v, err := strconv.ParseFloat(tl, 64)
	if err != nil {
		return s, usageErr("--target-lufs must be a number (LUFS), got %q", tl)
	}
	s.targetLUFS = v
	if skip := fstr(fs, "skip"); skip != "" {
		d, derr := time.ParseDuration(skip)
		if derr != nil || d < 0 {
			return s, usageErr("--skip must be a non-negative duration (e.g. 30s, 1m30s), got %q", skip)
		}
		s.skipSeconds = d.Seconds()
	}
	if skip := fstr(fs, "skip-end"); skip != "" {
		d, derr := time.ParseDuration(skip)
		if derr != nil || d < 0 {
			return s, usageErr("--skip-end must be a non-negative duration (e.g. 30s, 1m30s), got %q", skip)
		}
		s.skipEndSeconds = d.Seconds()
	}
	return s, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// durFlag parses a duration flag, falling back to def on empty/invalid input.
func durFlag(fs *flag.FlagSet, name string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(fstr(fs, name)); err == nil {
		return d
	}
	return def
}

// normalizeOn reports whether any loudness normalization is requested.
func (s processSpec) normalizeOn() bool { return s.normalize == "target" || s.normalize == "match" }

// needsFFmpeg reports whether the spec forces processing regardless of input.
func (s processSpec) forcesProcessing() bool {
	return s.convert == "always" || s.normalizeOn() || s.skipSeconds > 0 || s.skipEndSeconds > 0
}

// prepareForUpload converts and/or normalizes inputPath as the spec dictates,
// returning the path to upload (the original when no processing is needed), a
// cleanup func, and the processing result (nil when untouched).
func (a *App) prepareForUpload(ctx context.Context, t toniecloud.CreativeTonie, inputPath string, s processSpec) (string, func(), *audio.ProcessResult, error) {
	ext := audio.ExtOf(inputPath)
	accepted := audio.IsAccepted(ext, nil)

	needConvert := false
	switch s.convert {
	case "always":
		needConvert = true
	case "never":
		needConvert = false
	default:
		needConvert = !accepted
	}

	if !needConvert && !s.normalizeOn() {
		return inputPath, func() {}, nil, nil // upload as-is
	}

	conv := audio.NewConverter()
	if !conv.Available() {
		return "", func() {}, nil, fmt.Errorf("audio processing requires ffmpeg, which was not found; install it (e.g. `brew install ffmpeg`) or upload an already-accepted format with --convert=never --normalize=off")
	}

	format := s.format
	if format == "" {
		if needConvert {
			format = "mp3"
		} else {
			format = keepFormat(ext)
		}
	}

	target := s.targetLUFS
	if s.normalize == "match" {
		target = a.matchTarget(ctx, t, s.targetLUFS)
	}

	res, err := conv.Process(ctx, inputPath, audio.ProcessOptions{
		Format:         format,
		Bitrate:        s.bitrate,
		Normalize:      s.normalizeOn(),
		TargetLUFS:     target,
		SkipSeconds:    s.skipSeconds,
		SkipEndSeconds: s.skipEndSeconds,
	})
	if err != nil {
		return "", func() {}, nil, err
	}
	switch {
	case res.Normalized && (math.IsInf(res.InputLUFS, 0) || math.IsNaN(res.InputLUFS)):
		a.info("Normalized (silent/near-silent input) → %.1f LUFS (%s)", res.AppliedLUFS, format)
	case res.Normalized:
		a.info("Normalized %.1f → %.1f LUFS (%s)", res.InputLUFS, res.AppliedLUFS, format)
	default:
		a.info("Converted to %s", format)
	}
	return res.OutputPath, res.Cleanup, &res, nil
}

// keepFormat maps an already-accepted input extension to the closest target
// format codecFor understands, so a normalize-only request (which must re-encode
// to apply loudnorm) doesn't silently downgrade the codec. Lossless inputs stay
// lossless; only formats with no sensible target fall back to mp3.
func keepFormat(ext string) string {
	switch ext {
	case "mp3", "opus", "ogg", "m4a", "flac", "wav":
		return ext
	case "oga":
		return "ogg"
	case "aac", "m4b":
		return "m4a"
	case "aif", "aiff":
		return "flac" // keep it lossless
	default: // wma and anything unexpected
		return "mp3"
	}
}

// matchTarget computes the loudness target that makes a new file match the
// chapters already on the tonie, using the loudness ledger (the levels of files
// previously uploaded through this tool — and any cloud chapters explicitly
// measured via `loudness measure-tonie`). TonieCloud exposes no audio download,
// so chapters this tool never produced cannot be measured; when none are known
// it falls back to the provided default target.
func (a *App) matchTarget(ctx context.Context, t toniecloud.CreativeTonie, fallback float64) float64 {
	cache := a.loudness()
	var sum float64
	var n int
	for _, ch := range t.Chapters {
		if r, ok := cache.get(ch.ID); ok {
			sum += r.IntegratedLUFS
			n++
		}
	}
	if n == 0 {
		a.info("normalize match: no known loudness for existing chapters; using %.1f LUFS", fallback)
		return fallback
	}
	avg := sum / float64(n)
	a.info("normalize match: targeting %.1f LUFS (mean of %d known chapter(s))", avg, n)
	return avg
}

// measureCloudChapter downloads a chapter's audio and measures its loudness.
// Returns ok=false when the chapter cannot be downloaded (e.g. content-token
// chapters or no available download endpoint).
func (a *App) measureCloudChapter(ctx context.Context, t toniecloud.CreativeTonie, ch toniecloud.Chapter) (float64, bool) {
	conv := audio.NewConverter()
	if !conv.Available() {
		return 0, false
	}
	tmp, err := os.CreateTemp("", "tonys-chapter-*")
	if err != nil {
		return 0, false
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	if err := a.Client().DownloadChapter(ctx, t, ch, path); err != nil {
		if a.Verbose {
			a.info("cannot download chapter %q: %v", ch.Title, err)
		}
		return 0, false
	}
	stats, err := conv.Measure(ctx, path)
	if err != nil {
		return 0, false
	}
	return stats.IntegratedLUFS, true
}

// diffNewChapters returns chapters present in after but not before, preserving
// after's order (new chapters are appended in upload order).
func diffNewChapters(before, after []toniecloud.Chapter) []toniecloud.Chapter {
	seen := make(map[string]bool, len(before))
	for _, c := range before {
		seen[c.ID] = true
	}
	var out []toniecloud.Chapter
	for _, c := range after {
		if !seen[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// recordChapterLoudness stores the loudness of freshly-uploaded chapters keyed by
// their (now known) chapter id, so future `--normalize match` runs can target
// them. prs is aligned, in order, with the newly-added chapters.
func (a *App) recordChapterLoudness(t toniecloud.CreativeTonie, before []toniecloud.Chapter, after toniecloud.CreativeTonie, prs []*audio.ProcessResult) {
	news := diffNewChapters(before, after.Chapters)
	for i, ch := range news {
		if i >= len(prs) {
			break
		}
		pr := prs[i]
		if pr == nil || !pr.Normalized {
			continue
		}
		a.loudness().put(loudnessRecord{
			ChapterID:      ch.ID,
			TonieID:        t.ID,
			Title:          ch.Title,
			IntegratedLUFS: pr.AppliedLUFS,
			Source:         "upload",
			At:             time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// bufferToTemp copies r to a temp file so stream input can be processed by
// ffmpeg (which needs a seekable file for some formats).
func bufferToTemp(r io.Reader) (string, func(), error) {
	tmp, err := os.CreateTemp("", "tonys-stdin-*")
	if err != nil {
		return "", func() {}, err
	}
	path := tmp.Name()
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(path)
		return "", func() {}, err
	}
	tmp.Close()
	return path, func() { os.Remove(path) }, nil
}
