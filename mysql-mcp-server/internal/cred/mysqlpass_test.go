package cred

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLookupMysqlpass(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mysqlpass")
	body := "127.0.0.1:3306:orders:mcp_ro:secret1\n" +
		"*:3306:shop:mcp_ro:secret2\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYSQL_PASSFILE", p)
	got, err := lookupMysqlpass("127.0.0.1", 3306, "orders", "mcp_ro")
	if err != nil || got != "secret1" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = lookupMysqlpass("10.0.0.1", 3306, "shop", "mcp_ro")
	if err != nil || got != "secret2" {
		t.Fatalf("wildcard got %q err=%v", got, err)
	}
}

func TestSplitPassEscapes(t *testing.T) {
	fields := splitPass(`h:3306:d:u:p\:w`)
	if len(fields) != 5 || fields[4] != "p:w" {
		t.Fatalf("%#v", fields)
	}
}

func TestLookupMysqlpassKeepsPasswordSpaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mysqlpass")
	if err := os.WriteFile(p, []byte("127.0.0.1:3306:db:user:secret \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYSQL_PASSFILE", p)
	got, err := lookupMysqlpass("127.0.0.1", 3306, "db", "user")
	if err != nil || got != "secret " {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestLookupMysqlpassCaseSensitive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mysqlpass")
	if err := os.WriteFile(p, []byte("127.0.0.1:3306:Shop:mcp_ro:secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYSQL_PASSFILE", p)
	got, err := lookupMysqlpass("127.0.0.1", 3306, "shop", "mcp_ro")
	if err != nil || got != "" {
		t.Fatalf("case-insensitive match should not succeed, got %q err=%v", got, err)
	}
}

func TestCheckMysqlpassPerms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mysqlpass")
	if err := os.WriteFile(p, []byte("h:3306:d:u:p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkMysqlpassPerms(p)
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
