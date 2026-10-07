package synth

import (
	"math/bits"
	"sort"
)

const golden = 0x9E3779B97F4A7C15

// mix64 is the splitmix64 finalizer. It is a bijection on uint64, which the
// vk/sk encodings rely on for uniqueness.
func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Purpose tags keep the random streams of different decisions independent.
const (
	tagVolume uint64 = iota + 1
	tagVisitor
	tagPerson
	tagPaths
	tagIP
	tagVK
	tagSK
)

// key derives a stream key from the seed, a purpose tag and three integers.
// Every random decision in the generator is keyed this way, never by
// generation order, which is what makes the output independent of worker
// count and of --start/--days.
func key(seed, tag, a, b, c uint64) uint64 {
	h := mix64(seed ^ (tag * golden))
	h = mix64(h ^ a ^ 0x243F6A8885A308D3) // pi digits: arbitrary distinct constants
	h = mix64(h ^ b ^ 0x13198A2E03707344)
	return mix64(h ^ c ^ 0xA4093822299F31D0)
}

// rng is a splitmix64 generator: tiny, fast and fully specified here, so the
// streams never change with the Go version.
type rng struct{ s uint64 }

func (r *rng) u64() uint64 {
	r.s += golden
	return mix64(r.s)
}

// intn returns a value in [0, n) (multiply-shift; bias is below 2^-40).
func (r *rng) intn(n int64) int64 {
	hi, _ := bits.Mul64(r.u64(), uint64(n))
	return int64(hi)
}

// f64 returns a value in [0, 1).
func (r *rng) f64() float64 { return float64(r.u64()>>11) * (1.0 / (1 << 53)) }

// chance returns true with probability ppm/1e6.
func (r *rng) chance(ppm int64) bool { return r.intn(1_000_000) < ppm }

// wtab picks an index with integer weights.
type wtab struct {
	cum   []int64
	total int64
}

func newWtab(w []int64) wtab {
	t := wtab{cum: make([]int64, len(w))}
	for i, x := range w {
		t.total += x
		t.cum[i] = t.total
	}
	return t
}

func (t *wtab) pick(r *rng) int {
	x := r.intn(t.total)
	lo, hi := 0, len(t.cum)-1
	for lo < hi {
		m := (lo + hi) / 2
		if t.cum[m] > x {
			hi = m
		} else {
			lo = m + 1
		}
	}
	return lo
}

// ftab picks an index from a float CDF (used for Zipf paths).
type ftab struct{ cdf []float64 }

func newZipf(n int, s float64) ftab {
	t := ftab{cdf: make([]float64, n)}
	var sum float64
	for k := 1; k <= n; k++ {
		sum += zipfWeight(k, s)
		t.cdf[k-1] = sum
	}
	for i := range t.cdf {
		t.cdf[i] /= sum
	}
	t.cdf[n-1] = 1
	return t
}

func (t *ftab) pick(r *rng) int {
	u := r.f64()
	i := sort.SearchFloat64s(t.cdf, u)
	if i < len(t.cdf) && t.cdf[i] == u {
		i++
	}
	if i >= len(t.cdf) {
		i = len(t.cdf) - 1
	}
	return i
}

// piecewise samples a duration from weighted uniform buckets [lo, hi) in ms.
type piecewise struct {
	lo, hi []int64
	w      wtab
}

func newPiecewise(b [][3]int64) piecewise {
	p := piecewise{}
	w := make([]int64, len(b))
	for i, x := range b {
		p.lo = append(p.lo, x[0])
		p.hi = append(p.hi, x[1])
		w[i] = x[2]
	}
	p.w = newWtab(w)
	return p
}

func (p *piecewise) sample(r *rng) int64 {
	i := p.w.pick(r)
	return p.lo[i] + r.intn(p.hi[i]-p.lo[i])
}
