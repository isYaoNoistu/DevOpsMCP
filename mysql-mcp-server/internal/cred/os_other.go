//go:build !windows

package cred

func lookupOS(string) (string, bool) { return "", false }
