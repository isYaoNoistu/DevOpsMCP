package targets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type Target struct {
	Name             string   `json:"name"`
	Aliases          []string `json:"aliases"`
	Description      string   `json:"description"`
	Environment      string   `json:"environment"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	DBName           string   `json:"dbname"`
	User             string   `json:"user"`
	SSLMode          string   `json:"sslmode"`
	CredentialRef    string   `json:"credential_ref"`
	Tags             []string `json:"tags"`
	Password         string   `json:"password"`
	InlineCredential bool     `json:"-"`
}

// Never serialize credential material.
func (t Target) MarshalJSON() ([]byte, error) { return json.Marshal(t.PublicView()) }

type fileShape struct {
	Targets []Target `json:"targets"`
}

type Registry struct {
	path         string
	platformJSON string
	platform     bool
	mu           sync.Mutex
	mod          time.Time
	list         []Target
}

func New(path string) (*Registry, error) {
	inline, platform := os.LookupEnv("PG_TARGETS_JSON")
	if path == "" && !platform {
		return nil, fmt.Errorf("PG_TARGETS_FILE or PG_TARGETS_JSON is required")
	}
	r := &Registry{path: path, platformJSON: inline, platform: platform}
	if err := r.loadLocked(true); err != nil {
		if platform {
			return nil, fmt.Errorf("invalid PG_TARGETS_JSON configuration")
		}
		return nil, err
	}
	return r, nil
}

func (r *Registry) Path() string {
	if r.platform {
		return ""
	}
	return r.path
}

func (r *Registry) List() ([]Target, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.loadLocked(false)
	out := make([]Target, len(r.list))
	copy(out, r.list)
	return out, err
}

// Snapshot returns the last successfully loaded targets without touching the file.
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
	fields := []string{t.Name, t.Description, t.Environment, t.Host, t.DBName, t.User}
	fields = append(fields, t.Aliases...)
	fields = append(fields, t.Tags...)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func (t Target) PublicView() map[string]any {
	port := t.Port
	if port == 0 {
		port = 5432
	}
	return map[string]any{
		"name":           t.Name,
		"aliases":        t.Aliases,
		"description":    t.Description,
		"environment":    t.Environment,
		"host":           t.Host,
		"port":           port,
		"dbname":         t.DBName,
		"user":           t.User,
		"sslmode":        t.SSLModeOrDefault(),
		"credential_ref": t.CredentialRef,
		"tags":           t.Tags,
	}
}

const DefaultSSLMode = "verify-full"

var allowedSSLModes = map[string]struct{}{
	"disable": {}, "allow": {}, "prefer": {},
	"require": {}, "verify-ca": {}, "verify-full": {},
}

func (t Target) SSLModeOrDefault() string {
	if t.SSLMode == "" {
		return DefaultSSLMode
	}
	return t.SSLMode
}

// IsProduction reports whether this target should be treated as production
// for EXPLAIN ANALYZE and TLS defaults. Matches environment, tags, and name suffix.
func (t Target) IsProduction() bool {
	switch strings.ToLower(strings.TrimSpace(t.Environment)) {
	case "prod", "production", "prd":
		return true
	}
	for _, tag := range t.Tags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "prod", "production", "prd":
			return true
		}
	}
	name := strings.ToLower(strings.TrimSpace(t.Name))
	for _, suf := range []string{"-prod", "-production", "_prod", "_production"} {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

func (t Target) PortOrDefault() int {
	if t.Port == 0 {
		return 5432
	}
	return t.Port
}

func (r *Registry) loadLocked(requireOK bool) error {
	var raw []byte
	var mod time.Time
	if r.platform {
		if !requireOK {
			return nil
		}
		raw = []byte(r.platformJSON)
		if len(raw) == 0 || len(raw) > 1<<20 {
			return fmt.Errorf("platform configuration must be between 1 byte and 1 MiB")
		}
	} else {
		st, err := os.Stat(r.path)
		if err != nil {
			return fmt.Errorf("read targets file: %w", err)
		}
		if !requireOK && st.ModTime().Equal(r.mod) && len(r.list) > 0 {
			return nil
		}
		mod = st.ModTime()
		raw, err = os.ReadFile(r.path)
		if err != nil {
			return fmt.Errorf("read targets file: %w", err)
		}
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	var file fileShape
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse targets file: %w", err)
	}
	if r.platform && (len(file.Targets) == 0 || len(file.Targets) > 100) {
		return fmt.Errorf("platform configuration requires 1 to 100 targets")
	}
	seen := map[string]struct{}{}
	for i, t := range file.Targets {
		name := strings.TrimSpace(t.Name)
		if r.platform && t.Password == "" {
			return fmt.Errorf("platform target requires password")
		}
		if name == "" {
			return fmt.Errorf("targets[%d].name is required", i)
		}
		if !r.platform && t.Password != "" {
			return fmt.Errorf("target %s must not store password in the targets file; use credential_ref or pgpass", name)
		}
		if t.Host == "" || t.DBName == "" || t.User == "" {
			return fmt.Errorf("target %s needs host, dbname, user", name)
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate target name %s", name)
		}
		seen[key] = struct{}{}
		file.Targets[i].Name = name
		file.Targets[i].InlineCredential = r.platform
		if file.Targets[i].Port == 0 {
			file.Targets[i].Port = 5432
		}
		mode := strings.ToLower(strings.TrimSpace(file.Targets[i].SSLMode))
		if mode == "" {
			mode = DefaultSSLMode
		}
		if _, ok := allowedSSLModes[mode]; !ok {
			return fmt.Errorf("target %s has invalid sslmode %q", name, file.Targets[i].SSLMode)
		}
		file.Targets[i].SSLMode = mode
		if file.Targets[i].IsProduction() {
			switch mode {
			case "disable", "allow", "prefer":
				return fmt.Errorf("target %s is production; sslmode %s is not allowed (use require, verify-ca, or verify-full)", name, mode)
			}
		}
	}
	r.list = file.Targets
	r.mod = mod
	return nil
}
