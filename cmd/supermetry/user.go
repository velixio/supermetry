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

func userCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: supermetry user {create|reset-password|make-admin|remove-admin}")
	}
	switch args[0] {
	case "create":
		return createUser(ctx, args[1:], output)
	case "reset-password":
		return resetUserPassword(ctx, args[1:], output)
	case "make-admin":
		return setUserAdmin(ctx, args[1:], output, true)
	case "remove-admin":
		return setUserAdmin(ctx, args[1:], output, false)
	default:
		return errors.New("usage: supermetry user {create|reset-password|make-admin|remove-admin}")
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
	box, err := platformcrypto.NewSecretBox(cfg.SecretKey, "supermetry/totp/v1")
	if err != nil {
		pool.Close()
		return config.Config{}, nil, nil, func() {}, fmt.Errorf("initialize TOTP encryption: %w", err)
	}
	data := &store.Store{Pool: pool}
	authService := platformauth.New(data, mail.NewSMTP(cfg.SMTP), box, cfg.BaseURL, platformauth.WithAuditSecret(cfg.SecretKey))
	return cfg, data, authService, pool.Close, nil
}

// setUserAdmin grants or removes the instance-admin flag, which lifts the
// per-user organization limit and the per-organization site limit.
func setUserAdmin(ctx context.Context, args []string, output io.Writer, admin bool) error {
	name := "user make-admin"
	if !admin {
		name = "user remove-admin"
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	email := flags.String("email", "", "user email")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("%s requires --email", name)
	}
	_, data, _, closeServices, err := userServices(ctx)
	if err != nil {
		return err
	}
	defer closeServices()
	if !admin {
		var others int
		if err := data.Pool.QueryRow(ctx, `select count(*) from users where is_admin and not is_system and lower(email)<>$1`, users.NormalizeEmail(*email)).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return errors.New("refusing to remove the last instance admin")
		}
	}
	tag, err := data.Pool.Exec(ctx, `update users set is_admin=$2 where lower(email)=$1 and not is_system`, users.NormalizeEmail(*email), admin)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("no user with that email")
	}
	if admin {
		fmt.Fprintln(output, "user is now an instance admin")
	} else {
		fmt.Fprintln(output, "user is no longer an instance admin")
	}
	return nil
}
