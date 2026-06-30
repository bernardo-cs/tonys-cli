// Package audio converts arbitrary audio files into a format accepted by
// TonieCloud, using ffmpeg when available.
package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AcceptedExtensions are the upload formats TonieCloud's /config endpoint
// advertises. Inputs already in one of these need no conversion.
var AcceptedExtensions = []string{
	"aac", "aif", "aiff", "flac", "mp3", "m4a", "m4b", "wav", "oga", "ogg", "opus", "wma",
}

// IsAccepted reports whether a file extension (with or without a leading dot) is
// an accepted upload format. accepts may be supplied (e.g. from the live
// /config); a nil/empty accepts falls back to AcceptedExtensions.
func IsAccepted(ext string, accepts []string) bool {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if ext == "" {
		return false
	}
	if len(accepts) == 0 {
		accepts = AcceptedExtensions
	}
	for _, a := range accepts {
		if strings.ToLower(strings.TrimPrefix(a, ".")) == ext {
			return true
		}
	}
	return false
}

// codecFor maps a target format to ffmpeg codec arguments and a file extension.
func codecFor(format, bitrate string) (args []string, ext string, err error) {
	if bitrate == "" {
		bitrate = "128k"
	}
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "", "mp3":
		return []string{"-c:a", "libmp3lame", "-b:a", bitrate}, "mp3", nil
	case "opus":
		return []string{"-c:a", "libopus", "-b:a", bitrate}, "opus", nil
	case "ogg", "oga":
		return []string{"-c:a", "libvorbis", "-b:a", bitrate}, "ogg", nil
	case "aac", "m4a":
		return []string{"-c:a", "aac", "-b:a", bitrate}, "m4a", nil
	case "flac":
		return []string{"-c:a", "flac"}, "flac", nil
	case "wav":
		return []string{"-c:a", "pcm_s16le"}, "wav", nil
	default:
		return nil, "", fmt.Errorf("unsupported target format %q (use mp3, opus, ogg, m4a, flac or wav)", format)
	}
}

// Converter transcodes audio via an external ffmpeg binary.
type Converter struct {
	// FFmpegPath is the ffmpeg executable; empty means look it up in $PATH.
	FFmpegPath string
}

// NewConverter resolves the ffmpeg binary, honoring $TONYS_FFMPEG /
// $TONIE_FFMPEG before $PATH.
func NewConverter() *Converter {
	path := firstNonEmpty(os.Getenv("TONYS_FFMPEG"), os.Getenv("TONIE_FFMPEG"))
	if path == "" {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			path = p
		}
	}
	return &Converter{FFmpegPath: path}
}

// Available reports whether an ffmpeg binary was found.
func (c *Converter) Available() bool { return c.FFmpegPath != "" }

// Version returns ffmpeg's first version line (e.g. "ffmpeg version 8.1.1 …"),
// confirming the binary actually executes.
func (c *Converter) Version(ctx context.Context) (string, error) {
	if !c.Available() {
		return "", errFFmpegMissing
	}
	out, err := exec.CommandContext(ctx, c.FFmpegPath, "-version").Output()
	if err != nil {
		return "", err
	}
	return strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0], nil
}

// ConvertFile transcodes inputPath to the target format, returning the path to a
// temporary output file and a cleanup function the caller must defer.
func (c *Converter) ConvertFile(ctx context.Context, inputPath, format, bitrate string) (outPath string, cleanup func(), err error) {
	if !c.Available() {
		return "", noop, errFFmpegMissing
	}
	if _, statErr := os.Stat(inputPath); statErr != nil {
		return "", noop, statErr
	}
	args, ext, err := codecFor(format, bitrate)
	if err != nil {
		return "", noop, err
	}

	out, err := os.CreateTemp("", "tonys-*."+ext)
	if err != nil {
		return "", noop, err
	}
	outPath = out.Name()
	out.Close()
	cleanup = func() { os.Remove(outPath) }

	full := append([]string{"-y", "-hide_banner", "-loglevel", "error", "-i", inputPath, "-vn"}, args...)
	full = append(full, outPath)
	if err := c.run(ctx, full); err != nil {
		cleanup()
		return "", noop, err
	}
	return outPath, cleanup, nil
}

// ConvertReader buffers r to a temp file then converts it, so streams of unknown
// format (e.g. stdin) can be transcoded. The returned cleanup removes both
// temporary files.
func (c *Converter) ConvertReader(ctx context.Context, r io.Reader, format, bitrate string) (outPath string, cleanup func(), err error) {
	if !c.Available() {
		return "", noop, errFFmpegMissing
	}
	in, err := os.CreateTemp("", "tonys-in-*")
	if err != nil {
		return "", noop, err
	}
	inPath := in.Name()
	if _, err := io.Copy(in, r); err != nil {
		in.Close()
		os.Remove(inPath)
		return "", noop, err
	}
	in.Close()

	out, cleanOut, err := c.ConvertFile(ctx, inPath, format, bitrate)
	if err != nil {
		os.Remove(inPath)
		return "", noop, err
	}
	return out, func() { cleanOut(); os.Remove(inPath) }, nil
}

func (c *Converter) run(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, c.FFmpegPath, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %s", ffmpegMessage(stderr.String(), err))
	}
	return nil
}

// ffmpegMessage extracts a useful diagnostic from ffmpeg stderr: the last few
// non-empty lines (skipping the generic "Conversion failed!" trailer that hides
// the real cause), falling back to err when stderr is empty (e.g. on
// cancellation or an exec failure).
func ffmpegMessage(stderr string, err error) string {
	var lines []string
	for _, ln := range strings.Split(stderr, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" && ln != "Conversion failed!" {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		if err != nil {
			return err.Error()
		}
		return "unknown error"
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, "; ")
}

// TargetExtension returns the output extension for a target format.
func TargetExtension(format string) string {
	_, ext, err := codecFor(format, "")
	if err != nil {
		return "mp3"
	}
	return ext
}

var errFFmpegMissing = fmt.Errorf("ffmpeg not found: install it (e.g. `brew install ffmpeg`) or set $TONYS_FFMPEG")

func noop() {}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ExtOf returns the lowercased extension (without dot) of a path.
func ExtOf(path string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}
