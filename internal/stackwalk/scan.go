package stackwalk

// Bounds on the last-resort stack scan.
const (
	maxScanWords  = 1024 // words examined per frame step
	maxScanFrames = 64   // frames recovered by scanning in one walk
)

// scan looks for the next word at or above sp that points into a module's
// code and, when symbols exist, into a known function just before it (a
// return address points after the call, so rel-1 must be covered).
func (w *walker) scan(sp uint64) (pc, newSP uint64, ok bool) {
	if w.scanned >= maxScanFrames {
		return 0, 0, false
	}
	for i := uint64(0); i < maxScanWords; i++ {
		addr := sp + i*8
		if addr < sp { // wrapped
			return 0, 0, false
		}
		v, ok := w.read(addr)
		if !ok {
			return 0, 0, false
		}
		m, ok := w.d.ModuleAt(v)
		if !ok || v == m.Base {
			continue
		}
		if w.syms != nil && !w.syms.Covers(m, v-m.Base-1) {
			continue
		}
		w.scanned++
		return v, addr + 8, true
	}
	return 0, 0, false
}
