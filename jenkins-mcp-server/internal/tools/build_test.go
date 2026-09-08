package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// buildEnvFixture seeds /<job>/<build>/api/json and the EnvInject endpoint.
// envInjectStatus overrides 200 on the EnvInject endpoint; set to 404 to
// exercise the "plugin not installed" path.
type buildEnvFixture struct {
	apiJSON         string
	envInjectJSON   string
	envInjectStatus int
}

func newBuildEnvDeps(t *testing.T, jobPath string, build int64, f buildEnvFixture) (Deps, *httptest.Server) {
	t.Helper()
	apiPath := jenkins.JobAPIPath(jobPath) + "/" + jenkins.BuildRef(build) + "/api/json"
	envPath := jenkins.JobAPIPath(jobPath) + "/" + jenkins.BuildRef(build) + "/injectedEnvVars/api/json"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case apiPath:
			_, _ = w.Write([]byte(f.apiJSON))
		case envPath:
			if f.envInjectStatus != 0 && f.envInjectStatus != http.StatusOK {
				w.WriteHeader(f.envInjectStatus)
				return
			}
			_, _ = w.Write([]byte(f.envInjectJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

func TestGetBuildEnvironment_HappyPath(t *testing.T) {
	d, srv := newBuildEnvDeps(t, "svc", 42, buildEnvFixture{
		apiJSON: `{
			"actions": [
				{"causes":[{"shortDescription":"Started by user Alice Example","userId":"alice","userName":"Alice Example"}]},
				{"parameters":[{"name":"BRANCH","value":"main"},{"name":"RELEASE_VERSION","value":"1.2.3"}]},
				{}
			]
		}`,
		envInjectJSON: `{"envMap":{"GIT_COMMIT":"abc1234","HOME":"/var/jenkins","PATH":"/usr/bin"}}`,
	})
	defer srv.Close()

	res, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{
		JobPath: "svc", BuildNumber: 42,
	})
	if err != nil {
		t.Fatalf("GetBuildEnvironment: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"Cause:",
		"Started by user Alice Example",
		"Parameters:",
		"BRANCH=main",
		"RELEASE_VERSION=1.2.3",
		"Injected Env Vars (3 total)",
		"GIT_COMMIT=abc1234",
		"HOME=/var/jenkins",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestGetBuildEnvironment_NameFilterNarrowsEnvVars(t *testing.T) {
	d, srv := newBuildEnvDeps(t, "svc", 0, buildEnvFixture{
		apiJSON:       `{"actions":[]}`,
		envInjectJSON: `{"envMap":{"GIT_COMMIT":"abc","GIT_BRANCH":"main","HOME":"/x","PATH":"/y"}}`,
	})
	defer srv.Close()

	res, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{
		JobPath: "svc", NameFilter: "^GIT_",
	})
	if err != nil {
		t.Fatalf("GetBuildEnvironment: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Injected Env Vars (4 total, 2 after filter)") {
		t.Errorf("expected filtered count header, got:\n%s", out)
	}
	if !strings.Contains(out, "GIT_COMMIT=abc") || !strings.Contains(out, "GIT_BRANCH=main") {
		t.Errorf("expected GIT_* to be retained, got:\n%s", out)
	}
	if strings.Contains(out, "HOME=") || strings.Contains(out, "PATH=") {
		t.Errorf("expected HOME/PATH to be filtered out, got:\n%s", out)
	}
}

func TestGetBuildEnvironment_MaskedParameterRendersMasked(t *testing.T) {
	d, srv := newBuildEnvDeps(t, "svc", 0, buildEnvFixture{
		apiJSON: `{"actions":[
			{"parameters":[{"name":"API_TOKEN","value":null},{"name":"BRANCH","value":"main"}]}
		]}`,
		envInjectJSON: `{"envMap":{}}`,
	})
	defer srv.Close()

	res, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetBuildEnvironment: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "API_TOKEN=(masked)") {
		t.Errorf("expected API_TOKEN=(masked), got:\n%s", out)
	}
	if !strings.Contains(out, "BRANCH=main") {
		t.Errorf("expected BRANCH=main, got:\n%s", out)
	}
}

func TestGetBuildEnvironment_EnvInject404Degrades(t *testing.T) {
	d, srv := newBuildEnvDeps(t, "svc", 0, buildEnvFixture{
		apiJSON: `{"actions":[
			{"causes":[{"shortDescription":"Started by an SCM change"}]},
			{"parameters":[{"name":"BRANCH","value":"main"}]}
		]}`,
		envInjectStatus: http.StatusNotFound,
	})
	defer srv.Close()

	res, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetBuildEnvironment: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"Started by an SCM change",
		"BRANCH=main",
		"EnvInject plugin not installed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestGetBuildEnvironment_NoCausesOrParamsRendersNone(t *testing.T) {
	d, srv := newBuildEnvDeps(t, "svc", 0, buildEnvFixture{
		apiJSON:       `{"actions":[]}`,
		envInjectJSON: `{"envMap":{}}`,
	})
	defer srv.Close()

	res, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetBuildEnvironment: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Cause:\n  (none)") {
		t.Errorf("expected Cause (none), got:\n%s", out)
	}
	if !strings.Contains(out, "Parameters:\n  (none)") {
		t.Errorf("expected Parameters (none), got:\n%s", out)
	}
}

func TestGetBuildEnvironment_RedactsSecretEnvVars(t *testing.T) {
	d, srv := newBuildEnvDeps(t, "svc", 0, buildEnvFixture{
		apiJSON:       `{"actions":[]}`,
		envInjectJSON: `{"envMap":{"GIT_COMMIT":"abc","API_TOKEN":"super-secret","DB_PASSWORD":"p"}}`,
	})
	defer srv.Close()

	res, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetBuildEnvironment: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "GIT_COMMIT=abc") {
		t.Errorf("expected GIT_COMMIT kept, got:\n%s", out)
	}
	if !strings.Contains(out, "API_TOKEN=(redacted)") || !strings.Contains(out, "DB_PASSWORD=(redacted)") {
		t.Errorf("expected secret env redacted, got:\n%s", out)
	}
	if strings.Contains(out, "super-secret") {
		t.Errorf("secret value leaked:\n%s", out)
	}
}

func TestGetBuildEnvironment_MissingJobPath(t *testing.T) {
	d := Deps{}
	if _, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{}); err == nil {
		t.Fatal("expected error for empty job_path")
	}
}

func TestGetBuildEnvironment_InvalidNameFilter(t *testing.T) {
	d := Deps{}
	_, _, err := d.GetBuildEnvironment(context.Background(), nil, GetBuildEnvironmentInput{
		JobPath: "svc", NameFilter: "[invalid",
	})
	if err == nil {
		t.Fatal("expected error from invalid regex")
	}
	if !strings.Contains(err.Error(), "name_filter") {
		t.Errorf("expected error to mention name_filter, got: %v", err)
	}
}
