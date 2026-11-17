package spans

import (
	"hash/fnv"
	"math"
	"sort"
)

// Histogram definition, shared by Go and SQL. Durations are microseconds;
// bucket b = clamp(floor(ln(max(d,1)) / ln(1.1)), 0, 230); the estimate for
// bucket b is 1.1^(b+0.5) µs, so the relative error is at most sqrt(1.1)-1.
const (
	histBase      = 1.1
	maxBucket     = 230
	histLogFactor = 0.09531017980432487 // ln(1.1)
)

// Bucket returns the log-scale bucket of a duration in microseconds.
func Bucket(durationUS int64) int16 {
	if durationUS < 1 {
		return 0
	}
	b := int(math.Floor(math.Log(float64(durationUS)) / histLogFactor))
	if b > maxBucket {
		b = maxBucket
	}
	if b < 0 {
		b = 0
	}
	return int16(b)
}

// Estimate returns the representative duration in microseconds of a bucket.
func Estimate(bucket int16) float64 {
	return math.Pow(histBase, float64(bucket)+0.5)
}

// Percentiles returns the nearest-rank estimate for each p in (0,1] over
// bucket counts: rank r = ceil(p*N) over the cumulative counts. Empty input
// gives NaN for every p.
func Percentiles(counts map[int16]int64, ps ...float64) []float64 {
	out := make([]float64, len(ps))
	var total int64
	keys := make([]int16, 0, len(counts))
	for b, c := range counts {
		if c > 0 {
			total += c
			keys = append(keys, b)
		}
	}
	if total == 0 {
		for i := range out {
			out[i] = math.NaN()
		}
		return out
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for i, p := range ps {
		rank := int64(math.Ceil(p * float64(total)))
		if rank < 1 {
			rank = 1
		}
		var cum int64
		out[i] = Estimate(keys[len(keys)-1])
		for _, b := range keys {
			cum += counts[b]
			if cum >= rank {
				out[i] = Estimate(b)
				break
			}
		}
	}
	return out
}

// GroupHash groups spans by operation and (parameterised) description:
// FNV-1a 64 of op + NUL + description with the sign bit cleared.
func GroupHash(op, description string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(op))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(description))
	return int64(h.Sum64() &^ (1 << 63))
}
