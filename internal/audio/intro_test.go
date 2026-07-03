package audio

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// lcg is a tiny deterministic PRNG so synthesized test audio never depends on
// math/rand or the run environment.
type lcg uint64

func (r *lcg) float() float64 {
	*r = *r*6364136223846793005 + 1442695040888963407
	return float64(uint32(*r>>32)) / (1 << 32)
}

// synthRange generates seconds of pseudo-music: amplitude-modulated sinusoids
// whose frequencies are drawn deterministically from seed within [lo, hi] Hz.
// The slow AM gives the Haitsma-Kalker hash real temporal structure to bite
// on, like actual music has.
func synthRange(seed uint64, seconds, lo, hi float64) []int16 {
	n := int(seconds * fpSampleRate)
	out := make([]int16, n)
	r := lcg(seed)
	r.float()
	r.float()
	const voices = 5
	var freq, amf, ph, amPh [voices]float64
	for v := 0; v < voices; v++ {
		freq[v] = lo + r.float()*(hi-lo)
		amf[v] = 0.4 + 4*r.float()
		ph[v] = 2 * math.Pi * r.float()
		amPh[v] = 2 * math.Pi * r.float()
	}
	for i := 0; i < n; i++ {
		t := float64(i) / fpSampleRate
		var s float64
		for v := 0; v < voices; v++ {
			s += 0.14 * math.Sin(2*math.Pi*freq[v]*t+ph[v]) * (0.6 + 0.4*math.Sin(2*math.Pi*amf[v]*t+amPh[v]))
		}
		out[i] = int16(s * 32767)
	}
	return out
}

func synth(seed uint64, seconds float64) []int16 {
	return synthRange(seed, seconds, 330, 2730)
}

func silence(seconds float64) []int16 {
	return make([]int16, int(seconds*fpSampleRate))
}

