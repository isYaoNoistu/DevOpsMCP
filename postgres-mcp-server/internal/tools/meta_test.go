package tools

import "testing"

func TestReadOnlyToolHints(t *testing.T) {
	tool := ReadOnlyTool("query_postgres", "Query Postgres", "read-only escape hatch")
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatal("ReadOnlyHint must be true")
	}
	if tool.OutputSchema == nil {
		t.Fatal("OutputSchema must be set")
	}
}
