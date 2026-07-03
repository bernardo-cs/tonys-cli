package audio

import "math"

// fft computes an in-place radix-2 Cooley-Tukey FFT over re/im, whose length
// must be a power of two. Precision from the twiddle recurrence is more than
// sufficient for band-energy fingerprinting.
func fft(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wRe, wIm := math.Cos(ang), math.Sin(ang)
		for start := 0; start < n; start += length {
			curRe, curIm := 1.0, 0.0
			half := length / 2
			for k := 0; k < half; k++ {
				i, j := start+k, start+k+half
				tRe := re[j]*curRe - im[j]*curIm
				tIm := re[j]*curIm + im[j]*curRe
				re[j], im[j] = re[i]-tRe, im[i]-tIm
				re[i], im[i] = re[i]+tRe, im[i]+tIm
				curRe, curIm = curRe*wRe-curIm*wIm, curRe*wIm+curIm*wRe
			}
		}
	}
}
