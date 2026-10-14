package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Options mirrors the interactive installer questions.
type Options struct {
	Mode    string
	DotEnv  string // dotenv file offered as answers; empty disables
	BaseURL string
	DB      DBConfig
	Listen  string
	Proxy   string
	Image   string
	Dir     string
	Yes     bool
	Version string
	Apply   string
	DryRun  bool
}

// System contains installer side effects so flow tests can remain isolated.
type System struct {
	Runner        Runner
	WriteFile     func(path string, data []byte, mode os.FileMode) error
	GOOS          string
	IsRoot        bool
	LookPath      func(file string) (string, error)
	Check         func(ctx context.Context, conn string) (int, error)
	AdminExists   func(ctx context.Context, conn string) (bool, error)
	CheckPort     func(listen string) error
	Ready         func(ctx context.Context, baseURL string, timeout time.Duration) error
	ReadFile      func(path string) ([]byte, error)
	SetupRequired func(ctx context.Context, baseURL string) (bool, error)
	Stat          func(path string) (os.FileInfo, error)
	PrepareSource func(ctx context.Context, opts Options) error
	LocalIP       func() (string, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type plannedFile struct {
	Path string      `json:"path"`
	Data []byte      `json:"data"`
	Mode os.FileMode `json:"mode"`
}

type privilegedPlan struct {
	Files          []plannedFile `json:"files"`
	InstallBinary  bool          `json:"install_binary"`
	InstallUpdater bool          `json:"install_updater"`
	Version        string        `json:"version"`
	Proxy          string        `json:"proxy"`
	Domain         string        `json:"domain"`
	Listen         string        `json:"listen"`
}

var errInstallDeclined = errors.New("installation declined")

// DefaultSystem returns the real operating-system integrations used by the
// command-line installer.
func DefaultSystem() System {
	sys := withSystemDefaults(System{})
	sys.AdminExists = CheckAdminExists
	sys.CheckPort = checkPortAvailable
	sys.SetupRequired = CheckSetupRequired
	sys.PrepareSource = prepareDockerBuildSource
	if runtime.GOOS == "linux" {
		current, err := user.Current()
		sys.IsRoot = err == nil && current.Uid == "0"
	}
	return sys
}

// Run performs an interactive or unattended installation.
func Run(ctx context.Context, opts Options, p *Prompter, sys System) error {
	sys = withSystemDefaults(sys)
	if opts.DryRun && opts.Apply != "" {
		return errors.New("--dry-run cannot be combined with --apply")
	}
	if opts.Apply != "" {
		return applySavedPlan(ctx, opts.Apply, sys)
	}
	if opts.Yes || opts.DryRun {
		if err := validateUnattended(opts); err != nil {
			return err
		}
	}

	var err error
	if !opts.Yes && !opts.DryRun {
		if opts, err = adoptDotEnv(opts, p); err != nil {
			return err
		}
	}
	if opts.Mode == "" {
		modes := []string{"binary"}
		if _, dockerErr := sys.Runner.Run(ctx, "docker", "compose", "version"); dockerErr == nil {
			modes = append(modes, "docker")
		}
		opts.Mode, err = p.Choose("Install mode", modes, "binary")
		if err != nil {
			return err
		}
	}
	if opts.Mode != "binary" && opts.Mode != "docker" {
		return fmt.Errorf("--mode must be binary or docker")
	}
	// The listen address comes first so the public URL can default to it.
	if opts.Listen == "" {
		if opts.Yes || opts.DryRun {
			opts.Listen = "127.0.0.1:8787"
		} else {
			opts.Listen, err = p.Ask("Listen address (0.0.0.0:8787 to accept other devices)", "127.0.0.1:8787", validateListen)
			if err != nil {
				return err
			}
		}
	}
	if err := validateListen(opts.Listen); err != nil {
		return err
	}
	if opts.BaseURL == "" {
		opts.BaseURL, err = p.Ask("Public URL", suggestBaseURL(opts.Listen, sys.LocalIP), validateHTTPURL)
		if err != nil {
			return err
		}
	} else if err := validateHTTPURL(opts.BaseURL); err != nil {
		return err
	}
	if opts.Proxy == "" {
		if opts.Yes || opts.DryRun || sys.GOOS != "linux" {
			opts.Proxy = "none"
		} else {
			opts.Proxy, err = p.Choose("Reverse proxy", []string{"nginx", "caddy", "none"}, "none")
			if err != nil {
				return err
			}
		}
	}
	if opts.Proxy != "none" && opts.Proxy != "nginx" && opts.Proxy != "caddy" {
		return errors.New("--proxy must be nginx, caddy or none")
	}
	if opts.Version == "" {
		opts.Version = "latest"
	}
	if opts.Mode == "docker" {
		if opts.Dir == "" {
			opts.Dir = "./supermetry"
			if sys.GOOS == "linux" {
				opts.Dir = "/opt/supermetry"
			}
		}
		if opts.Image == "" {
			opts.Image = "download"
		}
		if opts.Image != "download" && opts.Image != "build" {
			return errors.New("--image must be download or build")
		}
	}
	if opts.DryRun {
		return printDryRun(opts, p, sys)
	}
	if sys.GOOS == "linux" && opts.Proxy != "none" {
		if _, err := sys.LookPath(opts.Proxy); err != nil {
			fmt.Fprintf(p.out, "%s is not installed. Install it, then rerun this installer (for example: sudo apt install %s).\n", opts.Proxy, opts.Proxy)
			opts.Proxy = "none"
		}
	}
	if opts.Mode == "docker" {
		err = runDocker(ctx, opts, p, sys)
	} else {
		err = runBinary(ctx, opts, p, sys)
	}
	if errors.Is(err, errInstallDeclined) {
		return nil
	}
	return err
}

func runBinary(ctx context.Context, opts Options, p *Prompter, sys System) error {
	adminExists := false
	if opts.DB.URL == "" && opts.DB.Host == "" {
		var err error
		opts.DB, err = askDB(p, opts.DB)
		if err != nil {
			return err
		}
	}
	for {
		version, err := sys.Check(ctx, opts.DB.ConnString())
		if err == nil {
			if version < 140000 {
				return fmt.Errorf("PostgreSQL 14 or newer is required; server reports %s", postgresMajor(version))
			}
			if version < 180000 {
				fmt.Fprintln(p.out, "Warning: PostgreSQL 18 is the tested baseline and is recommended because it is the bundled version and the only version exercised by CI; older supported versions are not regularly tested.")
			}
			if sys.AdminExists != nil {
				adminExists, err = sys.AdminExists(ctx, opts.DB.ConnString())
				if err != nil {
					return fmt.Errorf("check for an existing admin account: %w", err)
				}
			}
			break
		}
		if opts.Yes {
			return err
		}
		var checkErr *CheckError
		if errors.As(err, &checkErr) {
			fmt.Fprintln(p.out, checkErr.Reason)
		} else {
			fmt.Fprintln(p.out, err)
		}
		opts.DB, err = askDB(p, opts.DB)
		if err != nil {
			return err
		}
	}

	envPath := ".env"
	if sys.GOOS == "linux" {
		envPath = "/etc/supermetry/supermetry.env"
	}
	if err := checkInstallPort(opts.Listen, envPath, sys); err != nil {
		return err
	}
	if sys.GOOS == "linux" {
		if _, err := sys.LookPath("systemctl"); err != nil {
			return errors.New("systemd is required for a Linux binary install; use --mode docker or install on a systemd host")
		}
		for _, client := range []string{"pg_dump", "pg_restore"} {
			if _, err := sys.LookPath(client); err != nil {
				return fmt.Errorf("%s is required for verified pre-update backups; install postgresql-client and rerun the installer", client)
			}
		}
	}
	secret := ReadExistingSecret(envPath)
	if secret == "" {
		var err error
		secret, err = NewSecretKey()
		if err != nil {
			return err
		}
	}
	env := RenderEnv(EnvValues{
		DatabaseURL:    opts.DB.ConnString(),
		BaseURL:        opts.BaseURL,
		Listen:         opts.Listen,
		TrustedProxies: trustedProxies(opts.Proxy),
		SecretKey:      secret,
	})
	files := []plannedFile{{Path: envPath, Data: env, Mode: 0o600}}
	if sys.GOOS == "linux" {
		files[0].Mode = 0o640
		proxyFile, err := plannedProxy(opts, sys)
		if err != nil {
			return err
		}
		if proxyFile.Path != "" {
			files = append(files, proxyFile)
		}
	}

	if sys.GOOS == "linux" && !sys.IsRoot {
		return handOffPrivileged(ctx, p, sys, privilegedPlan{Files: files, InstallBinary: true, Version: opts.Version, Proxy: opts.Proxy, Domain: proxyDomain(opts.BaseURL), Listen: opts.Listen})
	}
	if err := confirmFiles(p, opts.Yes, files, sys.Stat); err != nil {
		return err
	}
	baseFiles, proxyFile := splitProxyFile(files, opts.Proxy)
	if err := writePlannedFiles(baseFiles, sys.WriteFile); err != nil {
		return err
	}
	if sys.GOOS == "linux" {
		if err := installService(ctx, sys.Runner, sys.WriteFile, opts.Version, opts.Listen); err != nil {
			return err
		}
		if err := sys.Ready(ctx, localBaseURL(opts.Listen), 60*time.Second); err != nil {
			logs, _ := sys.Runner.Run(ctx, "journalctl", "-u", "supermetry", "-n", "50", "--no-pager")
			if len(logs) != 0 {
				fmt.Fprintln(p.out, string(logs))
			}
			return err
		}
		if err := applyProxyFile(ctx, opts.Proxy, proxyFile, sys); err != nil {
			return err
		}
		if err := configureProxy(ctx, opts.Proxy, proxyDomain(opts.BaseURL), opts.Yes, p, sys.LookPath, sys.Runner); err != nil {
			return err
		}
		status, _ := sys.Runner.Run(ctx, "systemctl", "status", "supermetry", "--no-pager")
		if len(status) != 0 {
			fmt.Fprintln(p.out, string(status))
		}
	}
	printFinish(p.out, opts.BaseURL, adminExists)
	if sys.GOOS == "linux" {
		fmt.Fprintln(p.out, "Manage the service:")
		fmt.Fprintln(p.out, "  sudo systemctl start supermetry")
		fmt.Fprintln(p.out, "  sudo systemctl stop supermetry")
		fmt.Fprintln(p.out, "  sudo systemctl restart supermetry")
		fmt.Fprintln(p.out, "  sudo systemctl enable supermetry")
		fmt.Fprintln(p.out, "  sudo systemctl disable supermetry")
		fmt.Fprintln(p.out, "  journalctl -u supermetry -f")
	} else {
		fmt.Fprintf(p.out, "Start Supermetry with: supermetry (configuration: %s)\n", envPath)
	}
	return nil
}

func runDocker(ctx context.Context, opts Options, p *Prompter, sys System) error {
	if opts.Dir == "" {
		defaultDir := "./supermetry"
		if sys.GOOS == "linux" {
			defaultDir = "/opt/supermetry"
		}
		if opts.Yes {
			opts.Dir = defaultDir
		} else {
			var err error
			opts.Dir, err = p.Ask("Install directory", defaultDir, required("install directory"))
			if err != nil {
				return err
			}
		}
	}
	if opts.Image == "" {
		if opts.Yes {
			opts.Image = "download"
		} else {
			var err error
			opts.Image, err = p.Choose("Image source", []string{"download", "build"}, "download")
			if err != nil {
				return err
			}
		}
	}
	if opts.Image != "download" && opts.Image != "build" {
		return errors.New("--image must be download or build")
	}
	if err := checkInstallPort(opts.Listen, filepath.Join(opts.Dir, "docker-compose.yml"), sys); err != nil {
		return err
	}
	secret := ReadExistingSecret(filepath.Join(opts.Dir, ".env"))
	if secret == "" {
		var err error
		secret, err = NewSecretKey()
		if err != nil {
			return err
		}
	}
	postgresPassword, err := NewSecretKey()
	if err != nil {
		return err
	}
	if existing := readExistingValue(filepath.Join(opts.Dir, ".env"), "POSTGRES_PASSWORD"); existing != "" {
		postgresPassword = existing
	}
	_, port, ok := strings.Cut(opts.Listen, ":")
	if !ok {
		return errors.New("--listen must be HOST:PORT")
	}
	host := strings.TrimSuffix(opts.Listen, ":"+port)
	managedUpdates := sys.GOOS == "linux" && opts.Image == "download" && filepath.Clean(opts.Dir) == "/opt/supermetry"
	if managedUpdates {
		if _, err := sys.LookPath("pg_restore"); err != nil {
			return errors.New("pg_restore is required on the host for independently verified pre-update backups; install postgresql-client and rerun the installer")
		}
	}
	files := []plannedFile{
		{Path: filepath.Join(opts.Dir, ".env"), Data: RenderEnv(EnvValues{BaseURL: opts.BaseURL, Listen: ":8787", TrustedProxies: trustedProxies(opts.Proxy), SecretKey: secret, Docker: true, PostgresPassword: postgresPassword}), Mode: 0o600},
		{Path: filepath.Join(opts.Dir, "docker-compose.yml"), Data: RenderCompose(ComposeValues{Version: opts.Version, Image: opts.Image, ListenHost: host, Port: port, Updater: managedUpdates, UpdaterGID: updaterGroupIDMarker}), Mode: 0o644},
	}
	if sys.GOOS == "linux" {
		proxyFile, err := plannedProxy(opts, sys)
		if err != nil {
			return err
		}
		if proxyFile.Path != "" {
			files = append(files, proxyFile)
		}
	}
	if err := confirmFiles(p, opts.Yes, files, sys.Stat); err != nil {
		return err
	}
	baseFiles, proxyFile := splitProxyFile(files, opts.Proxy)
	if managedUpdates {
		if !sys.IsRoot {
			if err := handOffPrivileged(ctx, p, sys, privilegedPlan{Files: baseFiles, InstallUpdater: true, Version: opts.Version, Listen: opts.Listen}); err != nil {
				return err
			}
		} else {
			gid, err := installDockerUpdater(ctx, sys.Runner, sys.WriteFile, opts.Listen)
			if err != nil {
				return err
			}
			if err := writePlannedFiles(replaceUpdaterGroupID(baseFiles, gid), sys.WriteFile); err != nil {
				return err
			}
		}
	} else if err := writePlannedFiles(baseFiles, sys.WriteFile); err != nil {
		return err
	}
	if sys.Runner == nil {
		return errors.New("Docker installation needs a command runner")
	}
	composeArgs := []string{"compose", "--project-directory", opts.Dir}
	if opts.Image == "download" {
		if _, err := sys.Runner.Run(ctx, "docker", append(composeArgs, "pull")...); err != nil {
			return err
		}
	} else {
		if err := sys.PrepareSource(ctx, opts); err != nil {
			return err
		}
		if _, err := sys.Runner.Run(ctx, "docker", append(composeArgs, "build")...); err != nil {
			return err
		}
	}
	if _, err := sys.Runner.Run(ctx, "docker", append(composeArgs, "up", "-d")...); err != nil {
		return err
	}
	if err := sys.Ready(ctx, localBaseURL(opts.Listen), 60*time.Second); err != nil {
		logs, _ := sys.Runner.Run(ctx, "docker", append(composeArgs, "logs", "--tail", "50", "supermetry")...)
		if len(logs) != 0 {
			fmt.Fprintln(p.out, string(logs))
		}
		return err
	}
	adminExists := false
	if sys.SetupRequired != nil {
		setupRequired, err := sys.SetupRequired(ctx, localBaseURL(opts.Listen))
		if err != nil {
			return fmt.Errorf("check setup status: %w", err)
		}
		adminExists = !setupRequired
	}
	if err := applyProxyFile(ctx, opts.Proxy, proxyFile, sys); err != nil {
		return err
	}
	if err := configureProxy(ctx, opts.Proxy, proxyDomain(opts.BaseURL), opts.Yes, p, sys.LookPath, sys.Runner); err != nil {
		return err
	}
	printFinish(p.out, opts.BaseURL, adminExists)
	fmt.Fprintf(p.out, "Manage the service with: docker compose --project-directory %s start|stop|restart\n", opts.Dir)
	return nil
}

func askDB(p *Prompter, current DBConfig) (DBConfig, error) {
	var err error
	current.URL, err = p.Ask("Database URL (leave blank for separate settings)", current.URL, nil)
	if err != nil || current.URL != "" {
		return current, err
	}
	current.Host, err = p.Ask("Database host", defaultString(current.Host, "localhost"), required("database host"))
	if err != nil {
		return current, err
	}
	current.Port, err = p.Ask("Database port", defaultString(current.Port, "5432"), required("database port"))
	if err != nil {
		return current, err
	}
	current.User, err = p.Ask("Database user", defaultString(current.User, "supermetry"), required("database user"))
	if err != nil {
		return current, err
	}
	current.Password, err = p.AskSecret("Database password", current.Password)
	if err != nil {
		return current, err
	}
	current.Name, err = p.Ask("Database name", defaultString(current.Name, "supermetry"), required("database name"))
	if err != nil {
		return current, err
	}
	sslDefault := current.SSLMode
	if sslDefault == "" {
		sslDefault = "require"
		if current.Host == "localhost" || current.Host == "127.0.0.1" || current.Host == "::1" {
			sslDefault = "disable"
		}
	}
	current.SSLMode, err = p.Choose("Database SSL mode", []string{"disable", "require", "verify-ca", "verify-full"}, sslDefault)
	return current, err
}

func validateUnattended(opts Options) error {
	if opts.Mode == "" {
		return errors.New("--mode is required with --yes or --dry-run")
	}
	if opts.BaseURL == "" {
		return errors.New("--base-url is required with --yes or --dry-run")
	}
	if opts.Mode == "binary" && opts.DB.URL == "" && opts.DB.Host == "" {
		return errors.New("--database-url or --db-host settings are required with --yes or --dry-run")
	}
	if opts.Mode == "binary" && opts.DB.URL == "" && (opts.DB.Port == "" || opts.DB.User == "" || opts.DB.Password == "" || opts.DB.Name == "") {
		return errors.New("--db-port, --db-user, --db-password and --db-name are required with --yes or --dry-run")
	}
	return nil
}

func validateHTTPURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("public URL must be an http:// or https:// URL")
	}
	return nil
}

