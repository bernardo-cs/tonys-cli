package audio

import (
	"math"
	"math/bits"
	"sort"
)

// IntroOptions bounds intro detection. Zero fields take the documented
// defaults.
type IntroOptions struct {
	// MinSeconds is the shortest prefix that counts as an intro (default 1).
	MinSeconds float64
	// MaxSeconds is the longest intro to look for (default 60). A shared
	// prefix longer than this is not treated as an intro.
	MaxSeconds float64
	// MaxShiftSeconds is the largest per-file lead difference (extra silence
	// or padding before the intro) absorbed while aligning a pair (default 2).
	MaxShiftSeconds float64
}

func (o IntroOptions) withDefaults() IntroOptions {
	if o.MinSeconds <= 0 {
		o.MinSeconds = 1
	}
	if o.MaxSeconds <= 0 {
		o.MaxSeconds = 60
	}
	if o.MaxShiftSeconds <= 0 {
		o.MaxShiftSeconds = 2
	}
	return o
}

// FingerprintSeconds is how much audio a fingerprint must cover for detection
// under these options: the intro scan window, alignment slack, and enough
// post-boundary context to observe the files diverging.
func (o IntroOptions) FingerprintSeconds() float64 {
	o = o.withDefaults()
	return o.MaxSeconds + o.MaxShiftSeconds + 15
}

// Matching thresholds. A hash of the same audio in two different lossy encodes
// disagrees on a small fraction of bits, while unrelated audio disagrees on
// ~50%; the windowed bit-error rate separates the two regimes cleanly.
const (
	berWindow         = 86   // coarse divergence window, ~1 s of hashes
	berThreshold      = 0.35 // windowed BER above this = diverged
	fineWindow        = 20   // forward window (~0.23 s) for boundary refinement
	fineThreshold     = 0.45
	minInfoBits       = 16 * 32 // windows with fewer informative bits are inconclusive
	identicalFrac     = 0.95    // prefix covering this much of the overlap = same recording
	minContentSeconds = 0.5     // a prefix must contain this much non-silent audio to be an intro
	silenceExtSeconds = 3.0     // longest run of a file's own trailing silence added to its cut
)

// ItemIntro is one file's slice of a DetectCommonIntro result.
type ItemIntro struct {
	// CutSeconds is how much to trim from the start of this file (0 = the
	// file does not share the intro).
	CutSeconds float64 `json:"cutSeconds"`
	// Pairs is how many pairwise comparisons informed the cut.
	Pairs int `json:"pairs"`
}

// CommonIntroResult is the outcome of cross-file intro detection.
type CommonIntroResult struct {
	// IntroSeconds is the consensus intro length across files that share one
	// (0 = no common intro found).
	IntroSeconds float64 `json:"introSeconds"`
	// Items aligns with the fingerprints passed to DetectCommonIntro.
	Items []ItemIntro `json:"items"`
}

// DetectCommonIntro estimates the shared audio prefix ("intro") across a set
// of files and returns a per-file cut point. Every pair of fingerprints is
// aligned (absorbing small lead differences) and walked forward until the
// audio diverges; a file's cut is the median of its pairwise prefix lengths,
// so a file lacking the intro gets 0 and a duplicated song cannot drag the
// consensus. Pairs that match across (almost) their whole compared span are
// the same recording twice and contribute no evidence. Prefixes made of
// silence alone do not count as intros.
func DetectCommonIntro(fps []Fingerprint, opts IntroOptions) CommonIntroResult {
	opts = opts.withDefaults()
	n := len(fps)
	res := CommonIntroResult{Items: make([]ItemIntro, n)}
	if n < 2 {
		return res
	}
	maxShift := int(opts.MaxShiftSeconds / fpHopSeconds)

	// For large sets, comparing every pair is wasteful; each file is compared
	// against an evenly-spaced reference subset instead, which keeps the
	// per-file median just as robust.
	refs := make([]int, 0, n)
	if n <= 12 {
		for i := 0; i < n; i++ {
			refs = append(refs, i)
		}
	} else {
		const numRefs = 8
		for k := 0; k < numRefs; k++ {
			refs = append(refs, k*(n-1)/(numRefs-1))
		}
	}

	scratch := newPairScratch()
	cuts := make([][]float64, n)
	done := make(map[[2]int]bool)
	for i := 0; i < n; i++ {
		for _, j := range refs {
			if i == j {
				continue
			}
			if fps[i].Frames() == 0 || fps[j].Frames() == 0 {
				continue // an unanalyzable file must not vote zeros into the consensus
			}
			key := [2]int{min(i, j), max(i, j)}
			if done[key] {
				continue
			}
			done[key] = true
			m := matchPair(fps[i], fps[j], maxShift, scratch)
			if m.identical {
				continue // duplicate recording: no boundary evidence
			}
			secI, secJ := fps[i].Seconds(m.prefixA), fps[j].Seconds(m.prefixB)
			if fps[i].Seconds(m.content) < minContentSeconds {
				secI, secJ = 0, 0 // silence-only or empty prefix
			}
			// matchPair compares (a=fps[i], b=fps[j]) in call order; map the
			// pair back to the unordered key.
			if i > j {
				secI, secJ = secJ, secI
			}
			cuts[key[0]] = append(cuts[key[0]], secI)
			cuts[key[1]] = append(cuts[key[1]], secJ)
			res.Items[key[0]].Pairs++
			res.Items[key[1]].Pairs++
		}
	}

	var nonzero []float64
	for i := 0; i < n; i++ {
		cut := median(cuts[i])
		if cut < opts.MinSeconds || cut > opts.MaxSeconds {
			cut = 0
		}
		if cut > 0 {
			nonzero = append(nonzero, cut)
			// The matched prefix ends at the last positively-matching audio;
			// if this file follows its intro with silence, cut that too.
			cut += silenceRun(fps[i], framesFor(cut))
		}
		res.Items[i].CutSeconds = cut
	}
	res.IntroSeconds = median(nonzero)
	return res
}

