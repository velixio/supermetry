package stackwalk

import (
	"strings"
	"testing"
)

func TestEvalPostfix(t *testing.T) {
	mem := map[uint64]uint64{0x1000: 0x4242, 0x1008: 7}
	read := func(a uint64) (uint64, bool) { v, ok := mem[a]; return v, ok }
	regs := map[string]uint64{"rsp": 0x1000, "rbp": 0x2000, "sp": 0x1000, "x29": 0x3000}

	cases := []struct {
		name, expr string
		want       map[string]uint64
		err        bool
	}{
		{"basic", ".cfa: $rsp 8 + .ra: .cfa -8 + ^", map[string]uint64{".cfa": 0x1008, ".ra": 0x4242}, false},
		{"callee saved", ".cfa: $rsp 16 + $rbp: .cfa -8 + ^", map[string]uint64{".cfa": 0x1010, "rbp": 7}, false},
		{"order independent", ".ra: .cfa -8 + ^ .cfa: $rsp 8 +", map[string]uint64{".cfa": 0x1008, ".ra": 0x4242}, false},
		{"bare arm64 names", ".cfa: sp 16 + x29: .cfa 16 -", map[string]uint64{".cfa": 0x1010, "x29": 0x1000}, false},
		{"ops", ".cfa: 100 7 % 3 * 20 @ 6 /", map[string]uint64{".cfa": 0}, false},
		{"align", ".cfa: $rsp 5 + 16 @", map[string]uint64{".cfa": 0x1000}, false},
		{"assign", ".cfa: $x $rsp 4 + =", map[string]uint64{"x": 0x1004}, false},
		{"div zero", ".cfa: 1 0 /", nil, true},
		{"mod zero", ".cfa: 1 0 %", nil, true},
		{"align zero", ".cfa: 1 0 @", nil, true},
		{"unknown reg", ".cfa: $nope 8 +", nil, true},
		{"bad read", ".cfa: 5 ^", nil, true},
		{"underflow", ".cfa: +", nil, true},
		{"leftover", ".cfa: 1 2", nil, true},
		{"no group", "1 2 +", nil, true},
		{"empty", "", nil, true},
		{"empty group", ".cfa:", nil, true},
		{"overflow", ".cfa: " + strings.Repeat("1 ", 65), nil, true},
		{"too many tokens", ".cfa: " + strings.Repeat("1 ", 2000), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalPostfix(tc.expr, regs, read)
			if tc.err {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %#x, want %#x (all %v)", k, got[k], v, got)
				}
			}
		})
	}
}

func TestEvalPostfixSpecExample(t *testing.T) {
	got, err := EvalPostfix(".cfa: $rsp 8 + .ra: .cfa -8 + ^", map[string]uint64{"rsp": 0x1000},
		func(a uint64) (uint64, bool) { return 0x4242, a == 0x1000 })
	if err != nil || got[".cfa"] != 0x1008 || got[".ra"] != 0x4242 {
		t.Fatalf("%v %v", got, err)
	}
}

func FuzzEvalPostfix(f *testing.F) {
	f.Add(".cfa: $rsp 8 + .ra: .cfa -8 + ^")
	f.Add(".cfa: $rsp 16 + $rbp: .cfa -16 + ^ x29: .x 1 =")
	f.Add(".cfa: 1 0 / ^ ^ ^ @ % * - +")
	f.Fuzz(func(t *testing.T, s string) {
		regs := map[string]uint64{"rsp": 0x1000, "rbp": 0x2000, "sp": 8}
		_, _ = EvalPostfix(s, regs, func(a uint64) (uint64, bool) { return a * 3, a%2 == 0 })
	})
}
