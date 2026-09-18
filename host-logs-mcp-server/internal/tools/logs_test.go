package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"host-logs-mcp-server/internal/targets"
)

func TestTargetToolsNeverReturnPassword(t *testing.T) {
	const secret = "test-only-tool-password"
	p := filepath.Join(t.TempDir(), "targets.json")
	if err := os.WriteFile(p, []byte(`{"targets":[{"name":"prod","host":"example.com","user":"reader","password":"`+secret+`","private_key":"test-only-private-key","private_key_passphrase":"test-only-passphrase","paths":["/var/log/nginx"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	reg, err := targets.New(p)
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Reg: reg}
	result, structured, err := d.ListTargets(context.Background(), nil, ListTargetsInput{})
	if err != nil {
		t.Fatal(err)
	}
	info, infoStructured, err := d.GetTargetInfo(context.Background(), nil, GetTargetInfoInput{Target: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{result, structured, info, infoStructured} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "test-only-private-key") || strings.Contains(string(raw), "test-only-passphrase") {
			t.Fatal("MCP target result leaked credentials")
		}
	}
}