func validateListen(value string) error {
	host, port, ok := strings.Cut(value, ":")
	if !ok || host == "" || port == "" || strings.Contains(port, ":") {
		return errors.New("listen address must be HOST:PORT")
	}
	return nil
}

func localBaseURL(listen string) string {
	host, port, ok := strings.Cut(listen, ":")
	if !ok {
		return "http://" + listen
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + port
}

func required(label string) func(string) error {
	return func(value string) error {
		if value == "" {
			return fmt.Errorf("%s is required", label)
		}
		return nil
	}
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func trustedProxies(proxy string) string {
	if proxy == "nginx" || proxy == "caddy" {
		return "127.0.0.1"
	}
	return ""
}

func plannedProxy(opts Options, sys System) (plannedFile, error) {
	if opts.Proxy == "none" || opts.Proxy == "" {
		return plannedFile{}, nil
	}
	u, err := url.Parse(opts.BaseURL)
	if err != nil {
		return plannedFile{}, err
	}
	switch opts.Proxy {
	case "nginx":
		return plannedFile{Path: "/etc/nginx/conf.d/supermetry.conf", Data: RenderNginx(u.Hostname(), opts.Listen), Mode: 0o644}, nil
	case "caddy":
		block := RenderCaddy(u.Hostname(), opts.Listen)
		existing, readErr := sys.ReadFile("/etc/caddy/Caddyfile")
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return plannedFile{}, fmt.Errorf("read /etc/caddy/Caddyfile: %w", readErr)
		}
		merged, err := mergeCaddyfile(existing, block, u.Hostname())
		if err != nil {
			return plannedFile{}, err
		}
		return plannedFile{Path: "/etc/caddy/Caddyfile", Data: merged, Mode: 0o644}, nil
	default:
		return plannedFile{}, errors.New("--proxy must be nginx, caddy or none")
	}
}

func mergeCaddyfile(existing, block []byte, domain string) ([]byte, error) {
	if len(existing) == 0 {
		return block, nil
	}
	const begin = "# BEGIN SUPERMETRY"
	const end = "# END SUPERMETRY"
	text := string(existing)
	if start := strings.Index(text, begin); start >= 0 {
		finish := strings.Index(text[start:], end)
		if finish < 0 {
			return nil, errors.New("Caddyfile has an incomplete Supermetry block; restore the backup or fix the markers before rerunning")
		}
		finish = start + finish + len(end)
		return []byte(text[:start] + strings.TrimSpace(string(block)) + text[finish:]), nil
	}
	legacy := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(domain) + ` \{\n[ \t]+reverse_proxy [^\n]+\n\}\n?`)
	if legacy.Match(existing) {
		return legacy.ReplaceAll(existing, bytes.TrimSpace(block)), nil
	}
	if strings.Contains(text, domain) {
		return nil, fmt.Errorf("Caddyfile already contains %s in an unmanaged block; edit it manually before rerunning", domain)
	}
	data := append(bytes.TrimRight(existing, "\n"), '\n', '\n')
	return append(data, block...), nil
}

