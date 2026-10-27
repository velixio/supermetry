package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
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
	if s.config.User != "" && !s.config.StartTLS {
		return errors.New("SMTP authentication requires STARTTLS")
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
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer conn.Close()
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
	if s.config.StartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.config.Host, MinVersion: tls.VersionTLS12}); err != nil {
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
	fmt.Fprintf(&data, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, message.Text)
	fmt.Fprintf(&data, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, message.HTML)
	fmt.Fprintf(&data, "--%s--\r\n", boundary)
	return data.Bytes()
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
