package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/config"
	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
)

func init() {
	if len(os.Args) < 2 || os.Args[1] != "user" {
		return
	}
	if err := userCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func userCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: supermetry user {create|reset-password}")
	}
	switch args[0] {
	case "create":
		return createUser(ctx, args[1:], output)
	case "reset-password":
		return resetUserPassword(ctx, args[1:], output)
	default:
		return errors.New("usage: supermetry user {create|reset-password}")
	}
}

func createUser(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("user create", flag.ContinueOnError)
	flags.SetOutput(output)
	email := flags.String("email", "", "user email")
	orgName := flags.String("org", "", "organization name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" || strings.TrimSpace(*orgName) == "" {
		return errors.New("user create requires --email and --org")
	}

	cfg, data, authService, closeServices, err := userServices(ctx)
	if err != nil {
		return err
	}
	defer closeServices()
	accounts := users.New(data)
	organizations := orgs.New(data)
	temporaryPassword, _ := platformcrypto.NewToken("")
	name := strings.TrimSpace(strings.SplitN(*email, "@", 2)[0])
	if name == "" {
		name = "Owner"
	}
	var userID int64
	err = data.Tx(ctx, func(tx pgx.Tx) error {
		var createErr error
		userID, createErr = accounts.CreateWithDB(ctx, tx, *email, name, temporaryPassword, time.Now().UTC())
		if createErr != nil {
			return createErr
		}
		_, createErr = organizations.Create(ctx, tx, strings.TrimSpace(*orgName), userID)
		return createErr
	})
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	link, err := authService.StartReset(ctx, *email)
	if err != nil {
		return fmt.Errorf("start password reset: %w", err)
	}
	if link != "" {
		fmt.Fprintln(output, link)
	} else if cfg.SMTP.Host != "" {
		fmt.Fprintln(output, "password reset email sent")
	}
	return nil
}

func resetUserPassword(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("user reset-password", flag.ContinueOnError)
	flags.SetOutput(output)
	email := flags.String("email", "", "user email")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return errors.New("user reset-password requires --email")
	}
	cfg, _, authService, closeServices, err := userServices(ctx)
	if err != nil {
		return err
	}
	defer closeServices()
	link, err := authService.StartReset(ctx, *email)
	if err != nil {
		return fmt.Errorf("start password reset: %w", err)
	}
	if link != "" {
		fmt.Fprintln(output, link)
	} else if cfg.SMTP.Host != "" {
		fmt.Fprintln(output, "password reset email sent")
	}
	return nil
}

func userServices(ctx context.Context) (config.Config, *store.Store, *platformauth.Auth, func(), error) {
	cfg, err := config.LoadLookup(os.LookupEnv)
	if err != nil {
		return config.Config{}, nil, nil, func() {}, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return config.Config{}, nil, nil, func() {}, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return config.Config{}, nil, nil, func() {}, err
	}
	box, err := platformcrypto.NewSecretBox(cfg.SecretKey, "panel/totp/v1")
	if err != nil {
		pool.Close()
		return config.Config{}, nil, nil, func() {}, fmt.Errorf("initialize TOTP encryption: %w", err)
	}
	data := &store.Store{Pool: pool}
	authService := platformauth.New(data, mail.NewSMTP(cfg.SMTP), box, cfg.BaseURL, platformauth.WithAuditSecret(cfg.SecretKey))
	return cfg, data, authService, pool.Close, nil
}