func concat(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestFingerprintPCMBasics(t *testing.T) {
	if fp := FingerprintPCM(nil); fp.Frames() != 0 {
		t.Fatalf("empty input: got %d frames", fp.Frames())
	}
	if fp := FingerprintPCM(make([]int16, fpWindowSize)); fp.Frames() != 0 {
		t.Fatalf("single window: got %d frames", fp.Frames())
	}
	fp := FingerprintPCM(synth(1, 3))
	want := (3*fpSampleRate-fpWindowSize)/fpHopSize + 1 - 1
	if fp.Frames() != want {
		t.Fatalf("frames = %d, want %d", fp.Frames(), want)
	}
	if len(fp.Silent) != fp.Frames() || len(fp.RMSdB) != fp.Frames() || len(fp.Bands) != fp.Frames() {
		t.Fatal("fingerprint slices are not aligned")
	}
	for i, s := range fp.Silent {
		if s {
			t.Fatalf("frame %d of loud synth marked silent", i)
		}
	}
	if sfp := FingerprintPCM(silence(3)); sfp.Frames() > 0 && !sfp.Silent[0] {
		t.Fatal("silence not marked silent")
	}
}

// approx asserts got is within slack of want.
func approx(t *testing.T, name string, got, want, slack float64) {
	t.Helper()
	if math.Abs(got-want) > slack {
		t.Fatalf("%s = %.2fs, want %.2f±%.2fs", name, got, want, slack)
	}
}

func fingerprintAll(pcms ...[]int16) []Fingerprint {
	fps := make([]Fingerprint, len(pcms))
	for i, p := range pcms {
		fps[i] = FingerprintPCM(p)
	}
	return fps
}

func TestCommonIntroDetected(t *testing.T) {
	intro := synth(100, 5)
	fps := fingerprintAll(
		concat(intro, synth(1, 20)),
		concat(intro, synth(2, 20)),
		concat(intro, synth(3, 20)),
	)
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	approx(t, "IntroSeconds", res.IntroSeconds, 5, 0.6)
	for i, item := range res.Items {
		approx(t, "cut", item.CutSeconds, 5, 0.6)
		if item.Pairs != 2 {
			t.Fatalf("item %d pairs = %d, want 2", i, item.Pairs)
		}
	}
}

func TestCommonIntroWithOffsetLead(t *testing.T) {
	// One file has 0.8s of extra silence before the shared intro — not a
	// multiple of the hop, so alignment must also survive sub-hop offsets.
	intro := synth(100, 5)
	fps := fingerprintAll(
		concat(intro, synth(1, 20)),
		concat(silence(0.8), intro, synth(2, 20)),
		concat(intro, synth(3, 20)),
	)
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	approx(t, "cut[0]", res.Items[0].CutSeconds, 5, 0.6)
	approx(t, "cut[1]", res.Items[1].CutSeconds, 5.8, 0.6)
	approx(t, "cut[2]", res.Items[2].CutSeconds, 5, 0.6)
}

func TestNoCommonIntro(t *testing.T) {
	fps := fingerprintAll(synth(1, 20), synth(2, 20), synth(3, 20))
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	if res.IntroSeconds != 0 {
		t.Fatalf("IntroSeconds = %.2f, want 0", res.IntroSeconds)
	}
	for i, item := range res.Items {
		if item.CutSeconds != 0 {
			t.Fatalf("item %d cut = %.2f, want 0", i, item.CutSeconds)
		}
	}
}

func TestIdenticalFilesGiveNoEvidence(t *testing.T) {
	// Two copies of the same song share their entire span; that is a
	// duplicate, not an intro.
	song := concat(synth(100, 5), synth(1, 20))
	res := DetectCommonIntro(fingerprintAll(song, song), IntroOptions{MaxSeconds: 15})
	if res.IntroSeconds != 0 || res.Items[0].CutSeconds != 0 || res.Items[1].CutSeconds != 0 {
		t.Fatalf("identical files produced cuts: %+v", res)
	}
}

func TestOneItemMissingIntro(t *testing.T) {
	intro := synth(100, 5)
	fps := fingerprintAll(
		concat(intro, synth(1, 20)),
		concat(intro, synth(2, 20)),
		synth(3, 25), // no intro
		concat(intro, synth(4, 20)),
	)
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	approx(t, "IntroSeconds", res.IntroSeconds, 5, 0.6)
	if res.Items[2].CutSeconds != 0 {
		t.Fatalf("intro-less item got cut %.2f", res.Items[2].CutSeconds)
	}
	for _, i := range []int{0, 1, 3} {
		approx(t, "cut", res.Items[i].CutSeconds, 5, 0.6)
	}
}

func TestSilenceOnlyPrefixIsNotAnIntro(t *testing.T) {
	fps := fingerprintAll(
		concat(silence(3), synth(1, 20)),
		concat(silence(3), synth(2, 20)),
	)
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	if res.IntroSeconds != 0 || res.Items[0].CutSeconds != 0 {
		t.Fatalf("shared silence treated as intro: %+v", res)
	}
}

func TestTrailingSilenceJoinsTheCut(t *testing.T) {
	// A file whose intro is followed by a pause should have the pause
	// trimmed too; a file that jumps straight into music should not.
	intro := synth(100, 4)
	fps := fingerprintAll(
		concat(intro, silence(1), synth(1, 20)),
		concat(intro, synth(2, 20)),
	)
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	approx(t, "cut[0]", res.Items[0].CutSeconds, 5, 0.6)
	approx(t, "cut[1]", res.Items[1].CutSeconds, 4, 0.6)
}

func TestCommonIntroTooFewInputs(t *testing.T) {
	if res := DetectCommonIntro(nil, IntroOptions{}); res.IntroSeconds != 0 || len(res.Items) != 0 {
		t.Fatalf("nil input: %+v", res)
	}
	res := DetectCommonIntro(fingerprintAll(synth(1, 10)), IntroOptions{})
	if res.IntroSeconds != 0 || len(res.Items) != 1 || res.Items[0].CutSeconds != 0 {
		t.Fatalf("single input: %+v", res)
	}
}

func TestSoloIntroBoundary(t *testing.T) {
	// Low-register jingle, then a song living in a different part of the
	// spectrum: the boundary should be found without any cross-file help.
	pcm := concat(synthRange(7, 5, 330, 900), synthRange(8, 20, 1300, 2900))
	res := DetectSoloIntro(FingerprintPCM(pcm), IntroOptions{MaxSeconds: 15})
	if !res.Confident {
		t.Fatalf("expected confident detection, got score %.3f", res.Score)
	}
	approx(t, "CutSeconds", res.CutSeconds, 5, 0.8)
}

func TestSoloIntroHomogeneousSong(t *testing.T) {
	res := DetectSoloIntro(FingerprintPCM(synth(9, 25)), IntroOptions{MaxSeconds: 15})
	if res.Confident {
		t.Fatalf("homogeneous audio reported an intro at %.2fs (score %.3f)", res.BoundarySeconds, res.Score)
	}
	if res.CutSeconds != 0 {
		t.Fatalf("unconfident result still set CutSeconds %.2f", res.CutSeconds)
	}
}

func TestThreeItemsOneMissingIntro(t *testing.T) {
	// With three items, each file gets exactly two pairwise votes. A file
	// sharing the intro with only one peer must still get the full cut —
	// an averaging median would halve it (votes [5, 0] → 2.5).
	intro := synth(100, 5)
	fps := fingerprintAll(
		concat(intro, synth(1, 20)),
		concat(intro, synth(2, 20)),
		synth(3, 25), // no intro
	)
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	approx(t, "cut[0]", res.Items[0].CutSeconds, 5, 0.6)
	approx(t, "cut[1]", res.Items[1].CutSeconds, 5, 0.6)
	if res.Items[2].CutSeconds != 0 {
		t.Fatalf("intro-less item got cut %.2f", res.Items[2].CutSeconds)
	}
}

func TestEmptyFingerprintDoesNotVote(t *testing.T) {
	// An unanalyzable (zero-frame) file must not dilute the consensus with
	// zero votes, and must not receive a cut itself.
	intro := synth(100, 5)
	fps := fingerprintAll(
		concat(intro, synth(1, 20)),
		concat(intro, synth(2, 20)),
		nil,
	)
	res := DetectCommonIntro(fps, IntroOptions{MaxSeconds: 15})
	approx(t, "cut[0]", res.Items[0].CutSeconds, 5, 0.6)
	approx(t, "cut[1]", res.Items[1].CutSeconds, 5, 0.6)
	if res.Items[2].CutSeconds != 0 || res.Items[2].Pairs != 0 {
		t.Fatalf("empty fingerprint voted: %+v", res.Items[2])
	}
}

// handMadeFP builds a fingerprint directly from hash values (all frames
// non-silent), for tests that need exact bit-error rates.
func handMadeFP(hashes []uint32) Fingerprint {
	fp := Fingerprint{
		Hashes: hashes,
		Silent: make([]bool, len(hashes)),
		RMSdB:  make([]float64, len(hashes)),
		Bands:  make([][]float32, len(hashes)),
	}
	for i := range fp.RMSdB {
		fp.RMSdB[i] = -20
		fp.Bands[i] = make([]float32, fpBands)
	}
	return fp
}

func TestWalkDivergenceScratchReuseIsDeterministic(t *testing.T) {
	// A pair whose post-boundary bit-error rate sits between the coarse
	// (0.35) and fine (0.45) thresholds makes the fine scan walk past the
	// coarse break point. The scratch buffer is reused across pairs; stale
	// entries from an earlier walk must not change this pair's boundary.
	n := 600
	mkPair := func() (Fingerprint, Fingerprint) {
		a := make([]uint32, n)
		b := make([]uint32, n)
		for i := 0; i < n; i++ {
			a[i] = 0x5A5A5A5A
			if i < 300 {
				b[i] = a[i]
			} else {
				b[i] = a[i] ^ 0x1FFF // 13 of 32 bits differ: BER ≈ 0.41
			}
		}
		return handMadeFP(a), handMadeFP(b)
	}
	fpA, fpB := mkPair()

	fresh := newPairScratch()
	want := matchPair(fpA, fpB, 0, fresh)

	// Pollute a scratch with a fully-diverged pair, then reuse it.
	polluted := newPairScratch()
	div := make([]uint32, n)
	for i := range div {
		div[i] = 0xFFFF0000
	}
	matchPair(handMadeFP(div), handMadeFP(make([]uint32, n)), 0, polluted)
	got := matchPair(fpA, fpB, 0, polluted)

	if got.prefixA != want.prefixA || got.prefixB != want.prefixB {
		t.Fatalf("scratch reuse changed the boundary: fresh=%+v reused=%+v", want, got)
	}
}

func TestFramesForRoundTrip(t *testing.T) {
	fp := Fingerprint{}
	for p := 1; p < 4000; p++ {
		if got := framesFor(fp.Seconds(p)); got != p {
			t.Fatalf("framesFor(Seconds(%d)) = %d", p, got)
		}
	}
}

// tail returns the last seconds of pcm (all of it when shorter).
func tail(pcm []int16, seconds float64) []int16 {
	n := int(seconds * fpSampleRate)
	if n >= len(pcm) {
		return pcm
	}
	return pcm[len(pcm)-n:]
}

func TestReversedRoundTrip(t *testing.T) {
	fp := FingerprintPCM(concat(synth(5, 6), silence(1), synth(6, 3)))
	rr := fp.Reversed().Reversed()
	if fp.Frames() != rr.Frames() {
		t.Fatalf("frames changed: %d → %d", fp.Frames(), rr.Frames())
	}
	for i := range fp.Hashes {
		if fp.Hashes[i] != rr.Hashes[i] || fp.Silent[i] != rr.Silent[i] || fp.RMSdB[i] != rr.RMSdB[i] {
			t.Fatalf("frame %d differs after double reversal", i)
		}
	}
}

func TestCommonOutroDetected(t *testing.T) {
	// Three songs that end with the same closing jingle; one also has a
	// second of trailing silence, which should join its cut.
	outro := synth(200, 5)
	fps := fingerprintAll(
		tail(concat(synth(1, 20), outro), 15),
		tail(concat(synth(2, 20), outro, silence(1)), 15),
		tail(concat(synth(3, 20), outro), 15),
	)
	res := DetectCommonOutro(fps, IntroOptions{MaxSeconds: 12})
	approx(t, "IntroSeconds", res.IntroSeconds, 5, 0.6)
	approx(t, "cut[0]", res.Items[0].CutSeconds, 5, 0.6)
	approx(t, "cut[1]", res.Items[1].CutSeconds, 6, 0.6)
	approx(t, "cut[2]", res.Items[2].CutSeconds, 5, 0.6)
}

func TestSoloOutroBoundary(t *testing.T) {
	pcm := concat(synthRange(11, 20, 1300, 2900), synthRange(12, 5, 330, 900))
	res := DetectSoloOutro(FingerprintPCM(tail(pcm, 15)), IntroOptions{MaxSeconds: 12})
	if !res.Confident {
		t.Fatalf("expected confident outro, got score %.3f", res.Score)
	}
	approx(t, "CutSeconds", res.CutSeconds, 5, 0.8)
}

// writeWAV writes samples as a mono 16-bit PCM WAV file.
func writeWAV(t *testing.T, path string, samples []int16, rate int) {
	t.Helper()
	data := make([]byte, 44+2*len(samples))
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*len(samples)))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:], 1) // mono
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*len(samples)))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(s))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintFileDecodesViaFFmpeg(t *testing.T) {
	conv := NewConverter()
	if !conv.Available() {
		t.Skip("ffmpeg not available")
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	pcm := synth(42, 6)
	writeWAV(t, path, pcm, fpSampleRate)

	fp, err := conv.FingerprintFile(context.Background(), path, 4)
	if err != nil {
		t.Fatal(err)
	}
	// maxSeconds must cap the decode: ~4s of frames, not 6s.
	gotSec := fp.Seconds(fp.Frames())
	if gotSec < 3 || gotSec > 4.5 {
		t.Fatalf("decoded %.2fs, want ~4s", gotSec)
	}

	// The decoded fingerprint must match a direct PCM fingerprint of the
	// same audio closely enough for pair matching to see them as identical.
	direct := FingerprintPCM(pcm[:4*fpSampleRate])
	m := matchPair(fp, direct, framesFor(0.5), newPairScratch())
	if !m.identical {
		t.Fatalf("ffmpeg-decoded audio does not match direct fingerprint: %+v", m)
	}
}

func TestFingerprintFileTailDecodesTheEnd(t *testing.T) {
	conv := NewConverter()
	if !conv.Available() {
		t.Skip("ffmpeg not available")
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	pcm := synth(43, 10)
	writeWAV(t, path, pcm, fpSampleRate)

	fp, err := conv.FingerprintFileTail(context.Background(), path, 4)
	if err != nil {
		t.Fatal(err)
	}
	gotSec := fp.Seconds(fp.Frames())
	if gotSec < 3 || gotSec > 4.5 {
		t.Fatalf("decoded %.2fs of tail, want ~4s", gotSec)
	}
	direct := FingerprintPCM(tail(pcm, 4))
	m := matchPair(fp, direct, framesFor(0.5), newPairScratch())
	if !m.identical {
		t.Fatalf("tail decode does not match the file's actual tail: %+v", m)
	}

	// A tail request longer than the file must decode the whole file.
	whole, err := conv.FingerprintFileTail(context.Background(), path, 60)
	if err != nil {
		t.Fatal(err)
	}
	if sec := whole.Seconds(whole.Frames()); sec < 9 || sec > 10.5 {
		t.Fatalf("oversized tail request decoded %.2fs, want ~10s", sec)
	}
}
