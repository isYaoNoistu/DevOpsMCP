package targets

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTargets(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolve_NameAndAlias(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {
	      "name": "orders-prod",
	      "aliases": ["订单生产库", "orders prod"],
	      "host": "127.0.0.1",
	      "dbname": "orders",
	      "user": "mcp_ro",
	      "tags": ["orders", "production"]
	    },
	    {
	      "name": "shop-prod",
	      "aliases": ["shop"],
	      "host": "127.0.0.1",
	      "dbname": "shop",
	      "user": "mcp_ro"
	    }
	  ]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := r.Resolve("orders-prod")
	if err != nil || got.Name != "orders-prod" {
		t.Fatalf("name: %v %#v", err, got)
	}
	got, _, err = r.Resolve("订单生产库")
	if err != nil || got.Name != "orders-prod" {
		t.Fatalf("alias: %v %#v", err, got)
	}
	got, _, err = r.Resolve("orders")
	if err != nil || got.Name != "orders-prod" {
		t.Fatalf("query: %v %#v", err, got)
	}
	_, hits, err := r.Resolve("prod")
	if err == nil || len(hits) != 2 {
		t.Fatalf("ambiguous: err=%v hits=%d", err, len(hits))
	}
}

func TestReloadOnMtime(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {"name":"a","host":"h","dbname":"d","user":"u"}
	  ]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.List()
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v %#v", err, got)
	}
	if err := os.WriteFile(p, []byte(`{
	  "targets": [
	    {"name":"a","host":"h","dbname":"d","user":"u"},
	    {"name":"b","host":"h","dbname":"d2","user":"u"}
	  ]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, st.ModTime().Add(time.Second), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = r.List()
	if err != nil || len(got) != 2 {
		t.Fatalf("want reload to 2, got %d err=%v", len(got), err)
	}
}

func TestReloadReportsJSONError(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {"name":"a","host":"h","dbname":"d","user":"u"}
	  ]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"targets":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, st.ModTime().Add(time.Second), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	list, err := r.List()
	if err == nil {
		t.Fatal("expected reload parse error")
	}
	if len(list) != 1 || list[0].Name != "a" {
		t.Fatalf("should keep last good list, got %#v", list)
	}
	_, _, rerr := r.Resolve("a")
	if rerr == nil {
		t.Fatal("Resolve should surface the reload error so callers do not query a stale switch")
	}
}

func TestDefaultSSLModeVerifyFull(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {"name":"a","host":"h","dbname":"d","user":"u"}
	  ]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if list[0].SSLMode != "verify-full" {
		t.Fatalf("default sslmode=%q", list[0].SSLMode)
	}
	if list[0].SSLModeOrDefault() != "verify-full" {
		t.Fatalf("SSLModeOrDefault=%q", list[0].SSLModeOrDefault())
	}
}

func TestRejectInsecureSSLOnProduction(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {"name":"orders-prod","environment":"production","host":"h","dbname":"d","user":"u","sslmode":"prefer"}
	  ]
	}`)
	if _, err := New(p); err == nil {
		t.Fatal("expected prefer on production to be rejected")
	}
}

func TestAllowPreferOnNonProd(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {"name":"local-dev","environment":"dev","host":"h","dbname":"d","user":"u","sslmode":"prefer"}
	  ]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.List()
	if err != nil || list[0].SSLMode != "prefer" {
		t.Fatalf("non-prod prefer: %v %#v", err, list)
	}
}

func TestIsProduction(t *testing.T) {
	cases := []struct {
		t    Target
		want bool
	}{
		{Target{Name: "x", Environment: "prod"}, true},
		{Target{Name: "x", Environment: "production"}, true},
		{Target{Name: "x", Environment: "PRD"}, true},
		{Target{Name: "x", Tags: []string{"production"}}, true},
		{Target{Name: "app-prod"}, true},
		{Target{Name: "app_prod"}, true},
		{Target{Name: "local-dev", Environment: "dev"}, false},
		{Target{Name: "x"}, false},
	}
	for _, c := range cases {
		if got := c.t.IsProduction(); got != c.want {
			t.Fatalf("%#v: got %v want %v", c.t, got, c.want)
		}
	}
}

func TestRejectPasswordInFile(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {"name":"a","host":"h","dbname":"d","user":"u","password":"secret"}
	  ]
	}`)
	if _, err := New(p); err == nil {
		t.Fatal("expected password rejection")
	}
}
