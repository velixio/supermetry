package stripe

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var sigNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func TestVerifySignature(t *testing.T) {
	payload := []byte(`{"id":"evt_1","type":"invoice.paid"}`)
	secret := []byte("whsec_test_secret_value")
	valid := SignatureHeader(payload, secret, sigNow)
	other := SignatureHeader(payload, []byte("whsec_other_secret_value"), sigNow)
	otherV1 := strings.TrimPrefix(other[strings.Index(other, ",")+1:], "v1=")
	validV1 := strings.TrimPrefix(valid[strings.Index(valid, ",")+1:], "v1=")
	ts := strconv.FormatInt(sigNow.Unix(), 10)

	tests := []struct {
		name    string
		header  string
		payload []byte
		secret  []byte
		now     time.Time
		want    error
	}{
		{name: "valid", header: valid, payload: payload, secret: secret, now: sigNow},
		{name: "valid at tolerance edge", header: valid, payload: payload, secret: secret, now: sigNow.Add(SignatureTolerance)},
		{name: "wrong secret", header: valid, payload: payload, secret: []byte("whsec_wrong"), now: sigNow, want: ErrSignatureInvalid},
		{name: "tampered payload", header: valid, payload: []byte(`{"id":"evt_2"}`), secret: secret, now: sigNow, want: ErrSignatureInvalid},
		{name: "expired", header: valid, payload: payload, secret: secret, now: sigNow.Add(SignatureTolerance + time.Second), want: ErrSignatureExpired},
		{name: "future", header: valid, payload: payload, secret: secret, now: sigNow.Add(-SignatureTolerance - time.Second), want: ErrSignatureExpired},
		{name: "multiple signatures, second matches", header: "t=" + ts + ",v1=" + otherV1 + ",v1=" + validV1, payload: payload, secret: secret, now: sigNow},
		{name: "multiple signatures, first matches", header: "t=" + ts + ",v1=" + validV1 + ",v1=" + otherV1, payload: payload, secret: secret, now: sigNow},
		{name: "v0 and malformed v1 ignored", header: "t=" + ts + ",v0=abc,v1=zz,v1=" + validV1, payload: payload, secret: secret, now: sigNow},
		{name: "spaces around parts", header: "t=" + ts + ", v1=" + validV1, payload: payload, secret: secret, now: sigNow},
		{name: "only other signatures", header: "t=" + ts + ",v1=" + otherV1, payload: payload, secret: secret, now: sigNow, want: ErrSignatureInvalid},
		{name: "empty", header: "", payload: payload, secret: secret, now: sigNow, want: ErrSignatureHeader},
		{name: "no timestamp", header: "v1=" + validV1, payload: payload, secret: secret, now: sigNow, want: ErrSignatureHeader},
		{name: "no v1", header: "t=" + ts + ",v0=" + validV1, payload: payload, secret: secret, now: sigNow, want: ErrSignatureHeader},
		{name: "bad timestamp", header: "t=abc,v1=" + validV1, payload: payload, secret: secret, now: sigNow, want: ErrSignatureHeader},
		{name: "duplicate timestamp", header: "t=" + ts + ",t=" + ts + ",v1=" + validV1, payload: payload, secret: secret, now: sigNow, want: ErrSignatureHeader},
		{name: "part without equals", header: "t=" + ts + ",garbage,v1=" + validV1, payload: payload, secret: secret, now: sigNow, want: ErrSignatureHeader},
		{name: "oversized header", header: "t=" + ts + ",v1=" + validV1 + strings.Repeat(",v1=00", 1000), payload: payload, secret: secret, now: sigNow, want: ErrSignatureHeader},
		{name: "empty secret", header: valid, payload: payload, secret: nil, now: sigNow, want: ErrSignatureHeader},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := VerifySignature(test.payload, test.header, test.secret, test.now, SignatureTolerance)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
		})
	}
}

func TestMinorToMicros(t *testing.T) {
	tests := []struct {
		amount   int64
		currency string
		micros   int64
		code     string
	}{
		{1999, "usd", 19_990_000, "USD"},
		{1999, "EUR", 19_990_000, "EUR"},
		{500, "jpy", 500_000_000, "JPY"},
		{1500, "krw", 1_500_000_000, "KRW"},
		{12345, "kwd", 12_345_000, "KWD"},
		{1000, "bhd", 1_000_000, "BHD"},
		{50000, "isk", 500_000_000, "ISK"}, // two decimals in Stripe's API
		{50000, "huf", 500_000_000, "HUF"},
		{50000, "ugx", 500_000_000, "UGX"},
		{0, "usd", 0, "USD"},
	}
	for _, test := range tests {
		micros, code, err := MinorToMicros(test.amount, test.currency)
		if err != nil || micros != test.micros || code != test.code {
			t.Errorf("MinorToMicros(%d,%q)=%d,%q,%v want %d,%q", test.amount, test.currency, micros, code, err, test.micros, test.code)
		}
	}
	for _, bad := range []struct {
		amount   int64
		currency string
	}{
		{-1, "usd"}, {1, "us"}, {1, "usdd"}, {1, "u$d"}, {1, ""}, {1 << 62, "jpy"}, {9_300_000_000_000_000, "usd"},
	} {
		if _, _, err := MinorToMicros(bad.amount, bad.currency); err == nil {
			t.Errorf("MinorToMicros(%d,%q) accepted", bad.amount, bad.currency)
		}
	}
	if CurrencyDecimals("jpy") != 0 || CurrencyDecimals("TND") != 3 || CurrencyDecimals("usd") != 2 {
		t.Fatal("decimals lookup is wrong")
	}
}
