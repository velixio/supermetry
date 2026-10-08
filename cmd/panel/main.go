package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"panel/internal/config"
	"panel/internal/db"
)

var version = "dev"

func main() {
	// Read ./.env (or PANEL_ENV_FILE); variables already in the environment win.
	envFile := os.Getenv("PANEL_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := config.LoadDotEnv(envFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: panel {serve|migrate|load|close-days|geoip|backup|restore|recompute-rollups|version}")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version":
		fmt.Printf("panel %s\n", version)
	case "migrate":
		if err := migrate(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "serve":
		cfg, err := config.Load(os.Getenv)
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
		fmt.Fprintln(os.Stderr, "usage: panel {serve|migrate|load|close-days|geoip|backup|restore|recompute-rollups|version}")
		os.Exit(2)
	}
}

func migrate(ctx context.Context) error {
	cfg, err := config.Load(os.Getenv)
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
