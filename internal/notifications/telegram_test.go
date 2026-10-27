package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testToken = "123456789:AAEhBOweik6ad9r_QXMENQjcrTu-oSYZ0abc"
	testChat  = "-1001234567890"
)

func tlsClient(t *testing.T, handler http.HandlerFunc, timeout time.Duration) *TelegramClient {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewTelegramClient(TelegramOptions{BaseURL: server.URL, Transport: server.Client().Transport, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestTelegramSendSuccessPlainText(t *testing.T) {
	var path string
	var got map[string]any
	client := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, 0)
	if err := client.SendMessage(context.Background(), testToken, testChat, "hello"); err != nil {
		t.Fatal(err)
	}
	if path != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("path = %q", path)
	}
	if got["chat_id"] != testChat || got["text"] != "hello" {
		t.Fatalf("body = %v", got)
	}
	if _, ok := got["parse_mode"]; ok {
		t.Fatalf("parse_mode must not be set: %v", got)
	}
}

func TestTelegramCapsMessageLength(t *testing.T) {
	var got struct{ Text string }
	client := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, 0)
	if err := client.SendMessage(context.Background(), testToken, testChat, strings.Repeat("é", 9000)); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(got.Text)); n != 4000 {
		t.Fatalf("sent %d characters, want 4000", n)
	}
}

func TestTelegramErrorsNeverContainToken(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"4xx": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found ` + testToken + `"}`))
		},
		"5xx": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream " + r.URL.Path))
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example/"+r.URL.Path, http.StatusFound)
		},
		"oversized": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true,"pad":"` + strings.Repeat("x", 40<<10) + `"}`))
		},
		"not json": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<html>" + testToken + "</html>"))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			err := tlsClient(t, handler, 0).SendMessage(context.Background(), testToken, testChat, "x")
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "AAEhBOweik6ad9r") {
				t.Fatalf("error leaks the token: %v", err)
			}
		})
	}
}

func TestTelegramRedirectIsNotFollowed(t *testing.T) {
	followed := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	client := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}, 0)
	err := client.SendMessage(context.Background(), testToken, testChat, "x")
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v, want redirect refusal", err)
	}
	if followed {
		t.Fatal("redirect target was contacted")
	}
}

func TestTelegramTimeout(t *testing.T) {
	release := make(chan struct{})
	client := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, 100*time.Millisecond)
	defer close(release)
	start := time.Now()
	err := client.SendMessage(context.Background(), testToken, testChat, "x")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestTelegramRejectsBadInputAndPlainHTTP(t *testing.T) {
	if _, err := NewTelegramClient(TelegramOptions{BaseURL: "http://api.telegram.org"}); err == nil {
		t.Fatal("http base URL must be refused")
	}
	client, err := NewTelegramClient(TelegramOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SendMessage(context.Background(), "not-a-token", testChat, "x"); err == nil {
		t.Fatal("invalid token must be refused before any request")
	}
	if err := client.SendMessage(context.Background(), testToken, "bad chat", "x"); err == nil {
		t.Fatal("invalid chat id must be refused")
	}
}

func TestScrub(t *testing.T) {
	got := Scrub("post https://api.telegram.org/bot"+testToken+"/sendMessage failed; other 999999:ZZZZZZZZZZZZZZZZZZZZZZZZZ", testToken)
	if strings.Contains(got, "AAEh") || strings.Contains(got, "ZZZZ") {
		t.Fatalf("Scrub() = %q", got)
	}
	if n := len([]rune(Scrub(strings.Repeat("a", 1000), ""))); n != 300 {
		t.Fatalf("length = %d", n)
	}
}
