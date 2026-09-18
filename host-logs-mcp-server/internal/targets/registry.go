package targets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"host-logs-mcp-server/internal/pathguard"
)

type Target struct {
	Name                 string   `json:"name"`
	Aliases              []string `json:"aliases"`
	Description          string   `json:"description"`
	Environment          string   `json:"environment"`
	Host                 string   `json:"host"`
	Port                 int      `json:"port"`
	User                 string   `json:"user"`
	Transport            string   `json:"transport"`
	IdentityFile         string   `json:"identity_file"`
	Paths                []string `json:"paths"`
	Tags                 []string `json:"tags"`
	Password             string   `json:"password"`
	PrivateKey           string   `json:"private_key"`
	PrivateKeyPassphrase string   `json:"private_key_passphrase"`
	HostKeySHA256        string   `json:"host_key_sha256,omitempty"`
}

// Targets are decoded from a private local file, but must never serialize secrets.
func (t Target) MarshalJSON() ([]byte, error) { return json.Marshal(t.PublicView()) }

type fileShape struct {
	Targets []Target `json:"targets"`
}

type Registry struct {
	path string
	mu   sync.Mutex
	mod  time.Time
	list []Target
}

func New(path string) (*Registry, error) {
	if path == "" {
		return nil, fmt.Errorf("HOST_LOGS_TARGETS_FILE is required")
	}
	r := &Registry{path: path}
	if err := r.loadLocked(true); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) Path() string { return r.path }

func (r *Registry) List() ([]Target, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.loadLocked(false)
	out := make([]Target, len(r.list))
	copy(out, r.list)
	return out, err
}

func (r *Registry) Snapshot() []Target {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Target, len(r.list))
	copy(out, r.list)
	return out
}

func (r *Registry) Resolve(q string) (*Target, []Target, error) {
	all, err := r.List()
	if err != nil {
		return nil, all, fmt.Errorf("reload targets file: %w", err)
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, all, fmt.Errorf("target is required")
	}
	for i := range all {
		if strings.EqualFold(all[i].Name, q) {
			t := all[i]
			return &t, nil, nil
		}
	}
	var aliasHits []Target
	for i := range all {
		for _, a := range all[i].Aliases {
			if strings.EqualFold(a, q) {
				aliasHits = append(aliasHits, all[i])
				break
			}
		}
	}
	if len(aliasHits) == 1 {
		t := aliasHits[0]
		return &t, nil, nil
	}
	if len(aliasHits) > 1 {
		return nil, aliasHits, fmt.Errorf("target %q is ambiguous", q)
	}
	hits := Filter(all, q)
	if len(hits) == 1 {
		t := hits[0]
		return &t, nil, nil
	}
	if len(hits) > 1 {
		return nil, hits, fmt.Errorf("target %q is ambiguous; call list_targets", q)
	}
	return nil, nil, fmt.Errorf("no target matched %q; call list_targets", q)
}

func Filter(all []Target, query string) []Target {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return all
	}
	var out []Target
	for _, t := range all {
		if matchTarget(t, query) {
			out = append(out, t)
		}
	}
	return out
}

