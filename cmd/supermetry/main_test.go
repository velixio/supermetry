package main

import "testing"

func TestCommandNameDefaultsToServe(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"supermetry"}, "serve"},
		{[]string{"supermetry", "serve"}, "serve"},
		{[]string{"supermetry", "migrate"}, "migrate"},
		{[]string{"supermetry", "--help"}, "help"},
		{[]string{"supermetry", "-h"}, "help"},
		{[]string{"supermetry", "help"}, "help"},
	} {
		if got := commandName(tc.args); got != tc.want {
			t.Errorf("commandName(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
