package ref

import (
	"bufio"
	"encoding/json"
	"os"

	"supermetry/bench/proto/internal/core"
)

func decodeProp(props []byte, key string) (string, bool) {
	if props == nil {
		return "", false
	}
	var m map[string]string
	if json.Unmarshal(props, &m) != nil {
		return "", false
	}
	v, ok := m[key]
	return v, ok
}

// ReadNDJSON reads all records of a file.
func ReadNDJSON(path string) ([]core.Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []core.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r core.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, sc.Err()
}
