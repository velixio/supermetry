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
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: panel {serve|migrate|close-days|version}")
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
	case "close-days":
		if err := closeDays(context.Background(), os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: panel {serve|migrate|close-days|version}")
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
