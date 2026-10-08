package siteexport

import "testing"

func TestCSVValueEscapesFormulaTextButNotNumbers(t *testing.T) {
	if got := csvValue("=HYPERLINK(\"x\")"); got != "'=HYPERLINK(\"x\")" {
		t.Fatalf("string cell = %q", got)
	}
	if got := csvValue([]byte("@x")); got != "'@x" {
		t.Fatalf("bytes cell = %q", got)
	}
	if got := csvValue(int64(-3)); got != "-3" {
		t.Fatalf("number cell = %q", got)
	}
}
