package audio

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sineWAV writes a mono 16-bit 11025 Hz sine-tone WAV of the given length.
func sineWAV(t *testing.T, path string, seconds float64) {
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
		s := 0.5 * math.Sin(2*math.Pi*440*float64(i)/rate)
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(int16(s*32767)))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
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
