package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const (
	csvPageSize = 1000
	csvMaxRows  = 100000
)

func writeCSV(w http.ResponseWriter, response QueryResponse, filename string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	writeCSVRows(writer, response.Columns, response.Rows, true)
	writer.Flush()
}

func streamQueryCSV(w http.ResponseWriter, r *http.Request, service queryService, request QueryRequest, filename string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	written := 0
	header := true
	limit := csvPageSize
	request.Limit = &limit
	for written < csvMaxRows {
		response, err := service.Query(r.Context(), request)
		if err != nil {
			if written == 0 {
				writeQueryError(w, err)
			}
			return
		}
		rows := response.Rows
		if remaining := csvMaxRows - written; len(rows) > remaining {
			rows = rows[:remaining]
		}
		writeCSVRows(writer, response.Columns, rows, header)
		header = false
		written += len(rows)
		writer.Flush()
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if response.NextCursor == nil || len(rows) == 0 {
			return
		}
		request.Cursor = response.NextCursor
	}
}

func writeCSVRows(writer *csv.Writer, columns []string, rows []map[string]any, header bool) {
	if header {
		_ = writer.Write(columns)
	}
	record := make([]string, len(columns))
	for _, row := range rows {
		for index, column := range columns {
			record[index] = csvValue(row[column])
		}
		_ = writer.Write(record)
	}
}

func csvFilename(site, endpoint, from, to string) string {
	return filenamePart(site) + "-" + endpoint + "-" + from + "-" + to + ".csv"
}

func filenamePart(value string) string {
	return strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char) {
			return char
		}
		return '_'
	}, value)
}

func csvValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		// Spreadsheets run cells starting with these as formulas; values such
		// as page paths and referrers come from visitors, so prefix a quote.
		if typed != "" && strings.ContainsRune("=+-@\t\r", rune(typed[0])) {
			return "'" + typed
		}
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(typed)
	}
}
