package mail_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"panel/internal/config"
	"panel/internal/platform/mail"
)

func TestFakeRecordsMessages(t *testing.T) {
	fake := &mail.Fake{}
	message := mail.Message{To: "person@example.com", Subject: "Welcome", Text: "Hello", HTML: "<p>Hello</p>"}
	if err := fake.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if !fake.Enabled() || len(fake.Sent) != 1 || fake.Sent[0] != message {
		t.Fatalf("sent = %#v", fake.Sent)
	}
}

func TestSMTPDisabledWithoutHost(t *testing.T) {
	sender := mail.NewSMTP(config.SMTP{})
	if sender.Enabled() {
		t.Fatal("sender enabled without a host")
	}
	if err := sender.Send(context.Background(), mail.Message{To: "person@example.com"}); err == nil {
		t.Fatal("disabled sender accepted a message")
	}
}

func TestSMTPRefusesAuthenticationWithoutSTARTTLS(t *testing.T) {
	sender := mail.NewSMTP(config.SMTP{
		Host: "127.0.0.1", Port: 1, User: "mailer", Pass: "secret",
		From: "panel@example.com", StartTLS: false,
	})
	err := sender.Send(context.Background(), mail.Message{To: "person@example.com"})
	if err == nil || !strings.Contains(err.Error(), "authentication requires STARTTLS") {
		t.Fatalf("Send error = %v, want STARTTLS requirement", err)
	}
}

func TestSMTPRejectsAddressHeaderInjection(t *testing.T) {
	const injected = "person@example.com (note\r\nX-Injected: yes\r\n)"
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{name: "sender", from: injected, to: "person@example.com", wantErr: "parse SMTP from address"},
		{name: "recipient", from: "panel@example.com", to: injected, wantErr: "parse SMTP recipient"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := mail.NewSMTP(config.SMTP{Host: "127.0.0.1", Port: 1, From: tt.from})
			err := sender.Send(context.Background(), mail.Message{To: tt.to})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Send error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSMTPCancellationStopsStalledExchange(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	host, rawPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	sender := mail.NewSMTP(config.SMTP{Host: host, Port: port, From: "panel@example.com"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- sender.Send(ctx, mail.Message{To: "person@example.com", Subject: "test"})
	}()

	conn := <-accepted
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send succeeded while the SMTP server was stalled")
		}
	case <-time.After(250 * time.Millisecond):
		_ = conn.Close()
		t.Fatal("Send did not stop after context cancellation")
	}
	_ = conn.Close()
}
