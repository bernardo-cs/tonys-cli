package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// EBU R128 normalization defaults. -16 LUFS suits spoken-word / kids content and
// matches common streaming levels; broadcast (-23) would be noticeably quiet.
const (
	DefaultTargetLUFS = -16.0
	DefaultTruePeak   = -1.5
	DefaultLRA        = 11.0
)

// LoudnessStats holds the measured loudness of a file (EBU R128). Loudness is
// computed on decoded PCM, so it is independent of the input bitrate or codec.
type LoudnessStats struct {
	IntegratedLUFS float64 `json:"integratedLufs"`
	TruePeakDB     float64 `json:"truePeakDb"`
	LRA            float64 `json:"lra"`
	Threshold      float64 `json:"threshold"`
	// Offset is loudnorm's suggested gain offset (only set by a measure pass).
	Offset float64 `json:"-"`
}

// ffmpeg's loudnorm print_format=json payload.
type loudnormJSON struct {
	InputI       string `json:"input_i"`
	InputTP      string `json:"input_tp"`
	InputLRA     string `json:"input_lra"`
	InputThresh  string `json:"input_thresh"`
	TargetOffset string `json:"target_offset"`
}

// Measure returns the integrated loudness (LUFS), true peak and loudness range
// of a file using a single loudnorm analysis pass.
func (c *Converter) Measure(ctx context.Context, inputPath string) (LoudnessStats, error) {
	return c.measure(ctx, inputPath, DefaultTargetLUFS, DefaultTruePeak, DefaultLRA, 0)
}

func (c *Converter) measure(ctx context.Context, inputPath string, target, tp, lra, skipSeconds float64) (LoudnessStats, error) {
	if !c.Available() {
		return LoudnessStats{}, errFFmpegMissing
	}
	if _, err := os.Stat(inputPath); err != nil {
		return LoudnessStats{}, err
	}
	filter := fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:print_format=json", target, tp, lra)
	args := []string{"-hide_banner", "-nostats"}
	if skipSeconds > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", skipSeconds))
	}
	args = append(args, "-i", inputPath, "-vn", "-af", filter, "-f", "null", "-")

	cmd := exec.CommandContext(ctx, c.FFmpegPath, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return LoudnessStats{}, fmt.Errorf("ffmpeg measure: %s", ffmpegMessage(stderr.String(), err))
	}

	lj, err := parseLoudnormJSON(stderr.String())
	if err != nil {
		return LoudnessStats{}, err
	}
	return LoudnessStats{
		IntegratedLUFS: atof(lj.InputI),
		TruePeakDB:     atof(lj.InputTP),
		LRA:            atof(lj.InputLRA),
		Threshold:      atof(lj.InputThresh),
		Offset:         atof(lj.TargetOffset),
	}, nil
}

// parseLoudnormJSON extracts the JSON object loudnorm prints to stderr.
func parseLoudnormJSON(stderr string) (loudnormJSON, error) {
	start := strings.Index(stderr, "{")
	end := strings.LastIndex(stderr, "}")
	if start < 0 || end < 0 || end < start {
		return loudnormJSON{}, fmt.Errorf("could not find loudnorm output in ffmpeg stderr")
	}
	var lj loudnormJSON
	if err := json.Unmarshal([]byte(stderr[start:end+1]), &lj); err != nil {
		return loudnormJSON{}, fmt.Errorf("parse loudnorm output: %w", err)
	}
	return lj, nil
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
