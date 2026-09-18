package logop

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"host-logs-mcp-server/internal/limits"
	"host-logs-mcp-server/internal/pathguard"
	"host-logs-mcp-server/internal/quote"
	"host-logs-mcp-server/internal/targets"
)

type SSHConfig struct {
	Timeout        time.Duration
	StrictHostKey  string
	KnownHostsFile string
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Len() int       { return b.buffer.Len() }
func (b *limitedBuffer) String() string { return b.buffer.String() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	remaining := limits.MaxBytes - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return n, nil
	}
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, err := b.buffer.Write(p)
	return n, err
}

func (c SSHConfig) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 20 * time.Second
	}
	return c.Timeout
}

func (c SSHConfig) strict() string {
	s := strings.ToLower(strings.TrimSpace(c.StrictHostKey))
	switch s {
	case "", "yes", "true":
		return "yes"
	case "accept-new":
		return "accept-new"
	default:
		return "yes"
	}
}

func sshBaseArgs(t targets.Target, cfg SSHConfig) []string {
	args := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ClearAllForwardings=yes",
		"-o", "StrictHostKeyChecking=" + cfg.strict(),
		"-o", "ConnectTimeout=" + strconv.Itoa(int(cfg.timeout().Seconds())),
		"-p", strconv.Itoa(t.PortOrDefault()),
	}
	if t.IdentityFile != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", t.IdentityFile)
	}
	if cfg.KnownHostsFile != "" {
		args = append(args, "-o", "UserKnownHostsFile="+cfg.KnownHostsFile)
	}
	args = append(args, fmt.Sprintf("%s@%s", t.User, t.Host))
	return args
}

