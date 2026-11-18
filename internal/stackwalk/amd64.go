package stackwalk

// amd64 follows the System V and Microsoft x64 frame conventions: after
// `push rbp; mov rbp, rsp` the saved rbp sits at [rbp] and the return address
// at [rbp+8], so the caller's rsp is rbp+16.
var amd64Arch = &arch{
	name: "amd64",
	pc:   "rip",
	sp:   "rsp",
	fp:   "rbp",
}