// silenceRun returns the duration of the file's own silent frames starting at
// frame, capped at silenceExtSeconds.
func silenceRun(fp Fingerprint, frame int) float64 {
	end := min(frame+framesFor(silenceExtSeconds), fp.Frames())
	i := frame
	for i < end && fp.Silent[i] {
		i++
	}
	return fp.Seconds(i - frame)
}

// pairMatch is the outcome of aligning two fingerprints.
type pairMatch struct {
	prefixA, prefixB int  // matched prefix from each file's start, in frames
	content          int  // non-silent frames inside the matched prefix
	identical        bool // the files match across (nearly) the whole overlap
}

type pairScratch struct {
	d, info []int32
}

func newPairScratch() *pairScratch { return &pairScratch{} }

func (s *pairScratch) grow(n int) {
	if cap(s.d) < n {
		s.d = make([]int32, n)
		s.info = make([]int32, n)
	}
	s.d = s.d[:n]
	s.info = s.info[:n]
}

// matchPair finds the longest shared audio prefix between a and b, trying
// frame shifts up to ±maxShift so differing lead-ins (extra silence/padding
// before the shared part) still align. Larger prefixes win; ties prefer the
// smaller shift.
func matchPair(a, b Fingerprint, maxShift int, scratch *pairScratch) pairMatch {
	var best pairMatch
	bestPrefix, bestShift := -1, 0
	for s := -maxShift; s <= maxShift; s++ {
		startA, startB := 0, 0
		if s > 0 {
			startA = s
		} else {
			startB = -s
		}
		n := min(a.Frames()-startA, b.Frames()-startB)
		if n <= bestPrefix {
			continue // cannot beat the current best
		}
		prefix, raw, content := walkDivergence(a, b, startA, startB, n, scratch)
		if prefix > bestPrefix || (prefix == bestPrefix && abs(s) < abs(bestShift)) {
			bestPrefix, bestShift = prefix, s
			best = pairMatch{
				prefixA:   startA + prefix,
				prefixB:   startB + prefix,
				content:   content,
				identical: n > 0 && float64(raw) >= identicalFrac*float64(n),
			}
		}
	}
	return best
}

