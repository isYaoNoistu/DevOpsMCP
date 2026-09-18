package pathguard

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var unixAbs = regexp.MustCompile(`^(/[A-Za-z0-9._+-]+)+$`)

var deniedSubstrings = []string{
	"/.ssh/",
	"/proc/",
	"/sys/",
	"/etc/shadow",
	"/etc/passwd",
	"/root/",
}

var deniedSuffixes = []string{
	".env",
	".pem",
	".key",
	"id_rsa",
	"id_ed25519",
	"authorized_keys",
	"pgpass",
	"pgpass.conf",
	"mysqlpass",
	".pgpass",
}

// CleanUnix returns a cleaned absolute Unix path with no "..".
func CleanUnix(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.Contains(p, "\\") {
		return "", fmt.Errorf("path must use / not backslash")
	}
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("path must not contain ..")
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path must be absolute")
	}
	cleaned := path.Clean(p)
	if cleaned != "/" && strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
		// keep directory identity without requiring trailing slash
	}
	if !unixAbs.MatchString(cleaned) {
		return "", fmt.Errorf("path %q is not an allowed Unix path", p)
	}
	return cleaned, nil
}

// CheckAllowlistRoot rejects overly broad or sensitive roots at config load.
func CheckAllowlistRoot(root string) (string, error) {
	cleaned, err := CleanUnix(root)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(cleaned, "/"), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("allowlist path %q is too broad (need at least /dir/subdir)", root)
	}
	switch cleaned {
	case "/etc", "/root", "/home", "/usr", "/var", "/opt", "/data", "/var/log", "/tmp":
		return "", fmt.Errorf("allowlist path %q is too broad", cleaned)
	}
	if err := denySensitive(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

// CheckSensitive rejects paths that look like secrets or kernel files.
func CheckSensitive(p string) error {
	return denySensitive(p)
}

func denySensitive(p string) error {
	lower := strings.ToLower(p)
	for _, s := range deniedSubstrings {
		if strings.Contains(lower, s) {
			return fmt.Errorf("path %q hits a blocked prefix", p)
		}
	}
	base := path.Base(lower)
	for _, s := range deniedSuffixes {
		if base == s || strings.HasSuffix(lower, s) {
			return fmt.Errorf("path %q looks like a secret file", p)
		}
	}
	return nil
}

// Under reports whether candidate is the root or a file/dir inside it.
func Under(candidate, root string) bool {
	if candidate == root {
		return true
	}
	return strings.HasPrefix(candidate, root+"/")
}

// ResolveUnix checks candidate is under one of the unix allowlist roots.
func ResolveUnix(candidate string, roots []string) (cleaned string, root string, err error) {
	cleaned, err = CleanUnix(candidate)
	if err != nil {
		return "", "", err
	}
	if err := denySensitive(cleaned); err != nil {
		return "", "", err
	}
	for _, r := range roots {
		if Under(cleaned, r) {
			return cleaned, r, nil
		}
	}
	return "", "", fmt.Errorf("path %q is outside this target allowlist", candidate)
}

// CheckLocalRoot validates a transport=local allowlist root on the MCP machine.
func CheckLocalRoot(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(abs)
	vol := filepath.VolumeName(clean)
	if vol == "" {
		return CheckAllowlistRoot(filepath.ToSlash(clean))
	}
	rest := strings.TrimPrefix(clean, vol)
	rest = strings.Trim(rest, `/\`)
	if rest == "" {
		return "", fmt.Errorf("allowlist path %q is too broad", p)
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) < 2 {
		return "", fmt.Errorf("allowlist path %q is too broad (need at least two directories)", p)
	}
	if strings.EqualFold(parts[0], "Users") && len(parts) < 3 {
		return "", fmt.Errorf("allowlist path %q is too broad (user profile)", p)
	}
	if err := denySensitive(filepath.ToSlash(clean)); err != nil {
		return "", err
	}
	return clean, nil
}

// ResolveLocal resolves a path on the MCP machine (lab / transport=local).
// Both the lexical path and EvalSymlinks target must sit under a root.
func ResolveLocal(candidate string, roots []string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", fmt.Errorf("path is empty")
	}
	if !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("path must be absolute")
	}
	clean, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	clean = filepath.Clean(clean)
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q: %w", candidate, err)
	}
	if err := denySensitive(filepath.ToSlash(real)); err != nil {
		return "", err
	}
	if err := denySensitive(filepath.ToSlash(clean)); err != nil {
		return "", err
	}
	for _, r := range roots {
		rootAbs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		rootAbs = filepath.Clean(rootAbs)
		rootReal, err := filepath.EvalSymlinks(rootAbs)
		if err != nil {
			rootReal = rootAbs
		}
		if localUnder(clean, rootAbs) && localUnder(real, rootReal) {
			return real, nil
		}
	}
	return "", fmt.Errorf("path %q is outside this target allowlist", candidate)
}

func localUnder(candidate, root string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	sep := string(os.PathSeparator)
	if rel == ".." || strings.HasPrefix(rel, ".."+sep) {
		return false
	}
	return true
}
