package audio

// Calibration harness (skipped unless enabled): set TONYS_CALIB_DIR to a
// directory of audio files sharing an intro and run
//
//	TONYS_CALIB_DIR=... go test ./internal/audio -run Calibration -v
//
// It prints alignment, matched/diverged BER and detected cut points so the
// thresholds can be sanity-checked against real-world encodes.

import (
	"context"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestCalibrationRealFiles(t *testing.T) {
	dir := os.Getenv("TONYS_CALIB_DIR")
	if dir == "" {
		t.Skip("TONYS_CALIB_DIR not set")
	}
	conv := NewConverter()
	if !conv.Available() {
		t.Skip("ffmpeg not available")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	opts := IntroOptions{}.withDefaults()
	fps := make([]Fingerprint, len(files))
	for i, f := range files {
		fp, err := conv.FingerprintFile(context.Background(), f, opts.FingerprintSeconds())
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		fps[i] = fp
		nonSilent := 0
		for _, s := range fp.Silent {
			if !s {
				nonSilent++
			}
		}
		t.Logf("%s: %d frames (%.1fs), %d non-silent", filepath.Base(f), fp.Frames(), fp.Seconds(fp.Frames()), nonSilent)
	}

	maxShift := int(opts.MaxShiftSeconds / fpHopSeconds)
	scratch := newPairScratch()
	for i := 0; i < len(fps); i++ {
		for j := i + 1; j < len(fps); j++ {
			m := matchPair(fps[i], fps[j], maxShift, scratch)
			t.Logf("pair (%s, %s): prefixA=%.2fs prefixB=%.2fs content=%.2fs identical=%v",
				filepath.Base(files[i]), filepath.Base(files[j]),
				fps[i].Seconds(m.prefixA), fps[j].Seconds(m.prefixB), fps[i].Seconds(m.content), m.identical)
			// BER profile at the chosen alignment (recovered via brute force
			// over shifts to find the same winner).
			logBERProfile(t, fps[i], fps[j], maxShift, scratch)
		}
	}

	res := DetectCommonIntro(fps, opts)
	t.Logf("consensus intro: %.2fs", res.IntroSeconds)
	for i, item := range res.Items {
		t.Logf("  %s: cut=%.2fs pairs=%d", filepath.Base(files[i]), item.CutSeconds, item.Pairs)
	}

	for i := range fps {
		solo := DetectSoloIntro(fps[i], opts)
		t.Logf("solo %s: boundary=%.2fs score=%.3f cohesion=%.3f confident=%v", filepath.Base(files[i]), solo.BoundarySeconds, solo.Score, solo.Cohesion, solo.Confident)
	}

	// Outro: fingerprint tails and run the mirrored detection.
	outroOpts := IntroOptions{MaxShiftSeconds: 5}.withDefaults()
	tails := make([]Fingerprint, len(files))
	for i, f := range files {
		fp, err := conv.FingerprintFileTail(context.Background(), f, outroOpts.FingerprintSeconds())
		if err != nil {
			t.Fatalf("%s tail: %v", f, err)
		}
		tails[i] = fp
	}
	ores := DetectCommonOutro(tails, outroOpts)
	t.Logf("consensus outro: %.2fs", ores.IntroSeconds)
	for i, item := range ores.Items {
		t.Logf("  %s: cut-from-end=%.2fs pairs=%d", filepath.Base(files[i]), item.CutSeconds, item.Pairs)
	}
	for i := range tails {
		solo := DetectSoloOutro(tails[i], outroOpts)
		t.Logf("solo outro %s: boundary=%.2fs score=%.3f cohesion=%.3f confident=%v", filepath.Base(files[i]), solo.BoundarySeconds, solo.Score, solo.Cohesion, solo.Confident)
	}
}

// logBERProfile prints the 1s-windowed BER at the best shift, sampled every
// half second, so matched vs diverged margins are visible.
func logBERProfile(t *testing.T, a, b Fingerprint, maxShift int, scratch *pairScratch) {
	bestShift, bestPrefix := 0, -1
	for s := -maxShift; s <= maxShift; s++ {
		startA, startB := 0, 0
		if s > 0 {
			startA = s
		} else {
			startB = -s
		}
		n := min(a.Frames()-startA, b.Frames()-startB)
		if n <= bestPrefix {
			continue
		}
		prefix, _, _ := walkDivergence(a, b, startA, startB, n, scratch)
		if prefix > bestPrefix {
			bestPrefix, bestShift = prefix, s
		}
	}
	startA, startB := 0, 0
	if bestShift > 0 {
		startA = bestShift
	} else {
		startB = -bestShift
	}
	n := min(a.Frames()-startA, b.Frames()-startB)
	line := fmt.Sprintf("  shift=%+d (%.0fms) BER/0.5s:", bestShift, float64(bestShift)*fpHopSeconds*1000)
	step := framesFor(0.5)
	for w := 0; w+step <= n && w < step*40; w += step {
		var d, info int
		for k := w; k < w+step; k++ {
			ia, ib := startA+k, startB+k
			if a.Silent[ia] || b.Silent[ib] {
				continue
			}
			d += bits.OnesCount32(a.Hashes[ia] ^ b.Hashes[ib])
			info += 32
		}
		if info == 0 {
			line += "  -- "
		} else {
			line += fmt.Sprintf(" %.2f", float64(d)/float64(info))
		}
	}
	t.Log(line)
}
