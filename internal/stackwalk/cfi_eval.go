package stackwalk

import (
	"errors"
	"strconv"
	"strings"
)

// Limits on one rule string: Breakpad CFI rules are a handful of short
// postfix expressions, so anything larger is malformed or hostile.
const (
	maxEvalStack  = 64
	maxEvalTokens = 1024
)

var errEval = errors.New("stackwalk: bad CFI rule")

// EvalPostfix evaluates a Breakpad STACK CFI rule string such as
//
//	.cfa: $rsp 8 + .ra: .cfa -8 + ^
//
// It is a series of "dest: expression" groups. An expression is postfix over
// register names (`$rsp`, or a bare name such as `sp`/`x29` on arm64), the
// pseudo registers `.cfa` and `.ra`, decimal integers and the operators
// `+ - * / % @` (align down), `^` (dereference through read) and `=`
// (assign the top value to the variable below it). The `.cfa` group is
// evaluated first and is visible to the others as `.cfa`; every other
// expression sees the callee's registers in regs. The result maps each
// destination to its value: `.cfa`, `.ra`, and register names without `$`.
//
// Division by zero, an unknown register used as a value, a failed memory
// read, a stack deeper than 64 or an unbalanced expression is an error.
func EvalPostfix(expr string, regs map[string]uint64, read func(addr uint64) (uint64, bool)) (map[string]uint64, error) {
	toks := strings.Fields(expr)
	if len(toks) > maxEvalTokens {
		return nil, errEval
	}
	type group struct {
		dest string
		expr []string
	}
	var groups []group
	for _, t := range toks {
		if len(t) > 1 && t[len(t)-1] == ':' {
			groups = append(groups, group{dest: normName(t[:len(t)-1])})
			continue
		}
		if len(groups) == 0 {
			return nil, errEval
		}
		g := &groups[len(groups)-1]
		g.expr = append(g.expr, t)
	}
	if len(groups) == 0 {
		return nil, errEval
	}
	out := make(map[string]uint64, len(groups))
	env := func(name string) (uint64, bool) {
		if name == ".cfa" {
			if v, ok := out[".cfa"]; ok {
				return v, true
			}
		}
		v, ok := regs[name]
		return v, ok
	}
	for pass := 0; pass < 2; pass++ {
		for _, g := range groups {
			if (g.dest == ".cfa") != (pass == 0) {
				continue
			}
			v, assigned, err := evalGroup(g.expr, env, read, out)
			if err != nil {
				return nil, err
			}
			if !assigned {
				out[g.dest] = v
			}
		}
	}
	return out, nil
}

func normName(s string) string { return strings.TrimPrefix(s, "$") }

type item struct {
	name  string // variable operand, the target of '='
	val   uint64
	known bool
}

// evalGroup runs one postfix expression. assigned reports that `=` consumed
// the result, which it wrote to out directly.
func evalGroup(toks []string, env func(string) (uint64, bool), read func(uint64) (uint64, bool), out map[string]uint64) (v uint64, assigned bool, err error) {
	var st [maxEvalStack]item
	n := 0
	for _, t := range toks {
		switch t {
		case "+", "-", "*", "/", "%", "@":
			if n < 2 || !st[n-1].known || !st[n-2].known {
				return 0, false, errEval
			}
			b, a := st[n-1].val, st[n-2].val
			n -= 2
			var r uint64
			switch t {
			case "+":
				r = a + b
			case "-":
				r = a - b
			case "*":
				r = a * b
			case "/", "%":
				if b == 0 {
					return 0, false, errEval
				}
				if t == "/" {
					r = a / b
				} else {
					r = a % b
				}
			case "@":
				if b == 0 {
					return 0, false, errEval
				}
				r = a &^ (b - 1)
			}
			st[n] = item{val: r, known: true}
			n++
		case "^":
			if n < 1 || !st[n-1].known {
				return 0, false, errEval
			}
			m, ok := read(st[n-1].val)
			if !ok {
				return 0, false, errEval
			}
			st[n-1] = item{val: m, known: true}
		case "=":
			if n < 2 || st[n-2].name == "" || !st[n-1].known {
				return 0, false, errEval
			}
			out[st[n-2].name] = st[n-1].val
			n -= 2
			assigned = true
		default:
			if n >= maxEvalStack {
				return 0, false, errEval
			}
			if i, err := strconv.ParseInt(t, 10, 64); err == nil {
				st[n] = item{val: uint64(i), known: true}
			} else if u, err := strconv.ParseUint(t, 10, 64); err == nil {
				st[n] = item{val: u, known: true}
			} else {
				name := normName(t)
				val, ok := env(name)
				st[n] = item{name: name, val: val, known: ok}
			}
			n++
		}
	}
	if assigned {
		if n != 0 {
			return 0, false, errEval
		}
		return 0, true, nil
	}
	if n != 1 || !st[0].known {
		return 0, false, errEval
	}
	return st[0].val, false, nil
}
