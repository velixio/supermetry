package opencounters

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type poolStore struct{ pool *pgxpool.Pool }

func PoolStore(pool *pgxpool.Pool) Store { return poolStore{pool: pool} }

func (s poolStore) Upsert(ctx context.Context, rows map[ViewKey]int64) error {
	batch := &pgx.Batch{}
	for key, views := range rows {
		batch.Queue(`
			with counter as (
				insert into open_counters(key,last_seen_day) values ($1,$2::date)
				on conflict (key) do update set last_seen_day=greatest(open_counters.last_seen_day,excluded.last_seen_day)
				returning id
			)
			insert into open_counter_views_daily(counter_id,day,views)
			select id,$2::date,$3 from counter
			on conflict (counter_id,day) do update set views=open_counter_views_daily.views+excluded.views`,
			key.Key, key.Day, views)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.SendBatch(ctx, batch).Close()
	})
}

func (s poolStore) Totals(ctx context.Context, keys []string) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `
		select c.key,coalesce(sum(v.views),0)::bigint
		from open_counters c left join open_counter_views_daily v on v.counter_id=c.id
		where c.key=any($1) group by c.id,c.key`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64, len(keys))
	for rows.Next() {
		var key string
		var total int64
		if err := rows.Scan(&key, &total); err != nil {
			return nil, err
		}
		out[key] = total
	}
	return out, rows.Err()
}
