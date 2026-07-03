package audio

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
)

// ProcessOptions controls a combined convert + loudness-normalize pass.
type ProcessOptions struct {
	// Format is the target output format (mp3, opus, ogg, m4a, flac, wav).
	// Empty defaults to mp3.
	Format  string
	Bitrate string

	// Normalize enables EBU R128 two-pass loudness normalization.
	Normalize  bool
	TargetLUFS float64
	TruePeak   float64
	LRA        float64

	// SkipSeconds trims this many seconds from the start of the input before
	// encoding. Both the loudness-measure pass and the encode pass skip the
	// same offset so normalization is computed on the kept audio only.
	SkipSeconds float64

	// SkipEndSeconds trims this many seconds from the end of the input before
	// encoding. Requires probing the file duration; both the measure pass and
	// the encode pass use the same trimmed window.
	SkipEndSeconds float64
}

// ProcessResult describes the produced file.
type ProcessResult struct {
	OutputPath  string  `json:"-"`
	Cleanup     func()  `json:"-"`
	Format      string  `json:"format"`
	Converted   bool    `json:"converted"`
	Normalized  bool    `json:"normalized"`
	Measured    bool    `json:"measured"`
	InputLUFS   float64 `json:"inputLufs,omitempty"`
	AppliedLUFS float64 `json:"appliedLufs,omitempty"`
}

// Process converts inputPath to opts.Format and, when opts.Normalize is set,
// applies a two-pass EBU R128 loudness normalization in the same encode. Because
// loudness is measured on decoded audio, the result is independent of the input
// bitrate or codec. The caller must defer the returned Cleanup.
func (c *Converter) Process(ctx context.Context, inputPath string, opts ProcessOptions) (ProcessResult, error) {
	if !c.Available() {
		return ProcessResult{Cleanup: noop}, errFFmpegMissing
	}
	if opts.Format == "" {
		opts.Format = "mp3"
	}
	if opts.TruePeak == 0 {
		opts.TruePeak = DefaultTruePeak
	}
	if opts.LRA == 0 {
		opts.LRA = DefaultLRA
	}
	codecArgs, ext, err := codecFor(opts.Format, opts.Bitrate)
	if err != nil {
		return ProcessResult{Cleanup: noop}, err
	}

	res := ProcessResult{Cleanup: noop, Format: ext, Converted: true}

	// Compute output duration when an end-trim is requested. This probe runs
	// before both passes so they operate on the same trimmed window.
	var encodeDur float64
	if opts.SkipEndSeconds > 0 {
		total, err := c.probeDuration(ctx, inputPath)
		if err != nil {
			return ProcessResult{Cleanup: noop}, err
		}
		encodeDur = total - opts.SkipSeconds - opts.SkipEndSeconds
		if encodeDur <= 0 {
			return ProcessResult{Cleanup: noop}, fmt.Errorf(
				"--skip (%.3fs) + --skip-end (%.3fs) exceeds file duration (%.3fs)",
				opts.SkipSeconds, opts.SkipEndSeconds, total)
		}
	}

	// Build the audio filter chain.
	var filters []string
	if opts.Normalize {
		stats, err := c.measure(ctx, inputPath, opts.TargetLUFS, opts.TruePeak, opts.LRA, opts.SkipSeconds, encodeDur)
		if err != nil {
			return ProcessResult{Cleanup: noop}, err
		}
		res.Measured = true
		res.Normalized = true
		res.InputLUFS = stats.IntegratedLUFS
		res.AppliedLUFS = opts.TargetLUFS
		if finiteStats(stats) {
			filters = append(filters, fmt.Sprintf(
				"loudnorm=I=%g:TP=%g:LRA=%g:measured_I=%g:measured_TP=%g:measured_LRA=%g:measured_thresh=%g:offset=%g:linear=true:print_format=summary",
				opts.TargetLUFS, opts.TruePeak, opts.LRA,
				stats.IntegratedLUFS, stats.TruePeakDB, stats.LRA, stats.Threshold, stats.Offset,
			))
		} else {
			// Silent/near-silent input yields non-finite measured values (-inf),
			// which the two-pass filter rejects. Fall back to single-pass dynamic
			// loudnorm so the encode still succeeds.
			filters = append(filters, fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g", opts.TargetLUFS, opts.TruePeak, opts.LRA))
		}
	}

	out, err := os.CreateTemp("", "tonys-*."+ext)
	if err != nil {
		return ProcessResult{Cleanup: noop}, err
	}
	outPath := out.Name()
	out.Close()
	res.OutputPath = outPath
	res.Cleanup = func() { os.Remove(outPath) }

	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	if opts.SkipSeconds > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", opts.SkipSeconds))
	}
	args = append(args, "-i", inputPath, "-vn")
	if encodeDur > 0 {
		args = append(args, "-t", fmt.Sprintf("%.3f", encodeDur))
	}
	if len(filters) > 0 {
		args = append(args, "-af", strings.Join(filters, ","))
	}
	args = append(args, codecArgs...)
	args = append(args, outPath)

	if err := c.run(ctx, args); err != nil {
		res.Cleanup()
		return ProcessResult{Cleanup: noop}, err
	}
	return res, nil
}

// finiteStats reports whether every loudness measurement is a finite number
// (digital silence produces -inf, which the two-pass loudnorm filter rejects).
func finiteStats(s LoudnessStats) bool {
	for _, v := range []float64{s.IntegratedLUFS, s.TruePeakDB, s.LRA, s.Threshold, s.Offset} {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return false
		}
	}
	return true
}
