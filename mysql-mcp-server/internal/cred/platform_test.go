package cred

import (
	"mysql-mcp-server/internal/targets"
	"testing"
)

func TestPlatformPasswordDoesNotUsePasswordFile(t *testing.T) {
	t.Setenv("MYSQL_PASSFILE", "/does/not/exist")
	got, err := Password(targets.Target{InlineCredential: true, Password: " space : \" secret "})
	if err != nil || got != " space : \" secret " {
		t.Fatalf("inline password changed or unavailable")
	}
}
