package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxFileBytes = 1 << 20

// Registry reads configuration for every operation so removed permissions never
// survive a failed reload. It retains no mutable configuration or credentials.
type Registry struct {
	file     string
	inline   string
	platform bool
}

func New(file string) *Registry {
	inline, platform := os.LookupEnv("KAFKA_TARGETS_JSON")
	return &Registry{file: file, inline: inline, platform: platform}
}

func (r *Registry) List() ([]Target, error) {
	var b []byte
	var err error
	if r.platform {
		b = []byte(r.inline)
	} else {
		f, e := os.Open(r.file)
		if e != nil {
			return nil, errors.New("target configuration cannot be read")
		}
		defer f.Close()
		b, err = io.ReadAll(io.LimitReader(f, maxFileBytes+1))
		if err != nil {
			return nil, errors.New("target configuration cannot be read")
		}
	}
	if len(b) > maxFileBytes {
		return nil, errors.New("target configuration exceeds 1 MiB")
	}
	var cfg struct {
		Targets []Target `json:"targets"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&cfg); err != nil {
		return nil, errors.New("invalid target configuration JSON")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid trailing target configuration JSON")
	}
	if cfg.Targets == nil || len(cfg.Targets) > 100 {
		return nil, errors.New("target configuration requires an array of at most 100 targets")
	}
	seen := map[string]bool{}
	for _, t := range cfg.Targets {
		if r.platform && (t.TLS.CAFile != "" || t.TLS.CertFile != "" || t.TLS.KeyFile != "") {
			return nil, errors.New("platform TLS requires inline PEM, not file paths")
		}
		if seen[t.Name] {
			return nil, errors.New("duplicate target name")
		}
		seen[t.Name] = true
		if err = validate(t); err != nil {
			return nil, err
		}
	}
	return cfg.Targets, nil
}
func (r *Registry) Resolve(name string) (Target, error) {
	ts, e := r.List()
	if e != nil {
		return Target{}, e
	}
	for _, t := range ts {
		if t.Name == name {
			return t, nil
		}
	}
	return Target{}, errors.New("target is not configured")
}

func validName(s string, pattern bool) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || pattern && c == '*' {
			continue
		}
		return false
	}
	return true
}
func validPatterns(ps []string) bool {
	if len(ps) == 0 {
		return false
	}
	for _, p := range ps {
		if !validName(p, true) {
			return false
		}
		if _, e := path.Match(p, ""); e != nil {
			return false
		}
	}
	return true
}
func validate(t Target) error {
	if !validName(t.Name, false) {
		return errors.New("invalid target name")
	}
	if len(t.Brokers) == 0 {
		return errors.New("target requires brokers")
	}
	for _, broker := range t.Brokers {
		host, port, e := net.SplitHostPort(broker)
		p, pe := strconv.Atoi(port)
		if e != nil || pe != nil || host == "" || strings.ContainsAny(host, " /\\\t\r\n@") || p < 1 || p > 65535 {
			return errors.New("invalid broker host or port")
		}
		for _, c := range port {
			if c < '0' || c > '9' {
				return errors.New("invalid broker port")
			}
		}
		if strings.Contains(host, ":") {
			if _, err := netip.ParseAddr(host); err != nil {
				return errors.New("invalid broker IPv6 address")
			}
		}
	}
	if !validPatterns(t.Topics) || len(t.Groups) == 0 {
		return errors.New("target requires explicit valid topic and group allowlists")
	}
	for _, p := range t.Groups {
		if !ValidGroupName(p) {
			return errors.New("invalid group allowlist")
		}
	}
	if (t.TLS.CertPEM == "") != (t.TLS.KeyPEM == "") {
		return errors.New("TLS PEM certificate and key must be configured together")
	}
	if (t.TLS.CAPEM != "" && t.TLS.CAFile != "") || ((t.TLS.CertPEM != "" || t.TLS.KeyPEM != "") && (t.TLS.CertFile != "" || t.TLS.KeyFile != "")) {
		return errors.New("TLS file and PEM sources conflict")
	}
	if (t.TLS.CertFile == "") != (t.TLS.KeyFile == "") {
		return errors.New("TLS certificate and key must be configured together")
	}
	if !t.TLS.Enabled && (t.TLS.CAFile != "" || t.TLS.CertFile != "" || t.TLS.KeyFile != "" || t.TLS.ServerName != "" || t.TLS.CAPEM != "" || t.TLS.CertPEM != "" || t.TLS.KeyPEM != "") {
		return errors.New("TLS options require TLS enabled")
	}
	s := t.SASL
	if s.Mechanism == "" {
		if s.Username != "" || s.Password != "" {
			return errors.New("SASL mechanism is required")
		}
	} else {
		switch s.Mechanism {
		case "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
		default:
			return errors.New("unsupported SASL mechanism")
		}
		if s.Username == "" || s.Password == "" {
			return errors.New("SASL credentials are required")
		}
	}
	return nil
}
func allows(ps []string, name string) bool {
	if !validName(name, false) {
		return false
	}
	for _, p := range ps {
		if !validName(p, true) {
			continue
		}
		if ok, e := path.Match(p, name); e == nil && ok {
			return true
		}
	}
	return false
}
func (t Target) AllowsTopic(name string) bool { return allows(t.Topics, name) }

// Group IDs are not topic names. Only '*' is special in group allowlists;
// separators and regex/glob punctuation otherwise match literally.
func ValidGroupName(name string) bool {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func (t Target) AllowsGroup(name string) bool {
	if !ValidGroupName(name) {
		return false
	}
	for _, pattern := range t.Groups {
		if !ValidGroupName(pattern) {
			continue
		}
		expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
		if matched, _ := regexp.MatchString(expr, name); matched {
			return true
		}
	}
	return false
}

// PublicView deliberately omits all credential values and certificate paths.
func (t Target) PublicView() map[string]any {
	return map[string]any{
		"name": t.Name, "brokers": t.Brokers, "description": t.Description, "labels": t.Labels,
		"tls": map[string]any{"enabled": t.TLS.Enabled}, "sasl": map[string]any{"mechanism": t.SASL.Mechanism},
		"topics": t.Topics, "groups": t.Groups, "allow_payload": t.AllowPayload,
	}
}
