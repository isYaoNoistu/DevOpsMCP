package pg

import (
	"strings"
	"testing"

	"postgres-mcp-server/internal/sqlguard"
	"postgres-mcp-server/internal/targets"
)

func TestUniqueColumnNames(t *testing.T) {
	got := uniqueColumnNames([]string{"id", "id", "name", ""})
	want := []string{"id", "id_2", "name", "column_4"}
	if len(got) != len(want) {
		t.Fatalf("%#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%#v", got)
		}
	}
}

func TestRowBytesUsesJSON(t *testing.T) {
	row := map[string]any{
		"payload": strings.Repeat("x", 200),
	}
	n, err := rowBytes(row)
	if err != nil {
		t.Fatal(err)
	}
	if n < 200 {
		t.Fatalf("expected JSON size to include payload, got %d", n)
	}
}

func TestRowBytesRejectsOverLimit(t *testing.T) {
	row := map[string]any{
		"payload": strings.Repeat("x", sqlguard.MaxBytes+8),
	}
	n, err := rowBytes(row)
	if err != nil {
		t.Fatal(err)
	}
	if n <= sqlguard.MaxBytes {
		t.Fatalf("got %d", n)
	}
}

func TestReapEmpty(t *testing.T) {
	p := NewPooler(Config{})
	p.Reap(nil)
	p.Reap([]targets.Target{{Name: "gone"}})
}

func TestFingerprintIncludesSSL(t *testing.T) {
	a := targets.Target{Name: "a", Host: "h", Port: 5432, DBName: "d", User: "u", SSLMode: "prefer"}
	b := a
	b.SSLMode = "verify-full"
	if fingerprint(a) == fingerprint(b) {
		t.Fatal("sslmode change must change pool fingerprint")
	}
}
