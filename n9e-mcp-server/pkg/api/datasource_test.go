package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n9e/n9e-mcp-server/pkg/client"
	"github.com/n9e/n9e-mcp-server/pkg/types"
)

func TestSafeDatasourceOmitsCredentials(t *testing.T) {
	ds := types.Datasource{Id: 1, Name: "logs", PluginType: "loki", Settings: map[string]any{"token": "secret"}}
	ds.HTTP.Url = "https://user:password@logs.example.com/path?token=secret#fragment"
	ds.HTTP.Headers = map[string]string{"Authorization": "secret"}
	ds.Auth.BasicAuthPassword = "secret"
	b, err := json.Marshal(safeDatasource(ds))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, forbidden := range []string{"secret", "settings", "headers", "basic_auth_password", "client_key"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("safe datasource contains %q: %s", forbidden, s)
		}
	}
	if !strings.Contains(s, `"url":"https://logs.example.com/path"`) {
		t.Fatalf("safe URL missing: %s", s)
	}
}

func TestGetDatasourceReturnsOnlySafeView(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":1,"name":"logs","plugin_type":"loki","http":{"url":"https://user:password@logs.example.com/path?token=secret","headers":{"Authorization":"secret"},"tls":{"client_key":"secret"}},"auth":{"basic_auth_password":"secret"},"settings":{"token":"secret"}},"error":null}`))
	}))
	defer srv.Close()
	c, err := client.NewClient("token", srv.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, getDatasourceTool(func(context.Context) *client.Client { return c }), `{"id":1}`)
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	text := resultText(res)
	for _, forbidden := range []string{"secret", "password", "headers", "settings", "client_key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("result contains %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "https://logs.example.com/path") {
		t.Fatalf("sanitized URL missing: %s", text)
	}
}
