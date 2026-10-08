package platformhttp

import "testing"

func TestValidGoalRevenueCurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     string
		currency string
		want     bool
	}{
		{name: "ordinary page goal", kind: "page", want: true},
		{name: "ordinary event goal", kind: "event", want: true},
		{name: "event revenue goal", kind: "event", currency: "USD", want: true},
		{name: "page revenue goal", kind: "page", currency: "USD", want: false},
		{name: "lowercase currency", kind: "event", currency: "usd", want: false},
		{name: "currency too short", kind: "event", currency: "US", want: false},
		{name: "currency too long", kind: "event", currency: "USDD", want: false},
		{name: "non ascii currency", kind: "event", currency: "US€", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := goalInput{Kind: tt.kind, Match: "exact", Value: "purchase", Name: "Purchase", Currency: tt.currency}
			if got := validGoal(input); got != tt.want {
				t.Fatalf("validGoal(%+v) = %t, want %t", input, got, tt.want)
			}
		})
	}
}

func TestValidGoalScrollThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input goalInput
		want  bool
	}{
		{
			name:  "scroll goal",
			input: goalInput{Kind: "scroll", Match: "exact", Value: "/pricing", Name: "Read pricing", ScrollThreshold: int16Pointer(75)},
			want:  true,
		},
		{
			name:  "scroll goal missing threshold",
			input: goalInput{Kind: "scroll", Match: "exact", Value: "/pricing", Name: "Read pricing"},
		},
		{
			name:  "scroll goal threshold zero",
			input: goalInput{Kind: "scroll", Match: "exact", Value: "/pricing", Name: "Read pricing", ScrollThreshold: int16Pointer(0)},
		},
		{
			name:  "scroll goal threshold above maximum",
			input: goalInput{Kind: "scroll", Match: "exact", Value: "/pricing", Name: "Read pricing", ScrollThreshold: int16Pointer(101)},
		},
		{
			name:  "page goal with threshold",
			input: goalInput{Kind: "page", Match: "exact", Value: "/pricing", Name: "Pricing", ScrollThreshold: int16Pointer(75)},
		},
		{
			name:  "scroll goal with currency",
			input: goalInput{Kind: "scroll", Match: "exact", Value: "/pricing", Name: "Read pricing", ScrollThreshold: int16Pointer(75), Currency: "USD"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := validGoal(tt.input); got != tt.want {
				t.Fatalf("validGoal(%+v) = %t, want %t", tt.input, got, tt.want)
			}
		})
	}
}

func int16Pointer(value int16) *int16 { return &value }
