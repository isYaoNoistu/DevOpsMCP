package cred

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLookupPgpass(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pgpass")
	body := "127.0.0.1:5432:orders:mcp_ro:secret1\n" +
		"*:5432:shop:mcp_ro:secret2\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGPASSFILE", p)
	got, err := lookupPgpass("127.0.0.1", 5432, "orders", "mcp_ro")
	if err != nil || got != "secret1" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = lookupPgpass("10.0.0.1", 5432, "shop", "mcp_ro")
	if err != nil || got != "secret2" {
		t.Fatalf("wildcard got %q err=%v", got, err)
	}
}

func TestSplitPgpassEscapes(t *testing.T) {
	fields := splitPgpass(`h:5432:d:u:p\:w`)
	if len(fields) != 5 || fields[4] != "p:w" {
		t.Fatalf("%#v", fields)
	}
}

func TestLookupPgpassKeepsPasswordSpaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pgpass")
	if err := os.WriteFile(p, []byte("127.0.0.1:5432:db:user:secret \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGPASSFILE", p)
	got, err := lookupPgpass("127.0.0.1", 5432, "db", "user")
	if err != nil || got != "secret " {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestLookupPgpassCaseSensitive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pgpass")
	if err := os.WriteFile(p, []byte("127.0.0.1:5432:Shop:mcp_ro:secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGPASSFILE", p)
	got, err := lookupPgpass("127.0.0.1", 5432, "shop", "mcp_ro")
	if err != nil || got != "" {
		t.Fatalf("case-insensitive match should not succeed, got %q err=%v", got, err)
	}
}

func TestCheckPgpassPerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pgpass")
	if err := os.WriteFile(p, []byte("h:5432:d:u:p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkPgpassPerms(p)
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("windows should skip mode check: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("expected 0644 to be rejected on unix")
	}
}
