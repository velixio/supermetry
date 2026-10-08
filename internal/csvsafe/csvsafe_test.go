package csvsafe

import "testing"

func TestCellNeutralisesFormulaPrefixes(t *testing.T) {
	cases := map[string]string{
		"=SUM(A1)":   "'=SUM(A1)",
		"+1":         "'+1",
		"-2":         "'-2",
		"@cmd":       "'@cmd",
		"\tx":        "'\tx",
		"\rx":        "'\rx",
		"/pricing":   "/pricing",
		"":           "",
		"google.com": "google.com",
	}
	for input, want := range cases {
		if got := Cell(input); got != want {
			t.Errorf("Cell(%q) = %q, want %q", input, got, want)
		}
	}
}
