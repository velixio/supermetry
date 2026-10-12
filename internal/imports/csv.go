package imports

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type csvTable struct {
	reader *csv.Reader
	header map[string]int
	stats  *ParseStats
}

func newCSVTable(input io.Reader, stats *ParseStats, required ...string) (*csvTable, error) {
	return newCSVTableComment(input, stats, 0, required...)
}

// newCSVTableComment skips lines starting with comment (0 disables it).
func newCSVTableComment(input io.Reader, stats *ParseStats, comment rune, required ...string) (*csvTable, error) {
	reader := csv.NewReader(input)
	reader.FieldsPerRecord = -1
	reader.Comment = comment
	row, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	header := make(map[string]int, len(row))
	for i, value := range row {
		value = strings.TrimPrefix(value, "\ufeff")
		header[strings.ToLower(strings.TrimSpace(value))] = i
	}
	for _, name := range required {
		if _, ok := header[name]; !ok {
			return nil, fmt.Errorf("missing required CSV column %q", name)
		}
	}
	return &csvTable{reader: reader, header: header, stats: stats}, nil
}

func (t *csvTable) each(fn func(csvRow) error) error {
	for {
		values, err := t.reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			// Malformed records are skipped; anything else (I/O, ZIP checksum,
			// cancellation) would repeat forever, so it ends the parse.
			var parseErr *csv.ParseError
			if !errors.As(err, &parseErr) {
				return err
			}
			t.stats.badRow(parseErr.StartLine, parseErr.Err)
			continue
		}
		line, _ := t.reader.FieldPos(0)
		if err := fn(csvRow{values: values, header: t.header, line: line}); err != nil {
			if errors.Is(err, errSkipRow) {
				continue
			}
			var fatal fatalError
			if errors.As(err, &fatal) {
				return fatal.err
			}
			t.stats.badRow(line, err)
			continue
		}
		t.stats.Rows++
	}
}

// errSkipRow ends a row without counting it as accepted or bad (the callback
// has already recorded why, e.g. as a duplicate).
var errSkipRow = errors.New("row skipped")

// fatalError aborts a parse instead of counting the current row as bad.
type fatalError struct{ err error }

func (e fatalError) Error() string { return e.err.Error() }
func (e fatalError) Unwrap() error { return e.err }

type csvRow struct {
	values []string
	header map[string]int
	line   int
}

func (r csvRow) text(name string) string {
	index, ok := r.header[name]
	if !ok || index >= len(r.values) {
		return ""
	}
	return strings.TrimSpace(r.values[index])
}

func (r csvRow) integer(name string) (int64, error) {
	value := r.text(name)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid %s %q", name, value)
	}
	return parsed, nil
}

func (r csvRow) day() (time.Time, error) {
	return time.Parse("2006-01-02", r.text("date"))
}

func metricsFromRow(row csvRow) (Metrics, error) {
	var result Metrics
	fields := []struct {
		name string
		to   *int64
	}{
		{"visitors", &result.Visitors}, {"visits", &result.Visits},
		{"pageviews", &result.Pageviews}, {"bounces", &result.Bounces},
		{"visit_duration", &result.VisitDurationS}, {"events", &result.Events},
	}
	for _, field := range fields {
		value, err := row.integer(field.name)
		if err != nil {
			return Metrics{}, err
		}
		*field.to = value
	}
	return result, nil
}

// contextReader fails reads once ctx is done, so a long parse stops promptly
// on shutdown.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
