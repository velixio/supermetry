package alerts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestWebhookSignsTimestampAndExactBody(t *testing.T) {
	var body []byte
	var signature string
	var timestamp string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ = io.ReadAll(request.Body)
		signature = request.Header.Get("X-Supermetry-Signature")
		timestamp = request.Header.Get("X-Supermetry-Timestamp")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	before := time.Now().Add(-time.Second).Unix()
	client := NewWebhookClient(WebhookOptions{AllowPrivate: true})
	if err := client.Send(context.Background(), ChannelWebhook, server.URL, "secret", testNotification()); err != nil {
		t.Fatal(err)
	}
	after := time.Now().Add(time.Second).Unix()

	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		t.Fatalf("X-Supermetry-Timestamp = %q, want Unix seconds: %v", timestamp, err)
	}
	if seconds < before || seconds > after {
		t.Fatalf("X-Supermetry-Timestamp = %d, want between %d and %d", seconds, before, after)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if signature != want {
		t.Fatalf("X-Supermetry-Signature = %q, want %q", signature, want)
	}
}

func TestDeliverEventSignsTheCallerBody(t *testing.T) {
	var body []byte
	var signature, timestamp string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ = io.ReadAll(request.Body)
		signature = request.Header.Get("X-Supermetry-Signature")
		timestamp = request.Header.Get("X-Supermetry-Timestamp")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	d := NewDispatcher(nil, NewWebhookClient(WebhookOptions{AllowPrivate: true}), nil)
	payload := map[string]any{"type": "resolved", "issue": map[string]any{"id": 5}}
	channel := DeliveryChannel{Kind: ChannelWebhook, URL: server.URL, Secret: "secret"}
	if err := d.DeliverEvent(context.Background(), channel, "resolved", "s", "text", payload); err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"issue":{"id":5},"type":"resolved"}` {
		t.Fatalf("body = %s", body)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	if signature != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("signature = %q", signature)
	}
	// Chat channels get the text, not the structured body.
	channel = DeliveryChannel{Kind: ChannelSlack, URL: server.URL}
	if err := d.DeliverEvent(context.Background(), channel, "resolved", "s", "hello", payload); err != nil || string(body) != `{"text":"hello"}` {
		t.Fatalf("slack body = %s err %v", body, err)
	}
}
