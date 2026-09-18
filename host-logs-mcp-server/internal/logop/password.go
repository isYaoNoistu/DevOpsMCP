package logop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
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
	dst     io.Writer
	secret  string
	pending string
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	if w.secret == "" {
		return w.dst.Write(p)
	}
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

func builtinHostKey(t targets.Target, cfg SSHConfig) (ssh.HostKeyCallback, error) {
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
	// Built-in auth is a two-file setup: first-use fingerprints are system-managed.
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

func runBuiltinSSH(ctx context.Context, t targets.Target, cfg SSHConfig, remote string) (out string, retErr error) {
	methods, secrets, authErr := builtinAuth(t)
	defer func() {
		if retErr != nil {
			message := retErr.Error()
			for _, secret := range secrets {
				message = strings.ReplaceAll(message, secret, "[redacted]")
			}
			if len(message) > limits.MaxBytes {
				message = message[:limits.MaxBytes] + "...[truncated]"
			}
			retErr = errors.New(message)
		}
	}()
	if authErr != nil {
		return "", authErr
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()
	verify, err := builtinHostKey(t, cfg)
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
		Auth:            methods,
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
	output, flushOutput := redactedOutput(&stdout, secrets)
	errorsOutput, flushErrors := redactedOutput(&stderr, secrets)
	session.Stdout = output
	session.Stderr = errorsOutput
	err = session.Run(remote)
	flushOutput()
	flushErrors()
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

// Valid keys are tried first; a rejected key can fall back to a supplied password.
// Malformed credentials fail locally instead of silently masking a configuration error.
func builtinAuth(t targets.Target) ([]ssh.AuthMethod, []string, error) {
	key := t.PrivateKey
	if key == "" && t.IdentityFile != "" {
		raw, err := os.ReadFile(t.IdentityFile)
		if err != nil {
			return nil, nil, fmt.Errorf("ssh: cannot read private key file")
		}
		key = string(raw)
	}
	secrets := []string{t.Password, t.PrivateKeyPassphrase, key}
	// Mask individual PEM payload lines as well, even when a log reformats newlines.
	for _, line := range strings.Split(key, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "-----") {
			secrets = append(secrets, line)
		}
	}
	unique := map[string]bool{}
	clean := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" && !unique[secret] {
			unique[secret] = true
			clean = append(clean, secret)
		}
	}
	sort.SliceStable(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	var methods []ssh.AuthMethod
	if key != "" || t.IdentityFile != "" {
		var signer ssh.Signer
		var err error
		if t.PrivateKeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(key), []byte(t.PrivateKeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(key))
		}
		if err != nil {
			return nil, clean, fmt.Errorf("ssh: invalid private key or missing/incorrect private_key_passphrase")
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if t.Password != "" {
		methods = append(methods, ssh.Password(t.Password))
	}
	if len(methods) == 0 {
		return nil, clean, fmt.Errorf("ssh: password or private key is required")
	}
	return methods, clean, nil
}

func redactedOutput(dst io.Writer, secrets []string) (io.Writer, func()) {
	writers := make([]*redactingWriter, len(secrets))
	for i := len(secrets) - 1; i >= 0; i-- {
		writers[i] = &redactingWriter{dst: dst, secret: secrets[i]}
		dst = writers[i]
	}
	return dst, func() {
		for _, writer := range writers {
			writer.flush()
		}
	}
}