func matchTarget(t Target, q string) bool {
	fields := []string{t.Name, t.Description, t.Environment, t.Host, t.User, t.Transport}
	fields = append(fields, t.Aliases...)
	fields = append(fields, t.Tags...)
	fields = append(fields, t.Paths...)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func (t Target) IsLocal() bool {
	return strings.ToLower(strings.TrimSpace(t.Transport)) == "local"
}

func (t Target) PortOrDefault() int {
	if t.Port == 0 {
		return 22
	}
	return t.Port
}

func (t Target) PublicView() map[string]any {
	transport := t.Transport
	if transport == "" {
		if t.IsLocal() {
			transport = "local"
		} else {
			transport = "ssh"
		}
	}
	ident := t.IdentityFile
	if ident != "" {
		ident = filepath.Base(ident)
	}
	return map[string]any{
		"name":          t.Name,
		"aliases":       t.Aliases,
		"description":   t.Description,
		"environment":   t.Environment,
		"host":          t.Host,
		"port":          t.PortOrDefault(),
		"user":          t.User,
		"transport":     transport,
		"identity_file": ident,
		"paths":         t.Paths,
		"tags":          t.Tags,
		"auth":          t.authMode(),
	}
}

func (t Target) authMode() string {
	if t.IsLocal() {
		return "local"
	}
	if t.PrivateKey != "" || (t.IdentityFile != "" && t.UsesBuiltinSSH()) {
		if t.Password != "" {
			return "private_key_or_password"
		}
		return "private_key"
	}
	if t.Password != "" {
		return "password"
	}
	return "key_or_ssh_config"
}

// Existing agent/ssh_config and key-file-only connections retain OpenSSH behavior.
func (t Target) UsesBuiltinSSH() bool {
	return t.Password != "" || t.PrivateKey != "" || t.PrivateKeyPassphrase != "" || (t.HostKeySHA256 != "" && t.IdentityFile != "")
}

func (r *Registry) loadLocked(requireOK bool) error {
	st, err := os.Stat(r.path)
	if err != nil {
		return fmt.Errorf("read targets file %s: %w", r.path, err)
	}
	if !requireOK && st.ModTime().Equal(r.mod) && len(r.list) > 0 {
		return nil
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("read targets file %s: %w", r.path, err)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	var file fileShape
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse targets file: %w", err)
	}
	seen := map[string]struct{}{}
	for i, t := range file.Targets {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			return fmt.Errorf("targets[%d].name is required", i)
		}
		if t.Port < 0 || t.Port > 65535 {
			return fmt.Errorf("target %s port must be 1-65535 (or omitted for 22)", name)
		}
		if t.PrivateKey != "" && strings.TrimSpace(t.IdentityFile) != "" {
			return fmt.Errorf("target %s must choose private_key or identity_file, not both", name)
		}
		if t.PrivateKeyPassphrase != "" && t.PrivateKey == "" && strings.TrimSpace(t.IdentityFile) == "" {
			return fmt.Errorf("target %s private_key_passphrase requires private_key or identity_file", name)
		}
		if t.HostKeySHA256 != "" {
			decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(t.HostKeySHA256, "SHA256:"))
			if !strings.HasPrefix(t.HostKeySHA256, "SHA256:") || err != nil || len(decoded) != 32 {
				return fmt.Errorf("target %s host_key_sha256 must be an OpenSSH SHA256 fingerprint", name)
			}
			if !t.UsesBuiltinSSH() {
				return fmt.Errorf("target %s host_key_sha256 requires password, private_key or identity_file", name)
			}
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate target name %s", name)
		}
		seen[key] = struct{}{}
		file.Targets[i].Name = name

		tr := strings.ToLower(strings.TrimSpace(t.Transport))
		if tr == "" {
			if strings.EqualFold(strings.TrimSpace(t.Host), "local") {
				tr = "local"
			} else {
				tr = "ssh"
			}
		}
		if tr != "ssh" && tr != "local" {
			return fmt.Errorf("target %s transport must be ssh or local", name)
		}
		file.Targets[i].Transport = tr
		if tr == "local" && (t.UsesBuiltinSSH() || t.HostKeySHA256 != "") {
			return fmt.Errorf("target %s local transport cannot use SSH credentials", name)
		}

		if len(t.Paths) == 0 {
			return fmt.Errorf("target %s needs at least one allowlisted path", name)
		}
		cleaned := make([]string, 0, len(t.Paths))
		for _, p := range t.Paths {
			if tr == "local" {
				c, err := pathguard.CheckLocalRoot(p)
				if err != nil {
					return fmt.Errorf("target %s path %q: %w", name, p, err)
				}
				cleaned = append(cleaned, c)
				continue
			}
			c, err := pathguard.CheckAllowlistRoot(p)
			if err != nil {
				return fmt.Errorf("target %s path %q: %w", name, p, err)
			}
			cleaned = append(cleaned, c)
		}
		file.Targets[i].Paths = cleaned

		if tr == "ssh" {
			if strings.TrimSpace(t.Host) == "" || strings.TrimSpace(t.User) == "" {
				return fmt.Errorf("target %s ssh transport needs host and user", name)
			}
			if file.Targets[i].Port == 0 {
				file.Targets[i].Port = 22
			}
		}
		if ident := strings.TrimSpace(t.IdentityFile); ident != "" {
			file.Targets[i].IdentityFile = expandHome(ident)
		}
	}
	r.list = file.Targets
	r.mod = st.ModTime()
	return nil
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return p
}
