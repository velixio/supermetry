package installer

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
)

// EnvValues are the settings written by the installer.
type EnvValues struct {
	DatabaseURL      string
	BaseURL          string
	Listen           string
	TrustedProxies   string
	SecretKey        string
	Docker           bool
	PostgresPassword string
}

// NewSecretKey returns a standard-base64 encoding of 32 random bytes.
func NewSecretKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// RenderEnv renders settings in the subset of dotenv syntax config.LoadDotEnv
// accepts. PostgreSQL URLs percent-encode reserved credential characters.
func RenderEnv(v EnvValues) []byte {
	var b strings.Builder
	writeEnv(&b, "SUPERMETRY_DATABASE_URL", v.DatabaseURL)
	writeEnv(&b, "SUPERMETRY_BASE_URL", v.BaseURL)
	writeEnv(&b, "SUPERMETRY_LISTEN", v.Listen)
	writeEnv(&b, "SUPERMETRY_TRUSTED_PROXIES", v.TrustedProxies)
	writeEnv(&b, "SUPERMETRY_SECRET_KEY", v.SecretKey)
	if v.Docker {
		writeEnv(&b, "POSTGRES_PASSWORD", v.PostgresPassword)
	}
	b.WriteString("\n# Optional. Fill in any time, then restart Supermetry.\n")
	b.WriteString("# SUPERMETRY_SMTP_HOST=\n")
	b.WriteString("# SUPERMETRY_SMTP_PORT=\n")
	b.WriteString("# SUPERMETRY_SMTP_USER=\n")
	b.WriteString("# SUPERMETRY_SMTP_PASS=\n")
	b.WriteString("# SUPERMETRY_SMTP_FROM=\n")
	return []byte(b.String())
}

func writeEnv(b *strings.Builder, key, value string) {
	if value != "" {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
	}
}

// ReadExistingSecret reads a secret from an existing dotenv file without
// mutating the process environment.
func ReadExistingSecret(path string) string {
	return readExistingValue(path, "SUPERMETRY_SECRET_KEY")
}

func readExistingValue(path, wanted string) string {
	return readDotEnv(path)[wanted]
}

// readDotEnv parses KEY=VALUE lines without touching the process environment.
func readDotEnv(path string) map[string]string {
	values := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return values
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.HasPrefix(key, "#") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if _, seen := values[key]; !seen {
			values[key] = value
		}
	}
	return values
}
