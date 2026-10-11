package installer

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed templates/nginx.conf.tmpl
var nginxTemplate string

//go:embed templates/caddy.tmpl
var caddyTemplate string

type proxyValues struct {
	Domain   string
	Upstream string
}

// RenderNginx renders an HTTP nginx reverse-proxy site.
func RenderNginx(domain, upstream string) []byte {
	return renderTemplate("nginx", nginxTemplate, proxyValues{Domain: domain, Upstream: upstream})
}

// RenderCaddy renders a Caddy reverse-proxy site block.
func RenderCaddy(domain, upstream string) []byte {
	return renderTemplate("caddy", caddyTemplate, proxyValues{Domain: domain, Upstream: upstream})
}

func renderTemplate(name, source string, values any) []byte {
	tmpl := template.Must(template.New(name).Option("missingkey=error").Parse(source))
	var out bytes.Buffer
	if err := tmpl.Execute(&out, values); err != nil {
		panic(err)
	}
	return out.Bytes()
}
