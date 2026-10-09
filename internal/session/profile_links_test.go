package session_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"supermetry/internal/session"
)

func TestProfileTombstoneBlocksLinkedSessionAndRelinkUntilExpiry(t *testing.T) {
	m := session.NewMemory(2, realClock{})
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	result := m.Touch(session.Hit{SiteID: 7, TS: now, LocalDay: now, VisitorID: 42})

	if !m.LinkProfile(7, result.SessionID, "customer-1") {
		t.Fatal("initial profile link was rejected")
	}
	hash := sha256.Sum256([]byte("customer-1"))
	m.TombstoneProfile(7, hash)
	if !m.ProfileBlocked(7, result.SessionID) {
		t.Fatal("linked session was not blocked")
	}
	if m.LinkProfile(7, result.SessionID, "customer-1") {
		t.Fatal("tombstoned profile re-linked to live session")
	}

	m.ExpireProfileTombstone(7, hash)
	if m.ProfileBlocked(7, result.SessionID) {
		t.Fatal("session remained blocked after tombstone expiry")
	}
	if !m.LinkProfile(7, result.SessionID, "customer-1") {
		t.Fatal("profile link remained blocked after tombstone expiry")
	}
}

func TestExpiredSessionDropsProfileBlock(t *testing.T) {
	m := session.NewMemory(1, realClock{})
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	result := m.Touch(session.Hit{SiteID: 7, TS: now, LocalDay: now, VisitorID: 42})
	if !m.LinkProfile(7, result.SessionID, "customer-1") {
		t.Fatal("initial profile link was rejected")
	}
	m.TombstoneProfile(7, sha256.Sum256([]byte("customer-1")))

	if expired := m.Expire(now.Add(session.IdleTimeout + time.Nanosecond)); expired != 1 {
		t.Fatalf("expired sessions = %d, want 1", expired)
	}
	if m.ProfileBlocked(7, result.SessionID) {
		t.Fatal("expired session retained its profile block")
	}
}

func TestProfileBlocksTrackEveryIdentityLinkedToSession(t *testing.T) {
	m := session.NewMemory(1, realClock{})
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	result := m.Touch(session.Hit{SiteID: 7, TS: now, LocalDay: now, VisitorID: 42})
	if !m.LinkProfile(7, result.SessionID, "customer-1") || !m.LinkProfile(7, result.SessionID, "customer-2") {
		t.Fatal("link profiles")
	}
	first := sha256.Sum256([]byte("customer-1"))
	second := sha256.Sum256([]byte("customer-2"))
	m.TombstoneProfile(7, first)
	if !m.ProfileBlocked(7, result.SessionID) {
		t.Fatal("session lost its earlier linked identity")
	}
	m.TombstoneProfile(7, second)
	if !m.ProfileBlocked(7, result.SessionID) {
		t.Fatal("session with tombstoned historical identity was not blocked")
	}
	m.ExpireProfileTombstone(7, first)
	if !m.ProfileBlocked(7, result.SessionID) {
		t.Fatal("expiring one identity removed another identity's block")
	}
	m.ExpireProfileTombstone(7, second)
	if m.ProfileBlocked(7, result.SessionID) {
		t.Fatal("session stayed blocked after every linked tombstone expired")
	}
}
