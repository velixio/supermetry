package sites

import "testing"

func TestParseReplaySiteInputRejectsOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "sample rate above one hundred", raw: `{"sample_rate":101}`},
		{name: "negative sample rate", raw: `{"sample_rate":-1}`},
		{name: "zero retention", raw: `{"retention_days":0}`},
		{name: "retention above one year", raw: `{"retention_days":366}`},
		{name: "unknown field", raw: `{"unknown":true}`},
		{name: "invalid consent", raw: `{"consent":"sometimes"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseReplaySiteInput([]byte(tt.raw)); err == nil {
				t.Fatalf("ParseReplaySiteInput(%s) accepted invalid input", tt.raw)
			}
		})
	}
}

func TestParseReplaySiteInputAcceptsExplicitValues(t *testing.T) {
	got, err := ParseReplaySiteInput([]byte(`{
		"enabled":true,
		"mask":true,
		"sample_rate":25,
		"retention_days":90,
		"quota_bytes":1048576,
		"triggers":{"error":false,"rage_click":false,"url":true,"manual":false,"url_patterns":["/checkout/*"]},
		"consent":"always"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Mask == nil || !*got.Mask || got.SampleRate == nil || *got.SampleRate != 25 || got.RetentionDays == nil || *got.RetentionDays != 90 || got.QuotaBytes == nil || *got.QuotaBytes != 1048576 {
		t.Fatalf("ParseReplaySiteInput() = %+v", got)
	}
	if got.Triggers.Error == nil || *got.Triggers.Error || got.Triggers.URL == nil || !*got.Triggers.URL || len(got.Triggers.URLPatterns) != 1 || got.Consent == nil || *got.Consent != "always" {
		t.Fatalf("ParseReplaySiteInput() nested values = %+v", got)
	}
}