func confirmFiles(p *Prompter, yes bool, files []plannedFile, stat func(string) (os.FileInfo, error)) error {
	if yes {
		return nil
	}
	for _, file := range files {
		if _, err := stat(file.Path); err == nil {
			confirmed, err := p.Confirm("Replace "+file.Path, false)
			if err != nil {
				return err
			}
			if !confirmed {
				return errInstallDeclined
			}
		}
	}
	return nil
}

func handOffPrivileged(ctx context.Context, p *Prompter, sys System, plan privilegedPlan) error {
	fmt.Fprintln(p.out, "Supermetry needs sudo to apply this plan:")
	for _, file := range plan.Files {
		fmt.Fprintln(p.out, "  write", file.Path)
	}
	if plan.InstallBinary {
		fmt.Fprintf(p.out, "  install /opt/supermetry/versions/%s/supermetry\n", plan.Version)
		fmt.Fprintln(p.out, "  write", "/etc/systemd/system/supermetry.service")
	}
	if plan.InstallUpdater {
		fmt.Fprintln(p.out, "  install", "/opt/supermetry/updater/supermetry")
		fmt.Fprintln(p.out, "  write", deploymentDescriptorPath)
		fmt.Fprintln(p.out, "  write", "/etc/systemd/system/supermetry-updater.socket")
		fmt.Fprintln(p.out, "  write", "/etc/systemd/system/supermetry-updater.service")
	}
	confirmed, err := p.Confirm("Apply this plan with sudo", false)
	if err != nil {
		return err
	}
	if !confirmed {
		return errInstallDeclined
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("supermetry-install-%d.json", os.Getpid()))
	if err := sys.WriteFile(path, payload, 0o600); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := sys.LookPath("sudo"); err != nil {
		fmt.Fprintf(p.out, "Run: sudo %s install --apply %s\n", executable, path)
		return errInstallDeclined
	}
	defer os.Remove(path)
	if sys.Runner == nil {
		return errors.New("sudo handoff needs a command runner")
	}
	_, err = sys.Runner.Run(ctx, "sudo", executable, "install", "--apply", path)
	return err
}

