// Package stripe records Stripe payments as Supermetry revenue. Each site gets
// its own webhook endpoint; events are verified with the endpoint's signing
// secret, deduplicated by Stripe event id and written as custom events with a
// revenue amount. Attribution comes from a short-lived sealed token that the
// site passes to Stripe Checkout as client_reference_id.
package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SignatureTolerance is the accepted clock difference between the Stripe
// timestamp and the server, the same default the official libraries use.
const SignatureTolerance = 5 * time.Minute

const maxSignatureHeaderBytes = 4096

var (
	ErrSignatureHeader  = errors.New("stripe signature header is malformed")
	ErrSignatureExpired = errors.New("stripe signature timestamp is outside the tolerance")
	ErrSignatureInvalid = errors.New("stripe signature does not match")
)

// VerifySignature checks a Stripe-Signature header ("t=<unix>,v1=<hex>,...")
// against payload. Any v1 signature may match, which is how Stripe signs with
// both secrets while one is being rolled. Other schemes (v0) are ignored.
func VerifySignature(payload []byte, header string, secret []byte, now time.Time, tolerance time.Duration) error {
	if len(header) == 0 || len(header) > maxSignatureHeaderBytes || len(secret) == 0 {
		return ErrSignatureHeader
	}
	var (
		timestamp  int64
		haveTime   bool
		signatures [][]byte
	)
	for part := range strings.SplitSeq(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return ErrSignatureHeader
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed <= 0 || haveTime {
				return ErrSignatureHeader
			}
			timestamp, haveTime = parsed, true
		case "v1":
			signature, err := hex.DecodeString(value)
			if err != nil || len(signature) != sha256.Size {
				// A malformed entry cannot match; the others may.
				continue
			}
			signatures = append(signatures, signature)
		}
	}
	if !haveTime || len(signatures) == 0 {
		return ErrSignatureHeader
	}
	expected := sign(payload, secret, timestamp)
	matched := false
	for _, signature := range signatures {
		// Compare every candidate so the timing does not depend on which matched.
		if hmac.Equal(expected, signature) {
			matched = true
		}
	}
	if !matched {
		return ErrSignatureInvalid
	}
	signed := time.Unix(timestamp, 0)
	if now.Sub(signed) > tolerance || signed.Sub(now) > tolerance {
		return ErrSignatureExpired
	}
	return nil
}

// SignatureHeader returns a Stripe-Signature header for payload, as Stripe
// would send it. Tests use it to build deliveries.
func SignatureHeader(payload []byte, secret []byte, at time.Time) string {
	return "t=" + strconv.FormatInt(at.Unix(), 10) + ",v1=" + hex.EncodeToString(sign(payload, secret, at.Unix()))
}

func sign(payload, secret []byte, timestamp int64) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte{'.'})
	mac.Write(payload)
	return mac.Sum(nil)
}
