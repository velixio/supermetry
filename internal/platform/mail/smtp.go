package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	stdmail "net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/config"
)

type smtpSender struct {
	config config.SMTP
	// rootCAs overrides the system roots; tests use it to trust a throwaway
	// certificate without ever disabling verification.
	rootCAs *x509.CertPool
}

// NewSMTP creates a sender backed by net/smtp. An empty host disables email.
func NewSMTP(cfg config.SMTP) Sender {
	return &smtpSender{config: cfg}
}

func (s *smtpSender) Enabled() bool { return s.config.Host != "" }

func (s *smtpSender) Send(ctx context.Context, message Message) error {
	if !s.Enabled() {
		return errors.New("SMTP is not configured")
	}
	if s.config.User != "" && !s.config.StartTLS && !s.config.ImplicitTLS {
		return errors.New("SMTP authentication requires STARTTLS or implicit TLS")
	}
	if strings.ContainsAny(s.config.From, "\r\n") {
		return errors.New("parse SMTP from address: line breaks are not allowed")
	}
	from, err := stdmail.ParseAddress(s.config.From)
	if err != nil {
		return fmt.Errorf("parse SMTP from address: %w", err)
	}
	if strings.ContainsAny(message.To, "\r\n") {
		return errors.New("parse SMTP recipient: line breaks are not allowed")
	}
	to, err := stdmail.ParseAddress(message.To)
	if err != nil {
		return fmt.Errorf("parse SMTP recipient: %w", err)
	}

	port := s.config.Port
	if port == 0 {
		port = 587
	}
	address := net.JoinHostPort(s.config.Host, strconv.Itoa(port))
	var conn net.Conn
	rawConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	conn = rawConn
	defer func() { _ = conn.Close() }()
	if s.config.ImplicitTLS {
		// SMTPS: TLS from the first byte; the certificate is always verified.
		tlsConn := tls.Client(rawConn, s.tlsConfig())
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
		conn = tlsConn
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set SMTP deadline: %w", err)
		}
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopCancel()

	client, err := smtp.NewClient(conn, s.config.Host)
	if err != nil {
		return fmt.Errorf("create SMTP client: %w", err)
	}
	defer client.Close()
	// Announce a real host name: the client's default, "localhost", makes some
	// providers hold the message for a minute or more.
	hello := s.config.Hello
	if hello == "" {
		hello = config.SMTPHelloName("", "", s.config.From)
	}
	if err := client.Hello(hello); err != nil {
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	if s.config.StartTLS && !s.config.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if s.config.User != "" {
		if err := client.Auth(smtp.PlainAuth("", s.config.User, s.config.Pass, s.config.Host)); err != nil {
			return fmt.Errorf("authenticate SMTP: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP message: %w", err)
	}
	data := renderMessage(s.config.From, hello, message, time.Now())
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("quit SMTP: %w", err)
	}
	return nil
}

func (s *smtpSender) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: s.config.Host, MinVersion: tls.VersionTLS12, RootCAs: s.rootCAs}
}

// ErrorStep returns the step an SMTP send error names ("connect SMTP",
// "authenticate SMTP", ...), without the detail after it. Server replies can
// echo a recipient address, so callers that avoid personal data in logs use
// the step rather than the whole error.
func ErrorStep(err error) string {
	if err == nil {
		return ""
	}
	step, _, _ := strings.Cut(err.Error(), ": ")
	return step
}

func renderMessage(from, hello string, message Message, now time.Time) []byte {
	var data bytes.Buffer
	fmt.Fprintf(&data, "From: %s\r\n", from)
	fmt.Fprintf(&data, "To: %s\r\n", message.To)
	// Date and Message-ID are required or expected of every message; filters score a message without them as suspicious.
	fmt.Fprintf(&data, "Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&data, "Message-ID: <%s@%s>\r\n", messageIDToken(), hello)
	fmt.Fprintf(&data, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", sanitizeHeader(message.Subject)))
	data.WriteString("MIME-Version: 1.0\r\n")
	if message.HTML == "" {
		data.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		data.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		data.WriteString(message.Text)
		return data.Bytes()
	}
	const boundary = "supermetry-alternative-boundary"
	fmt.Fprintf(&data, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	// Quoted-printable keeps every line under the 998-byte SMTP limit; the
	// branded HTML has long inline-styled lines.
	writePart(&data, boundary, "text/plain", message.Text)
	writePart(&data, boundary, "text/html", message.HTML)
	fmt.Fprintf(&data, "--%s--\r\n", boundary)
	return data.Bytes()
}

func writePart(data *bytes.Buffer, boundary, contentType, body string) {
	fmt.Fprintf(data, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, contentType)
	encoder := quotedprintable.NewWriter(data)
	_, _ = encoder.Write([]byte(body))
	_ = encoder.Close()
	data.WriteString("\r\n")
}

// messageIDToken is 16 random bytes in hex: unique per message.
func messageIDToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