func applySavedPlan(ctx context.Context, path string, sys System) error {
	if !sys.IsRoot {
		return errors.New("--apply requires root")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var plan privilegedPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return err
	}
	baseFiles, proxyFile := splitProxyFile(plan.Files, plan.Proxy)
	if plan.InstallUpdater {
		gid, err := installDockerUpdater(ctx, sys.Runner, sys.WriteFile, plan.Listen)
		if err != nil {
			return err
		}
		baseFiles = replaceUpdaterGroupID(baseFiles, gid)
	}
	if err := writePlannedFiles(baseFiles, sys.WriteFile); err != nil {
		return err
	}
	if plan.InstallBinary {
		if err := installService(ctx, sys.Runner, sys.WriteFile, plan.Version, plan.Listen); err != nil {
			return err
		}
		if err := sys.Ready(ctx, localBaseURL(plan.Listen), 60*time.Second); err != nil {
			return err
		}
	}
	if err := applyProxyFile(ctx, plan.Proxy, proxyFile, sys); err != nil {
		return err
	}
	return configureProxy(ctx, plan.Proxy, plan.Domain, true, NewPrompter(strings.NewReader(""), io.Discard), sys.LookPath, sys.Runner)
}

func splitProxyFile(files []plannedFile, proxy string) ([]plannedFile, *plannedFile) {
	proxyPath := ""
	switch proxy {
	case "nginx":
		proxyPath = "/etc/nginx/conf.d/supermetry.conf"
	case "caddy":
		proxyPath = "/etc/caddy/Caddyfile"
	}
	base := make([]plannedFile, 0, len(files))
	var selected *plannedFile
	for _, file := range files {
		if file.Path == proxyPath {
			copy := file
			selected = &copy
			continue
		}
		base = append(base, file)
	}
	return base, selected
}

