package internal

import (
	"strings"
	"testing"
)

func TestNewMCPServerRequiresReadOnly(t *testing.T) {
	_, err := NewMCPServer(ServerConfig{
		Version:  "test",
		Token:    "x",
		BaseURL:  "http://127.0.0.1:17000",
		ReadOnly: false,
	})
	if err == nil || !strings.Contains(err.Error(), "N9E_READ_ONLY") {
		t.Fatalf("expected fail-closed read-only, got %v", err)
	}
}
