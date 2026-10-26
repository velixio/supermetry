package storageusage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WorkUnit struct {
	Key        string   `json:"key"`
	Scanner    string   `json:"scanner"`
	Table      string   `json:"table,omitempty"`
	Relation   string   `json:"relation,omitempty"`
	Category   Category `json:"category,omitempty"`
	SiteID     int32    `json:"site_id,omitempty"`
	FromSiteID int64    `json:"from_site_id,omitempty"`
	ToSiteID   int64    `json:"to_site_id,omitempty"`
}

const postgresSiteBatchSize int64 = 100

type PostgresScanner struct {
	pool             *pgxpool.Pool
	statementTimeout time.Duration
}

func NewPostgresScanner(pool *pgxpool.Pool, statementTimeout time.Duration) *PostgresScanner {
	if statementTimeout <= 0 {
		statementTimeout = 30 * time.Second
	}
	return &PostgresScanner{pool: pool, statementTimeout: statementTimeout}
}

func (s *PostgresScanner) Fingerprint() string {
	var definition strings.Builder
	fmt.Fprintf(&definition, "estimator=v1;site_batch=%d\n", postgresSiteBatchSize)
	for _, source := range sourceRegistry {
		fmt.Fprintf(&definition, "%s|%s|%t|%s|%s|%s\n",
			source.Table, source.Category, source.Partitioned,
			source.OwnerTable, source.OwnerForeignKey, source.ExcludedReason)
	}
	sum := sha256.Sum256([]byte(definition.String()))
	return fmt.Sprintf("postgres:%x", sum[:])
}

func (s *PostgresScanner) WorkUnits(ctx context.Context, runID int64) ([]WorkUnit, error) {
	rows, err := s.pool.Query(ctx, `
		select min(site_id)::bigint,(max(site_id)::bigint)+1
		from (
			select site_id,(row_number() over(order by site_id)-1)/$2 as batch
			from storage_usage_run_sites where run_id=$1
		) captured
		group by batch order by batch`, runID, postgresSiteBatchSize)
	if err != nil {
		return nil, fmt.Errorf("load storage scan site ranges: %w", err)
	}
	var siteRanges [][2]int64
	for rows.Next() {
		var bounds [2]int64
		if err := rows.Scan(&bounds[0], &bounds[1]); err != nil {
			rows.Close()
			return nil, err
		}
		siteRanges = append(siteRanges, bounds)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	units := make([]WorkUnit, 0, len(sourceRegistry))
	for _, source := range sourceRegistry {
		if source.ExcludedReason != "" {
			continue
		}
		if !source.Partitioned {
			for _, bounds := range siteRanges {
				units = append(units, rangedWorkUnit(source, source.Table, bounds[0], bounds[1]))
			}
			continue
		}
		rows, err := s.pool.Query(ctx, `
			select c.relname
			from pg_partition_tree(to_regclass($1)) p
			join pg_class c on c.oid=p.relid
			where p.isleaf
			order by c.relname`, source.Table)
		if err != nil {
			return nil, fmt.Errorf("discover %s leaf relations: %w", source.Table, err)
		}
		for rows.Next() {
			var relation string
			if err := rows.Scan(&relation); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan %s leaf relation: %w", source.Table, err)
			}
			units = append(units, workUnit(source, relation))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate %s leaf relations: %w", source.Table, err)
		}
		rows.Close()
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Key < units[j].Key })
	return units, nil
}

func workUnit(source Source, relation string) WorkUnit {
	return WorkUnit{
		Key:      "postgres:" + source.Table + ":" + relation,
		Table:    source.Table,
		Relation: relation,
		Category: source.Category,
	}
}

func rangedWorkUnit(source Source, relation string, from, to int64) WorkUnit {
	unit := workUnit(source, relation)
	unit.FromSiteID, unit.ToSiteID = from, to
	unit.Key = fmt.Sprintf("postgres:%s:%s:%010d-%010d", source.Table, relation, from, to)
	return unit
}

