package targets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestResolveAndAlias(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [
	    {
	      "name": "db-prod",
	      "aliases": ["库主机"],
	      "host": "db.example.com",
	      "user": "mcp_logs",
	      "paths": ["/data/postgresql/log"],
	      "tags": ["postgres"]
	    }
	  ]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := r.Resolve("db-prod")
	if err != nil || got.Name != "db-prod" {
		t.Fatalf("%v %#v", err, got)
	}
	got, _, err = r.Resolve("库主机")
	if err != nil || got.Name != "db-prod" {
		t.Fatalf("alias %v %#v", err, got)
	}
}

func TestPasswordTargetAndPublicView(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [{"name":"生产环境","host":"h","user":"u","paths":["/var/log/nginx"],"password":" secret with spaces "}]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := r.Resolve("生产环境")
	if err != nil || target.Password != " secret with spaces " || target.PortOrDefault() != 22 {
		t.Fatal("password must load unchanged and port must default to 22")
	}
	for _, value := range []any{target.PublicView(), target} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), target.Password) || strings.Contains(string(raw), `"password":`) {
			t.Fatal("target serialization exposed password")
		}
	}
	if hits := Filter(r.Snapshot(), "secret with spaces"); len(hits) != 0 {
		t.Fatal("password must not be searchable")
	}
}

func TestRejectBroadPath(t *testing.T) {
	p := writeTargets(t, t.TempDir(), `{
	  "targets": [{"name":"x","host":"h","user":"u","paths":["/"]}]
	}`)
	if _, err := New(p); err == nil {
		t.Fatal("expected reject /")
	}
}

func TestRejectInvalidPasswordTargets(t *testing.T) {
	for _, extra := range []string{`"port":65536`, `"port":-1`, `"transport":"local"`, `"host_key_sha256":"invalid"`} {
		p := writeTargets(t, t.TempDir(), `{"targets":[{"name":"x","host":"h","user":"u","paths":["/var/log/nginx"],"password":"test-only-password",`+extra+`}]}`)
		if _, err := New(p); err == nil || strings.Contains(err.Error(), "test-only-password") {
			t.Fatalf("expected safe validation error for %s", extra)
		}
	}
}

func TestInlineKeyRegistryAndAuthSelection(t *testing.T) {
	for _, credentials := range []string{
		`"private_key":"test-only-inline-key"`,
		`"private_key":"test-only-inline-key","password":"test-only-pass"`,
		`"identity_file":"key.pem","password":"test-only-pass"`,
	} {
		p := writeTargets(t, t.TempDir(), `{"targets":[{"name":"keys","host":"example.com","user":"reader","paths":["/var/log/nginx"],`+credentials+`}]}`)
		r, err := New(p)
		if err != nil {
			t.Fatal(err)
		}
		target, _, err := r.Resolve("keys")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(target)
		if strings.Contains(string(raw), "test-only") {
			t.Fatal("registry exposed credential")
		}
		if !strings.Contains(string(raw), "private_key") && strings.Contains(credentials, "private_key") {
			t.Fatal("inline key auth mode not reported")
		}
	}
}

func TestRejectConflictingOrIncompleteKeyConfiguration(t *testing.T) {
	for _, extra := range []string{
		`"private_key":"test-only-key","identity_file":"key.pem"`,
		`"private_key_passphrase":"test-only-passphrase"`,
		`"private_key":"test-only-key","transport":"local"`,
	} {
		p := writeTargets(t, t.TempDir(), `{"targets":[{"name":"keys","host":"example.com","user":"reader","paths":["/var/log/nginx"],`+extra+`}]}`)
		_, err := New(p)
		if err == nil || strings.Contains(err.Error(), "test-only") {
			t.Fatal("expected safe key configuration error")
		}
	}
	if (Target{IdentityFile: "key.pem"}).UsesBuiltinSSH() {
		t.Fatal("legacy key-file-only mode must retain OpenSSH")
	}
}

func TestReloadKeepsPreviousOnBadJSON(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [{"name":"db-prod","host":"db.example.com","user":"mcp_logs","paths":["/data/postgresql/log"]}]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	list, err := r.List()
	if err == nil {
		t.Fatal("expected parse error")
	}
	if len(list) != 1 || list[0].Name != "db-prod" {
		t.Fatalf("should keep previous list: %v %#v", err, list)
	}
}

func TestLocalTransport(t *testing.T) {
	dir := t.TempDir()
	raw, err := json.Marshal(map[string]any{
		"targets": []map[string]any{{
			"name":      "lab-logs",
			"host":      "local",
			"transport": "local",
			"paths":     []string{dir},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := writeTargets(t, dir, string(raw))
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := r.Resolve("lab-logs")
	if err != nil || !got.IsLocal() {
		t.Fatalf("%v %#v", err, got)
	}
}

func TestSSHTransportNotLocalEvenIfHostLocal(t *testing.T) {
	dir := t.TempDir()
	p := writeTargets(t, dir, `{
	  "targets": [{
	    "name": "named-local",
	    "host": "local",
	    "user": "mcp_logs",
	    "transport": "ssh",
	    "paths": ["/data/postgresql/log"]
	  }]
	}`)
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := r.Resolve("named-local")
	if err != nil {
		t.Fatal(err)
	}
	if got.IsLocal() {
		t.Fatal("explicit transport=ssh must not be treated as local")
	}
}
