package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/config"
)

// fakeSMTP is an in-process SMTP server. mode is "plain", "starttls" or "implicit".
type fakeSMTP struct {
	t        *testing.T
	listener net.Listener
	tlsConf  *tls.Config
	mode     string
	mu       sync.Mutex
	authed   bool
	data     string
}

func testCert(t *testing.T, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	for _, name := range names {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startFakeSMTP(t *testing.T, mode string, cert tls.Certificate) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeSMTP{t: t, listener: listener, mode: mode, tlsConf: &tls.Config{Certificates: []tls.Certificate{cert}}}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server
}

func (s *fakeSMTP) port() int { return s.listener.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	secure := false
	if s.mode == "implicit" {
		conn = tls.Server(conn, s.tlsConf)
		if err := conn.(*tls.Conn).Handshake(); err != nil {
			return
		}
		secure = true
	}
	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 fake ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"):
			write("250-fake")
			if s.mode == "starttls" && !secure {
				write("250-STARTTLS")
			}
			write("250 AUTH PLAIN")
		case command == "STARTTLS":
			write("220 go ahead")
			tlsConn := tls.Server(conn, s.tlsConf)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn, reader, secure = tlsConn, bufio.NewReader(tlsConn), true
			write = func(line string) { _, _ = tlsConn.Write([]byte(line + "\r\n")) }
		case strings.HasPrefix(command, "AUTH"):
			s.mu.Lock()
			s.authed = true
			s.mu.Unlock()
			write("235 ok")
		case strings.HasPrefix(command, "MAIL"), strings.HasPrefix(command, "RCPT"):
			write("250 ok")
		case command == "DATA":
			write("354 go")
			var body strings.Builder
			for {
				part, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if part == ".\r\n" {
					break
				}
				body.WriteString(part)
			}
			s.mu.Lock()
			s.data = body.String()
			s.mu.Unlock()
			write("250 queued")
		case command == "QUIT":
			write("221 bye")
			return
		default:
			write("502 no")
		}
	}
}

func (s *fakeSMTP) received() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authed, s.data
}

func testSender(cfg config.SMTP, roots *x509.CertPool) *smtpSender {
	cfg.Host = "127.0.0.1"
	cfg.From = "Supermetry <supermetry@example.com>"
	return &smtpSender{config: cfg, rootCAs: roots}
}

func TestSMTPStartTLSPathWithAuth(t *testing.T) {
	cert, roots := testCert(t, "127.0.0.1")
	server := startFakeSMTP(t, "starttls", cert)
	sender := testSender(config.SMTP{Port: server.port(), StartTLS: true, User: "u", Pass: "p"}, roots)
	if err := sender.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "Body"}); err != nil {
		t.Fatal(err)
	}
	authed, data := server.received()
	if !authed || !strings.Contains(data, "Subject: Hi") {
		t.Fatalf("authed=%v data=%q", authed, data)
	}
}

func TestSMTPImplicitTLSWithAuth(t *testing.T) {
	cert, roots := testCert(t, "127.0.0.1")
	server := startFakeSMTP(t, "implicit", cert)
	sender := testSender(config.SMTP{Port: server.port(), ImplicitTLS: true, User: "u", Pass: "p"}, roots)
	if err := sender.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "Body"}); err != nil {
		t.Fatal(err)
	}
	authed, data := server.received()
	if !authed || !strings.Contains(data, "Subject: Hi") {
		t.Fatalf("authed=%v data=%q", authed, data)
	}
}

func TestSMTPImplicitTLSRejectsCertificateMismatch(t *testing.T) {
	cert, roots := testCert(t, "other.example.test")
	server := startFakeSMTP(t, "implicit", cert)
	sender := testSender(config.SMTP{Port: server.port(), ImplicitTLS: true}, roots)
	err := sender.Send(context.Background(), Message{To: "a@example.com", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "start SMTP TLS") {
		t.Fatalf("Send error = %v, want start SMTP TLS failure", err)
	}
}

func TestSMTPImplicitTLSRejectsUntrustedCertificate(t *testing.T) {
	cert, _ := testCert(t, "127.0.0.1")
	server := startFakeSMTP(t, "implicit", cert)
	sender := testSender(config.SMTP{Port: server.port(), ImplicitTLS: true}, nil)
	err := sender.Send(context.Background(), Message{To: "a@example.com", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "start SMTP TLS") {
		t.Fatalf("Send error = %v, want start SMTP TLS failure", err)
	}
}

func TestSMTPStartTLSRejectsCertificateMismatch(t *testing.T) {
	cert, roots := testCert(t, "other.example.test")
	server := startFakeSMTP(t, "starttls", cert)
	sender := testSender(config.SMTP{Port: server.port(), StartTLS: true}, roots)
	err := sender.Send(context.Background(), Message{To: "a@example.com", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "start SMTP TLS") {
		t.Fatalf("Send error = %v, want start SMTP TLS failure", err)
	}
}

func TestSMTPRefusesAuthOverPlaintext(t *testing.T) {
	cert, _ := testCert(t, "127.0.0.1")
	server := startFakeSMTP(t, "plain", cert)
	sender := testSender(config.SMTP{Port: server.port(), User: "u", Pass: "p"}, nil)
	err := sender.Send(context.Background(), Message{To: "a@example.com", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "authentication requires STARTTLS or implicit TLS") {
		t.Fatalf("Send error = %v", err)
	}
	if authed, _ := server.received(); authed {
		t.Fatal("credentials were sent over plaintext")
	}
}

func TestSMTPPlaintextWithoutAuthWorks(t *testing.T) {
	cert, _ := testCert(t, "127.0.0.1")
	server := startFakeSMTP(t, "plain", cert)
	sender := testSender(config.SMTP{Port: server.port()}, nil)
	if err := sender.Send(context.Background(), Message{To: "a@example.com", Subject: "S", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, data := server.received(); !strings.Contains(data, "Subject: S") {
		t.Fatalf("data=%q", data)
	}
}

func TestErrorStep(t *testing.T) {
	if got := ErrorStep(context.DeadlineExceeded); got != "context deadline exceeded" {
		t.Fatalf("got %q", got)
	}
	if got := ErrorStep(&stepErr{"authenticate SMTP: 535 bad user@example.com"}); got != "authenticate SMTP" {
		t.Fatalf("got %q", got)
	}
}

type stepErr struct{ s string }

func (e *stepErr) Error() string { return e.s }
