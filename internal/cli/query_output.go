package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
)

type queryOutputEncoder struct {
	w       io.Writer
	format  string
	columns []string
	csv     *csv.Writer
	first   bool
}

func newQueryOutputEncoder(w io.Writer, format string, columns []string) (*queryOutputEncoder, error) {
	q := &queryOutputEncoder{w: w, format: format, columns: append([]string(nil), columns...), first: true}
	switch format {
	case "json":
		cols, _ := json.Marshal(columns)
		if _, err := fmt.Fprintf(w, "{\"columns\":%s,\"rows\":[", cols); err != nil {
			return nil, err
		}
	case "ndjson":
		if err := writeJSONLine(w, map[string]any{"type": "columns", "columns": columns}); err != nil {
			return nil, err
		}
	case "csv", "tsv":
		q.csv = csv.NewWriter(w)
		if format == "tsv" {
			q.csv.Comma = '\t'
		}
		if err := q.csv.Write(columns); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("--format must be json, ndjson, csv, or tsv")
	}
	return q, nil
}

func (q *queryOutputEncoder) writeRows(rows [][]any) error {
	for _, row := range rows {
		if len(row) != len(q.columns) {
			return fmt.Errorf("query row has %d values for %d columns", len(row), len(q.columns))
		}
		switch q.format {
		case "json":
			if !q.first {
				if _, err := io.WriteString(q.w, ","); err != nil {
					return err
				}
			}
			b, err := json.Marshal(row)
			if err != nil {
				return fmt.Errorf("encode query row: %w", err)
			}
			if _, err := q.w.Write(b); err != nil {
				return err
			}
			q.first = false
		case "ndjson":
			if err := writeJSONLine(q.w, map[string]any{"type": "row", "row": row}); err != nil {
				return err
			}
		case "csv", "tsv":
			record := make([]string, len(row))
			for i, value := range row {
				record[i] = queryCell(value)
			}
			if err := q.csv.Write(record); err != nil {
				return err
			}
		}
	}
	return nil
}

func (q *queryOutputEncoder) finish(rowCount int, truncated bool) error {
	switch q.format {
	case "json":
		_, err := fmt.Fprintf(q.w, "],\"row_count\":%d,\"truncated\":%t}\n", rowCount, truncated)
		return err
	case "ndjson":
		return writeJSONLine(q.w, map[string]any{"type": "meta", "row_count": rowCount, "truncated": truncated})
	case "csv", "tsv":
		q.csv.Flush()
		return q.csv.Error()
	default:
		return nil
	}
}

func writeJSONLine(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}

func queryCell(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
		return fmt.Sprint(v)
	}
}
