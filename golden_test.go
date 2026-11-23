package walk

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestGoldenFiles is the F-22 differential/golden harness: each
// testdata/golden/*.cypher has an optional *.setup.cypher and an expected
// *.tsv. Rows are sorted before comparison so scan order does not matter.
func TestGoldenFiles(t *testing.T) {
	dir := filepath.Join("testdata", "golden")
	cases, err := filepath.Glob(filepath.Join(dir, "*.cypher"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(cases) == 0 {
		t.Fatalf("no golden cases in %s", dir)
	}
	for _, queryPath := range cases {
		if strings.HasSuffix(queryPath, ".setup.cypher") {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(queryPath), ".cypher")
		t.Run(name, func(t *testing.T) {
			db := newTestDB(t)
			setupPath := strings.TrimSuffix(queryPath, ".cypher") + ".setup.cypher"
			if setup, err := os.ReadFile(setupPath); err == nil {
				if _, err := db.Query(context.Background(), string(setup), QueryOptions{}); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			query, err := os.ReadFile(queryPath)
			if err != nil {
				t.Fatalf("read query: %v", err)
			}
			res, err := db.Query(context.Background(), string(query), QueryOptions{})
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			expectedBytes, err := os.ReadFile(strings.TrimSuffix(queryPath, ".cypher") + ".tsv")
			if err != nil {
				t.Fatalf("read expected: %v", err)
			}
			got := rowsToSortedTSV(res)
			want := sortedTSV(strings.TrimSpace(string(expectedBytes)))
			if got != want {
				t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

func rowsToSortedTSV(res *Result) string {
	var lines []string
	if len(res.Columns) > 0 {
		lines = append(lines, strings.Join(res.Columns, "\t"))
	}
	for _, row := range res.Rows {
		fields := make([]string, len(row))
		for i, v := range row {
			fields[i] = FormatValue(v)
		}
		lines = append(lines, strings.Join(fields, "\t"))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func sortedTSV(s string) string {
	var lines []string
	r := csv.NewReader(strings.NewReader(s))
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		lines = strings.Split(s, "\n")
	} else {
		for _, row := range rows {
			lines = append(lines, strings.Join(row, "\t"))
		}
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
