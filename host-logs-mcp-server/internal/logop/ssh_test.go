package logop

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"host-logs-mcp-server/internal/limits"
	"host-logs-mcp-server/internal/quote"
	"host-logs-mcp-server/internal/targets"
)

func localShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		const gitBash = `C:\Program Files\Git\bin\bash.exe`
		if _, err := os.Stat(gitBash); err != nil {
			t.Skip("Git Bash is unavailable")
		}
		return gitBash
	}
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell
	}
	t.Skip("POSIX shell is unavailable")
	return ""
}

func runBuiltSearch(t *testing.T, body []byte, suffix string, opt SearchOpts) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log"+suffix)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	unixDir := filepath.ToSlash(dir)
	unixPath := filepath.ToSlash(p)
	if runtime.GOOS == "windows" {
		unixDir = "/" + strings.ToLower(unixDir[:1]) + unixDir[2:]
		unixPath = "/" + strings.ToLower(unixPath[:1]) + unixPath[2:]
	}
	tgt := targets.Target{Name: "test", Host: "example.com", User: "reader", Transport: "ssh", Paths: []string{unixDir}}
	opt.Path = unixPath
	command, err := BuildSearchRemote(tgt, opt)
	if err != nil {
		t.Fatal(err)
	}
	return exec.Command(localShell(t), "-c", command).CombinedOutput()
}

func sampleSSHTarget() targets.Target {
	return targets.Target{
		Name:      "db-prod",
		Host:      "db.example.com",
		User:      "mcp_logs",
		Transport: "ssh",
		Paths:     []string{"/data/postgresql/log"},
	}
}

func TestSSHBaseArgsHardenSession(t *testing.T) {
	args := sshBaseArgs(sampleSSHTarget(), SSHConfig{Timeout: 20 * time.Second, StrictHostKey: "yes"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-T ") && args[0] != "-T" {
		t.Fatalf("missing -T: %v", args)
	}
	if args[0] != "-T" {
		t.Fatalf("want -T first, got %v", args)
	}
	if !strings.Contains(joined, "ClearAllForwardings=yes") {
		t.Fatalf("missing ClearAllForwardings: %v", args)
	}
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Fatalf("missing BatchMode: %v", args)
	}
}

func TestLimitedBufferCapsBytes(t *testing.T) {
	var dst limitedBuffer
	payload := bytes.Repeat([]byte("x"), limits.MaxBytes+100)
	n, err := dst.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	if !dst.truncated || dst.Len() != limits.MaxBytes {
		t.Fatalf("len=%d truncated=%v", dst.Len(), dst.truncated)
	}
}

func TestLimitedBufferExactCapEmptyWrite(t *testing.T) {
	var dst limitedBuffer
	_, _ = dst.Write(bytes.Repeat([]byte("x"), limits.MaxBytes))
	_, _ = dst.Write(nil)
	if dst.truncated {
		t.Fatal("an empty write is not truncation")
	}
}