// walkDivergence walks the aligned frames a[startA+i] / b[startB+i] and
// returns the matching prefix trimmed back to its last informative frame, the
// raw (untrimmed) prefix, and how many prefix frames carried non-silent audio.
// Near-silent frames on either side are wildcards: they extend a match but
// neither confirm nor break it — and are trimmed from the end again so one
// file's post-intro silence can never claim the other file's music. The
// coarse pass finds the ~1 s window where the bit-error rate first exceeds
// berThreshold; the fine pass then locates the boundary inside it to ~0.1 s.
func walkDivergence(a, b Fingerprint, startA, startB, n int, scratch *pairScratch) (prefix, raw, content int) {
	scratch.grow(n)
	d, info := scratch.d, scratch.info
	compute := func(i int) {
		ia, ib := startA+i, startB+i
		if a.Silent[ia] || b.Silent[ib] {
			d[i], info[i] = 0, 0
		} else {
			d[i] = int32(bits.OnesCount32(a.Hashes[ia] ^ b.Hashes[ib]))
			info[i] = 32
		}
	}

	fail := -1
	written := 0 // d/info are valid for indices [0, written)
	var dSum, iSum int32
	for i := 0; i < n; i++ {
		compute(i)
		written = i + 1
		dSum += d[i]
		iSum += info[i]
		if i >= berWindow {
			dSum -= d[i-berWindow]
			iSum -= info[i-berWindow]
		}
		if i >= berWindow-1 && iSum >= minInfoBits && float64(dSum) > berThreshold*float64(iSum) {
			fail = i - berWindow + 1
			break
		}
	}
	if fail < 0 {
		prefix = n
	} else {
		// The boundary sits inside [fail, fail+berWindow); find the first
		// point whose short forward window is clearly diverged. The scan
		// reads past where the coarse loop broke, so first extend the
		// computed range — the scratch buffer is reused across pairs and
		// shifts and must never leak a previous walk's values into this one.
		for ; written < min(n, fail+berWindow+fineWindow); written++ {
			compute(written)
		}
		prefix = fail
		for j := fail; j <= fail+berWindow && j+fineWindow <= written; j++ {
			var fd, fi int32
			for k := j; k < j+fineWindow; k++ {
				fd += d[k]
				fi += info[k]
			}
			if fi >= minInfoBits/2 && float64(fd) > fineThreshold*float64(fi) {
				prefix = j
				break
			}
		}
	}
	raw = prefix
	for prefix > 0 && info[prefix-1] == 0 {
		prefix--
	}
	for i := 0; i < prefix; i++ {
		if info[i] > 0 {
			content++
		}
	}
	return prefix, raw, content
}

// Reversed returns the fingerprint with time running backwards, turning
// suffix (outro) questions into prefix (intro) questions. The hashes are
// reused as-is: they describe frame-to-frame transitions, and aligning two
// reversed sequences compares exactly the same transitions.
func (fp Fingerprint) Reversed() Fingerprint {
	n := fp.Frames()
	rev := Fingerprint{
		Hashes: make([]uint32, n),
		Silent: make([]bool, n),
		RMSdB:  make([]float64, n),
		Bands:  make([][]float32, n),
	}
	for i := 0; i < n; i++ {
		j := n - 1 - i
		rev.Hashes[i] = fp.Hashes[j]
		rev.Silent[i] = fp.Silent[j]
		rev.RMSdB[i] = fp.RMSdB[j]
		rev.Bands[i] = fp.Bands[j]
	}
	return rev
}

// DetectCommonOutro is DetectCommonIntro mirrored in time: fps must
// fingerprint each file's tail (FingerprintFileTail) and every CutSeconds is
// measured from the END of its file.
func DetectCommonOutro(fps []Fingerprint, opts IntroOptions) CommonIntroResult {
	rev := make([]Fingerprint, len(fps))
	for i := range fps {
		rev[i] = fps[i].Reversed()
	}
	return DetectCommonIntro(rev, opts)
}

// DetectSoloOutro mirrors DetectSoloIntro: fp should fingerprint the file's
// tail, and the returned boundary/cut are measured from the end of the file.
func DetectSoloOutro(fp Fingerprint, opts IntroOptions) SoloIntroResult {
	return DetectSoloIntro(fp.Reversed(), opts)
}

// SoloIntroResult is the outcome of single-file intro detection.
type SoloIntroResult struct {
	// CutSeconds is the boundary to trim at, or 0 when detection is not
	// confident enough to act on.
	CutSeconds float64 `json:"cutSeconds"`
	// BoundarySeconds is the best candidate boundary regardless of confidence.
	BoundarySeconds float64 `json:"boundarySeconds"`
	// Score is the spectral contrast at the boundary (0..2; higher = the
	// audio before and after differ more).
	Score float64 `json:"score"`
	// Cohesion measures how spectrally uniform the candidate jingle is
	// (0..1). Real intros/outros are one coherent texture; a stretch of song
	// with several sections in it is not, however sharp its edge.
	Cohesion float64 `json:"cohesion"`
	// Confident reports whether Score and Cohesion cleared the thresholds.
	Confident bool `json:"confident"`
}

// Solo-detection parameters: the candidate jingle (everything before the
// boundary) is compared against the soloContext seconds that follow it, and
// the boundary is only acted on when the edge is sharp (contrast) AND the
// jingle side is one coherent texture (cohesion) — the second test is what
// stops a verse→chorus change in the middle of a song from being mistaken
// for a jingle boundary.
const (
	soloContextSeconds = 4.0
	soloThreshold      = 0.45
	soloCohesionMin    = 0.75
	soloSnapSeconds    = 0.75 // snap the boundary to the quietest frame nearby
)

