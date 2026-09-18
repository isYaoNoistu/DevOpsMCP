package targets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTarget() Target {
	return Target{Name: "uat", Brokers: []string{"localhost:9092", "[::1]:9092"}, Topics: []string{"orders-*"}, Groups: []string{"consumer.*"}}
}
func writeTargets(t *testing.T, p string, ts ...Target) {
	t.Helper()
	b, e := json.Marshal(map[string]any{"targets": ts})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestReloadFailsClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "targets.json")
	writeTargets(t, p, validTarget())
	r := New(p)
	if _, e := r.Resolve("uat"); e != nil {
		t.Fatal(e)
	}
	next := validTarget()
	next.Name = "prod"
	writeTargets(t, p, next)
	if _, e := r.Resolve("uat"); e == nil {
		t.Fatal("removed target still resolved")
	}
	if _, e := r.Resolve("prod"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(`{"targets":[{"sasl":{"password":"SECRET"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := r.List(); e == nil || strings.Contains(e.Error(), "SECRET") {
		t.Fatalf("unsafe error: %v", e)
	}
	if _, e := r.Resolve("prod"); e == nil {
		t.Fatal("stale fallback")
	}
}
func TestValidation(t *testing.T) {
	cases := map[string]func(*Target){
		"empty name": func(v *Target) { v.Name = "" }, "bad broker": func(v *Target) { v.Brokers = []string{"host"} },
		"bad port": func(v *Target) { v.Brokers = []string{"host:99999"} }, "empty brokers": func(v *Target) { v.Brokers = nil },
		"signed port":  func(v *Target) { v.Brokers = []string{"host:+9092"} },
		"invalid ipv6": func(v *Target) { v.Brokers = []string{"[not:ipv6]:9092"} },
		"empty topics": func(v *Target) { v.Topics = nil }, "empty groups": func(v *Target) { v.Groups = nil },
		"slash": func(v *Target) { v.Topics = []string{"../*"} }, "control in group": func(v *Target) { v.Groups = []string{"bad\nname"} },
		"empty pattern": func(v *Target) { v.Topics = []string{""} }, "cert pair": func(v *Target) { v.TLS = TLS{Enabled: true, CertFile: "secret-cert"} },
		"disabled tls fields": func(v *Target) { v.TLS.CAFile = "secret-ca" },
		"unsupported sasl":    func(v *Target) { v.SASL = SASL{Mechanism: "GSSAPI", Username: "u", Password: "SECRET"} },
		"partial sasl":        func(v *Target) { v.SASL = SASL{Username: "u", Password: "SECRET"} },
		"missing password":    func(v *Target) { v.SASL = SASL{Mechanism: "PLAIN", Username: "u"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "targets.json")
			v := validTarget()
			change(&v)
			writeTargets(t, p, v)
			_, e := New(p).List()
			if e == nil {
				t.Fatal("expected validation failure")
			}
			if strings.Contains(e.Error(), "SECRET") || strings.Contains(e.Error(), "secret-") {
				t.Fatalf("leaked secret: %v", e)
			}
		})
	}
}
func TestLimitsAndStrictJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "targets.json")
	r := New(p)
	writeTargets(t, p, validTarget(), validTarget())
	if _, e := r.List(); e == nil {
		t.Fatal("duplicate allowed")
	}
	for _, body := range []string{strings.Repeat(" ", 1024*1024+1), `{"targets":[],"skip_verify":true}`, `{"targets":[]} {}`} {
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := r.List(); e == nil {
			t.Fatal("invalid config allowed")
		}
	}
	ts := make([]Target, 101)
	for i := range ts {
		ts[i] = validTarget()
	}
	writeTargets(t, p, ts...)
	if _, e := r.List(); e == nil {
		t.Fatal("too many targets")
	}
}
func TestGroupNamesAreNotTopicNames(t *testing.T) {
	for _, name := range []string{"reader:uat", "team/reader", "reader@uat", "reader[1]"} {
		v := validTarget()
		v.Groups = []string{"*"}
		if !v.AllowsGroup(name) {
			t.Errorf("wildcard denied group %q", name)
		}
		v.Groups = []string{name}
		if err := validate(v); err != nil || !v.AllowsGroup(name) {
			t.Errorf("exact group %q: %v", name, err)
		}
		if v.AllowsGroup(name + "-other") {
			t.Errorf("exact group matched other name")
		}
	}
	v := validTarget()
	v.Groups = []string{"team/*"}
	if !v.AllowsGroup("team/reader:uat") || v.AllowsGroup("other/reader") {
		t.Fatal("group glob mismatch")
	}
	for _, name := range []string{"", "bad\nname", "bad\x00name"} {
		v.Groups = []string{"*"}
		if v.AllowsGroup(name) {
			t.Errorf("invalid group accepted %q", name)
		}
	}
}

func TestAllowlistAndPublicView(t *testing.T) {
	v := validTarget()
	v.SASL = SASL{Mechanism: "PLAIN", Username: "SECRETUSER", Password: "SECRETPASS"}
	v.TLS = TLS{Enabled: true, CAFile: "SECRETCA", CertFile: "SECRETCERT", KeyFile: "SECRETKEY"}
	if !v.AllowsTopic("orders-created") || v.AllowsTopic("other") || v.AllowsTopic("orders-../x") || !v.AllowsGroup("consumer.one") || v.AllowsGroup("other") {
		t.Fatal("incorrect allowlist match")
	}
	v.Topics = []string{"*"}
	for _, n := range []string{"", ".", "..", "a/b", "a\\b", "bad name"} {
		if v.AllowsTopic(n) {
			t.Fatalf("invalid name allowed: %q", n)
		}
	}
	b, e := json.Marshal(v.PublicView())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("public view leaked credentials")
	}
}
