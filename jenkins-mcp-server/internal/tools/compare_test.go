package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// compareFixture is the per-build mock data routed by build number.
type compareFixture struct {
	Info   compareBuildAPI
	Stages *wfapiDescribe // nil → 404 on /wfapi/describe
	Tests  *junitReport   // nil → 404 on /testReport/api/json
}

// newCompareDeps builds a Jenkins client + handler that routes
// /job/<jobPath>/<n>/{api/json,wfapi/describe,testReport/api/json}
// to the per-build fixtures keyed by build number.
func newCompareDeps(t *testing.T, jobPath string, fixtures map[int64]compareFixture) (Deps, *httptest.Server) {
	t.Helper()
	prefix := jenkins.JobAPIPath(jobPath) + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if rest == r.URL.Path {
			http.NotFound(w, r)
			return
		}
		// rest is "<n>/<endpoint...>"
		slash := strings.IndexByte(rest, '/')
		if slash <= 0 {
			http.NotFound(w, r)
			return
		}
		n, err := strconv.ParseInt(rest[:slash], 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f, ok := fixtures[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch rest[slash+1:] {
		case "api/json":
			_ = json.NewEncoder(w).Encode(f.Info)
		case "wfapi/describe":
			if f.Stages == nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(f.Stages)
		case "testReport/api/json":
			if f.Tests == nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(f.Tests)
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

func boolPtr(b bool) *bool { return &b }

func TestCompareBuilds_HappyPath(t *testing.T) {
	fixtures := map[int64]compareFixture{
		85: {
			Info: compareBuildAPI{
				Result: "SUCCESS", Duration: 60000,
				Actions: []compareAction{{Parameters: []compareParam{
					{Name: "DEPLOY_ENV", Value: "staging"},
					{Name: "GO_VERSION", Value: "1.25"},
				}}},
				ChangeSets: []scmChangeSet{{Kind: "git", Items: []scmItem{
					{CommitID: "aaaaaaa1111", Author: scmAuthor{FullName: "alice"}, Msg: "Older commit"},
				}}},
			},
			Stages: &wfapiDescribe{Status: "SUCCESS", Stages: []wfapiStage{
				{ID: "1", Name: "Build", Status: "SUCCESS"},
				{ID: "2", Name: "Test", Status: "SUCCESS"},
				{ID: "3", Name: "Deploy", Status: "SUCCESS"},
			}},
			Tests: &junitReport{
				PassCount: 3, FailCount: 0,
				Suites: []junitSuite{{Name: "s", Cases: []junitCase{
					{ClassName: "pkg.A", Name: "TestX", Status: "PASSED"},
					{ClassName: "pkg.A", Name: "TestY", Status: "PASSED"},
					{ClassName: "pkg.B", Name: "TestZ", Status: "PASSED"},
				}}},
			},
		},
		86: {
			Info: compareBuildAPI{
				Result: "FAILURE", Duration: 180000,
				Actions: []compareAction{{Parameters: []compareParam{
					{Name: "DEPLOY_ENV", Value: "production"},
					{Name: "GO_VERSION", Value: "1.25"},
					{Name: "FEATURE_X", Value: true},
				}}},
				ChangeSets: []scmChangeSet{{Kind: "git", Items: []scmItem{
					{CommitID: "aaaaaaa1111", Author: scmAuthor{FullName: "alice"}, Msg: "Older commit"},
					{CommitID: "bbbbbbb2222", Author: scmAuthor{FullName: "bob"}, Msg: "Break the deploy"},
				}}},
			},
			Stages: &wfapiDescribe{Status: "FAILURE", Stages: []wfapiStage{
				{ID: "1", Name: "Build", Status: "SUCCESS"},
				{ID: "2", Name: "Test", Status: "SUCCESS"},
				{ID: "3", Name: "Deploy", Status: "FAILURE"},
				{ID: "4", Name: "Smoke", Status: "SUCCESS"},
			}},
			Tests: &junitReport{
				PassCount: 2, FailCount: 2,
				Suites: []junitSuite{{Name: "s", Cases: []junitCase{
					{ClassName: "pkg.A", Name: "TestX", Status: "PASSED"},
					{ClassName: "pkg.A", Name: "TestY", Status: "FAILED"},
					{ClassName: "pkg.C", Name: "TestNew", Status: "FAILED"},
				}}},
			},
		},
	}
	d, srv := newCompareDeps(t, "team/svc", fixtures)
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "team/svc", BuildA: 85, BuildB: 86,
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"Compare team/svc builds 85 → 86",
		"Result:   SUCCESS → FAILURE",
		"Duration: 1m0s → 3m0s (Δ +2m0s)",
		"  ~ DEPLOY_ENV: staging → production",
		"  + FEATURE_X=true",
		"SCM (commits in build 86 not in build 85):",
		"bbbbbbb bob",
		"\"Break the deploy\"",
		"Stages (changed only):",
		"  Deploy: SUCCESS → FAILURE",
		"  Smoke: (not in build 85) → SUCCESS",
		"Tests:",
		"  pass → fail (1):",
		"    pkg.A.TestY",
		"  new (1):",
		"    pkg.C.TestNew [FAILED]",
		"  removed (1):",
		"    pkg.B.TestZ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Stages that did not change must NOT appear (Build, Test were SUCCESS in both).
	if strings.Contains(out, "  Build:") || strings.Contains(out, "  Test:") {
		t.Errorf("unchanged stages leaked into output:\n%s", out)
	}
	// GO_VERSION did not change → must not appear in the param diff.
	if strings.Contains(out, "GO_VERSION") {
		t.Errorf("unchanged parameter leaked into output:\n%s", out)
	}
}

func TestCompareBuilds_NoDiff(t *testing.T) {
	same := compareFixture{
		Info: compareBuildAPI{Result: "SUCCESS", Duration: 60000},
		Stages: &wfapiDescribe{Stages: []wfapiStage{
			{Name: "Build", Status: "SUCCESS"},
		}},
		Tests: &junitReport{
			Suites: []junitSuite{{Cases: []junitCase{
				{ClassName: "p", Name: "T", Status: "PASSED"},
			}}},
		},
	}
	d, srv := newCompareDeps(t, "svc", map[int64]compareFixture{10: same, 11: same})
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "svc", BuildA: 10, BuildB: 11,
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"Parameters: (no diff)",
		"SCM (commits in build 11 not in build 10):",
		"  (none)",
		"Stages (changed only):",
		"  pass → fail (0):",
		"  fail → pass (0):",
		"  new (0):",
		"  removed (0):",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestCompareBuilds_IncludeTestsFalse(t *testing.T) {
	fix := compareFixture{
		Info: compareBuildAPI{Result: "SUCCESS"},
		Tests: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			{ClassName: "p", Name: "T", Status: "FAILED"},
		}}}},
	}
	d, srv := newCompareDeps(t, "svc", map[int64]compareFixture{1: fix, 2: fix})
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "svc", BuildA: 1, BuildB: 2, IncludeTests: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)
	if strings.Contains(out, "Tests:") {
		t.Errorf("Tests section should be skipped when include_tests=false:\n%s", out)
	}
}

