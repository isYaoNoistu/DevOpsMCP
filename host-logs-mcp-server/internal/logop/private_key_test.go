package logop

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"host-logs-mcp-server/internal/targets"
)

func testClientKey(t *testing.T, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(key, "test-only")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "test-only", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block)), signer.PublicKey()
}

func withInlineKey(t *testing.T, target targets.Target, key, passphrase, password string) targets.Target {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"name": target.Name, "host": target.Host, "port": target.Port, "user": target.User, "paths": target.Paths, "password": password, "private_key": key, "private_key_passphrase": passphrase})
	if err != nil {
		t.Fatal(err)
	}
	var decoded targets.Target
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestInlineKeyOnlyAndEncryptedKey(t *testing.T) {
	for _, passphrase := range []string{"", "test-only-key-passphrase"} {
		t.Run(passphrase, func(t *testing.T) {
			key, pub := testClientKey(t, passphrase)
			target, cfg, auths, commands, _ := passwordFixture(t, "key login succeeded\n", 0, false, pub)
			target = withInlineKey(t, target, key, passphrase, "")
			out, err := runSSH(context.Background(), target, cfg, "fixed-command")
			if err != nil || out != "key login succeeded\n" || auths.Load() == 0 {
				t.Fatalf("key login failed: %v", err)
			}
			if <-commands != "fixed-command" {
				t.Fatal("unexpected command")
			}
			raw, _ := json.Marshal(target)
			if strings.Contains(string(raw), "PRIVATE KEY") || (passphrase != "" && strings.Contains(string(raw), passphrase)) {
				t.Fatal("private key metadata leak")
			}
		})
	}
}

func TestInlineKeyFallsBackToPassword(t *testing.T) {
	key, _ := testClientKey(t, "")
	_, accepted := testClientKey(t, "")
	target, cfg, auths, _, _ := passwordFixture(t, "password fallback\n", 0, false, accepted)
	target = withInlineKey(t, target, key, "", testPassword)
	out, err := runSSH(context.Background(), target, cfg, "fixed-command")
	if err != nil || out != "password fallback\n" || auths.Load() < 2 {
		t.Fatalf("key then password fallback failed: %v", err)
	}
}

func TestIdentityFileAndPasswordCanCoexist(t *testing.T) {
	key, pub := testClientKey(t, "")
	target, cfg, _, _, _ := passwordFixture(t, "file key\n", 0, false, pub)
	target.Password = "wrong-test-password"
	target.IdentityFile = filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(target.IdentityFile, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := runSSH(context.Background(), target, cfg, "fixed-command")
	if err != nil || out != "file key\n" {
		t.Fatalf("file key not used before password: %v", err)
	}
}

func TestPrivateKeyAndPassphraseRedacted(t *testing.T) {
	const passphrase = "test-only-key-passphrase"
	key, pub := testClientKey(t, passphrase)
	body := strings.Split(strings.TrimSpace(key), "\n")[1]
	target, cfg, _, _, _ := passwordFixture(t, key+"\n"+body+"\n"+passphrase+"\n"+testPassword, 0, false, pub)
	target = withInlineKey(t, target, key, passphrase, testPassword)
	out, err := runSSH(context.Background(), target, cfg, "fixed-command")
	if err != nil || strings.Contains(out, body) || strings.Contains(out, passphrase) || strings.Contains(out, testPassword) {
		t.Fatal("key/password output redaction failed")
	}
}

func TestInvalidPrivateKeyFailsBeforeConnect(t *testing.T) {
	target := targets.Target{Name: "test", Host: "127.0.0.1", Port: 1, User: "reader"}
	target = withInlineKey(t, target, "test-only-invalid-key", "", testPassword)
	_, err := runSSH(context.Background(), target, SSHConfig{}, "fixed-command")
	if err == nil || !strings.Contains(err.Error(), "private key") || strings.Contains(err.Error(), "test-only-invalid-key") {
		t.Fatalf("expected safe key parsing failure, got %v", err)
	}
}

func TestEncryptedPrivateKeyWrongPassphrase(t *testing.T) {
	key, _ := testClientKey(t, "test-only-correct-passphrase")
	target := withInlineKey(t, targets.Target{Host: "127.0.0.1", Port: 1, User: "reader"}, key, "test-only-wrong-passphrase", "")
	_, err := runSSH(context.Background(), target, SSHConfig{}, "fixed-command")
	if err == nil || !strings.Contains(err.Error(), "private_key_passphrase") || strings.Contains(err.Error(), "test-only") {
		t.Fatal("encrypted key must fail safely before connect")
	}
}

func TestEmptyKeyFileFailsBeforePasswordFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-key")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := builtinAuth(targets.Target{IdentityFile: path, Password: testPassword})
	if err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatal("empty key file must report invalid key")
	}
}
