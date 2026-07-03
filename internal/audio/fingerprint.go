package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Fingerprinting parameters. Audio is decoded to mono 11025 Hz and analyzed in
// heavily-overlapping 2048-sample windows (~186 ms every ~11.6 ms), reduced to
// 33 log-spaced band energies between 300 and 3000 Hz. Each analysis frame
// yields one 32-bit hash whose bits are the signs of the time-and-frequency
// energy differences (the Haitsma-Kalker robust hash), which survives lossy
// re-encoding and level changes — exactly what two YouTube uploads of the same
// intro differ by.
const (
	fpSampleRate = 11025
	fpWindowSize = 2048
	fpHopSize    = 128
	fpBands      = 33 // adjacent-band differences yield 32 hash bits
	fpMinFreq    = 300.0
	fpMaxFreq    = 3000.0
	fpSilenceDB  = -55.0 // window RMS below this (dBFS) counts as silence
)

// fpHopSeconds is the time step between consecutive hashes.
const fpHopSeconds = float64(fpHopSize) / float64(fpSampleRate)

// Fingerprint is the per-frame spectral summary of (a prefix of) one audio
// file. All slices share the same length and index the same analysis frame.
type Fingerprint struct {
	// Hashes[i] fingerprints the transition from frame i to frame i+1.
	Hashes []uint32
	// Silent[i] is true when frame i or i+1 is near-silent; such hashes carry
	// no information and are treated as wildcards during matching.
	Silent []bool
	// RMSdB[i] is frame i's window RMS in dBFS.
	RMSdB []float64
	// Bands[i] holds frame i's log band energies (used by spectral intro
	// detection; float32 to keep long fingerprints small).
	Bands [][]float32
}

// Frames returns the number of hash frames.
func (fp Fingerprint) Frames() int { return len(fp.Hashes) }

// Seconds converts a frame count to seconds.
func (fp Fingerprint) Seconds(frames int) float64 { return float64(frames) * fpHopSeconds }

// FingerprintPCM computes the fingerprint of raw mono 11025 Hz s16le samples.
// It is deterministic and needs no external tools, which keeps the whole
// detection pipeline unit-testable with synthesized audio.
func FingerprintPCM(samples []int16) Fingerprint {
	if len(samples) < fpWindowSize+fpHopSize {
		return Fingerprint{}
	}
	numFrames := (len(samples)-fpWindowSize)/fpHopSize + 1

	window := make([]float64, fpWindowSize)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fpWindowSize-1))
	}
	binLo := bandBins()

	// Prefix sums of squared samples give each window's RMS in O(1).
	energy := make([]float64, len(samples)+1)
	for i, s := range samples {
		v := float64(s) / 32768.0
		energy[i+1] = energy[i] + v*v
	}

	re := make([]float64, fpWindowSize)
	im := make([]float64, fpWindowSize)
	logE := make([][]float64, numFrames)
	rmsDB := make([]float64, numFrames)
	for n := 0; n < numFrames; n++ {
		start := n * fpHopSize
		for i := 0; i < fpWindowSize; i++ {
			re[i] = float64(samples[start+i]) / 32768.0 * window[i]
			im[i] = 0
		}
		fft(re, im)
		e := make([]float64, fpBands)
		for b := 0; b < fpBands; b++ {
			var sum float64
			for k := binLo[b]; k < binLo[b+1]; k++ {
				sum += re[k]*re[k] + im[k]*im[k]
			}
			e[b] = math.Log(sum + 1e-12)
		}
		logE[n] = e
		mean := (energy[start+fpWindowSize] - energy[start]) / float64(fpWindowSize)
		rmsDB[n] = 20 * math.Log10(math.Sqrt(mean)+1e-12)
	}

	fp := Fingerprint{
		Hashes: make([]uint32, numFrames-1),
		Silent: make([]bool, numFrames-1),
		RMSdB:  rmsDB[:numFrames-1],
		Bands:  make([][]float32, numFrames-1),
	}
	for n := 0; n < numFrames-1; n++ {
		var h uint32
		cur, next := logE[n], logE[n+1]
		for m := 0; m < fpBands-1; m++ {
			if (next[m]-next[m+1])-(cur[m]-cur[m+1]) > 0 {
				h |= 1 << uint(m)
			}
		}
		fp.Hashes[n] = h
		fp.Silent[n] = rmsDB[n] < fpSilenceDB || rmsDB[n+1] < fpSilenceDB
		bands := make([]float32, fpBands)
		for b, v := range cur {
			bands[b] = float32(v)
		}
		fp.Bands[n] = bands
	}
	return fp
}

// bandBins returns the fpBands+1 FFT bin edges of the log-spaced analysis
// bands.
func bandBins() []int {
	edges := make([]int, fpBands+1)
	ratio := fpMaxFreq / fpMinFreq
	for i := 0; i <= fpBands; i++ {
		f := fpMinFreq * math.Pow(ratio, float64(i)/float64(fpBands))
		edges[i] = int(f * float64(fpWindowSize) / float64(fpSampleRate))
	}
	// Guarantee every band spans at least one bin.
	for i := 1; i <= fpBands; i++ {
		if edges[i] <= edges[i-1] {
			edges[i] = edges[i-1] + 1
		}
	}
	return edges
}

// FingerprintFile decodes up to maxSeconds of path (any format ffmpeg reads)
// and fingerprints it. maxSeconds <= 0 decodes the whole file.
func (c *Converter) FingerprintFile(ctx context.Context, path string, maxSeconds float64) (Fingerprint, error) {
	samples, err := c.decodePCM(ctx, path, maxSeconds, false)
	if err != nil {
		return Fingerprint{}, err
	}
	return FingerprintPCM(samples), nil
}

// FingerprintFileTail decodes and fingerprints the last maxSeconds of path
// (the whole file when it is shorter), for outro detection.
func (c *Converter) FingerprintFileTail(ctx context.Context, path string, maxSeconds float64) (Fingerprint, error) {
	samples, err := c.decodePCM(ctx, path, maxSeconds, true)
	if err != nil {
		return Fingerprint{}, err
	}
	return FingerprintPCM(samples), nil
}

// decodePCM shells out to ffmpeg to decode path into mono 11025 Hz s16le
// samples, capped at maxSeconds when positive — measured from the end of the
// file when fromEnd is set (ffmpeg clamps to the start for shorter files).
func (c *Converter) decodePCM(ctx context.Context, path string, maxSeconds float64, fromEnd bool) ([]int16, error) {
	if !c.Available() {
		return nil, errFFmpegMissing
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error"}
	if fromEnd && maxSeconds > 0 {
		args = append(args, "-sseof", fmt.Sprintf("-%.3f", maxSeconds))
	}
	args = append(args, "-i", path, "-vn")
	if !fromEnd && maxSeconds > 0 {
		args = append(args, "-t", fmt.Sprintf("%.3f", maxSeconds))
	}
	args = append(args, "-ac", "1", "-ar", strconv.Itoa(fpSampleRate), "-f", "s16le", "pipe:1")

	cmd := exec.CommandContext(ctx, c.FFmpegPath, args...)
	var out bytes.Buffer
	var stderr strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %s", ffmpegMessage(stderr.String(), err))
	}
	raw := out.Bytes()
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return samples, nil
}
