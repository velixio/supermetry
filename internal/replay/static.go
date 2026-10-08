package replay

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed static/dist/replay.js
var replayJS []byte

// ReplayJS returns the immutable browser recorder and its strong validator.
func ReplayJS() (body []byte, etag string) {
	digest := sha256.Sum256(replayJS)
	return replayJS, `"` + hex.EncodeToString(digest[:])[:16] + `"`
}
