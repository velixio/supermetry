package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"supermetry/internal/config"
	"supermetry/internal/db"
)

var version = "dev"

const usage = "usage: supermetry [serve|migrate|load|close-days|geoip|backup|restore|recompute-rollups|migrate-labels|user|version]\nWith no command, supermetry serves."

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
	// Read ./.env (or SUPERMETRY_ENV_FILE); variables already in the environment win.
	envFile := os.Getenv("SUPERMETRY_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := config.LoadDotEnv(envFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	command := commandName(os.Args)
	if command == "help" {
		fmt.Println(usage)
		return
	}

	switch command {
	case "version":
		fmt.Printf("supermetry %s\n", version)
	case "migrate":
		if err := migrate(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "serve":
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
