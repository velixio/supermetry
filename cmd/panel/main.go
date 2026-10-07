package main

import (
	"context"
	"fmt"
	"os"

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
	case "serve", "close-days":
		fmt.Fprintln(os.Stderr, "not implemented")
		os.Exit(2)
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
