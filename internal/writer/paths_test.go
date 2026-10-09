package writer

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/testpg"
)

func TestQueuedPathProvenanceValidation(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	ids, err := dictionary.IDs(ctx, dict.Path, []string{"/pricing", "/person-secret", "/[redacted]"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := privacy.NewPathPolicy([]string{"/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		pathID      int32
		version     int16
		policyID    string
		wantPath    int32
		wantVersion int16
	}{
		{"verified allowed", ids[0], 1, policy.Fingerprint(), ids[0], 1},
		{"verified fallback", ids[2], 1, policy.Fingerprint(), ids[2], 1},
		{"forged unsafe cached label", ids[1], 1, policy.Fingerprint(), 0, 0},
		{"forged missing dictionary id", 999999, 1, policy.Fingerprint(), 0, 0},
		{"wrong version", ids[0], 9, policy.Fingerprint(), 0, 0},
		{"stale policy", ids[0], 1, "old-policy", 0, 0},
		{"unverified safe-looking label", ids[0], 0, "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := New(nil, nil, 1, time.Hour, time.Hour, nil)
			w.SetPathPolicy(policy, dictionary)
			batch := []Event{{Hit: session.Hit{SiteID: 1, Kind: 0, PathID: tc.pathID}, PathSanitizerVersion: tc.version, PathPolicyID: tc.policyID}}
			w.suppressRestrictedIdentity(batch)
			got := batch[0]
			if got.PathID != tc.wantPath || got.PathSanitizerVersion != tc.wantVersion {
				t.Fatalf("path=%d version=%d; want path=%d version=%d", got.PathID, got.PathSanitizerVersion, tc.wantPath, tc.wantVersion)
			}
			if tc.wantVersion == 0 && got.PathPolicyID != "" {
				t.Fatalf("unverified policy ID=%q", got.PathPolicyID)
			}
		})
	}
}

func TestQueuedPathPolicyDowngradeNeverPromotes(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	ids, err := dictionary.IDs(ctx, dict.Path, []string{"/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := privacy.NewPathPolicy([]string{"/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	w := New(nil, nil, 1, time.Hour, time.Hour, nil)
	w.SetPathPolicy(policy, dictionary)
	if !w.Enqueue(Event{Hit: session.Hit{SiteID: 1, Kind: 0, PathID: ids[0]}, PathSanitizerVersion: 1, PathPolicyID: policy.Fingerprint()}) {
		t.Fatal("enqueue")
	}
	w.SetPathPolicy(privacy.PathPolicy{}, dictionary)
	batch := []Event{<-w.ch}
	w.suppressRestrictedIdentity(batch)
	w.SetPathPolicy(policy, dictionary)
	w.suppressRestrictedIdentity(batch)
	if e := batch[0]; e.PathID != 0 || e.PathSanitizerVersion != 0 || e.PathPolicyID != "" {
		t.Fatalf("queue regained trust after downgrade: %+v", e)
	}
}

func TestQueuedIdentityCannotClaimAnonymousPathProvenance(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	ids, err := dictionary.IDs(ctx, dict.Path, []string{"/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := privacy.NewPathPolicy([]string{"/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	w := New(nil, nil, 1, time.Hour, time.Hour, nil)
	w.SetPathPolicy(policy, dictionary)
	batch := []Event{{Hit: session.Hit{SiteID: 1, Kind: 0, PathID: ids[0]}, PathSanitizerVersion: 1, PathPolicyID: policy.Fingerprint(), ProfileExternalID: "forged", ProfileTraits: map[string]string{"email": "secret"}, Props: map[string]string{"email": "secret"}}}
	w.suppressRestrictedIdentity(batch)
	if e := batch[0]; e.PathID != 0 || e.PathSanitizerVersion != 0 || e.PathPolicyID != "" {
		t.Fatalf("inconsistent identity retained provenance: %+v", e)
	}
}