// DetectSoloIntro looks for an intro boundary inside a single file: the point
// within [MinSeconds, MaxSeconds] where the average spectral shape of
// everything before differs most from the audio right after. This is a
// heuristic — a song whose real first verse sounds nothing like its intro is
// indistinguishable from a foreign intro — so the result carries a confidence
// gate and CutSeconds stays 0 below it.
func DetectSoloIntro(fp Fingerprint, opts IntroOptions) SoloIntroResult {
	opts = opts.withDefaults()
	var res SoloIntroResult
	frames := fp.Frames()
	context := framesFor(soloContextSeconds)
	tMin := framesFor(opts.MinSeconds)
	tMax := min(framesFor(opts.MaxSeconds), frames-context)
	if tMin < 1 || tMax <= tMin {
		return res
	}

	// Per-frame spectral shape: log band energies, mean-removed and
	// L2-normalized so overall level drops out. Silent frames carry no shape.
	shapes := make([][]float32, frames)
	for i := 0; i < frames; i++ {
		if fp.RMSdB[i] < fpSilenceDB {
			continue
		}
		v := make([]float32, fpBands)
		var mean float64
		for _, e := range fp.Bands[i] {
			mean += float64(e)
		}
		mean /= fpBands
		var norm float64
		for b, e := range fp.Bands[i] {
			c := float64(e) - mean
			v[b] = float32(c)
			norm += c * c
		}
		if norm < 1e-9 {
			continue
		}
		inv := float32(1 / math.Sqrt(norm))
		for b := range v {
			v[b] *= inv
		}
		shapes[i] = v
	}

	// Prefix sums let every candidate's before/after means be computed in
	// O(bands).
	sum := make([][fpBands]float64, frames+1)
	count := make([]int, frames+1)
	for i := 0; i < frames; i++ {
		sum[i+1] = sum[i]
		count[i+1] = count[i]
		if shapes[i] != nil {
			for b, v := range shapes[i] {
				sum[i+1][b] += float64(v)
			}
			count[i+1]++
		}
	}

	// Each shape is a unit vector, so the cohesion of [0, t) — the average
	// cosine between its frames and their mean direction — is simply the
	// mean resultant length |Σv|/n, free from the same prefix sums.
	minFrames := framesFor(0.5) // need ≥0.5 s of shape on each side
	bestT, bestScore, bestCohesion, bestRank := -1, 0.0, 0.0, 0.0
	for t := tMin; t <= tMax; t++ {
		nBefore := count[t]
		nAfter := count[min(t+context, frames)] - count[t]
		if nBefore < minFrames || nAfter < minFrames {
			continue
		}
		var before, after [fpBands]float64
		end := min(t+context, frames)
		for b := 0; b < fpBands; b++ {
			before[b] = sum[t][b]
			after[b] = sum[end][b] - sum[t][b]
		}
		score := 1 - cosine(before[:], after[:])
		cohesion := norm(before[:]) / float64(nBefore)
		// Rank by contrast weighted by squared cohesion, so a sharp edge
		// right after a coherent jingle beats a sharper edge that needs a
		// patchwork "intro" to exist.
		rank := score * cohesion * cohesion
		if rank > bestRank {
			bestRank, bestScore, bestCohesion, bestT = rank, score, cohesion, t
		}
	}
	if bestT < 0 {
		return res
	}

	// Transitions usually pass through a brief dip; snap to the quietest
	// frame near the peak-contrast point.
	snap := framesFor(soloSnapSeconds)
	boundary := bestT
	for t := max(tMin, bestT-snap); t <= min(tMax, bestT+snap); t++ {
		if fp.RMSdB[t] < fp.RMSdB[boundary] {
			boundary = t
		}
	}

	res.BoundarySeconds = fp.Seconds(boundary)
	res.Score = bestScore
	res.Cohesion = bestCohesion
	res.Confident = bestScore >= soloThreshold && bestCohesion >= soloCohesionMin
	if res.Confident {
		res.CutSeconds = res.BoundarySeconds
	}
	return res
}

// norm returns the Euclidean length of v.
func norm(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x * x
	}
	return math.Sqrt(s)
}

// cosine returns the cosine similarity of two vectors (0 when either is ~0).
func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na < 1e-12 || nb < 1e-12 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// median returns the middle value, taking the upper middle on even counts
// instead of averaging. Pairwise cuts are bimodal — a file either shares the
// jingle (values near its length) or does not (zeros) — and averaging across
// an even split would invent a half-jingle cut that matches nothing in the
// audio (e.g. votes [5.5, 0] must not become 2.75).
func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// framesFor converts a duration in seconds to a hash-frame count. It rounds
// rather than truncates so that framesFor(Seconds(p)) == p — the float64
// round trip lands a hair under p for ~8% of frame counts, which would
// silently shift positions (e.g. the silence-extension start) one frame off.
func framesFor(seconds float64) int { return int(math.Round(seconds / fpHopSeconds)) }