func (s *PostgresScanner) Scan(ctx context.Context, runID int64, unit WorkUnit) ([]Measurement, error) {
	valid, err := s.validWorkUnit(ctx, unit)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("unregistered PostgreSQL work unit %q", unit.Key)
	}
	if source := sourceByTable(unit.Table); source != nil && source.Partitioned {
		var exists bool
		if err := s.pool.QueryRow(ctx, `select to_regclass($1) is not null`, unit.Relation).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return []Measurement{{Category: unit.Category}}, nil
		}
	}

	var values []Contribution
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		timeoutMS := s.statementTimeout.Milliseconds()
		if timeoutMS < 1 {
			timeoutMS = 1
		}
		if _, err := tx.Exec(ctx, `select set_config('statement_timeout',$1,true)`, strconv.FormatInt(timeoutMS, 10)); err != nil {
			return fmt.Errorf("set storage scan statement timeout: %w", err)
		}
		args := []any{runID}
		if unit.ToSiteID > unit.FromSiteID {
			args = append(args, unit.FromSiteID, unit.ToSiteID)
		}
		rows, err := tx.Query(ctx, s.scanSQL(unit), args...)
		if err != nil {
			if IsUndefinedRelation(err) {
				return nil
			}
			return fmt.Errorf("scan storage source %s: %w", unit.Key, err)
		}
		defer rows.Close()
		for rows.Next() {
			var value Contribution
			if err := rows.Scan(&value.SiteID, &value.Bytes); err != nil {
				return fmt.Errorf("scan storage contribution %s: %w", unit.Key, err)
			}
			values = append(values, value)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate storage contributions %s: %w", unit.Key, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return []Measurement{{Category: unit.Category, Values: values}}, nil
}

func (s *PostgresScanner) scanSQL(unit WorkUnit) string {
	relation := pgx.Identifier{unit.Relation}.Sanitize()
	source := sourceByTable(unit.Table)
	if source != nil && source.OwnerTable != "" {
		owner := pgx.Identifier{source.OwnerTable}.Sanitize()
		foreignKey := pgx.Identifier{source.OwnerForeignKey}.Sanitize()
		predicate := rangePredicate("owner.site_id", unit)
		return "select owner.site_id,sum(pg_column_size(row_alias))::bigint from " + relation +
			" row_alias join " + owner + " owner on owner.id=row_alias." + foreignKey +
			" join storage_usage_run_sites captured on captured.run_id=$1 and captured.site_id=owner.site_id" + predicate + " group by owner.site_id order by owner.site_id"
	}
	predicate := rangePredicate("row_alias.site_id", unit)
	return "select row_alias.site_id,sum(pg_column_size(row_alias))::bigint from " + relation +
		" row_alias join storage_usage_run_sites captured on captured.run_id=$1 and captured.site_id=row_alias.site_id" + predicate + " group by row_alias.site_id order by row_alias.site_id"
}

func rangePredicate(column string, unit WorkUnit) string {
	if unit.ToSiteID > unit.FromSiteID {
		return " where " + column + ">=$2 and " + column + "<$3"
	}
	return ""
}

func (s *PostgresScanner) validWorkUnit(ctx context.Context, unit WorkUnit) (bool, error) {
	source := sourceByTable(unit.Table)
	if source == nil || unit.Category != source.Category {
		return false, nil
	}
	if !source.Partitioned {
		return unit.Relation == source.Table && unit.ToSiteID > unit.FromSiteID && unit.Key == rangedWorkUnit(*source, unit.Relation, unit.FromSiteID, unit.ToSiteID).Key, nil
	}
	if unit.Key != workUnit(*source, unit.Relation).Key {
		return false, nil
	}
	var valid bool
	err := s.pool.QueryRow(ctx, `
		select exists(
			select 1 from pg_partition_tree(to_regclass($1))
			where relid=to_regclass($2) and isleaf
		)`, source.Table, unit.Relation).Scan(&valid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || IsUndefinedRelation(err) {
			return false, nil
		}
		return false, fmt.Errorf("validate storage work unit %s: %w", unit.Key, err)
	}
	if valid {
		return true, nil
	}
	// A leaf recorded in the immutable manifest may be dropped by retention
	// before it is scanned. Quoting plus the fixed parent-name prefix keeps this
	// safe; the scan then treats undefined_relation as an empty contribution.
	return safeRelationName.MatchString(unit.Relation) && strings.HasPrefix(unit.Relation, source.Table+"_"), nil
}

var safeRelationName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func sourceByTable(table string) *Source {
	for i := range sourceRegistry {
		candidate := &sourceRegistry[i]
		if candidate.Table == table && candidate.ExcludedReason == "" {
			return candidate
		}
	}
	return nil
}