func TestCompareBuilds_StagesMissingOnOneSide(t *testing.T) {
	d, srv := newCompareDeps(t, "svc", map[int64]compareFixture{
		1: {Info: compareBuildAPI{Result: "SUCCESS"}}, // no Stages → 404
		2: {
			Info: compareBuildAPI{Result: "SUCCESS"},
			Stages: &wfapiDescribe{Stages: []wfapiStage{
				{Name: "Build", Status: "SUCCESS"},
			}},
		},
	})
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "svc", BuildA: 1, BuildB: 2,
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Stages: build 1 is not a pipeline") {
		t.Errorf("expected pipeline-missing hint for build A, got:\n%s", out)
	}
}

func TestCompareBuilds_TestsMissingOnBothSides(t *testing.T) {
	fix := compareFixture{Info: compareBuildAPI{Result: "SUCCESS"}} // no Tests
	d, srv := newCompareDeps(t, "svc", map[int64]compareFixture{1: fix, 2: fix})
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "svc", BuildA: 1, BuildB: 2,
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Tests: (no test report on either build)") {
		t.Errorf("expected no-test-report hint, got:\n%s", out)
	}
}

func TestCompareBuilds_DurationDeltaNegative(t *testing.T) {
	d, srv := newCompareDeps(t, "svc", map[int64]compareFixture{
		1: {Info: compareBuildAPI{Result: "SUCCESS", Duration: 180000}},
		2: {Info: compareBuildAPI{Result: "SUCCESS", Duration: 60000}},
	})
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "svc", BuildA: 1, BuildB: 2,
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "(Δ -2m0s)") {
		t.Errorf("expected negative delta, got:\n%s", out)
	}
}