func writePlannedFiles(files []plannedFile, writeFile func(string, []byte, os.FileMode) error) error {
	for _, file := range files {
		if err := writeFile(file.Path, file.Data, file.Mode); err != nil {
			return err
		}
	}
	return nil
}

func replaceUpdaterGroupID(files []plannedFile, gid string) []plannedFile {
	replaced := make([]plannedFile, len(files))
	copy(replaced, files)
	for i := range replaced {
		replaced[i].Data = bytes.ReplaceAll(replaced[i].Data, []byte(updaterGroupIDMarker), []byte(gid))
	}
	return replaced
}

func applyProxyFile(ctx context.Context, proxy string, file *plannedFile, sys System) error {
	if file == nil {
		return nil
	}
	if proxy == "caddy" {
		if err := backupCaddyIfPresent(ctx, sys); err != nil {
			return err
		}
	}
	return sys.WriteFile(file.Path, file.Data, file.Mode)
}

func configureProxy(ctx context.Context, proxy, domain string, unattended bool, p *Prompter, lookPath func(string) (string, error), runner Runner) error {
	if proxy == "none" || proxy == "" {
		return nil
	}
	if runner == nil {
		return errors.New("proxy setup needs a command runner")
	}
	switch proxy {
	case "nginx":
		if output, err := runner.Run(ctx, "nginx", "-t"); err != nil {
			return fmt.Errorf("nginx configuration test failed; fix /etc/nginx/conf.d/supermetry.conf and rerun: %s: %w", strings.TrimSpace(string(output)), err)
		}
		if _, err := runner.Run(ctx, "systemctl", "reload", "nginx"); err != nil {
			return fmt.Errorf("reload nginx: %w", err)
		}
		if _, err := lookPath("certbot"); err != nil {
			fmt.Fprintf(p.out, "To enable HTTPS later, run: sudo certbot --nginx -d %s\n", domain)
			return nil
		}
		if unattended {
			return nil
		}
		confirmed, err := p.Confirm("Configure HTTPS with certbot", false)
		if err != nil || !confirmed {
			return err
		}
		if output, err := runner.Run(ctx, "certbot", "--nginx", "-d", domain); err != nil {
			return fmt.Errorf("certbot failed: %s: %w", strings.TrimSpace(string(output)), err)
		}
		return nil
	case "caddy":
		if output, err := runner.Run(ctx, "caddy", "validate", "--config", "/etc/caddy/Caddyfile"); err != nil {
			return fmt.Errorf("Caddy configuration validation failed; restore /etc/caddy/Caddyfile.supermetry.bak and rerun: %s: %w", strings.TrimSpace(string(output)), err)
		}
		if _, err := runner.Run(ctx, "systemctl", "reload", "caddy"); err != nil {
			return fmt.Errorf("reload Caddy: %w", err)
		}
		return nil
	default:
		return errors.New("--proxy must be nginx, caddy or none")
	}
}

