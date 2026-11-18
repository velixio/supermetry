package stackwalk

// arm64 (AAPCS64) keeps a frame record {x29, x30} at [x29] when a function
// sets up a frame: the saved frame pointer at [x29] and the return address at
// [x29+8]. A leaf function with no frame leaves its return address in x30
// (the link register), which is the only source for the very first caller.
var arm64Arch = &arch{
	name:   "arm64",
	pc:     "pc",
	sp:     "sp",
	fp:     "x29",
	lr:     "x30",
	lrLeaf: true,
}
