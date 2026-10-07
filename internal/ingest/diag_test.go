package ingest

import "testing"

func TestMaskHeaderNeverEchoesAddresses(t *testing.T) {
	cases := map[string]string{
		`for="8.8.8.8:5678";proto=https`: "<public>",
		`for="[2001:4860:4860::8888]:1"`: "<public>",
		`8.8.8.8:99, 192.168.0.200`:      "<public>,<private>",
		`127.0.0.1`:                      "<private>",
		`for=garbage`:                    "<unparsed>",
		`1.2.3.4abc`:                     "<unparsed>",
		``:                               "-",
	}
	for in, want := range cases {
		if got := maskHeader(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
