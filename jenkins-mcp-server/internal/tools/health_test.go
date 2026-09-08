package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// healthMock describes a fake Jenkins for the health_check probes. Empty
// fields default to "endpoint absent": the test server returns 404, which
// exercises the WARN/ERROR branches of the corresponding subcheck.
type healthMock struct {
	apiJSONStatus    int // non-zero overrides the 200 on /api/json
	jenkinsHeader    string
	dateHeader       string
	meJSON           string
	meStatus         int
	crumbEnabled     bool
	pluginManagerErr int    // non-zero status code overrides the plugin response
	pluginsJSON      string // when pluginManagerErr == 0
	computerJSON     string
}

func newHealthDeps(t *testing.T, m healthMock) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/json":
			if m.jenkinsHeader != "" {
				w.Header().Set("X-Jenkins", m.jenkinsHeader)
			}
			if m.dateHeader != "" {
				w.Header().Set("Date", m.dateHeader)
			} else {
				// net/http auto-injects a Date header — clear it so
				// "missing Date" tests actually exercise that branch.
				w.Header()["Date"] = nil
			}
			if m.apiJSONStatus != 0 && m.apiJSONStatus != http.StatusOK {
				w.WriteHeader(m.apiJSONStatus)
				return
			}
			_, _ = w.Write([]byte(`{"mode":"NORMAL"}`))
		case "/me/api/json":
			if m.meStatus != 0 && m.meStatus != http.StatusOK {
				w.WriteHeader(m.meStatus)
				return
			}
			_, _ = w.Write([]byte(m.meJSON))
		case "/crumbIssuer/api/json":
			if !m.crumbEnabled {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"crumb":"x","crumbRequestField":"Jenkins-Crumb"}`))
		case "/pluginManager/api/json":
			if m.pluginManagerErr != 0 {
				w.WriteHeader(m.pluginManagerErr)
				return
			}
			_, _ = w.Write([]byte(m.pluginsJSON))
		case "/computer/api/json":
			_, _ = w.Write([]byte(m.computerJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{
		Client:   cli,
		Cache:    &jenkins.ConsoleCache{Dir: "/tmp/x", MaxBytes: 0},
		Version:  "v1.2.3",
		ReadOnly: true,
	}, srv
}

func TestHealthCheck_AllGreen(t *testing.T) {
	d, srv := newHealthDeps(t, healthMock{
		jenkinsHeader: "2.426.3",
		dateHeader:    time.Now().UTC().Format(http.TimeFormat),
		meJSON:        `{"fullName":"Alice","id":"alice"}`,
		crumbEnabled:  true,
		pluginsJSON: `{"plugins":[
			{"shortName":"workflow-aggregator","active":true,"version":"600.v"},
			{"shortName":"junit","active":true,"version":"1234.v"}
		]}`,
		computerJSON: `{"computer":[
			{"offline":false,"temporarilyOffline":false},
			{"offline":false,"temporarilyOffline":false}
		]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"version 2.426.3",
		"alice (Alice)",
		"CSRF crumb issuer",
		"enabled",
		"Pipeline plugin",
		"installed (600.v)",
		"JUnit plugin",
		"installed (1234.v)",
		"2 online, 0 offline",
		"jenkins-mcp version: v1.2.3",
		"read-only mode:      true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, statusError) {
		t.Errorf("did not expect ERROR in all-green output:\n%s", out)
	}
}

