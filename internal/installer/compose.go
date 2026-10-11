package installer

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed templates/docker-compose.yml.tmpl
var composeTemplate string

// ComposeValues controls the generated Docker Compose file.
type ComposeValues struct {
	Version    string
	Image      string
	ListenHost string
	Port       string
}

// RenderCompose renders the bundled app and PostgreSQL services.
func RenderCompose(c ComposeValues) []byte {
	if c.Version == "" {
		c.Version = "latest"
	}
	tmpl := template.Must(template.New("compose").Option("missingkey=error").Parse(composeTemplate))
	var out bytes.Buffer
	if err := tmpl.Execute(&out, c); err != nil {
		panic(err)
	}
	return out.Bytes()
}
