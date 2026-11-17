package mail

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"supermetry/internal/config"
	platformcrypto "supermetry/internal/platform/crypto"
)

type memStore struct {
	mu  sync.Mutex
	raw []byte
}

func (m *memStore) LoadSMTP(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.raw, nil
}

func (m *memStore) SaveSMTP(_ context.Context, raw []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raw = raw
	return nil
}

func newTestManager(t *testing.T, env config.SMTP, store *memStore) *Manager {
	t.Helper()
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/smtp/v1")
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(env, store, box)
}

func validSettings(port int) Settings {
	return Settings{Host: "127.0.0.1", Port: port, TLS: "none", From: "Supermetry <supermetry@example.com>"}
}

func TestManagerSaveEncryptsPasswordAndNeverReturnsIt(t *testing.T) {
	store := &memStore{}
	manager := newTestManager(t, config.SMTP{}, store)
	settings := validSettings(587)
	settings.TLS, settings.User, settings.Pass = "starttls", "mailer", "hunter2-secret"
	if err := manager.Save(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(store.raw), "hunter2-secret") {
		t.Fatalf("password stored in clear: %s", store.raw)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Source != SourceDatabase || !status.PasswordSet || status.Settings.Pass != "" || status.Settings.Host != "127.0.0.1" || status.Settings.User != "mailer" {
		t.Fatalf("status = %+v", status)
	}
	if !manager.Enabled() {
		t.Fatal("manager disabled after save")
	}
}

func TestManagerEmptyPasswordKeepsExistingAndClearDropsIt(t *testing.T) {
	store := &memStore{}
	manager := newTestManager(t, config.SMTP{}, store)
	ctx := context.Background()
	first := validSettings(587)
	first.TLS, first.User, first.Pass = "starttls", "mailer", "one"
	if err := manager.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Pass = ""
	second.Port = 2587
	if err := manager.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	cfg, err := manager.effective(ctx)
	if err != nil || cfg.Pass != "one" || cfg.Port != 2587 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if err := manager.ClearPassword(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _ := manager.Status(ctx); status.PasswordSet {
		t.Fatal("password still set after clear")
	}
	if err := manager.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _ := manager.Status(ctx); status.Source != SourceNone || manager.Enabled() {
		t.Fatalf("status=%+v", status)
	}
}

func TestManagerRefusesToCarryPasswordToNewHostOrUser(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(*Settings){
		"host": func(s *Settings) { s.Host = "evil.example.net" },
		"user": func(s *Settings) { s.User = "someone-else" },
	} {
		t.Run(name, func(t *testing.T) {
			store := &memStore{}
			manager := newTestManager(t, config.SMTP{}, store)
			first := validSettings(587)
			first.TLS, first.User, first.Pass = "starttls", "mailer", "one"
			if err := manager.Save(ctx, first); err != nil {
				t.Fatal(err)
			}
			second := first
			second.Pass = ""
			change(&second)
			if err := manager.Save(ctx, second); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("err=%v, want ErrInvalidSettings", err)
			}
			cfg, err := manager.effective(ctx)
			if err != nil || cfg.Host != first.Host || cfg.Pass != "one" {
				t.Fatalf("stored settings changed: cfg=%+v err=%v", cfg, err)
			}
			second.Pass = "two"
			if err := manager.Save(ctx, second); err != nil {
				t.Fatalf("save with new password: %v", err)
			}
		})
	}
}

func TestManagerValidatesLikeConfig(t *testing.T) {
	manager := newTestManager(t, config.SMTP{}, &memStore{})
	bad := []Settings{
		{Host: "", Port: 587, TLS: "starttls", From: "a@example.com"},
		{Host: "smtp.example.test", Port: 70000, TLS: "starttls", From: "a@example.com"},
		{Host: "smtp.example.test", Port: 587, TLS: "ssl", From: "a@example.com"},
		{Host: "smtp.example.test", Port: 587, TLS: "starttls", From: ""},
		{Host: "smtp.example.test", Port: 587, TLS: "none", User: "u", From: "a@example.com"},
		{Host: "smtp.example.test", Port: 587, TLS: "starttls", From: "a@example.com", Hello: "bad name"},
	}
	for i, settings := range bad {
		if err := manager.Save(context.Background(), settings); err == nil || !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("case %d: error = %v", i, err)
		}
	}
}

func TestManagerEnvironmentWins(t *testing.T) {
	store := &memStore{}
	env := config.SMTP{Host: "env.example.test", Port: 587, From: "env@example.com", StartTLS: true, User: "envuser", Pass: "envpass"}
	manager := newTestManager(t, env, store)
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Source != SourceEnvironment || status.Settings.Host != "env.example.test" || status.Settings.TLS != "starttls" || status.Settings.Pass != "" || !status.PasswordSet {
		t.Fatalf("status = %+v", status)
	}
	if err := manager.Save(context.Background(), validSettings(25)); !errors.Is(err, ErrEnvironmentManaged) {
		t.Fatalf("Save error = %v", err)
	}
	if err := manager.Clear(context.Background()); !errors.Is(err, ErrEnvironmentManaged) {
		t.Fatalf("Clear error = %v", err)
	}
	if !manager.Enabled() {
		t.Fatal("env config should enable email")
	}
}

func TestManagerSendsWithSavedSettingsWithoutRestart(t *testing.T) {
	cert, _ := testCert(t, "127.0.0.1")
	server := startFakeSMTP(t, "plain", cert)
	manager := newTestManager(t, config.SMTP{}, &memStore{})
	ctx := context.Background()
	if manager.Enabled() {
		t.Fatal("enabled before configuration")
	}
	if err := manager.Send(ctx, Message{To: "a@example.com", Text: "x"}); err == nil {
		t.Fatal("send without configuration succeeded")
	}
	if err := manager.Save(ctx, validSettings(server.port())); err != nil {
		t.Fatal(err)
	}
	if err := manager.SendTest(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, data := server.received(); !strings.Contains(data, "Subject: ") {
		t.Fatalf("data = %q", data)
	}
}

func TestManagerSendTestReportsFailedStep(t *testing.T) {
	manager := newTestManager(t, config.SMTP{}, &memStore{})
	settings := validSettings(1) // nothing listens on port 1
	if err := manager.Save(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	err := manager.SendTest(context.Background(), "a@example.com")
	if err == nil || ErrorStep(err) != "connect SMTP" {
		t.Fatalf("error = %v", err)
	}
}
