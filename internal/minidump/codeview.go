package minidump

import (
	"encoding/hex"
	"fmt"

	"supermetry/internal/debugfile"
)

const (
	cvRSDS = 0x53445352 // "RSDS": PDB 7.0 record (GUID + age + path)
	cvBpEL = 0x4c457042 // "BpEL": ELF build id
	maxCV  = 4096
	maxBID = 64
)

// applyCodeView fills the debug and code ids of m from its CodeView record.
func applyCodeView(m *Module, cv []byte, timestamp uint32) {
	if len(cv) < 4 {
		return
	}
	if len(cv) > maxCV {
		cv = cv[:maxCV]
	}
	switch le.Uint32(cv) {
	case cvRSDS:
		if len(cv) < 24 {
			return
		}
		g := cv[4:20]
		id := fmt.Sprintf("%08x-%04x-%04x-%x-%x", le.Uint32(g), le.Uint16(g[4:]), le.Uint16(g[6:]), g[8:10], g[10:16])
		if age := le.Uint32(cv[20:]); age != 0 {
			id += fmt.Sprintf("-%x", age)
		}
		m.DebugID = debugfile.NormalizeDebugID(id)
		name := cv[24:]
		for i, c := range name {
			if c == 0 {
				name = name[:i]
				break
			}
		}
		if len(name) > 0 {
			m.DebugFile = baseName(string(name))
		}
		if timestamp != 0 || m.Size != 0 {
			m.CodeID = fmt.Sprintf("%08X%x", timestamp, m.Size)
		}
	case cvBpEL:
		bid := cv[4:]
		if len(bid) > maxBID {
			bid = bid[:maxBID]
		}
		if len(bid) == 0 {
			return
		}
		m.DebugID = debugfile.DebugIDFromBuildID(bid)
		m.CodeID = hex.EncodeToString(bid)
	}
}
