package logop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"host-logs-mcp-server/internal/limits"
	"host-logs-mcp-server/internal/targets"
)

// Redact across SSH packet boundaries before enforcing the byte limit. Otherwise
// truncation can split a secret and leave a nearly complete password in output.
type redactingWriter struct {
	dst     *limitedBuffer
	secret  string
	pending string
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	data := w.pending + string(p)
	for {
		i := strings.Index(data, w.secret)
		if i < 0 {
			break
		}
		_, _ = w.dst.Write([]byte(data[:i]))
		_, _ = w.dst.Write([]byte("[redacted]"))
		data = data[i+len(w.secret):]
	}
	cut := max(0, len(data)-len(w.secret)+1)
	_, _ = w.dst.Write([]byte(data[:cut]))
	w.pending = data[cut:]
	return len(p), nil
}

func (w *redactingWriter) flush() {
	_, _ = w.dst.Write([]byte(w.pending))
	w.pending = ""
}

// Serialize first-use trust updates from concurrent calls in this process.
var hostTrustMu sync.Mutex

func passwordHostKey(t targets.Target, cfg SSHConfig) (ssh.HostKeyCallback, error) {
	if t.HostKeySHA256 != "" {
		return func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if ssh.FingerprintSHA256(key) != t.HostKeySHA256 {
				return fmt.Errorf("host key fingerprint mismatch")
			}
			return nil
		}, nil
	}
	path := cfg.KnownHostsFile
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("host key trust location: %w", err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	// Password mode is a two-file setup: first-use fingerprints are system-managed.
	// Explicit strict=yes keeps the existing pre-provisioned known_hosts workflow.
	acceptNew := strings.TrimSpace(cfg.StrictHostKey) == "" || cfg.strict() == "accept-new"
	return func(host string, addr net.Addr, key ssh.PublicKey) error {
		hostTrustMu.Lock()
		defer hostTrustMu.Unlock()
		check, err := knownhosts.New(path)
		if err == nil {
			err = check(host, addr, key)
			if err == nil {
				return nil
			}
			var missing *knownhosts.KeyError
			if !errors.As(err, &missing) || len(missing.Want) != 0 {
				return fmt.Errorf("host key verification failed: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("host key trust file: %w", err)
		}
		if !acceptNew {
			return fmt.Errorf("host key is unknown; verify it and add it to known_hosts or set host_key_sha256")
		}
		if _, certificate := key.(*ssh.Certificate); certificate {
			return fmt.Errorf("host key certificate requires a trusted CA in known_hosts")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("host key trust directory: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("host key trust file: %w", err)
		}
		// Leading newline also handles a pre-existing file without a trailing newline.
		prefix := ""
		if raw, readErr := os.ReadFile(path); readErr != nil {
			_ = f.Close()
			return fmt.Errorf("host key trust file: %w", readErr)
		} else if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			prefix = "\n"
		}
		_, writeErr := f.WriteString(prefix + knownhosts.Line([]string{knownhosts.Normalize(host)}, key) + "\n")
		if writeErr == nil {
			writeErr = f.Sync()
		}
		closeErr := f.Close()
		if writeErr != nil {
			return fmt.Errorf("host key trust save: %w", writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("host key trust close: %w", closeErr)
		}
		return nil
	}, nil
}

func runPasswordSSH(ctx context.Context, t targets.Target, cfg SSHConfig, remote string) (out string, retErr error) {
	defer func() {
		if retErr != nil {
			message := strings.ReplaceAll(retErr.Error(), t.Password, "[redacted]")
			if len(message) > limits.MaxBytes {
				message = message[:limits.MaxBytes] + "...[truncated]"
			}
			retErr = errors.New(message)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()
	verify, err := passwordHostKey(t, cfg)
	if err != nil {
		return "", fmt.Errorf("ssh: %w", err)
	}
	address := net.JoinHostPort(t.Host, strconv.Itoa(t.PortOrDefault()))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", fmt.Errorf("ssh: connect: %w", err)
	}
	defer conn.Close()
	// Covers handshake, session creation, execution, and cancellation, not just dial.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	clientConn, channels, requests, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{
		User:            t.User,
		Auth:            []ssh.AuthMethod{ssh.Password(t.Password)},
		HostKeyCallback: verify,
	})
	if err != nil {
		return "", fmt.Errorf("ssh: handshake/authentication: %w", err)
	}
	client := ssh.NewClient(clientConn, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh: session: %w", err)
	}
	defer session.Close()
	var stdout, stderr limitedBuffer
	output := &redactingWriter{dst: &stdout, secret: t.Password}
	errorsOutput := &redactingWriter{dst: &stderr, secret: t.Password}
	session.Stdout = output
	session.Stderr = errorsOutput
	err = session.Run(remote)
	output.flush()
	errorsOutput.flush()
	out = stdout.String()
	if stdout.truncated {
		out += "\n...[truncated]\n"
	}
	if ctx.Err() != nil {
		return out, fmt.Errorf("ssh: %w", ctx.Err())
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return out, fmt.Errorf("ssh: %s", message)
	}
	return out, nil
}
