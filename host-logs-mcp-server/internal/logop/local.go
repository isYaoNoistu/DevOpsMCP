package logop

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"host-logs-mcp-server/internal/limits"
	"host-logs-mcp-server/internal/pathguard"
	"host-logs-mcp-server/internal/targets"
)

type FileInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type SearchOpts struct {
	Path          string
	Pattern       string
	Fixed         bool
	Start         string
	End           string
	MaxLines      int
	ContextBefore int
	ContextAfter  int
}

func ResolvePath(t targets.Target, p string) (string, error) {
	p = strings.TrimSpace(p)
	if t.IsLocal() {
		return pathguard.ResolveLocal(p, t.Paths)
	}
	cleaned, _, err := pathguard.ResolveUnix(p, t.Paths)
	return cleaned, err
}

func ListLocal(t targets.Target, dir string, max int) ([]FileInfo, error) {
	if max <= 0 || max > limits.ListMax {
		max = limits.ListMax
	}
	root := strings.TrimSpace(dir)
	if root == "" {
		var out []FileInfo
		for _, p := range t.Paths {
			part, err := listDirLocal(p, max-len(out))
			if err != nil {
				return nil, err
			}
			out = append(out, part...)
			if len(out) >= max {
				return out[:max], nil
			}
		}
		return out, nil
	}
	resolved, err := ResolvePath(t, root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []FileInfo{{Path: resolved, Size: st.Size()}}, nil
	}
	return listDirLocal(resolved, max)
}

func listDirLocal(root string, max int) ([]FileInfo, error) {
	var out []FileInfo
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		depth := 0
		if rel != "." {
			depth = len(strings.Split(rel, string(os.PathSeparator)))
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if depth >= limits.MaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > limits.MaxDepth {
			return nil
		}
		if pathguard.CheckSensitive(filepath.ToSlash(p)) != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, FileInfo{Path: p, Size: info.Size()})
		if len(out) >= max {
			return io.EOF
		}
		return nil
	})
	if err == io.EOF {
		err = nil
	}
	return out, err
}

func SearchLocal(t targets.Target, opt SearchOpts) ([]string, bool, error) {
	path, err := ResolvePath(t, opt.Path)
	if err != nil {
		return nil, false, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if st.IsDir() {
		return nil, false, fmt.Errorf("path must be a file; use list_log_files first")
	}
	n := limits.ClampLines(opt.MaxLines, limits.DefaultLines, limits.MaxLines)
	cb := opt.ContextBefore
	ca := opt.ContextAfter
	if cb < 0 {
		cb = 0
	}
	if ca < 0 {
		ca = 0
	}
	if cb > limits.MaxContext {
		cb = limits.MaxContext
	}
	if ca > limits.MaxContext {
		ca = limits.MaxContext
	}

	var rx *regexp.Regexp
	if !opt.Fixed {
		rx, err = regexp.Compile(opt.Pattern)
		if err != nil {
			return nil, false, fmt.Errorf("invalid pattern: %w", err)
		}
	}

	f, err := openLog(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	var ring []string
	if cb > 0 {
		ring = make([]string, 0, cb)
	}
	var afterLeft int
	var hits []string
	truncated := false
	outputBytes := 0
	appendHit := func(line string) bool {
		if len(hits) >= n {
			truncated = true
			return false
		}
		separator := 0
		if len(hits) > 0 {
			separator = 1
		}
		remaining := limits.MaxBytes - outputBytes - separator
		if remaining <= 0 {
			truncated = true
			return false
		}
		if len(line) > remaining {
			line = line[:remaining]
			truncated = true
		}
		hits = append(hits, line)
		outputBytes += separator + len(line)
		return !truncated
	}
	lineNo := 0
scanLoop:
	for sc.Scan() {
		line := sc.Text()
		lineNo++
		if opt.Start != "" && line < opt.Start {
			if cb > 0 {
				ring = append(ring, fmt.Sprintf("%d:%s", lineNo, line))
				if len(ring) > cb {
					ring = ring[len(ring)-cb:]
				}
			}
			continue
		}
		if opt.End != "" && line >= opt.End {
			continue
		}
		match := false
		if opt.Fixed {
			match = strings.Contains(line, opt.Pattern)
		} else {
			match = rx.MatchString(line)
		}
		numbered := fmt.Sprintf("%d:%s", lineNo, line)
		if match {
			if cb > 0 && len(ring) > 0 {
				for _, contextLine := range ring {
					if !appendHit(contextLine) {
						break scanLoop
					}
				}
				ring = ring[:0]
			}
			if !appendHit(numbered) {
				break
			}
			afterLeft = ca
			if len(hits) >= n {
				truncated = true
				break
			}
		} else if afterLeft > 0 {
			if !appendHit(numbered) {
				break
			}
			afterLeft--
			if len(hits) >= n {
				truncated = true
				break
			}
		} else if cb > 0 {
			ring = append(ring, numbered)
			if len(ring) > cb {
				ring = ring[len(ring)-cb:]
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, truncated, err
	}
	return hits, truncated, nil
}

func TailLocal(t targets.Target, path string, n int) ([]string, error) {
	resolved, err := ResolvePath(t, path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("path must be a file; use list_log_files first")
	}
	n = limits.ClampLines(n, limits.DefaultLines, limits.MaxLines)
	if isGzipPath(resolved) {
		f, err := openLog(resolved)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return tailScanner(f, n)
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const window = 2 << 20
	size := st.Size()
	start := int64(0)
	if size > window {
		start = size - window
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	lines, err := tailScanner(f, n)
	if err != nil {
		return nil, err
	}
	return lines, nil
}

func isGzipPath(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".gz")
}

type gzipReadCloser struct {
	gz *gzip.Reader
	f  *os.File
}

func (g *gzipReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }

func (g *gzipReadCloser) Close() error {
	err1 := g.gz.Close()
	err2 := g.f.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func openLog(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !isGzipPath(path) {
		return f, nil
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &gzipReadCloser{gz: gz, f: f}, nil
}

func tailScanner(r io.Reader, n int) ([]string, error) {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}
