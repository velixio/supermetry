package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Options mirrors the interactive installer questions.
type Options struct {
	Mode    string
	BaseURL string
	DB      DBConfig
	Listen  string
	Proxy   string
	Image   string
	Dir     string
	Yes     bool
	Version string
	Apply   string
}

// System contains installer side effects so flow tests can remain isolated.
type System struct {
	Runner    Runner
	WriteFile func(path string, data []byte, mode os.FileMode) error
	GOOS      string
	IsRoot    bool
	LookPath  func(file string) (string, error)
	Check     func(ctx context.Context, conn string) (int, error)
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
	Files         []plannedFile `json:"files"`
	InstallBinary bool          `json:"install_binary"`
	Proxy         string        `json:"proxy"`
}

var errInstallDeclined = errors.New("installation declined")

// DefaultSystem returns the real operating-system integrations used by the
// command-line installer.
func DefaultSystem() System {
	sys := withSystemDefaults(System{})
	if runtime.GOOS == "linux" {
		output, err := exec.Command("id", "-u").Output()
		sys.IsRoot = err == nil && strings.TrimSpace(string(output)) == "0"
	}
	return sys
}

// Run performs an interactive or unattended installation.
func Run(ctx context.Context, opts Options, p *Prompter, sys System) error {
	sys = withSystemDefaults(sys)
	if opts.Apply != "" {
		return applySavedPlan(ctx, opts.Apply, sys)
	}
	if opts.Yes {
		if err := validateUnattended(opts); err != nil {
			return err
		}
	}

	var err error
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
	if opts.BaseURL == "" {
		opts.BaseURL, err = p.Ask("Public URL", "", validateHTTPURL)
		if err != nil {
			return err
		}
	} else if err := validateHTTPURL(opts.BaseURL); err != nil {
		return err
	}
	if opts.Listen == "" {
		if opts.Yes {
			opts.Listen = "127.0.0.1:8787"
		} else {
			opts.Listen, err = p.Ask("Listen address", "127.0.0.1:8787", validateListen)
			if err != nil {
				return err
			}
		}
	}
	if err := validateListen(opts.Listen); err != nil {
		return err
	}
	if opts.Proxy == "" {
		if opts.Yes || sys.GOOS != "linux" {
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
	if sys.GOOS == "linux" && opts.Proxy != "none" {
		if _, err := sys.LookPath(opts.Proxy); err != nil {
			fmt.Fprintf(p.out, "%s is not installed. Install it, then rerun this installer (for example: sudo apt install %s).\n", opts.Proxy, opts.Proxy)
			opts.Proxy = "none"
		}
	}
	if opts.Version == "" {
		opts.Version = "latest"
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
			if version < 180000 {
				fmt.Fprintf(p.out, "Warning: PostgreSQL %d is older than the supported PostgreSQL 18 baseline.\n", version)
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
		if proxyFile := plannedProxy(opts); proxyFile.Path != "" {
			files = append(files, proxyFile)
		}
	}

	if sys.GOOS == "linux" && !sys.IsRoot {
		return handOffPrivileged(ctx, p, sys, privilegedPlan{Files: files, InstallBinary: true, Proxy: opts.Proxy})
	}
	if err := confirmFiles(p, opts.Yes, files); err != nil {
		return err
	}
	for _, file := range files {
		if err := sys.WriteFile(file.Path, file.Data, file.Mode); err != nil {
			return err
		}
	}
	if sys.GOOS == "linux" {
		if err := installService(ctx, sys.Runner, sys.WriteFile); err != nil {
			return err
		}
		if err := configureProxy(ctx, opts.Proxy, sys.Runner); err != nil {
			return err
		}
		if err := WaitReady(ctx, localBaseURL(opts.Listen), 60*time.Second); err != nil {
			logs, _ := sys.Runner.Run(ctx, "journalctl", "-u", "supermetry", "-n", "50", "--no-pager")
			if len(logs) != 0 {
				fmt.Fprintln(p.out, string(logs))
			}
			return err
		}
		status, _ := sys.Runner.Run(ctx, "systemctl", "status", "supermetry", "--no-pager")
		if len(status) != 0 {
			fmt.Fprintln(p.out, string(status))
		}
	}
	fmt.Fprintf(p.out, "Open %s/app to create your admin account\n", strings.TrimRight(opts.BaseURL, "/"))
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
	files := []plannedFile{
		{Path: filepath.Join(opts.Dir, ".env"), Data: RenderEnv(EnvValues{BaseURL: opts.BaseURL, Listen: ":8787", TrustedProxies: trustedProxies(opts.Proxy), SecretKey: secret, Docker: true, PostgresPassword: postgresPassword}), Mode: 0o600},
		{Path: filepath.Join(opts.Dir, "docker-compose.yml"), Data: RenderCompose(ComposeValues{Version: opts.Version, Image: opts.Image, ListenHost: host, Port: port}), Mode: 0o644},
	}
	if sys.GOOS == "linux" {
		if proxyFile := plannedProxy(opts); proxyFile.Path != "" {
			files = append(files, proxyFile)
		}
	}
	if err := confirmFiles(p, opts.Yes, files); err != nil {
		return err
	}
	for _, file := range files {
		if err := sys.WriteFile(file.Path, file.Data, file.Mode); err != nil {
			return err
		}
	}
	if sys.Runner == nil {
		return errors.New("Docker installation needs a command runner")
	}
	composeArgs := []string{"compose", "--project-directory", opts.Dir}
	if opts.Image == "download" {
		if _, err := sys.Runner.Run(ctx, "docker", append(composeArgs, "pull")...); err != nil {
			return err
		}
	} else if _, err := sys.Runner.Run(ctx, "docker", append(composeArgs, "build")...); err != nil {
		return err
	}
	if _, err := sys.Runner.Run(ctx, "docker", append(composeArgs, "up", "-d")...); err != nil {
		return err
	}
	if err := configureProxy(ctx, opts.Proxy, sys.Runner); err != nil {
		return err
	}
	if err := WaitReady(ctx, localBaseURL(opts.Listen), 60*time.Second); err != nil {
		logs, _ := sys.Runner.Run(ctx, "docker", append(composeArgs, "logs", "--tail", "50", "supermetry")...)
		if len(logs) != 0 {
			fmt.Fprintln(p.out, string(logs))
		}
		return err
	}
	fmt.Fprintf(p.out, "Open %s/app to create your admin account\n", strings.TrimRight(opts.BaseURL, "/"))
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
		return errors.New("--mode is required with --yes")
	}
	if opts.BaseURL == "" {
		return errors.New("--base-url is required with --yes")
	}
	if opts.Mode == "binary" && opts.DB.URL == "" && opts.DB.Host == "" {
		return errors.New("--database-url or --db-host settings are required with --yes")
	}
	if opts.Mode == "binary" && opts.DB.URL == "" && (opts.DB.Port == "" || opts.DB.User == "" || opts.DB.Password == "" || opts.DB.Name == "") {
		return errors.New("--db-port, --db-user, --db-password and --db-name are required with --yes")
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

func plannedProxy(opts Options) plannedFile {
	if opts.Proxy == "none" || opts.Proxy == "" {
		return plannedFile{}
	}
	u, err := url.Parse(opts.BaseURL)
	if err != nil {
		return plannedFile{}
	}
	switch opts.Proxy {
	case "nginx":
		return plannedFile{Path: "/etc/nginx/conf.d/supermetry.conf", Data: RenderNginx(u.Hostname(), opts.Listen), Mode: 0o644}
	case "caddy":
		return plannedFile{Path: "/etc/caddy/Caddyfile", Data: RenderCaddy(u.Hostname(), opts.Listen), Mode: 0o644}
	default:
		return plannedFile{}
	}
}

func confirmFiles(p *Prompter, yes bool, files []plannedFile) error {
	if yes {
		return nil
	}
	for _, file := range files {
		if _, err := os.Stat(file.Path); err == nil {
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
		fmt.Fprintln(p.out, "  install", "/usr/local/bin/supermetry")
		fmt.Fprintln(p.out, "  write", "/etc/systemd/system/supermetry.service")
	}
	confirmed, err := p.Confirm("Apply this plan with sudo", false)
	if err != nil || !confirmed {
		return err
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
		return nil
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
	for _, file := range plan.Files {
		if err := sys.WriteFile(file.Path, file.Data, file.Mode); err != nil {
			return err
		}
	}
	if plan.InstallBinary {
		if err := installService(ctx, sys.Runner, sys.WriteFile); err != nil {
			return err
		}
	}
	return configureProxy(ctx, plan.Proxy, sys.Runner)
}

func configureProxy(ctx context.Context, proxy string, runner Runner) error {
	if proxy == "none" || proxy == "" {
		return nil
	}
	if runner == nil {
		return errors.New("proxy setup needs a command runner")
	}
	switch proxy {
	case "nginx":
		if _, err := runner.Run(ctx, "nginx", "-t"); err != nil {
			return err
		}
		_, err := runner.Run(ctx, "systemctl", "reload", "nginx")
		return err
	case "caddy":
		if _, err := runner.Run(ctx, "caddy", "validate", "--config", "/etc/caddy/Caddyfile"); err != nil {
			return err
		}
		_, err := runner.Run(ctx, "systemctl", "reload", "caddy")
		return err
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
	return sys
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
