package core

// MaxDayBlockLevel is the largest materialized dyadic block: 2^9 = 512 days.
const MaxDayBlockLevel uint8 = 9

// DayBlock is an epoch-aligned inclusive range of 2^Level civil days.
type DayBlock struct {
	Start Day
	Level uint8
}

// Days returns the number of civil days in b.
func (b DayBlock) Days() Day { return Day(1) << b.Level }

// End returns the last civil day in b.
func (b DayBlock) End() Day { return b.Start + b.Days() - 1 }

// ContainingDayBlock returns the epoch-aligned block at level containing d.
func ContainingDayBlock(d Day, level uint8) DayBlock {
	if level > MaxDayBlockLevel {
		level = MaxDayBlockLevel
	}
	size := int64(1) << level
	n := int64(d)
	rem := n % size
	if rem < 0 {
		rem += size
	}
	return DayBlock{Start: Day(n - rem), Level: level}
}

// DecomposeDayRange covers inclusive [from, to] with the fewest greedy
// epoch-aligned dyadic blocks up to MaxDayBlockLevel. Blocks are ordered,
// disjoint, and cover every day exactly once.
func DecomposeDayRange(from, to Day) []DayBlock {
	if to < from {
		return nil
	}
	out := make([]DayBlock, 0, 2*(int(MaxDayBlockLevel)+1))
	for from <= to {
		remaining := int64(to-from) + 1
		level := uint8(0)
		for level < MaxDayBlockLevel {
			next := level + 1
			size := int64(1) << next
			if remaining < size || ContainingDayBlock(from, next).Start != from {
				break
			}
			level = next
		}
		b := DayBlock{Start: from, Level: level}
		out = append(out, b)
		from = b.End() + 1
	}
	return out
}
