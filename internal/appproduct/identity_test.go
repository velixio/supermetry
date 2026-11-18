package appproduct_test

import (
	"strings"
	"testing"

	"supermetry/internal/appproduct"
)

func TestVisitorIDStable(t *testing.T) {
	a, b := []byte("0123456789abcdef0123456789abcdef"), []byte("fedcba9876543210fedcba9876543210")
	first := appproduct.VisitorID(a, "install-1")
	if first != appproduct.VisitorID(a, "install-1") {
		t.Fatal("same secret and install must give the same id")
	}
	if first == appproduct.VisitorID(b, "install-1") {
		t.Fatal("different secrets must give different ids")
	}
	if first == appproduct.VisitorID(a, "install-2") {
		t.Fatal("different installs must give different ids")
	}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		if appproduct.VisitorID(a, id) < 0 {
			t.Fatalf("id for %q is negative", id)
		}
	}
}

func TestProfileExternalIDNoRawID(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	got := appproduct.ProfileExternalID(secret, "raw-install-id")
	if !strings.HasPrefix(got, "i:") || len(got) != 66 {
		t.Fatalf("got %q (len %d)", got, len(got))
	}
	if strings.Contains(got, "raw-install-id") {
		t.Fatal("external id leaks the raw install id")
	}
}
