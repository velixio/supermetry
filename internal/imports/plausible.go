package imports

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// ParsePlausible reads the public Plausible export format without retaining
// whole CSV files in memory. Unknown files and columns are ignored.
func ParsePlausible(path string) (Aggregate, ParseStats, error) {
	return parsePlausible(context.Background(), path)
}

func parsePlausible(ctx context.Context, filename string) (Aggregate, ParseStats, error) {
	result := newAggregate()
	var stats ParseStats
	file, err := os.Open(filename)
	if err != nil {
		return result, stats, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, stats, err
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return result, stats, fmt.Errorf("open Plausible ZIP: %w", err)
	}
	if len(archive.File) > maxZIPEntries {
		return result, stats, fmt.Errorf("ZIP has %d entries, limit is %d", len(archive.File), maxZIPEntries)
	}
	// Entries are only streamed, never extracted, so names are used solely to
	// pick a file family. archive/zip rejects entries that inflate beyond their
	// declared size, which makes the declared sizes safe to budget against.
	var total uint64
	for _, entry := range archive.File {
		family := plausibleFamily(entryBase(entry.Name))
		if family == "" || entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > maxZIPEntryBytes {
			return result, stats, fmt.Errorf("%s exceeds uncompressed size limit", entry.Name)
		}
		total += entry.UncompressedSize64
		if total > maxZIPTotalBytes {
			return result, stats, fmt.Errorf("ZIP exceeds total uncompressed size limit of %d bytes", maxZIPTotalBytes)
		}
		reader, err := entry.Open()
		if err != nil {
			return result, stats, fmt.Errorf("open %s: %w", entry.Name, err)
		}
		err = parsePlausibleCSV(contextReader{ctx, io.LimitReader(reader, int64(maxZIPEntryBytes))}, family, result, &stats)
		closeErr := reader.Close()
		if err != nil {
			return result, stats, fmt.Errorf("parse %s: %w", entry.Name, err)
		}
		if closeErr != nil {
			return result, stats, fmt.Errorf("close %s: %w", entry.Name, closeErr)
		}
	}
	return result, stats, nil
}

// entryBase returns the last element of a ZIP entry name, accepting either
// separator. Directory parts, including traversal segments, are discarded.
func entryBase(name string) string {
	return name[strings.LastIndexAny(name, `/\`)+1:]
}

func plausibleFamily(name string) string {
	name = strings.ToLower(name)
	for _, family := range []string{
		"imported_operating_systems", "imported_custom_events", "imported_entry_pages",
		"imported_exit_pages", "imported_locations", "imported_browsers", "imported_devices",
		"imported_sources", "imported_visitors", "imported_pages",
	} {
		if strings.HasPrefix(name, family+"_") && strings.HasSuffix(name, ".csv") {
			return family
		}
	}
	return ""
}

func parsePlausibleCSV(input io.Reader, family string, result Aggregate, stats *ParseStats) error {
	required := []string{"date"}
	key := ""
	switch family {
	case "imported_pages":
		key = "page"
	case "imported_entry_pages":
		key = "entry_page"
	case "imported_exit_pages":
		key = "exit_page"
	case "imported_devices":
		key = "device"
	case "imported_browsers":
		key = "browser"
	case "imported_operating_systems":
		key = "operating_system"
	case "imported_custom_events":
		key = "name"
	}
	if key != "" {
		required = append(required, key)
	}
	table, err := newCSVTable(input, stats, required...)
	if err != nil {
		return err
	}
	return table.each(func(row csvRow) error {
		day, err := row.day()
		if err != nil {
			return err
		}
		metrics, err := metricsFromRow(row)
		if err != nil {
			return err
		}
		switch family {
		case "imported_visitors":
			result.addDay(day, metrics)
		case "imported_pages":
			result.addDimension(DimensionKey{day, DimPage, row.text("page")}, metrics)
		case "imported_sources":
			value := firstNonempty(row.text("source"), row.text("utm_source"), row.text("referrer"))
			result.addDimension(DimensionKey{day, DimSource, value}, metrics)
		case "imported_entry_pages":
			entrances, err := row.integer("entrances")
			if err != nil {
				return err
			}
			metrics.Visits = firstPositive(metrics.Visits, entrances)
			result.addDimension(DimensionKey{day, DimEntry, row.text("entry_page")}, metrics)
		case "imported_exit_pages":
			exits, err := row.integer("exits")
			if err != nil {
				return err
			}
			metrics.Visits = firstPositive(metrics.Visits, exits)
			result.addDimension(DimensionKey{day, DimExit, row.text("exit_page")}, metrics)
		case "imported_locations":
			for dim, value := range map[int16]string{DimCountry: row.text("country"), DimRegion: row.text("region"), DimCity: row.text("city")} {
				result.addDimension(DimensionKey{day, dim, value}, metrics)
			}
		case "imported_devices":
			result.addDimension(DimensionKey{day, DimDevice, row.text("device")}, metrics)
		case "imported_browsers":
			result.addDimension(DimensionKey{day, DimBrowser, row.text("browser")}, metrics)
		case "imported_operating_systems":
			result.addDimension(DimensionKey{day, DimOS, row.text("operating_system")}, metrics)
		case "imported_custom_events":
			// The imported schema has no event-name dimension code. Retain event
			// counts by their path when one is present, without inventing a code.
			result.addDimension(DimensionKey{day, DimPage, row.text("path")}, Metrics{Events: metrics.Events})
		}
		return result.checkLimit()
	})
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
