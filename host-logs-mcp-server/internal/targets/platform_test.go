package targets

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlatformCredentialsWithoutFiles(t *testing.T) {
	t.Setenv("HOST_LOGS_TARGETS_JSON", `{"targets":[{"name":"test","host":"host.example.com","user":"reader","password":"test-secret","paths":["/var/log/nginx"],"host_key_sha256":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]}`)
	r, err := New("/does/not/exist")
	if err != nil {
		t.Fatal(err)
	}
	all, err := r.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("load: %v", err)
	}
	if all[0].Password != "test-secret" {
		t.Fatal("credential lost")
	}
	b, _ := json.Marshal(all[0])
	if strings.Contains(string(b), "test-secret") {
		t.Fatal("secret exposed")
	}
}
func TestInvalidPlatformDoesNotFallBack(t *testing.T) {
	t.Setenv("HOST_LOGS_TARGETS_JSON", " ")
	if _, err := New("/does/not/exist"); err == nil {
		t.Fatal("empty platform config accepted")
	}
}

func TestPlatformErrorDoesNotExposeSecret(t *testing.T) {
	t.Setenv("HOST_LOGS_TARGETS_JSON", `{"targets":[{"name":"SECRET_MARKER"}]}`)
	_, err := New("")
	if err == nil || strings.Contains(err.Error(), "SECRET_MARKER") {
		t.Fatal("unsafe configuration error")
	}
}
