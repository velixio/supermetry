package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestHashLoginClientUsesSecretKey(t *testing.T) {
	client := "203.0.113.9"
	firstKey := []byte("first-secret-key-first-secret-key")
	secondKey := []byte("second-secret-key-second-secret")

	first := hashLoginClient(firstKey, client)
	second := hashLoginClient(secondKey, client)
	if first == second {
		t.Fatal("different server secrets produced the same client hash")
	}
	mac := hmac.New(sha256.New, firstKey)
	_, _ = mac.Write([]byte(loginClientHashPurpose + "\x00" + client))
	if want := hex.EncodeToString(mac.Sum(nil)); first != want {
		t.Fatalf("hashLoginClient() = %q, want HMAC %q", first, want)
	}
}

func TestAuthShutdownWaitsForInFlightLoginAudit(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	a := &Auth{
		auditSecret: []byte("test-secret"),
		writeLoginFailureAudit: func(context.Context, int64, string) error {
			close(started)
			<-release
			return nil
		},
	}

	a.auditLoginFailure(42, "client")
	<-started
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- a.Shutdown(ctx)
	}()

	select {
	case err := <-done:
		t.Fatalf("Shutdown returned before audit completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
