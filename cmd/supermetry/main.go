package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/installer"
)

var version = "dev"

const usage = "usage: supermetry [install|serve|migrate|load|close-days|geoip|backup|restore|recompute-rollups|migrate-labels|user|version]\nWith no command, supermetry serves."

// commandName returns the subcommand to run. With no command, supermetry serves.
func commandName(args []string) string {
	if len(args) < 2 {
		return "serve"
	}
	switch args[1] {
	case "-h", "--help", "help":
		return "help"
	}
	return args[1]
}

func main() {
	command := commandName(os.Args)
	if command == "updater-helper" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := updaterHelperCommand(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Read ./.env (or SUPERMETRY_ENV_FILE); variables already in the environment win.
	envFile := os.Getenv("SUPERMETRY_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := config.LoadDotEnv(envFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if command == "help" {
		fmt.Println(usage)
		return
	}

	switch command {
	case "version":
		fmt.Printf("supermetry %s\n", version)
	case "install":
		if err := installCommand(context.Background(), os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "migrate":
		if err := migrate(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "serve":
		if _, err := os.Stat(envFile); errors.Is(err, os.ErrNotExist) && os.Getenv("SUPERMETRY_DATABASE_URL") == "" && os.Getenv("SUPERMETRY_DATABASE_HOST") == "" {
			fmt.Fprintln(os.Stderr, `Supermetry is not configured. Run "supermetry install", or set SUPERMETRY_DATABASE_URL, SUPERMETRY_SECRET_KEY and SUPERMETRY_BASE_URL.`)
			os.Exit(1)
		}
		cfg, err := config.LoadLookup(os.LookupEnv)
		if err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err = serve(ctx, cfg)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "load":
		if err := loadCommand(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "migrate-labels":
		if err := migrateLabelsCommand(context.Background(), os.Args[2:], os.Stdout, os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "recompute-rollups":
		if err := recomputeRollupsCommand(context.Background(), os.Args[2:], os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "close-days":
		if err := closeDays(context.Background(), os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "geoip":
		if err := geoIPCommand(context.Background(), os.Args[2:], os.Stdout, os.Getenv, nil); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "backup":
		if err := backupCommand(context.Background(), os.Args[2:], os.Stdout, os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "restore":
		if err := restoreCommand(context.Background(), os.Args[2:], os.Stdout, os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func installCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("supermetry install", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() {
		fmt.Fprintln(out, `Usage: supermetry install [flags]

Flags:
  --mode docker|binary        installation mode
  --base-url URL              public HTTP(S) URL
  --database-url URL          complete PostgreSQL connection URL
  --db-host HOST              PostgreSQL host
  --db-port PORT              PostgreSQL port (default 5432)
  --db-user USER              PostgreSQL user (default supermetry)
  --db-password PASSWORD      PostgreSQL password
  --db-name NAME              PostgreSQL database (default supermetry)
  --db-sslmode MODE           disable, require, verify-ca, or verify-full
  --listen HOST:PORT          listen address (default 127.0.0.1:8787)
  --proxy nginx|caddy|none    reverse proxy (default none)
  --image download|build      Docker image source (default download)
  --dir PATH                  installation directory
  --yes                       run without interactive confirmation
  --dry-run                   print the complete plan without changing anything
  --version VERSION           release version
  --apply PATH                apply a prepared privileged plan (internal)

Examples:
  supermetry install --mode binary --base-url https://analytics.example.com
  supermetry install --mode docker --base-url https://analytics.example.com --dir /opt/supermetry --yes
  supermetry install --dry-run --mode docker --base-url https://analytics.example.com`)
	}
	var opts installer.Options
	opts.DotEnv = envFileName()
	flags.StringVar(&opts.Mode, "mode", "", "installation mode: docker or binary")
	flags.StringVar(&opts.BaseURL, "base-url", "", "public http(s) URL")
	flags.StringVar(&opts.DB.URL, "database-url", "", "complete PostgreSQL connection URL")
	flags.StringVar(&opts.DB.Host, "db-host", "", "PostgreSQL host")
	flags.StringVar(&opts.DB.Port, "db-port", "5432", "PostgreSQL port")
	flags.StringVar(&opts.DB.User, "db-user", "supermetry", "PostgreSQL user")
	flags.StringVar(&opts.DB.Password, "db-password", "", "PostgreSQL password")
	flags.StringVar(&opts.DB.Name, "db-name", "supermetry", "PostgreSQL database")
	flags.StringVar(&opts.DB.SSLMode, "db-sslmode", "", "PostgreSQL SSL mode")
	flags.StringVar(&opts.Listen, "listen", "", "listen address (default 127.0.0.1:8787)")
	flags.StringVar(&opts.Proxy, "proxy", "", "reverse proxy: nginx, caddy or none (default none)")
	flags.StringVar(&opts.Image, "image", "", "Docker image source: download or build (default download)")
	flags.StringVar(&opts.Dir, "dir", "", "installation directory")
	flags.BoolVar(&opts.Yes, "yes", false, "run without interactive confirmation")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "print the complete plan without changing anything")
	flags.StringVar(&opts.Version, "version", version, "release version")
	flags.StringVar(&opts.Apply, "apply", "", "apply a prepared privileged installation plan")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return installer.Run(ctx, opts, installer.NewPrompter(in, out), installer.DefaultSystem())
}

func migrate(ctx context.Context) error {
	cfg, err := config.LoadLookup(os.LookupEnv)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool)
}

// envFileName is the dotenv file main loads at startup.
func envFileName() string {
	if name := os.Getenv("SUPERMETRY_ENV_FILE"); name != "" {
		return name
	}
	return ".env"
}
