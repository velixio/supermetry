package installer

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CheckError describes a PostgreSQL connection failure in terms an installer
// user can act on. Err is retained for errors.Is and errors.As, but Error does
// not include a connection URL or password.
type CheckError struct {
	Reason string
	Field  string
	Err    error
}

func (e *CheckError) Error() string { return e.Reason }

func (e *CheckError) Unwrap() error { return e.Err }

// CheckPostgres connects within five seconds and returns server_version_num.
func CheckPostgres(ctx context.Context, conn string) (int, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	db, err := pgx.Connect(checkCtx, conn)
	if err != nil {
		return 0, classifyCheckError(err)
	}
	defer db.Close(context.Background())

	var version int
	if err := db.QueryRow(checkCtx, "select current_setting('server_version_num')::int").Scan(&version); err != nil {
		return 0, classifyCheckError(err)
	}
	return version, nil
}

// CheckAdminExists reports whether an already-migrated installation has a user.
// A database without the users table is a fresh installation, not an error.
func CheckAdminExists(ctx context.Context, conn string) (bool, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := pgx.Connect(checkCtx, conn)
	if err != nil {
		return false, classifyCheckError(err)
	}
	defer db.Close(context.Background())
	var table *string
	if err := db.QueryRow(checkCtx, "select to_regclass('public.users')::text").Scan(&table); err != nil {
		return false, classifyCheckError(err)
	}
	if table == nil {
		return false, nil
	}
	// The admin API key's system user (is_system, migration 0230) is not a real
	// admin. to_jsonb keeps the check working on databases migrated before 0230.
	var exists bool
	if err := db.QueryRow(checkCtx, `select exists(select 1 from users u where u.is_admin and not coalesce((to_jsonb(u)->>'is_system')::boolean,false) limit 1)`).Scan(&exists); err != nil {
		return false, classifyCheckError(err)
	}
	return exists, nil
}

func classifyCheckError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01":
			return &CheckError{Reason: pgErr.Message, Field: "password", Err: err}
		case "3D000":
			return &CheckError{Reason: pgErr.Message, Field: "name", Err: err}
		case "28000":
			return &CheckError{Reason: pgErr.Message, Field: "user", Err: err}
		}
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &CheckError{Reason: fmt.Sprintf("database DNS name %q does not resolve; check --db-host or --database-url", dnsErr.Name), Field: "host", Err: err}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return &CheckError{Reason: "connection refused by PostgreSQL host", Field: "host", Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &CheckError{Reason: "timed out connecting to PostgreSQL host", Field: "host", Err: err}
	}
	var authorityErr x509.UnknownAuthorityError
	if errors.As(err, &authorityErr) {
		return &CheckError{Reason: "PostgreSQL TLS certificate could not be verified", Field: "sslmode", Err: err}
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "tls") || strings.Contains(lower, "ssl") {
		return &CheckError{Reason: "PostgreSQL TLS connection failed", Field: "sslmode", Err: err}
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return &CheckError{Reason: "could not connect to PostgreSQL host", Field: "host", Err: err}
	}
	return &CheckError{Reason: "could not connect to PostgreSQL", Field: "host", Err: err}
}
