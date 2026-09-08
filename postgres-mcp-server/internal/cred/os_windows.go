//go:build windows

package cred

import "github.com/danieljoos/wincred"

func lookupOS(ref string) (string, bool) {
	c, err := wincred.GetGenericCredential(ref)
	if err != nil || c == nil {
		return "", false
	}
	if len(c.CredentialBlob) == 0 {
		return "", false
	}
	return string(c.CredentialBlob), true
}
