package replay

import (
	"bytes"
	"io"
	"testing"
)

func TestReadConcatenatedToleratesTruncatedTail(t *testing.T) {
	first := gzipMember(t, "a")
	second := gzipMember(t, "b")
	last := gzipMember(t, "c")
	input := append(append(append([]byte{}, first...), second...), last[:len(last)/2]...)

	got, err := io.ReadAll(NewConcatReader(bytes.NewReader(input)))
	if err != nil {
		t.Fatalf("ReadAll() error = %v, want nil", err)
	}
	if string(got) != "ab" {
		t.Fatalf("ReadAll() = %q, want ab", got)
	}
}
