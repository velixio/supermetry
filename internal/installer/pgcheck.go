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
		return &CheckError{Reason: fmt.Sprintf("DNS name not found: %s", dnsErr.Name), Field: "host", Err: err}
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
