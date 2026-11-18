//go:build ignore

// Fixture program for the native symbol cache tests. Every function is
// kept out of line so the oracle (go tool addr2line) and the cache agree.
package main

//go:noinline
func alpha(n int) int {
	s := 0
	for i := 0; i < n; i++ {
		s += i * 3
	}
	return s
}

//go:noinline
func beta(n int) int {
	if n > 10 {
		return alpha(n) + 1
	}
	return alpha(n) - 1
}

//go:noinline
func gamma(n int) int {
	t := 0
	for i := 0; i < n; i++ {
		t += beta(i)
	}
	return t
}

//go:noinline
func delta(n int) int {
	return gamma(n) * 2
}

func main() {
	println(delta(20))
}