func TestHealthCheck_CrumbDisabledAndPluginMissing(t *testing.T) {
	d, srv := newHealthDeps(t, healthMock{
		jenkinsHeader: "2.401.1",
		meJSON:        `{"fullName":"Bot","id":"bot"}`,
		crumbEnabled:  false, // -> 404 -> WARN row
		pluginsJSON:   `{"plugins":[{"shortName":"junit","active":true,"version":"9.9"}]}`,
		computerJSON:  `{"computer":[{"offline":false,"temporarilyOffline":false}]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "disabled (GET-only MCP does not POST)") {
		t.Errorf("expected crumb-disabled WARN, got:\n%s", out)
	}
	if !strings.Contains(out, "Pipeline plugin") || !strings.Contains(out, "not installed") {
		t.Errorf("expected 'Pipeline plugin … not installed', got:\n%s", out)
	}
}

func TestHealthCheck_PluginManagerForbidden(t *testing.T) {
	d, srv := newHealthDeps(t, healthMock{
		jenkinsHeader:    "2.401.1",
		meJSON:           `{"fullName":"Bot","id":"bot"}`,
		crumbEnabled:     true,
		pluginManagerErr: http.StatusForbidden,
		computerJSON:     `{"computer":[{"offline":true,"temporarilyOffline":false}]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	// 403 on /pluginManager is downgraded to WARN, not ERROR — non-admin
	// tokens hit this routinely.
	if !strings.Contains(out, "plugin status unknown") {
		t.Errorf("expected 'plugin status unknown' WARN row, got:\n%s", out)
	}
	if strings.Count(out, "Pipeline plugin") == 0 || strings.Count(out, "JUnit plugin") == 0 {
		t.Errorf("expected both plugin labels to still appear, got:\n%s", out)
	}
	if !strings.Contains(out, "0 online, 1 offline") {
		t.Errorf("expected '0 online, 1 offline', got:\n%s", out)
	}
}

func TestHealthCheck_ReachabilityErrorOn5xx(t *testing.T) {
	// The probe that exists specifically for "agent can't see Jenkins":
	// /api/json returning 5xx must surface as an ERROR row, not a silent OK.
	d, srv := newHealthDeps(t, healthMock{
		apiJSONStatus: http.StatusBadGateway,
		meJSON:        `{"fullName":"Alice","id":"alice"}`,
		crumbEnabled:  true,
		pluginsJSON:   `{"plugins":[]}`,
		computerJSON:  `{"computer":[]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Jenkins reachable") {
			if !strings.Contains(line, "ERROR") {
				t.Errorf("expected ERROR on Jenkins reachable for 502, got: %q", line)
			}
			if !strings.Contains(line, "HTTP 502") {
				t.Errorf("expected status-coded detail, got: %q", line)
			}
			return
		}
	}
	t.Errorf("Jenkins reachable row missing entirely:\n%s", out)
}

func TestHealthCheck_AuthErrorOn401(t *testing.T) {
	// Token misconfig is the most common failure this tool diagnoses.
	d, srv := newHealthDeps(t, healthMock{
		jenkinsHeader: "2.426.3",
		meStatus:      http.StatusUnauthorized,
		crumbEnabled:  true,
		pluginsJSON:   `{"plugins":[]}`,
		computerJSON:  `{"computer":[]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Authenticated") {
			if !strings.Contains(line, "ERROR") {
				t.Errorf("expected ERROR on Authenticated for 401, got: %q", line)
			}
			if !strings.Contains(line, "HTTP 401") {
				t.Errorf("expected status-coded detail, got: %q", line)
			}
			return
		}
	}
	t.Errorf("Authenticated row missing entirely:\n%s", out)
}

func TestHealthCheck_NoDateHeaderSkipsClockSkew(t *testing.T) {
	// When the server doesn't send a Date header, the Clock skew row is
	// silently omitted. Guard against a future refactor that would emit a
	// bogus skew computed against a zero-value time.
	d, srv := newHealthDeps(t, healthMock{
		jenkinsHeader: "2.426.3",
		// dateHeader intentionally empty.
		meJSON:       `{"fullName":"Alice","id":"alice"}`,
		crumbEnabled: true,
		pluginsJSON: `{"plugins":[
			{"shortName":"workflow-aggregator","active":true,"version":"600.v"},
			{"shortName":"junit","active":true,"version":"1234.v"}
		]}`,
		computerJSON: `{"computer":[{"offline":false,"temporarilyOffline":false}]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	if strings.Contains(out, "Clock skew") {
		t.Errorf("did not expect Clock skew row when Date header is absent, got:\n%s", out)
	}
}

func TestHealthCheck_ClockSkewOKWithinThreshold(t *testing.T) {
	// 5s difference is well under the 60s warn threshold — must be OK, not
	// WARN. Guards against an inverted boundary comparison.
	d, srv := newHealthDeps(t, healthMock{
		jenkinsHeader: "2.426.3",
		dateHeader:    time.Now().UTC().Add(-5 * time.Second).Format(http.TimeFormat),
		meJSON:        `{"fullName":"Alice","id":"alice"}`,
		crumbEnabled:  true,
		pluginsJSON: `{"plugins":[
			{"shortName":"workflow-aggregator","active":true,"version":"600.v"},
			{"shortName":"junit","active":true,"version":"1234.v"}
		]}`,
		computerJSON: `{"computer":[{"offline":false,"temporarilyOffline":false}]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	var skewLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Clock skew") {
			skewLine = line
			break
		}
	}
	if skewLine == "" {
		t.Fatalf("expected Clock skew row, got:\n%s", out)
	}
	if !strings.Contains(skewLine, "OK") {
		t.Errorf("expected OK for 5s skew, got: %q", skewLine)
	}
	if strings.Contains(skewLine, "WARN") {
		t.Errorf("did not expect WARN for 5s skew, got: %q", skewLine)
	}
}

func TestHealthCheck_ClockSkewWarn(t *testing.T) {
	// Server reports a Date five minutes behind local — should trigger WARN.
	skew := -5 * time.Minute
	d, srv := newHealthDeps(t, healthMock{
		jenkinsHeader: "2.426.3",
		dateHeader:    time.Now().UTC().Add(skew).Format(http.TimeFormat),
		meJSON:        `{"fullName":"Alice","id":"alice"}`,
		crumbEnabled:  true,
		pluginsJSON: `{"plugins":[
			{"shortName":"workflow-aggregator","active":true,"version":"600.v"},
			{"shortName":"junit","active":true,"version":"1234.v"}
		]}`,
		computerJSON: `{"computer":[{"offline":false,"temporarilyOffline":false}]}`,
	})
	defer srv.Close()

	res, _, err := d.HealthCheck(context.Background(), nil, HealthCheckInput{})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "Clock skew") {
		t.Errorf("expected Clock skew row, got:\n%s", out)
	}
	if !strings.Contains(out, "behind local time") {
		t.Errorf("expected 'behind local time' detail, got:\n%s", out)
	}
	// Find the Clock skew line and check it is WARN.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Clock skew") {
			if !strings.Contains(line, "WARN") {
				t.Errorf("expected Clock skew to be WARN with 5-minute skew, got: %q", line)
			}
			return
		}
	}
}
