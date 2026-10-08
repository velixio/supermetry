package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"

	"panel/internal/csvsafe"
)

func writeCSV(w http.ResponseWriter, response QueryResponse) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="panel-export.csv"`)
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	_ = writer.Write(response.Columns)
	for _, row := range response.Rows {
		record := make([]string, len(response.Columns))
		for index, column := range response.Columns {
			record[index] = csvValue(row[column])
		}
		_ = writer.Write(record)
	}
	writer.Flush()
}

func csvValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return csvsafe.Cell(typed)
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
