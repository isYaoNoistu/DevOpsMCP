package pathguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanUnix(t *testing.T) {
	got, err := CleanUnix("/data/postgresql/log/postgresql-16-main-2026-09-17.log")
	if err != nil || got == "" {
		t.Fatalf("%v %q", err, got)
	}
	if _, err := CleanUnix("/data/postgresql/log/../etc/passwd"); err == nil {
		t.Fatal("expected .. reject")
	}
	if _, err := CleanUnix("relative/path"); err == nil {
		t.Fatal("expected absolute")
	}
}

func TestCheckAllowlistRoot(t *testing.T) {
	if _, err := CheckAllowlistRoot("/data/postgresql/log"); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckAllowlistRoot("/var/log/nginx"); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckAllowlistRoot("/var/log"); err == nil {
		t.Fatal("reject /var/log")
	}
	if _, err := CheckAllowlistRoot("/"); err == nil {
		t.Fatal("reject /")
	}
	if _, err := CheckAllowlistRoot("/tmp"); err == nil {
		t.Fatal("reject /tmp")
	}
	if _, err := CheckAllowlistRoot("/data"); err == nil {
		t.Fatal("reject /data")
	}
}

func TestResolveUnix(t *testing.T) {
	roots := []string{"/data/postgresql/log", "/data/logs/nginx"}
	p, root, err := ResolveUnix("/data/postgresql/log/a.log", roots)
	if err != nil || root != "/data/postgresql/log" || p != "/data/postgresql/log/a.log" {
		t.Fatalf("%v %q %q", err, p, root)
	}
	if _, _, err := ResolveUnix("/etc/nginx/nginx.conf", roots); err == nil {
		t.Fatal("expected outside allowlist")
	}
	if _, _, err := ResolveUnix("/data/postgresql/log/.env", roots); err == nil {
		t.Fatal("expected secret suffix")
	}
}

func TestResolveLocal(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "app.log")
	if err := os.WriteFile(f, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveLocal(f, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty")
	}
	outside := filepath.Join(os.TempDir(), "nope.log")
	if _, err := ResolveLocal(outside, []string{dir}); err == nil {
		t.Fatal("expected outside")
	}
}

func TestResolveLocalRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "secret.log")
	if err := os.WriteFile(secret, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "escape.log")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	if _, err := ResolveLocal(link, []string{dir}); err == nil {
		t.Fatal("expected symlink-out reject")
	}
}
