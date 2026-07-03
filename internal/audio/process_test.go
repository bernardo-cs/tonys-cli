package audio

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sineWAV writes a mono 16-bit 11025 Hz sine-tone WAV of the given length.
func sineWAV(t *testing.T, path string, seconds float64) {
	t.Helper()
	n := int(seconds * fpSampleRate)
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(0.5 * math.Sin(2*math.Pi*440*float64(i)/fpSampleRate) * 32767)
	}
	writeWAV(t, path, pcm, fpSampleRate)
}

func TestProcessRejectsTrimBeyondDuration(t *testing.T) {
	// ffmpeg happily seeks past EOF and produces an empty file; Process
	// must refuse instead of letting an empty chapter get uploaded.
	conv := NewConverter()
	if !conv.Available() {
		t.Skip("ffmpeg not available")
	}
	path := filepath.Join(t.TempDir(), "short.wav")
	sineWAV(t, path, 3)

	_, err := conv.Process(context.Background(), path, ProcessOptions{Format: "wav", SkipSeconds: 10})
	if err == nil || !strings.Contains(err.Error(), "leaves nothing") {
		t.Fatalf("skip beyond duration: err = %v, want trim-window error", err)
	}

	// A sane skip still works and the output is shorter than the input.
	res, err := conv.Process(context.Background(), path, ProcessOptions{Format: "wav", SkipSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	out, err := os.Stat(res.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := os.Stat(path)
	if out.Size() <= 0 || out.Size() >= in.Size() {
		t.Fatalf("skip output size %d vs input %d", out.Size(), in.Size())
	}
}
