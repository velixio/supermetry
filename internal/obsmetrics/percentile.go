package obsmetrics

import "math"

// Percentile estimates the q-quantile (0 to 1) of an explicit-bucket
// histogram. bounds are the upper bounds of the finite buckets; counts has one
// more entry than bounds (the last is the overflow bucket). The rank is
// q x total; buckets are walked cumulatively and the value is interpolated
// linearly inside the bucket (lower, upper]. The first bucket's lower edge is
// min when known, else 0 (or the bound itself when that is negative); the
// overflow bucket returns max when known, else the last bound. No samples, or
// counts that do not fit bounds, give NaN.
func Percentile(bounds []float64, counts []int64, q float64, min, max *float64) float64 {
	if len(counts) != len(bounds)+1 {
		return math.NaN()
	}
	var total int64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return math.NaN()
	}
	q = math.Min(math.Max(q, 0), 1)
	rank := q * float64(total)
	var cum int64
	for i, c := range counts {
		if c > 0 && float64(cum+c) >= rank {
			if i == len(bounds) { // overflow bucket
				if max != nil {
					return *max
				}
				if len(bounds) == 0 {
					return math.NaN()
				}
				return bounds[len(bounds)-1]
			}
			lower := 0.0
			switch {
			case i > 0:
				lower = bounds[i-1]
			case min != nil:
				lower = *min
			case bounds[0] < 0:
				lower = bounds[0]
			}
			upper := bounds[i]
			if lower > upper {
				lower = upper
			}
			return lower + (rank-float64(cum))/float64(c)*(upper-lower)
		}
		cum += c
	}
	if max != nil {
		return *max
	}
	if len(bounds) == 0 {
		return math.NaN()
	}
	return bounds[len(bounds)-1]
}
