package mail

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"supermetry/internal/config"
	platformcrypto "supermetry/internal/platform/crypto"
)

// SMTPSecretInfo is the key-derivation purpose for the stored SMTP password.
const SMTPSecretInfo = "supermetry/smtp/v1"

const settingsCacheTTL = 30 * time.Second

// Where the effective SMTP settings come from.
const (
	SourceEnvironment = "environment"
	SourceDatabase    = "database"
	SourceNone        = "none"
)

var (
	// ErrEnvironmentManaged is returned when SUPERMETRY_SMTP_HOST is set: the
	// environment wins entirely and the stored settings cannot be edited.
	ErrEnvironmentManaged = errors.New("SMTP is configured by environment variables")
	// ErrInvalidSettings wraps a validation failure; the message names each problem.
	ErrInvalidSettings = errors.New("invalid SMTP settings")
)

// Settings is the SMTP configuration shown in and saved from the admin screen.
// TLS is "starttls", "implicit" or "none".
type Settings struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	TLS   string `json:"tls"`
	User  string `json:"user"`
	Pass  string `json:"-"`
	From  string `json:"from"`
	Hello string `json:"helo"`
}

// Status reports the effective settings without the password.
type Status struct {
	Source      string
	Settings    Settings
	PasswordSet bool
}

// SettingsStore persists the instance SMTP document. LoadSMTP returns nil when
// nothing is saved; SaveSMTP(nil) removes the document.
type SettingsStore interface {
	LoadSMTP(context.Context) ([]byte, error)
	SaveSMTP(context.Context, []byte) error
}

// Manager is a Sender whose configuration is resolved at send time:
// environment variables when SUPERMETRY_SMTP_HOST is set, else the settings an
// instance admin saved. Saving takes effect without a restart.
type Manager struct {
	env     config.SMTP
	store   SettingsStore
	box     *platformcrypto.SecretBox
	baseURL string
	rootCAs *x509.CertPool

	mu       sync.Mutex
	cached   config.SMTP
	cachedAt time.Time
	loaded   bool
}

// NewManager builds a Manager. env is the configuration read from the
// environment; when its Host is set it wins entirely.
func NewManager(env config.SMTP, store SettingsStore, box *platformcrypto.SecretBox) *Manager {
	return &Manager{env: env, store: store, box: box}
}

// WithBaseURL supplies SUPERMETRY_BASE_URL so the EHLO name can be derived
// from it, as it is for environment configuration.
func (m *Manager) WithBaseURL(baseURL string) *Manager {
	m.baseURL = baseURL
	return m
}

func (m *Manager) envManaged() bool { return strings.TrimSpace(m.env.Host) != "" }

// Enabled reports whether email can be sent. It refreshes the cached stored
// settings when they are stale; a failed refresh keeps the previous answer.
func (m *Manager) Enabled() bool {
	if m.envManaged() {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cfg, err := m.effective(ctx)
	return err == nil && cfg.Host != ""
}

// Send delivers a message with the effective configuration.
func (m *Manager) Send(ctx context.Context, message Message) error {
	cfg, err := m.effective(ctx)
	if err != nil {
		return fmt.Errorf("load SMTP settings: %w", err)
	}
	sender := &smtpSender{config: cfg, rootCAs: m.rootCAs}
	return sender.Send(ctx, message)
}

// SendTest sends a short plain message to the given address with the
// effective configuration. A relay accepting it is not proof of inbox
// delivery.
func (m *Manager) SendTest(ctx context.Context, to string) error {
	return m.Send(ctx, Message{
		To:      to,
		Subject: "Supermetry test email",
		Text:    "This is a test email from your Supermetry instance. If you can read it, outgoing email is configured.\n",
	})
}

func (m *Manager) effective(ctx context.Context) (config.SMTP, error) {
	if m.envManaged() {
		return m.env, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded && time.Since(m.cachedAt) < settingsCacheTTL {
		return m.cached, nil
	}
	cfg, err := m.loadStored(ctx)
	if err != nil {
		if m.loaded {
			return m.cached, nil
		}
		return config.SMTP{}, err
	}
	m.cached, m.cachedAt, m.loaded = cfg, time.Now(), true
	return cfg, nil
}

func (m *Manager) invalidate() {
	m.mu.Lock()
	m.loaded = false
	m.mu.Unlock()
}

type storedSMTP struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	TLS   string `json:"tls"`
	User  string `json:"user"`
	Pass  string `json:"pass,omitempty"` // base64 of the sealed password
	From  string `json:"from"`
	Hello string `json:"helo"`
}

func (m *Manager) loadStored(ctx context.Context) (config.SMTP, error) {
	raw, err := m.store.LoadSMTP(ctx)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return config.SMTP{}, err
	}
	var wire storedSMTP
	if err := json.Unmarshal(raw, &wire); err != nil {
		return config.SMTP{}, fmt.Errorf("decode SMTP settings: %w", err)
	}
	cfg := toConfig(Settings{Host: wire.Host, Port: wire.Port, TLS: wire.TLS, User: wire.User, From: wire.From, Hello: wire.Hello}, m.baseURL)
	if wire.Pass != "" {
		sealed, err := base64.StdEncoding.Strict().DecodeString(wire.Pass)
		if err != nil {
			return config.SMTP{}, errors.New("decode sealed SMTP password")
		}
		plain, err := m.box.Open(sealed)
		if err != nil {
			return config.SMTP{}, fmt.Errorf("open SMTP password: %w", err)
		}
		cfg.Pass = string(plain)
	}
	return cfg, nil
}

