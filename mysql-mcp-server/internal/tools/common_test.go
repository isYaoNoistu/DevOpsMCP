package tools

import (
	"testing"

	mysqldb "mysql-mcp-server/internal/mysql"
)

func TestSectionTruncationIsVisible(t *testing.T) {
	for _, rows := range [][]map[string]any{nil, {{"id": 1}}} {
		out := map[string]any{}
		putSection(out, "workers", &mysqldb.Result{
			Rows: rows, Truncated: true, TruncatedReason: "response size limit",
		})
		if out["workers_truncated"] != true || out["workers_truncated_reason"] != "response size limit" {
			t.Fatalf("partial section looks complete: %#v", out)
		}
		if _, ok := out["workers"].([]map[string]any); !ok {
			t.Fatal("preserve the existing rows array contract")
		}
	}
}

func TestCompleteSectionHasNoTruncationWarning(t *testing.T) {
	out := map[string]any{}
	putSection(out, "workers", &mysqldb.Result{Rows: []map[string]any{}})
	if _, ok := out["workers_truncated"]; ok {
		t.Fatal("complete result must not have a truncation warning")
	}
}
