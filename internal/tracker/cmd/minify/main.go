// Command minify creates the checked-in browser tracker without Node.
package main

import (
	"flag"
	"fmt"
	"os"

	minifier "github.com/tdewolff/minify/v2"
	jsmin "github.com/tdewolff/minify/v2/js"
)

func main() {
	in := flag.String("in", "tracker.js", "input JavaScript file")
	appendPath := flag.String("append", "", "optional JavaScript file to append")
	out := flag.String("out", "tracker.min.js", "output JavaScript file")
	flag.Parse()

	source, err := os.ReadFile(*in)
	if err != nil {
		fail(err)
	}
	if *appendPath != "" {
		extra, err := os.ReadFile(*appendPath)
		if err != nil {
			fail(err)
		}
		source = append(source, extra...)
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
	m := minifier.New()
	m.AddFunc("application/javascript", jsmin.Minify)
	return m.Bytes("application/javascript", source)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
