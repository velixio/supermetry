// Command minify creates the checked-in browser tracker without Node.
package main

import (
	"bytes"
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
	full := flag.Bool("full", false, "keep /*@full*/ blocks")
	flag.Parse()

	source, err := os.ReadFile(*in)
	if err != nil {
		fail(err)
	}
	source, err = stripFull(source, *full)
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
	if err := os.WriteFile(*out, minified, 0o644); err != nil {
		fail(err)
	}
}

func stripFull(source []byte, keep bool) ([]byte, error) {
	const (
		fullMarker = "/*@full*/"
		coreMarker = "/*@core*/"
		endMarker  = "/*@end*/"
	)

	var output bytes.Buffer
	for len(source) > 0 {
		fullStart := bytes.Index(source, []byte(fullMarker))
		coreStart := bytes.Index(source, []byte(coreMarker))
		end := bytes.Index(source, []byte(endMarker))
		start := fullStart
		marker := fullMarker
		keepBlock := keep
		if start < 0 || coreStart >= 0 && coreStart < start {
			start = coreStart
			marker = coreMarker
			keepBlock = !keep
		}
		if end >= 0 && (start < 0 || end < start) {
			return nil, fmt.Errorf("stray %s marker", endMarker)
		}
		if start < 0 {
			output.Write(source)
			break
		}
		output.Write(source[:start])
		source = source[start+len(marker):]
		end = bytes.Index(source, []byte(endMarker))
		if end < 0 {
			return nil, fmt.Errorf("unclosed %s marker", marker)
		}
		fullStart = bytes.Index(source, []byte(fullMarker))
		coreStart = bytes.Index(source, []byte(coreMarker))
		if fullStart >= 0 && fullStart < end || coreStart >= 0 && coreStart < end {
			return nil, fmt.Errorf("nested conditional marker")
		}
		if keepBlock {
			output.Write(source[:end])
		}
		source = source[end+len(endMarker):]
	}
	return output.Bytes(), nil
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
