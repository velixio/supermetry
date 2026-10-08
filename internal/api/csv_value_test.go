package api

import "testing"

func TestCSVValueNeutralisesFormulas(t *testing.T) {
	cases := map[any]string{
		"=HYPERLINK(\"x\")": "'=HYPERLINK(\"x\")",
		"+1":                "'+1",
		"-2+3":              "'-2+3",
		"@SUM(A1)":          "'@SUM(A1)",
		"\tcmd":             "'\tcmd",
		"/pricing":          "/pricing",
		"":                  "",
		-5:                  "-5",
		-1.5:                "-1.5",
	}
	for input, want := range cases {
		if got := csvValue(input); got != want {
			t.Errorf("csvValue(%#v) = %q, want %q", input, got, want)
		}
	}
}
