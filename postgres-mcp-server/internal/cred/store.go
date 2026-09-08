package cred

import (
	"fmt"

	"postgres-mcp-server/internal/targets"
)

func Password(t targets.Target) (string, error) {
	if t.CredentialRef != "" {
		if p, ok := lookupOS(t.CredentialRef); ok && p != "" {
			return p, nil
		}
	}
	p, err := lookupPgpass(t.Host, t.PortOrDefault(), t.DBName, t.User)
	if err != nil {
		return "", err
	}
	if p != "" {
		return p, nil
	}
	ref := t.CredentialRef
	if ref == "" {
		ref = "postgres/" + t.Name
	}
	return "", fmt.Errorf(
		"no password for target %s: store one in Windows Credential Manager as generic target %q, or add a matching line in pgpass.conf",
		t.Name, ref,
	)
}