func TestCompareBuilds_TestsBucketTruncation(t *testing.T) {
	cases := make([]junitCase, 0, compareTestsBucketCap+5)
	for i := range compareTestsBucketCap + 5 {
		cases = append(cases, junitCase{
			ClassName: "p", Name: fmt.Sprintf("T%04d", i), Status: "FAILED",
		})
	}
	d, srv := newCompareDeps(t, "svc", map[int64]compareFixture{
		1: {Info: compareBuildAPI{}, Tests: &junitReport{Suites: []junitSuite{{Cases: passedAll(cases)}}}},
		2: {Info: compareBuildAPI{}, Tests: &junitReport{Suites: []junitSuite{{Cases: cases}}}},
	})
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "svc", BuildA: 1, BuildB: 2,
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "  pass → fail (105):") {
		t.Errorf("expected exact bucket count line, got:\n%s", out)
	}
	if !strings.Contains(out, "… (5 more, capped at 100)") {
		t.Errorf("expected truncation footer, got:\n%s", out)
	}
}

func passedAll(in []junitCase) []junitCase {
	out := make([]junitCase, len(in))
	for i, c := range in {
		out[i] = c
		out[i].Status = "PASSED"
	}
	return out
}

func TestCompareBuilds_InputValidation(t *testing.T) {
	d := Deps{}
	cases := []struct {
		name string
		in   CompareBuildsInput
		want string
	}{
		{"missing job_path", CompareBuildsInput{BuildA: 1, BuildB: 2}, "job_path"},
		{"zero build_a", CompareBuildsInput{JobPath: "svc", BuildA: 0, BuildB: 2}, "build_a and build_b"},
		{"zero build_b", CompareBuildsInput{JobPath: "svc", BuildA: 1, BuildB: 0}, "build_a and build_b"},
		{"equal builds", CompareBuildsInput{JobPath: "svc", BuildA: 5, BuildB: 5}, "must be different"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := d.CompareBuilds(context.Background(), nil, tc.in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error to mention %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestCompareBuilds_SCMCommonCommitFilteredOut(t *testing.T) {
	// Both builds list commit "shared"; only "newone" should appear in B's
	// "not in A" set.
	d, srv := newCompareDeps(t, "svc", map[int64]compareFixture{
		1: {Info: compareBuildAPI{ChangeSets: []scmChangeSet{{Items: []scmItem{
			{CommitID: "sharedaa", Author: scmAuthor{FullName: "alice"}, Msg: "Shared"},
		}}}}},
		2: {Info: compareBuildAPI{ChangeSets: []scmChangeSet{{Items: []scmItem{
			{CommitID: "sharedaa", Author: scmAuthor{FullName: "alice"}, Msg: "Shared"},
			{CommitID: "newoneaa", Author: scmAuthor{FullName: "bob"}, Msg: "New work"},
		}}}}},
	})
	defer srv.Close()

	res, _, err := d.CompareBuilds(context.Background(), nil, CompareBuildsInput{
		JobPath: "svc", BuildA: 1, BuildB: 2, IncludeTests: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("CompareBuilds: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "newonea bob") {
		t.Errorf("expected newone commit, got:\n%s", out)
	}
	if strings.Contains(out, "shareda") {
		t.Errorf("shared commit must be filtered out of SCM diff, got:\n%s", out)
	}
}