func toConfig(s Settings, baseURL string) config.SMTP {
	cfg := config.SMTP{Host: strings.TrimSpace(s.Host), Port: s.Port, User: s.User, Pass: s.Pass, From: strings.TrimSpace(s.From)}
	switch s.TLS {
	case "starttls":
		cfg.StartTLS = true
	case "implicit":
		cfg.ImplicitTLS = true
	}
	cfg.Hello = config.SMTPHelloName(strings.TrimSpace(s.Hello), baseURL, cfg.From)
	return cfg
}

func tlsMode(cfg config.SMTP) string {
	switch {
	case cfg.ImplicitTLS:
		return "implicit"
	case cfg.StartTLS:
		return "starttls"
	}
	return "none"
}

// Status returns where the effective settings come from and their values,
// never the password.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if m.envManaged() {
		return Status{Source: SourceEnvironment, Settings: settingsOf(m.env, m.env.Hello), PasswordSet: m.env.Pass != ""}, nil
	}
	raw, err := m.store.LoadSMTP(ctx)
	if err != nil {
		return Status{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return Status{Source: SourceNone, Settings: Settings{Port: 587, TLS: "starttls"}}, nil
	}
	var wire storedSMTP
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Status{}, fmt.Errorf("decode SMTP settings: %w", err)
	}
	return Status{
		Source:      SourceDatabase,
		Settings:    Settings{Host: wire.Host, Port: wire.Port, TLS: wire.TLS, User: wire.User, From: wire.From, Hello: wire.Hello},
		PasswordSet: wire.Pass != "",
	}, nil
}

func settingsOf(cfg config.SMTP, hello string) Settings {
	return Settings{Host: cfg.Host, Port: cfg.Port, TLS: tlsMode(cfg), User: cfg.User, From: cfg.From, Hello: hello}
}

// Save validates and stores settings. An empty Pass keeps the stored password
// (dropped when the username is cleared). It fails with ErrEnvironmentManaged
// while SUPERMETRY_SMTP_HOST is set.
func (m *Manager) Save(ctx context.Context, s Settings) error {
	if m.envManaged() {
		return ErrEnvironmentManaged
	}
	s.Host, s.From, s.User = strings.TrimSpace(s.Host), strings.TrimSpace(s.From), strings.TrimSpace(s.User)
	s.Hello = strings.TrimSpace(s.Hello)
	switch s.TLS {
	case "starttls", "implicit", "none":
	default:
		return fmt.Errorf("%w: TLS mode must be starttls, implicit or none", ErrInvalidSettings)
	}
	if err := config.ValidateSMTP(toConfig(s, "")); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidSettings, strings.ReplaceAll(err.Error(), "\n", "; "))
	}
	wire := storedSMTP{Host: s.Host, Port: s.Port, TLS: s.TLS, User: s.User, From: s.From, Hello: s.Hello}
	switch {
	case s.User == "":
	case s.Pass != "":
		sealed := m.box.Seal([]byte(s.Pass))
		wire.Pass = base64.StdEncoding.EncodeToString(sealed)
	default:
		raw, err := m.store.LoadSMTP(ctx)
		if err != nil {
			return err
		}
		var old storedSMTP
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &old); err != nil {
				return fmt.Errorf("decode SMTP settings: %w", err)
			}
		}
		wire.Pass = old.Pass
	}
	return m.write(ctx, wire)
}

func (m *Manager) write(ctx context.Context, wire storedSMTP) error {
	encoded, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("encode SMTP settings: %w", err)
	}
	if err := m.store.SaveSMTP(ctx, encoded); err != nil {
		return err
	}
	m.invalidate()
	return nil
}

// ClearPassword removes the stored password and keeps the other settings.
func (m *Manager) ClearPassword(ctx context.Context) error {
	if m.envManaged() {
		return ErrEnvironmentManaged
	}
	raw, err := m.store.LoadSMTP(ctx)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return err
	}
	var wire storedSMTP
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("decode SMTP settings: %w", err)
	}
	wire.Pass = ""
	return m.write(ctx, wire)
}

// Clear removes every stored setting, which turns email off.
func (m *Manager) Clear(ctx context.Context) error {
	if m.envManaged() {
		return ErrEnvironmentManaged
	}
	if err := m.store.SaveSMTP(ctx, nil); err != nil {
		return err
	}
	m.invalidate()
	return nil
}