func withSystemDefaults(sys System) System {
	if sys.Runner == nil {
		sys.Runner = commandRunner{}
	}
	if sys.WriteFile == nil {
		sys.WriteFile = writeFile
	}
	if sys.GOOS == "" {
		sys.GOOS = runtime.GOOS
	}
	if sys.LookPath == nil {
		sys.LookPath = exec.LookPath
	}
	if sys.Check == nil {
		sys.Check = CheckPostgres
	}
	if sys.Ready == nil {
		sys.Ready = WaitReady
	}
	if sys.ReadFile == nil {
		sys.ReadFile = os.ReadFile
	}
	if sys.Stat == nil {
		sys.Stat = os.Stat
	}
	if sys.LocalIP == nil {
		sys.LocalIP = detectLocalIP
	}
	if sys.PrepareSource == nil {
		sys.PrepareSource = prepareDockerBuildSource
	}
	return sys
}

func checkPortAvailable(listen string) error {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	return listener.Close()
}

func checkInstallPort(listen, markerPath string, sys System) error {
	if sys.CheckPort == nil {
		return nil
	}
	if _, err := sys.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := sys.CheckPort(listen); err != nil {
		_, port, _ := strings.Cut(listen, ":")
		return fmt.Errorf("port %s is already in use; choose another value with --listen: %w", port, err)
	}
	return nil
}

func postgresMajor(version int) string { return fmt.Sprintf("%d", version/10000) }

func proxyDomain(baseURL string) string {
	u, _ := url.Parse(baseURL)
	return u.Hostname()
}

func printFinish(out io.Writer, baseURL string, adminExists bool) {
	appURL := strings.TrimRight(baseURL, "/") + "/app"
	if adminExists {
		fmt.Fprintf(out, "Open %s and sign in with your existing admin account\n", appURL)
		return
	}
	fmt.Fprintf(out, "Open %s to create your admin account\n", appURL)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".supermetry-install-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
