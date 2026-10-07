package db

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RetentionClass is the number of days hot analytics data is retained.
// RetForever has no expiry and uses monthly rather than daily partitions.
type RetentionClass int16

const (
	RetForever RetentionClass = 0
	Ret7       RetentionClass = 7
	Ret30      RetentionClass = 30
	Ret90      RetentionClass = 90
	Ret365     RetentionClass = 365
)

var retentionClasses = []RetentionClass{Ret7, Ret30, Ret90, Ret365, RetForever}

// Partition describes a leaf partition below a retention-class partition.
type Partition struct {
	Name  string
	Class RetentionClass
	From  time.Time
	To    time.Time
}

// EnsurePartitions creates every hot-tier partition needed to cover [from, to).
func EnsurePartitions(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) error {
	if !from.Before(to) {
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin partition creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('panel:partitions'))`); err != nil {
		return fmt.Errorf("lock partition creation: %w", err)
	}
	for _, table := range []string{"events", "sessions"} {
		for _, class := range retentionClasses {
			if err := ensureTablePartitions(ctx, tx, table, class, from, to); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit partition creation: %w", err)
	}
	return nil
}

func ensureTablePartitions(ctx context.Context, tx pgx.Tx, table string, class RetentionClass, from, to time.Time) error {
	column := "ts"
	if table == "sessions" {
		column = "start"
	}
	parent := table + retentionSuffix(class)
	start := utcDay(from)
	step := func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	nameLayout := "20060102"
	if class == RetForever {
		start = time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
		step = func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }
		nameLayout = "200601"
	}

	for lower := start; lower.Before(to); lower = step(lower) {
		upper := step(lower)
		name := parent + "_" + lower.Format(nameLayout)
		sql := fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
			pgx.Identifier{name}.Sanitize(),
			pgx.Identifier{parent}.Sanitize(),
			lower.Format(time.RFC3339),
			upper.Format(time.RFC3339),
		)
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("create %s partition on %s for %s: %w", name, column, lower.Format(time.DateOnly), err)
		}
	}
	return nil
}

func retentionSuffix(class RetentionClass) string {
	if class == RetForever {
		return "_rf"
	}
	return "_r" + strconv.Itoa(int(class))
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

var rangeBoundPattern = regexp.MustCompile(`FROM \('([^']+)'\) TO \('([^']+)'\)`)

// ListPartitions returns the leaf retention partitions below table.
func ListPartitions(ctx context.Context, pool *pgxpool.Pool, table string) ([]Partition, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin listing %s partitions: %w", table, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// pg_get_expr renders timestamps using the session's TimeZone and
	// DateStyle, so pin both before parsing its catalog output.
	if _, err := tx.Exec(ctx, `set local time zone 'UTC'`); err != nil {
		return nil, fmt.Errorf("set partition listing time zone: %w", err)
	}
	if _, err := tx.Exec(ctx, `set local datestyle = 'ISO, YMD'`); err != nil {
		return nil, fmt.Errorf("set partition listing date style: %w", err)
	}

	rows, err := tx.Query(ctx, `
SELECT leaf.relname,
       pg_get_expr(class.relpartbound, class.oid),
       pg_get_expr(leaf.relpartbound, leaf.oid)
FROM pg_class AS root
JOIN pg_inherits AS class_inherit ON class_inherit.inhparent = root.oid
JOIN pg_class AS class ON class.oid = class_inherit.inhrelid
JOIN pg_inherits AS leaf_inherit ON leaf_inherit.inhparent = class.oid
JOIN pg_class AS leaf ON leaf.oid = leaf_inherit.inhrelid
WHERE root.oid = to_regclass($1)
ORDER BY leaf.relname`, table)
	if err != nil {
		return nil, fmt.Errorf("list %s partitions: %w", table, err)
	}
	defer rows.Close()

	partitions := make([]Partition, 0)
	for rows.Next() {
		var name, classBound, rangeBound string
		if err := rows.Scan(&name, &classBound, &rangeBound); err != nil {
			return nil, fmt.Errorf("scan %s partition: %w", table, err)
		}
		class, err := parseRetentionClass(classBound)
		if err != nil {
			return nil, fmt.Errorf("partition %s: %w", name, err)
		}
		from, to, err := parseRangeBound(rangeBound)
		if err != nil {
			return nil, fmt.Errorf("partition %s: %w", name, err)
		}
		partitions = append(partitions, Partition{Name: name, Class: class, From: from, To: to})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s partitions: %w", table, err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit listing %s partitions: %w", table, err)
	}
	return partitions, nil
}

func parseRetentionClass(bound string) (RetentionClass, error) {
	for _, class := range retentionClasses {
		if bound == fmt.Sprintf("FOR VALUES IN ('%d')", class) || bound == fmt.Sprintf("FOR VALUES IN (%d)", class) {
			return class, nil
		}
	}
	return 0, fmt.Errorf("unrecognized retention bound %q", bound)
}

func parseRangeBound(bound string) (time.Time, time.Time, error) {
	matches := rangeBoundPattern.FindStringSubmatch(bound)
	if len(matches) != 3 {
		return time.Time{}, time.Time{}, fmt.Errorf("unrecognized range bound %q", bound)
	}
	from, err := parsePartitionTime(matches[1])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse lower bound: %w", err)
	}
	to, err := parsePartitionTime(matches[2])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse upper bound: %w", err)
	}
	return from, to, nil
}

func parsePartitionTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05Z07"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}
