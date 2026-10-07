package query

import (
	"reflect"
	"testing"

	"panel/bench/proto/internal/core"
)

func TestAvailableDayBlocksFallbackPreservesCompletedDays(t *testing.T) {
	t.Parallel()
	from := core.CivilDay(2025, 10, 16)
	to := core.CivilDay(2026, 9, 30)
	firstDone := core.CivilDay(2026, 9, 17)
	done := map[core.DayBlock]bool{}
	for d := firstDone; d <= to; d++ {
		done[core.DayBlock{Start: d, Level: 0}] = true
	}
	// Independently mark a parent complete only when both children exist.
	for level := uint8(1); level <= core.MaxDayBlockLevel; level++ {
		seen := map[core.Day]bool{}
		for child := range done {
			if child.Level != level-1 {
				continue
			}
			start := child.Start - core.Day(int64(child.Start)%int64(core.Day(1)<<level))
			if int64(start) < 0 && int64(child.Start)%int64(core.Day(1)<<level) != 0 {
				start -= core.Day(1) << level
			}
			seen[start] = true
		}
		for start := range seen {
			half := core.Day(1) << (level - 1)
			if done[core.DayBlock{Start: start, Level: level - 1}] && done[core.DayBlock{Start: start + half, Level: level - 1}] {
				done[core.DayBlock{Start: start, Level: level}] = true
			}
		}
	}

	got := availableDayBlocks(from, to, done)
	covered := map[core.Day]bool{}
	for _, b := range got {
		if !done[b] {
			t.Fatalf("selected incomplete block %+v", b)
		}
		for d := b.Start; d <= b.End(); d++ {
			if covered[d] {
				t.Fatalf("day %s covered twice", d)
			}
			covered[d] = true
		}
	}
	want := map[core.Day]bool{}
	for d := firstDone; d <= to; d++ {
		want[d] = true
	}
	if !reflect.DeepEqual(covered, want) {
		t.Fatalf("covered days = %#v, want %#v", covered, want)
	}
}

func TestAvailableDayBlocksUsesLargestCompleteBlocks(t *testing.T) {
	t.Parallel()
	from := core.ContainingDayBlock(core.CivilDay(2026, 1, 1), 4).Start
	done := map[core.DayBlock]bool{}
	for level := uint8(0); level <= 4; level++ {
		size := core.Day(1) << level
		for start := from; start < from+16; start += size {
			done[core.DayBlock{Start: start, Level: level}] = true
		}
	}
	want := []core.DayBlock{{Start: from, Level: 4}}
	if got := availableDayBlocks(from, from+15, done); !reflect.DeepEqual(got, want) {
		t.Fatalf("available blocks = %#v, want %#v", got, want)
	}
}
