package logop

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"host-logs-mcp-server/internal/limits"
	"host-logs-mcp-server/internal/targets"
)

func TestSearchLocalTimeWindow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "postgresql-16-main-2026-09-17.log")
	body := strings.Join([]string{
		"2026-09-17 03:00:00.000 CST [1] LOG:  checkpoint",
		"2026-09-17 05:40:37.000 CST [688365] shop@shop_prod 203.0.113.10(56306) app=shop-web xid=0 vxid=25/64532 LOG:  unexpected EOF on client connection with an open transaction",
		"2026-09-17 06:00:00.000 CST [2] LOG:  later",
	}, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tgt := targets.Target{Name: "lab", Host: "local", Transport: "local", Paths: []string{dir}}
	hits, truncated, err := SearchLocal(tgt, SearchOpts{
		Path:     p,
		Pattern:  "unexpected EOF",
		Fixed:    true,
		Start:    "2026-09-17 05:38:00",
		End:      "2026-09-17 05:45:00",
		MaxLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(hits) != 1 {
		t.Fatalf("hits=%v truncated=%v", hits, truncated)
	}
	if !strings.Contains(hits[0], "shop-web") {
		t.Fatalf("got %q", hits[0])
	}
}

func TestSearchLocalTimeWindowSkipsOutOfWindowContinuation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log")
	body := strings.Join([]string{
		"2026-09-17 06:00:00 outside window",
		"TRACE continuation without a timestamp",
		"2026-09-17 05:40:37 ERROR in window",
	}, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tgt := targets.Target{Name: "lab", Host: "local", Transport: "local", Paths: []string{dir}}
	hits, truncated, err := SearchLocal(tgt, SearchOpts{
		Path: p, Pattern: "ERROR", Fixed: true,
		Start: "2026-09-17 05:38:00", End: "2026-09-17 05:45:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(hits) != 1 || !strings.Contains(hits[0], "ERROR in window") {
		t.Fatalf("hits=%v truncated=%v", hits, truncated)
	}
}

func TestSearchLocalCapsOutputBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log")
	line := "match " + strings.Repeat("x", 300*1024)
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tgt := targets.Target{Name: "lab", Host: "local", Transport: "local", Paths: []string{dir}}
	hits, truncated, err := SearchLocal(tgt, SearchOpts{Path: p, Pattern: "match", Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("expected byte truncation")
	}
	if len(strings.Join(hits, "\n")) > limits.MaxBytes {
		t.Fatalf("output exceeded byte cap: %d", len(strings.Join(hits, "\n")))
	}
}

func TestSearchLocalContextRespectsLineCap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log")
	if err := os.WriteFile(p, []byte("before\nmatch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tgt := targets.Target{Name: "lab", Host: "local", Transport: "local", Paths: []string{dir}}
	hits, truncated, err := SearchLocal(tgt, SearchOpts{
		Path: p, Pattern: "match", Fixed: true, MaxLines: 1, ContextBefore: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(hits) != 1 {
		t.Fatalf("hits=%v truncated=%v", hits, truncated)
	}
}

func TestSearchAndTailLocalGzip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log.gz")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	body := strings.Join([]string{
		"2026-09-17 03:00:00 first",
		"2026-09-17 05:40:37 unexpected EOF app=shop-web",
		"2026-09-17 06:00:00 later",
	}, "\n") + "\n"
	if _, err := zw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	tgt := targets.Target{Name: "lab", Host: "local", Transport: "local", Paths: []string{dir}}
	hits, truncated, err := SearchLocal(tgt, SearchOpts{Path: p, Pattern: "unexpected EOF", Fixed: true, MaxLines: 20})
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(hits) != 1 || !strings.Contains(hits[0], "shop-web") {
		t.Fatalf("gzip search hits=%v truncated=%v", hits, truncated)
	}
	tail, err := TailLocal(tgt, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || !strings.Contains(tail[0], "later") {
		t.Fatalf("gzip tail=%v", tail)
	}
}

func TestListLocalSkipsSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.log"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tgt := targets.Target{Name: "lab", Host: "local", Transport: "local", Paths: []string{dir}}
	files, err := ListLocal(tgt, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0].Path, "ok.log") {
		t.Fatalf("files=%v", files)
	}
}

func TestSearchLocalRejectsOutside(t *testing.T) {
	dir := t.TempDir()
	tgt := targets.Target{Name: "lab", Host: "local", Transport: "local", Paths: []string{dir}}
	_, _, err := SearchLocal(tgt, SearchOpts{Path: os.TempDir(), Pattern: "x"})
	if err == nil {
		t.Fatal("expected allowlist error")
	}
}
