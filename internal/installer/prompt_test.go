package installer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type interruptReader struct{}

func (interruptReader) Read([]byte) (int, error) { return 0, context.Canceled }

func TestPrompterCtrlCStopsPrompt(t *testing.T) {
	t.Parallel()
	_, err := NewPrompter(interruptReader{}, &bytes.Buffer{}).Ask("Public URL", "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask() error = %v, want context.Canceled", err)
	}
}

func TestPrompterAskUsesEditableDefault(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := NewPrompter(strings.NewReader("bad\n\n"), &out)
	got, err := p.Ask("Port", "5432", func(value string) error {
		if value != "5432" {
			return errors.New("unsupported port")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "5432" {
		t.Fatalf("Ask() = %q, want 5432", got)
	}
	if !strings.Contains(out.String(), "unsupported port") {
		t.Fatalf("prompt output = %q, want validation reason", out.String())
	}
}

func TestPrompterAskSecretKeepsDefault(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := NewPrompter(strings.NewReader("\n"), &out)
	got, err := p.AskSecret("Password", "kept-secret")
	if err != nil {
		t.Fatal(err)
	}
	if got != "kept-secret" {
		t.Fatalf("AskSecret() = %q, want kept default", got)
	}
	if strings.Contains(out.String(), "kept-secret") || !strings.Contains(out.String(), "[keep]") {
		t.Fatalf("secret prompt leaked or omitted keep marker: %q", out.String())
	}
}

func TestPrompterChooseAndConfirm(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := NewPrompter(strings.NewReader("invalid\ncaddy\n\n"), &out)
	choice, err := p.Choose("Proxy", []string{"nginx", "caddy", "none"}, "none")
	if err != nil {
		t.Fatal(err)
	}
	if choice != "caddy" {
		t.Fatalf("Choose() = %q, want caddy", choice)
	}
	confirmed, err := p.Confirm("Continue", true)
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("Confirm() = false, want default true")
	}
}
