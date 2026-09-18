package logop

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"host-logs-mcp-server/internal/limits"
	"host-logs-mcp-server/internal/targets"
)

const testPassword = "test-only password ' \" $ & 123"

func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// A loopback SSH fixture: no real host, shell, credentials, or filesystem access.
func passwordFixture(t *testing.T, output string, status uint32, stall bool) (targets.Target, SSHConfig, *atomic.Int32, <-chan string, ssh.Signer) {
	t.Helper()
	signer := newTestSigner(t)
	auths := &atomic.Int32{}
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
		auths.Add(1)
		if c.User() != "reader" || string(pass) != testPassword {
			return nil, fmt.Errorf("authentication rejected")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	commands := make(chan string, 10)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					if channel.ChannelType() != "session" {
						_ = channel.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, reqs, err := channel.Accept()
					if err != nil {
						return
					}
					for req := range reqs {
						if req.Type != "exec" {
							_ = req.Reply(false, nil)
							continue
						}
						var cmd struct{ Command string }
						if ssh.Unmarshal(req.Payload, &cmd) != nil {
							return
						}
						commands <- cmd.Command
						_ = req.Reply(true, nil)
						if stall {
							_ = server.Wait()
							return
						}
						_, _ = io.WriteString(ch, output)
						if status != 0 {
							_, _ = io.WriteString(ch.Stderr(), testPassword+" remote failure")
						}
						_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						_ = ch.Close()
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	target := targets.Target{Name: "test", Host: host, Port: p, User: "reader", Password: testPassword, Transport: "ssh", Paths: []string{"/var/log/nginx"}}
	cfg := SSHConfig{Timeout: 2 * time.Second, KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts")}
	return target, cfg, auths, commands, signer
}

func TestPasswordSSHSuccessAndTrustPersistence(t *testing.T) {
	target, cfg, auths, commands, _ := passwordFixture(t, "hello\n", 0, false)
	for i := 0; i < 2; i++ {
		out, err := runSSH(context.Background(), target, cfg, "fixed-command")
		if err != nil || out != "hello\n" {
			t.Fatalf("out=%q err=%v", out, err)
		}
		if got := <-commands; got != "fixed-command" {
			t.Fatal("command changed")
		}
	}
	if auths.Load() != 2 {
		t.Fatal("password authentication not used")
	}
	raw, err := os.ReadFile(cfg.KnownHostsFile)
	if err != nil || strings.Count(string(raw), "\n") != 1 || strings.Contains(string(raw), testPassword) {
		t.Fatal("host trust must persist once without credentials")
	}
}

func TestPasswordSSHRejectsUnknownStrictAndChangedHostBeforeAuth(t *testing.T) {
	for _, mode := range []string{"strict", "changed"} {
		t.Run(mode, func(t *testing.T) {
			target, cfg, auths, _, _ := passwordFixture(t, "", 0, false)
			if mode == "strict" {
				cfg.StrictHostKey = "yes"
			} else {
				line := knownhosts.Line([]string{knownhosts.Normalize(net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))}, newTestSigner(t).PublicKey()) + "\n"
				if err := os.WriteFile(cfg.KnownHostsFile, []byte(line), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := runSSH(context.Background(), target, cfg, "fixed-command")
			if err == nil || !strings.Contains(err.Error(), "host key") {
				t.Fatalf("expected host key error, got %v", err)
			}
			if auths.Load() != 0 {
				t.Fatal("password sent before host trust checked")
			}
		})
	}
}

func TestPasswordSSHBadPasswordAndOutputRedaction(t *testing.T) {
	target, cfg, _, _, _ := passwordFixture(t, testPassword+" failure\n", 2, false)
	out, err := runSSH(context.Background(), target, cfg, "fixed-command")
	if err == nil || strings.Contains(out, testPassword) || strings.Contains(err.Error(), testPassword) {
		t.Fatal("error/output must redact password")
	}
	target.Password = "wrong-test-password"
	_, err = runSSH(context.Background(), target, cfg, "fixed-command")
	if err == nil || strings.Contains(err.Error(), target.Password) {
		t.Fatal("expected safe authentication failure")
	}
}

func TestPasswordSSHTimeoutAndOutputCap(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		target, cfg, _, _, _ := passwordFixture(t, "", 0, true)
		cfg.Timeout = 250 * time.Millisecond
		start := time.Now()
		_, err := runSSH(context.Background(), target, cfg, "fixed-command")
		if err == nil || time.Since(start) > 2*time.Second || time.Since(start) < 150*time.Millisecond {
			t.Fatalf("deadline not enforced: %v", err)
		}
	})
	t.Run("cap", func(t *testing.T) {
		target, cfg, _, _, _ := passwordFixture(t, strings.Repeat("x", limits.MaxBytes+1000), 0, false)
		out, err := runSSH(context.Background(), target, cfg, "fixed-command")
		if err != nil || len(out) > limits.MaxBytes+30 || !strings.Contains(out, "[truncated]") {
			t.Fatalf("output cap failed: %v", err)
		}
	})
}

func TestPasswordSSHPinnedHostKey(t *testing.T) {
	for _, matches := range []bool{true, false} {
		t.Run(strconv.FormatBool(matches), func(t *testing.T) {
			target, cfg, auths, _, signer := passwordFixture(t, "pinned\n", 0, false)
			if !matches {
				signer = newTestSigner(t)
			}
			target.HostKeySHA256 = ssh.FingerprintSHA256(signer.PublicKey())
			out, err := runSSH(context.Background(), target, cfg, "fixed-command")
			if matches && (err != nil || out != "pinned\n") {
				t.Fatalf("pin rejected: %v", err)
			}
			if !matches && (err == nil || auths.Load() != 0) {
				t.Fatal("mismatched pin must reject before authentication")
			}
			if _, err := os.Stat(cfg.KnownHostsFile); !os.IsNotExist(err) {
				t.Fatal("pin should not require a trust cache file")
			}
		})
	}
}

func TestPasswordSSHCancelledHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	target := targets.Target{Host: host, Port: p, User: "reader", Password: testPassword}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := runSSH(ctx, target, SSHConfig{Timeout: 5 * time.Second}, "fixed-command")
		finished <- err
	}()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("client did not connect")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt handshake")
	}
}

func TestPasswordRedactionBeforeOutputCap(t *testing.T) {
	for _, secret := range []string{"a", "test-only-secret"} {
		var dst limitedBuffer
		writer := &redactingWriter{dst: &dst, secret: secret}
		payload := strings.Repeat("x", limits.MaxBytes-len(secret)+1) + secret + "suffix"
		for i := 0; i < len(payload); i += 7 {
			end := min(i+7, len(payload))
			_, _ = writer.Write([]byte(payload[i:end]))
		}
		writer.flush()
		if dst.Len() > limits.MaxBytes || strings.Contains(dst.String(), "test-only-secre") {
			t.Fatal("cap split leaked secret or grew output")
		}
		var repeated limitedBuffer
		w := &redactingWriter{dst: &repeated, secret: secret}
		_, _ = w.Write([]byte(strings.Repeat(secret, limits.MaxBytes)))
		w.flush()
		if repeated.Len() > limits.MaxBytes || !repeated.truncated {
			t.Fatal("redaction expansion must be capped")
		}
	}
}
