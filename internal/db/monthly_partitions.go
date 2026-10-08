package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListMonthlyPartitions returns the direct date-range partitions of table.
func ListMonthlyPartitions(ctx context.Context, pool *pgxpool.Pool, table string) ([]Partition, error) {
	var partitions []Partition
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `set local datestyle = 'ISO, YMD'`); err != nil {
			return fmt.Errorf("set partition listing date style: %w", err)
		}
		rows, err := tx.Query(ctx, `
SELECT child.relname, pg_get_expr(child.relpartbound, child.oid)
FROM pg_class AS root
JOIN pg_inherits AS inherit ON inherit.inhparent = root.oid
JOIN pg_class AS child ON child.oid = inherit.inhrelid
WHERE root.oid = to_regclass($1)
ORDER BY child.relname`, table)
		if err != nil {
			return fmt.Errorf("list %s monthly partitions: %w", table, err)
		}
		defer rows.Close()

		partitions = make([]Partition, 0)
		for rows.Next() {
			var name, bound string
			if err := rows.Scan(&name, &bound); err != nil {
				return fmt.Errorf("scan %s monthly partition: %w", table, err)
			}
			from, to, err := parseRangeBound(bound)
			if err != nil {
				return fmt.Errorf("partition %s: %w", name, err)
			}
			partitions = append(partitions, Partition{Name: name, From: from, To: to})
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate %s monthly partitions: %w", table, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return partitions, nil
}
