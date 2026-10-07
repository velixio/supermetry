// Command minify creates the checked-in browser tracker without requiring Node.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	in := flag.String("in", "tracker.js", "input JavaScript file")
	out := flag.String("out", "tracker.min.js", "output JavaScript file")
	flag.Parse()

	source, err := os.ReadFile(*in)
	if err != nil {
		fail(err)
	}
	minified, err := minify(source)
	if err != nil {
		fail(err)
	}
	minified = append(minified, '\n')
	if err := os.WriteFile(*out, minified, 0o644); err != nil {
		fail(err)
	}
}

func minify(source []byte) ([]byte, error) {
	out := make([]byte, 0, len(source))
	for i := 0; i < len(source); {
		switch {
		case isSpace(source[i]):
			j := i + 1
			for j < len(source) && isSpace(source[j]) {
				j++
			}
			if len(out) > 0 && j < len(source) && isWord(out[len(out)-1]) && isWord(source[j]) {
				out = append(out, ' ')
			}
			i = j
		case source[i] == '/' && i+1 < len(source) && source[i+1] == '/':
			i += 2
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case source[i] == '/' && i+1 < len(source) && source[i+1] == '*':
			i += 2
			for i+1 < len(source) && (source[i] != '*' || source[i+1] != '/') {
				i++
			}
			if i+1 >= len(source) {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += 2
		case source[i] == '\'' || source[i] == '"' || source[i] == '`':
			quote := source[i]
			out = append(out, quote)
			i++
			closed := false
			for i < len(source) {
				out = append(out, source[i])
				if source[i] == '\\' && i+1 < len(source) {
					i++
					out = append(out, source[i])
				} else if source[i] == quote {
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string")
			}
		default:
			out = append(out, source[i])
			i++
		}
	}
	return out, nil
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\r' || b == '\t'
}

func isWord(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '$'
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
