package installer

import (
	"fmt"
	"path/filepath"
	"strings"
)

func printDryRun(opts Options, p *Prompter, sys System) error {
	fmt.Fprintln(p.out, "DRY RUN: no files, commands, users, services, proxies, sockets, or database connections will be changed.")
	if opts.Mode == "docker" {
		return printDockerDryRun(opts, p, sys)
	}

	envPath := ".env"
	if sys.GOOS == "linux" {
		envPath = "/etc/supermetry/supermetry.env"
	}
	fmt.Fprintln(p.out, "Check: PostgreSQL connection and version (skipped in dry-run)")
	fmt.Fprintf(p.out, "File: %s (mode %04o)\n", envPath, envMode(sys.GOOS))
	fmt.Fprintln(p.out, "  contents: SUPERMETRY_DATABASE_URL=<redacted>, SUPERMETRY_SECRET_KEY=<generated>, SUPERMETRY_BASE_URL, SUPERMETRY_LISTEN, optional trusted proxy, commented SMTP settings")
	if sys.GOOS == "linux" {
		fmt.Fprintln(p.out, "User: create system user supermetry when absent")
		fmt.Fprintf(p.out, "File: /opt/supermetry/versions/%s/supermetry (installed executable, mode 0755)\n", opts.Version)
		fmt.Fprintln(p.out, "Symlink: /opt/supermetry/current -> installed version (atomic replacement)")
		fmt.Fprintln(p.out, "File: /etc/systemd/system/supermetry.service (bundled systemd unit, mode 0644)")
		fmt.Fprintln(p.out, "File: /etc/supermetry/deployment.json (fixed managed-deployment descriptor, mode 0644)")
		fmt.Fprintln(p.out, "Files: /etc/systemd/system/supermetry-updater.{socket,service} (root-owned updater units, mode 0644)")
		fmt.Fprintln(p.out, "Command: id -u supermetry")
		fmt.Fprintln(p.out, "Command: useradd --system --home /var/lib/supermetry --shell /usr/sbin/nologin supermetry (when absent)")
		fmt.Fprintln(p.out, "Command: chown root:supermetry /etc/supermetry/supermetry.env")
		fmt.Fprintln(p.out, "Service: systemctl daemon-reload")
		fmt.Fprintln(p.out, "Service: systemctl enable --now supermetry")
		fmt.Fprintf(p.out, "Check: GET %s/readyz (up to 60s)\n", localBaseURL(opts.Listen))
		printProxyDryRun(opts, p)
		fmt.Fprintln(p.out, "Command: systemctl status supermetry --no-pager")
	}
	return nil
}

func printDockerDryRun(opts Options, p *Prompter, sys System) error {
	fmt.Fprintf(p.out, "File: %s (mode 0600)\n", filepath.Join(opts.Dir, ".env"))
	fmt.Fprintln(p.out, "  contents: generated application and PostgreSQL secrets, base URL, listen address, optional trusted proxy, commented SMTP settings")
	fmt.Fprintf(p.out, "File: %s (mode 0644)\n", filepath.Join(opts.Dir, "docker-compose.yml"))
	fmt.Fprintf(p.out, "  contents: Supermetry %s image source, postgres:18-alpine, named volumes, health check, port %s\n", opts.Image, opts.Listen)
	if sys.GOOS == "linux" && opts.Image == "download" && filepath.Clean(opts.Dir) == "/opt/supermetry" {
		fmt.Fprintln(p.out, "Updater: install root-owned helper and systemd socket; mount only /run/supermetry-updater and the read-only deployment descriptor; add the numeric socket group")
	}
	if opts.Image == "download" {
		fmt.Fprintf(p.out, "Command: docker compose --project-directory %s pull\n", opts.Dir)
	} else {
		_, sourceURL, sourceDir := dockerSource(opts)
		fmt.Fprintf(p.out, "Directory: %s (downloaded source archive contents)\n", sourceDir)
		fmt.Fprintf(p.out, "Download: source archive %s and extract it with the built-in cross-platform extractor\n", sourceURL)
		fmt.Fprintf(p.out, "Command: docker compose --project-directory %s build\n", opts.Dir)
	}
	fmt.Fprintf(p.out, "Command: docker compose --project-directory %s up -d\n", opts.Dir)
	fmt.Fprintf(p.out, "Check: GET %s/readyz (up to 60s)\n", localBaseURL(opts.Listen))
	fmt.Fprintf(p.out, "Check: GET %s/api/private/setup/status\n", localBaseURL(opts.Listen))
	printProxyDryRun(opts, p)
	return nil
}

func printProxyDryRun(opts Options, p *Prompter) {
	switch opts.Proxy {
	case "nginx":
		fmt.Fprintln(p.out, "Proxy file: /etc/nginx/conf.d/supermetry.conf (generated site config, mode 0644)")
		fmt.Fprintln(p.out, "Command: nginx -t")
		fmt.Fprintln(p.out, "Service: systemctl reload nginx")
		fmt.Fprintf(p.out, "Optional command: certbot --nginx -d %s\n", proxyDomain(opts.BaseURL))
	case "caddy":
		fmt.Fprintln(p.out, "Proxy backup: /etc/caddy/Caddyfile.supermetry.bak")
		fmt.Fprintln(p.out, "Proxy file: /etc/caddy/Caddyfile (append generated site block, mode 0644)")
		fmt.Fprintln(p.out, "Command: caddy validate --config /etc/caddy/Caddyfile")
		fmt.Fprintln(p.out, "Service: systemctl reload caddy")
	default:
		fmt.Fprintf(p.out, "Proxy: none; forward traffic to %s\n", opts.Listen)
	}
}

func envMode(goos string) uint32 {
	if strings.EqualFold(goos, "linux") {
		return 0o640
	}
	return 0o600
}
