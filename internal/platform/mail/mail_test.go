package mail_test

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"supermetry/internal/config"
	"supermetry/internal/platform/mail"
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
		From: "supermetry@example.com", StartTLS: false,
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
		{name: "recipient", from: "supermetry@example.com", to: injected, wantErr: "parse SMTP recipient"},
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
	sender := mail.NewSMTP(config.SMTP{Host: host, Port: port, From: "supermetry@example.com"})
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

// fakeSMTP is a minimal plain SMTP server: it records the EHLO name and the
// message it receives.
func fakeSMTP(t *testing.T) (host string, port int, result chan [2]string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	result = make(chan [2]string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		var ehlo string
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch cmd := strings.ToUpper(line); {
			case strings.HasPrefix(cmd, "EHLO "), strings.HasPrefix(cmd, "HELO "):
				ehlo = strings.TrimSpace(line[5:])
				write("250 fake")
			case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
				write("250 ok")
			case cmd == "DATA":
				write("354 go on")
				for {
					l, err := r.ReadString('\n')
					if err != nil || strings.TrimRight(l, "\r\n") == "." {
						break
					}
					data.WriteString(l)
				}
				write("250 queued")
			case cmd == "QUIT":
				write("221 bye")
				result <- [2]string{ehlo, data.String()}
				return
			default:
				write("250 ok")
			}
		}
	}()
	h, p, _ := net.SplitHostPort(listener.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port, result
}

func TestSMTPAnnouncesARealHostNameAndAddsDateAndMessageID(t *testing.T) {
	host, port, result := fakeSMTP(t)
	cfg := config.SMTP{Host: host, Port: port, From: "Supermetry <noreply@example.net>", Hello: "app.example.net"}
	if err := mail.NewSMTP(cfg).Send(context.Background(), mail.Message{To: "person@example.com", Subject: "Hi", Text: "body"}); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got[0] != "app.example.net" {
		t.Fatalf("EHLO name = %q, want app.example.net", got[0])
	}
	for _, want := range []string{"\nDate: ", "\nMessage-ID: <", "@app.example.net>"} {
		if !strings.Contains("\n"+got[1], want) {
			t.Errorf("message lacks %q:\n%s", want, got[1])
		}
	}

	// Without a configured name the sender domain is announced, never "localhost".
	host, port, result = fakeSMTP(t)
	cfg = config.SMTP{Host: host, Port: port, From: "Supermetry <noreply@example.net>"}
	if err := mail.NewSMTP(cfg).Send(context.Background(), mail.Message{To: "person@example.com", Subject: "Hi", Text: "body"}); err != nil {
		t.Fatal(err)
	}
	if got = <-result; got[0] != "example.net" {
		t.Fatalf("default EHLO name = %q, want example.net", got[0])
	}
}
