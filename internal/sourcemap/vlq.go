// Package sourcemap parses JavaScript source maps (Source Map v3, regular and
// index maps) and Sentry artifact bundles, and resolves generated positions to
// original ones. It is pure Go with no I/O beyond the readers it is handed.
// The VLQ decoder and parser are written from the public Source Map v3
// specification.
package sourcemap

import "errors"

const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var b64Val [256]int8

func init() {
	for i := range b64Val {
		b64Val[i] = -1
	}
	for i := 0; i < len(b64); i++ {
		b64Val[b64[i]] = int8(i)
	}
}

var errVLQ = errors.New("sourcemap: invalid VLQ")

// maxVLQ bounds decoded values so they fit comfortably in int32 positions.
const maxVLQ = 1<<31 - 1

// decodeVLQ decodes one base64 VLQ value from s starting at i and returns the
// value and the index after it.
func decodeVLQ(s string, i int) (val int, next int, err error) {
	var result uint64
	shift := uint(0)
	for {
		if i >= len(s) {
			return 0, i, errVLQ
		}
		d := b64Val[s[i]]
		i++
		if d < 0 {
			return 0, i, errVLQ
		}
		result |= uint64(d&31) << shift
		if d&32 == 0 {
			break
		}
		shift += 5
		if shift > 30 {
			return 0, i, errVLQ
		}
	}
	v := int64(result >> 1)
	if result&1 == 1 {
		v = -v
	}
	if v > maxVLQ || v < -maxVLQ {
		return 0, i, errVLQ
	}
	return int(v), i, nil
}

// decodeSegment decodes every VLQ value of one mappings segment (the text
// between commas) into out, which is reused.
func decodeSegment(seg string, out []int) ([]int, error) {
	out = out[:0]
	for i := 0; i < len(seg); {
		v, n, err := decodeVLQ(seg, i)
		if err != nil {
			return out, err
		}
		out = append(out, v)
		i = n
	}
	return out, nil
}
