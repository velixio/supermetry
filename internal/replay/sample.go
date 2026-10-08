package replay

// Sampled deterministically assigns a visitor to a percentage sample. The
// splitmix64 finalizer avoids bias from sequential or otherwise structured
// visitor IDs before reducing into 10,000 basis points.
func Sampled(visitorID int64, ratePct int) bool {
	if ratePct <= 0 {
		return false
	}
	if ratePct >= 100 {
		return true
	}
	return mix64(uint64(visitorID))%10_000 < uint64(ratePct*100)
}

func mix64(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ value>>30) * 0xbf58476d1ce4e5b9
	value = (value ^ value>>27) * 0x94d049bb133111eb
	return value ^ value>>31
}
