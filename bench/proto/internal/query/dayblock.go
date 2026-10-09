package query

import (
	"context"
	"time"

	"supermetry/bench/proto/internal/core"
)

// availableDayBlocks recursively replaces an unavailable block with its two
// children. An unavailable level-0 block is known to contain no completed
// daily rollup and is omitted. The result is ordered and non-overlapping.
func availableDayBlocks(from, to core.Day, done map[core.DayBlock]bool) []core.DayBlock {
	var out []core.DayBlock
	var add func(core.DayBlock)
	add = func(b core.DayBlock) {
		if done[b] {
			out = append(out, b)
			return
		}
		if b.Level == 0 {
			return
		}
		level := b.Level - 1
		left := core.DayBlock{Start: b.Start, Level: level}
		add(left)
		add(core.DayBlock{Start: left.End() + 1, Level: level})
	}
	for _, b := range core.DecomposeDayRange(from, to) {
		add(b)
	}
	return out
}

func (e *Engine) completedDayBlocks(ctx context.Context, site int32) (map[core.DayBlock]bool, error) {
	e.blockMu.Lock()
	defer e.blockMu.Unlock()
	if c, ok := e.blocks[site]; ok && time.Since(c.at) < 10*time.Second {
		return c.done, nil
	}
	rows, err := e.Pool.Query(ctx, `SELECT level, block_start FROM rollup_block_done WHERE site_id = $1`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	done := map[core.DayBlock]bool{}
	for rows.Next() {
		var level int16
		var start time.Time
		if err := rows.Scan(&level, &start); err != nil {
			return nil, err
		}
		done[core.DayBlock{Start: core.Day(start.Unix() / 86400), Level: uint8(level)}] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if e.blocks == nil {
		e.blocks = map[int32]blockEntry{}
	}
	e.blocks[site] = blockEntry{done: done, at: time.Now()}
	return done, nil
}

type blockEntry struct {
	done map[core.DayBlock]bool
	at   time.Time
}
