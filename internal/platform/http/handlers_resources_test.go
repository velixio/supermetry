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