func TestBuildSearchRemoteRealpathAndGzip(t *testing.T) {
	tgt := sampleSSHTarget()
	got, err := BuildSearchRemote(tgt, SearchOpts{
		Path:     "/data/postgresql/log/postgresql-16-main-2026-09-17.log.gz",
		Pattern:  `unexpected EOF`,
		Fixed:    true,
		MaxLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range []string{
		"bash -o pipefail -c",
		"readlink -f --",
		"gzip -dc --",
		"grep -a -F -n",
		quote.POSIX("/data/postgresql/log"),
	} {
		if !strings.Contains(got, need) {
			t.Fatalf("missing %q in\n%s", need, got)
		}
	}
}

func TestBuildSearchRemoteQuotesInjection(t *testing.T) {
	out, err := runBuiltSearch(t, []byte("safe\n"), "", SearchOpts{Pattern: `'; exit 42; #`, Fixed: true})
	if err != nil {
		t.Fatalf("quoted pattern changed shell control flow: %v: %s", err, out)
	}
}

func TestBuildSearchRemoteGrepE(t *testing.T) {
	tgt := sampleSSHTarget()
	got, err := BuildSearchRemote(tgt, SearchOpts{
		Path:    "/data/postgresql/log/app.log",
		Pattern: `[0-9]+`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "grep -a -E -n") {
		t.Fatalf("regex search should use grep ERE mode:\n%s", got)
	}
}

func TestBuildSearchRemoteInvalidRegexFails(t *testing.T) {
	out, err := runBuiltSearch(t, nil, "", SearchOpts{Pattern: "["})
	if err == nil {
		t.Fatalf("invalid regex succeeded: %s", out)
	}
}

func TestBuildSearchRemoteFixedPatternPreservesBackslashes(t *testing.T) {
	out, err := runBuiltSearch(t, []byte("literal \\n text\n"), "", SearchOpts{Pattern: `\n`, Fixed: true})
	if err != nil || !strings.Contains(string(out), `\n`) {
		t.Fatalf("backslash pattern err=%v out=%q", err, out)
	}
}

func TestBuildSearchRemoteNoMatchSucceeds(t *testing.T) {
	out, err := runBuiltSearch(t, []byte("one\ntwo\n"), "", SearchOpts{Pattern: "absent", Fixed: true})
	if err != nil {
		t.Fatalf("no match should succeed: %v: %s", err, out)
	}
	if len(out) != 0 {
		t.Fatalf("no match output=%q", out)
	}
}

func TestBuildSearchRemoteCorruptGzipFails(t *testing.T) {
	out, err := runBuiltSearch(t, []byte("not gzip"), ".gz", SearchOpts{Pattern: "anything", Fixed: true})
	if err == nil {
		t.Fatalf("corrupt gzip succeeded: %s", out)
	}
}

func TestBuildSearchRemoteCapSucceeds(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 5000; i++ {
		if i%10 == 0 {
			body.WriteString("match\n")
		} else {
			body.WriteString("context\n")
		}
	}
	out, err := runBuiltSearch(t, []byte(body.String()), "", SearchOpts{
		Pattern: "match", Fixed: true, MaxLines: 20, ContextBefore: 5, ContextAfter: 5,
	})
	if err != nil {
		t.Fatalf("capped search failed: %v: %s", err, out)
	}
	if got := bytes.Count(out, []byte("\n")); got != 20 {
		t.Fatalf("lines=%d, want 20", got)
	}
}

func TestBuildSearchRemoteValidGzip(t *testing.T) {
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	if _, err := zw.Write([]byte("first\nneedle\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := runBuiltSearch(t, body.Bytes(), ".gz", SearchOpts{Pattern: "needle", Fixed: true})
	if err != nil || !strings.Contains(string(out), "needle") {
		t.Fatalf("valid gzip err=%v out=%q", err, out)
	}
}

func TestBuildTailRemoteGzip(t *testing.T) {
	tgt := sampleSSHTarget()
	got, err := BuildTailRemote(tgt, "/data/postgresql/log/app.log.gz", 40)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "gzip -dc --") || !strings.Contains(got, "readlink -f --") {
		t.Fatalf("tail gzip pipeline:\n%s", got)
	}
}

func TestBuildTailRemoteCorruptGzipFails(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log.gz")
	if err := os.WriteFile(p, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	unixDir := filepath.ToSlash(dir)
	unixPath := filepath.ToSlash(p)
	if runtime.GOOS == "windows" {
		unixDir = "/" + strings.ToLower(unixDir[:1]) + unixDir[2:]
		unixPath = "/" + strings.ToLower(unixPath[:1]) + unixPath[2:]
	}
	tgt := targets.Target{Name: "test", Host: "example.com", User: "reader", Transport: "ssh", Paths: []string{unixDir}}
	command, err := BuildTailRemote(tgt, unixPath, 20)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(localShell(t), "-c", command).CombinedOutput()
	if err == nil {
		t.Fatalf("corrupt gzip succeeded: %s", out)
	}
}

func TestBuildListRemoteFindHAndRealpath(t *testing.T) {
	tgt := sampleSSHTarget()
	got, err := BuildListRemote(tgt, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "find -H ") {
		t.Fatalf("want find -H:\n%s", got)
	}
	if !strings.Contains(got, "readlink -f --") {
		t.Fatalf("want readlink:\n%s", got)
	}
	if strings.Contains(got, "find -L ") {
		t.Fatal("must not follow all symlinks with -L")
	}
}

func TestBuildSearchRemoteRejectsOutside(t *testing.T) {
	tgt := sampleSSHTarget()
	if _, err := BuildSearchRemote(tgt, SearchOpts{Path: "/etc/passwd", Pattern: "x"}); err == nil {
		t.Fatal("expected allowlist reject")
	}
}
