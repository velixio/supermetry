package replay

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
)

func NewConcatReader(r io.Reader) io.Reader {
	return &concatReader{source: bufio.NewReader(r)}
}

type concatReader struct {
	source  *bufio.Reader
	current *bytes.Reader
	done    bool
}

func (r *concatReader) Read(p []byte) (int, error) {
	for {
		if r.current != nil && r.current.Len() != 0 {
			return r.current.Read(p)
		}
		if r.done {
			return 0, io.EOF
		}
		zr, err := gzip.NewReader(r.source)
		if err != nil {
			r.done = true
			return 0, io.EOF
		}
		zr.Multistream(false)
		member, readErr := io.ReadAll(zr)
		closeErr := zr.Close()
		if readErr != nil || closeErr != nil {
			r.done = true
			return 0, io.EOF
		}
		r.current = bytes.NewReader(member)
	}
}