func runSSH(ctx context.Context, t targets.Target, cfg SSHConfig, remote string) (string, error) {
	if t.IsLocal() {
		return "", fmt.Errorf("internal: ssh runner used on local target")
	}
	if t.UsesBuiltinSSH() {
		return runBuiltinSSH(ctx, t, cfg, remote)
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()
	args := append(sshBaseArgs(t, cfg), remote)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout limitedBuffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if stdout.truncated {
		out += "\n...[truncated]\n"
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("ssh: %s", msg)
	}
	return out, nil
}

func resolveRoots(roots []string) string {
	var b strings.Builder
	for i, r := range roots {
		fmt.Fprintf(&b, "r%d=$(readlink -f -- %s) || exit 1; ", i, quote.POSIX(r))
	}
	return b.String()
}

func caseRealRoots(n int) string {
	var pats []string
	for i := 0; i < n; i++ {
		pats = append(pats, fmt.Sprintf(`"$r%d"|"$r%d"/*`, i, i))
	}
	return "case $canon in " + strings.Join(pats, "|") +
		`) ;; *) printf '%s\n' 'outside allowlist' >&2; exit 1 ;; esac`
}

func resolvePrelude(lexical string, roots []string) string {
	return fmt.Sprintf(
		`%s canon=$(readlink -f -- %s) || exit 1; test -e "$canon" || exit 1; %s`,
		resolveRoots(roots),
		quote.POSIX(lexical),
		caseRealRoots(len(roots)),
	)
}

func bashPipefail(script string) string {
	return "bash -o pipefail -c " + quote.POSIX(script)
}

func grepCmd(opt SearchOpts) string {
	grep := "grep -a -E -n"
	if opt.Fixed {
		grep = "grep -a -F -n"
	}
	if opt.ContextBefore > 0 {
		grep += fmt.Sprintf(" -B %d", clampCtx(opt.ContextBefore))
	}
	if opt.ContextAfter > 0 {
		grep += fmt.Sprintf(" -A %d", clampCtx(opt.ContextAfter))
	}
	grep += " -- " + quote.POSIX(opt.Pattern)
	return grep
}

func checkedGrep(command string) string {
	return fmt.Sprintf(`{ %s; rc=$?; case $rc in 0|1) exit 0 ;; *) exit "$rc" ;; esac; }`, command)
}

// BuildListRemote is the remote find pipeline (for tests). Uses find -H so a
// symlink root still lists, but does not follow -L into other trees.
func BuildListRemote(t targets.Target, dir string, max int) (string, error) {
	if max <= 0 || max > limits.ListMax {
		max = limits.ListMax
	}
	root := strings.TrimSpace(dir)
	var starts []string
	if root == "" {
		if len(t.Paths) == 0 {
			return "", fmt.Errorf("no allowlisted paths")
		}
		starts = append(starts, t.Paths...)
	} else {
		resolved, err := ResolvePath(t, root)
		if err != nil {
			return "", err
		}
		starts = []string{resolved}
	}
	var parts []string
	for _, p := range starts {
		parts = append(parts, fmt.Sprintf(
			`%s; find -H %s -maxdepth %d -type f`,
			resolvePrelude(p, t.Paths), quote.POSIX(p), limits.MaxDepth,
		))
	}
	if len(parts) == 1 {
		return bashPipefail(fmt.Sprintf("%s | awk 'NR <= %d'", parts[0], max)), nil
	}
	return bashPipefail(fmt.Sprintf("{ %s || exit 1; } | awk 'NR <= %d'", strings.Join(parts, " || exit 1; "), max)), nil
}

func ListRemote(ctx context.Context, t targets.Target, cfg SSHConfig, dir string, max int) (string, error) {
	remote, err := BuildListRemote(t, dir, max)
	if err != nil {
		return "", err
	}
	out, err := runSSH(ctx, t, cfg, remote)
	if err != nil {
		return "", err
	}
	return filterListed(out, t.Paths), nil
}

func filterListed(out string, roots []string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, _, err := pathguard.ResolveUnix(line, roots); err != nil {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// BuildSearchRemote is the remote search pipeline (for tests).
func BuildSearchRemote(t targets.Target, opt SearchOpts) (string, error) {
	lexical, err := ResolvePath(t, opt.Path)
	if err != nil {
		return "", err
	}
	if err := quote.CheckRemoteArg("pattern", opt.Pattern, limits.MaxPattern); err != nil {
		return "", err
	}
	if err := quote.CheckRemoteArg("start", opt.Start, 128); err != nil {
		return "", err
	}
	if err := quote.CheckRemoteArg("end", opt.End, 128); err != nil {
		return "", err
	}
	n := limits.ClampLines(opt.MaxLines, limits.DefaultLines, limits.MaxLines)
	prelude := resolvePrelude(lexical, t.Paths)
	src := `{ case $canon in *.gz|*.GZ) gzip -dc -- "$canon" ;; *) cat -- "$canon" ;; esac; }`
	if opt.Start != "" || opt.End != "" {
		src += fmt.Sprintf(
			` | START=%s END=%s awk '(ENVIRON["START"]=="" || $0>=ENVIRON["START"]) && (ENVIRON["END"]=="" || $0<ENVIRON["END"]) {print}'`,
			quote.POSIX(opt.Start), quote.POSIX(opt.End),
		)
	}
	search := fmt.Sprintf(`%s | %s | awk 'NR <= %d'`, src, checkedGrep(grepCmd(opt)), n)
	command := fmt.Sprintf(
		`%s; test -f "$canon" || exit 1; %s`,
		prelude, search,
	)
	return bashPipefail(command), nil
}

func SearchRemote(ctx context.Context, t targets.Target, cfg SSHConfig, opt SearchOpts) (string, error) {
	remote, err := BuildSearchRemote(t, opt)
	if err != nil {
		return "", err
	}
	return runSSH(ctx, t, cfg, remote)
}

// BuildTailRemote is the remote tail pipeline (for tests).
func BuildTailRemote(t targets.Target, path string, n int) (string, error) {
	lexical, err := ResolvePath(t, path)
	if err != nil {
		return "", err
	}
	n = limits.ClampLines(n, limits.DefaultLines, limits.MaxLines)
	prelude := resolvePrelude(lexical, t.Paths)
	command := fmt.Sprintf(
		`%s; test -f "$canon" || exit 1; case $canon in *.gz|*.GZ) gzip -dc -- "$canon" | tail -n %d ;; *) tail -n %d -- "$canon" ;; esac`,
		prelude, n, n,
	)
	return bashPipefail(command), nil
}

func TailRemote(ctx context.Context, t targets.Target, cfg SSHConfig, path string, n int) (string, error) {
	remote, err := BuildTailRemote(t, path, n)
	if err != nil {
		return "", err
	}
	return runSSH(ctx, t, cfg, remote)
}

func clampCtx(n int) int {
	if n < 0 {
		return 0
	}
	if n > limits.MaxContext {
		return limits.MaxContext
	}
	return n
}
