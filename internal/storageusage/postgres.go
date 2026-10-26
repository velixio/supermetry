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
	// FromBlock/ToBlock bound a heap block range [from,to) of a large leaf
	// partition so one unit always fits the statement timeout.
	FromBlock int64 `json:"from_block,omitempty"`
	ToBlock   int64 `json:"to_block,omitempty"`
}

func (u WorkUnit) blockRanged() bool { return u.ToBlock > u.FromBlock }

const postgresSiteBatchSize int64 = 100

// defaultBlocksPerUnit bounds one scan of a partition leaf to 512 MiB of heap
// (65536 blocks of 8 KiB), which a sequential TID range scan reads well inside
// the 30 s statement timeout. Leaves above it are split into consecutive block
// ranges; the last range is open-ended so rows appended after discovery are
// still counted.
const defaultBlocksPerUnit int64 = 65536

// maxBlock is the largest heap block number, used as the open upper bound.
const maxBlock int64 = 4294967295

type PostgresScanner struct {
	pool             *pgxpool.Pool
	statementTimeout time.Duration
	blocksPerUnit    int64

	// afterExistenceCheck is a test hook that runs between the partition
	// existence check and the scan transaction.
	afterExistenceCheck func()
}

func NewPostgresScanner(pool *pgxpool.Pool, statementTimeout time.Duration) *PostgresScanner {
	if statementTimeout <= 0 {
		statementTimeout = 30 * time.Second
	}
	return &PostgresScanner{pool: pool, statementTimeout: statementTimeout, blocksPerUnit: defaultBlocksPerUnit}
}

// WithBlocksPerUnit overrides the heap block budget of one partition scan unit.
func (s *PostgresScanner) WithBlocksPerUnit(blocks int64) *PostgresScanner {
	if blocks > 0 {
		s.blocksPerUnit = blocks
	}
	return s
}

func (s *PostgresScanner) Fingerprint() string {
	var definition strings.Builder
	fmt.Fprintf(&definition, "estimator=v2;site_batch=%d;blocks_per_unit=%d\n", postgresSiteBatchSize, s.blocksPerUnit)
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
			select c.relname,
			       ceil(pg_relation_size(c.oid)::numeric/current_setting('block_size')::numeric)::bigint
			from pg_partition_tree(to_regclass($1)) p
			join pg_class c on c.oid=p.relid
			where p.isleaf
			order by c.relname`, source.Table)
		if err != nil {
			return nil, fmt.Errorf("discover %s leaf relations: %w", source.Table, err)
		}
		for rows.Next() {
			var relation string
			var blocks int64
			if err := rows.Scan(&relation, &blocks); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan %s leaf relation: %w", source.Table, err)
			}
			if blocks <= s.blocksPerUnit {
				units = append(units, workUnit(source, relation))
				continue
			}
			for from := int64(0); from < blocks; from += s.blocksPerUnit {
				to := from + s.blocksPerUnit
				if to >= blocks {
					to = maxBlock
				}
				units = append(units, blockWorkUnit(source, relation, from, to))
			}
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

func blockWorkUnit(source Source, relation string, from, to int64) WorkUnit {
	unit := workUnit(source, relation)
	unit.FromBlock, unit.ToBlock = from, to
	unit.Key = fmt.Sprintf("postgres:%s:%s:blk%010d-%010d", source.Table, relation, from, to)
	return unit
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
	partitioned := false
	if source := sourceByTable(unit.Table); source != nil && source.Partitioned {
		partitioned = true
		var exists bool
		if err := s.pool.QueryRow(ctx, `select to_regclass($1) is not null`, unit.Relation).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return []Measurement{{Category: unit.Category}}, nil
		}
		if s.afterExistenceCheck != nil {
			s.afterExistenceCheck()
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
		} else if unit.blockRanged() {
			args = append(args, fmt.Sprintf("(%d,0)", unit.FromBlock), fmt.Sprintf("(%d,0)", unit.ToBlock))
		}
		rows, err := tx.Query(ctx, s.scanSQL(unit), args...)
		if err != nil {
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
		// Retention may drop a leaf after the existence check. The failed
		// statement aborted the transaction, so the vanished leaf is handled
		// here, outside it. Only a confirmed-missing leaf counts as empty; any
		// other undefined relation is a schema bug and surfaces.
		if partitioned && IsUndefinedRelation(err) {
			var exists bool
			if checkErr := s.pool.QueryRow(ctx, `select to_regclass($1) is not null`, unit.Relation).Scan(&exists); checkErr == nil && !exists {
				return []Measurement{{Category: unit.Category}}, nil
			}
		}
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
	if unit.blockRanged() {
		// A TID range scan reads only the unit's heap blocks, whatever indexes
		// the relation has (events has none that leads with site_id).
		return " where row_alias.ctid>=$2::text::tid and row_alias.ctid<$3::text::tid"
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
	if unit.blockRanged() {
		if unit.FromBlock < 0 || unit.ToBlock > maxBlock || unit.FromSiteID != 0 || unit.ToSiteID != 0 ||
			unit.Key != blockWorkUnit(*source, unit.Relation, unit.FromBlock, unit.ToBlock).Key {
			return false, nil
		}
	} else if unit.Key != workUnit(*source, unit.Relation).Key {
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
	// before it is scanned. Accept it only if the name is exactly what the
	// partition creator would have produced for this parent; a prefix match
	// would let sessions_closed_* pass as sessions_* and rollup_day_dim_* as
	// rollup_day_*. The scan then treats a vanished leaf as empty.
	return partitionNamePattern(source.Table).MatchString(unit.Relation), nil
}

// partitionNamePattern mirrors internal/db/partitions.go naming: hot tables
// are partitioned per retention class (<t>_r<days>_YYYYMMDD, <t>_rf_YYYYMM),
// every other partitioned table by month (<t>_YYYYMM).
func partitionNamePattern(table string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(table)
	if table == "events" || table == "sessions" {
		return regexp.MustCompile(`^` + quoted + `_(?:r[0-9]{1,4}_[0-9]{8}|rf_[0-9]{6})$`)
	}
	return regexp.MustCompile(`^` + quoted + `_[0-9]{6}$`)
}

func sourceByTable(table string) *Source {
	for i := range sourceRegistry {
		candidate := &sourceRegistry[i]
		if candidate.Table == table && candidate.ExcludedReason == "" {
			return candidate
		}
	}
	return nil
}
