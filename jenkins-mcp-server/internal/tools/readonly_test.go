package tools

import "testing"

func TestReadOnlyToolHints(t *testing.T) {
	tool := ReadOnlyTool("list_jobs", "List jobs", "read-only")
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatal("ReadOnlyHint must be true")
	}
	if tool.Name != "list_jobs" {
		t.Fatalf("name=%q", tool.Name)
	}
}
