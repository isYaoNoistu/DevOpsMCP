package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// recentFailFixture seeds responses for find_recent_failures tests. paths
// in `bodies` get JSON back; statuses set non-200 for paths that should
// fail.
type recentFailFixture struct {
	bodies   map[string]string
	statuses map[string]int
}

func newRecentFailDeps(t *testing.T, f recentFailFixture) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s, ok := f.statuses[r.URL.Path]; ok {
			w.WriteHeader(s)
			return
		}
		if body, ok := f.bodies[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

// Compact JSON helpers ----------

func listingTwoJobs(names ...string) string {
	var b strings.Builder
	b.WriteString(`{"jobs":[`)
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"name":%q,"_class":"hudson.model.FreeStyleProject","url":"u","color":"blue"}`, n)
	}
	b.WriteString(`]}`)
	return b.String()
}

type buildRow struct {
	Number    int64
	Result    string
	Timestamp int64 // ms since epoch
	Duration  int64 // ms
	URL       string
}

func buildsJSON(rows ...buildRow) string {
	var b strings.Builder
	b.WriteString(`{"builds":[`)
	for i, r := range rows {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"number":%d,"result":%q,"timestamp":%d,"duration":%d,"url":%q}`,
			r.Number, r.Result, r.Timestamp, r.Duration, r.URL)
	}
	b.WriteString(`]}`)
	return b.String()
}

func msNow() int64 {
	return time.Now().UnixMilli()
}

func TestFindRecentFailures_HappyPath_FiltersByResultAndWindow(t *testing.T) {
	now := msNow()
	d, srv := newRecentFailDeps(t, recentFailFixture{
		bodies: map[string]string{
			"/api/json": listingTwoJobs("job-a", "job-b"),
			"/job/job-a/api/json": buildsJSON(
				buildRow{Number: 92, Result: "FAILURE", Timestamp: now - int64(2*time.Hour/time.Millisecond), Duration: 272000, URL: "ja92"},
				buildRow{Number: 91, Result: "SUCCESS", Timestamp: now - int64(3*time.Hour/time.Millisecond), Duration: 60000, URL: "ja91"},
			),
			"/job/job-b/api/json": buildsJSON(
				buildRow{Number: 831, Result: "FAILURE", Timestamp: now - int64(6*time.Hour/time.Millisecond), Duration: 72000, URL: "jb831"},
				buildRow{Number: 830, Result: "UNSTABLE", Timestamp: now - int64(7*time.Hour/time.Millisecond), Duration: 65000, URL: "jb830"},
			),
		},
	})
	defer srv.Close()

	res, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{})
	if err != nil {
		t.Fatalf("FindRecentFailures: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		`Recent failures under "" (last 24h0m0s, filter=FAILURE)`,
		"job-a",
		"#92",
		"FAILURE",
		"4m32s",
		"job-b",
		"#831",
		"1m12s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// UNSTABLE should be excluded under the default FAILURE filter.
	if strings.Contains(out, "UNSTABLE") {
		t.Errorf("expected UNSTABLE excluded under FAILURE filter, got:\n%s", out)
	}
}

func TestFindRecentFailures_ParseDurationAcceptsDays(t *testing.T) {
	got, err := parseLookback("7d")
	if err != nil {
		t.Fatalf("parseLookback(7d): %v", err)
	}
	if got != 7*24*time.Hour {
		t.Errorf("parseLookback(7d) = %v, want 168h", got)
	}
}

func TestFindRecentFailures_AnyNonSuccess(t *testing.T) {
	now := msNow()
	d, srv := newRecentFailDeps(t, recentFailFixture{
		bodies: map[string]string{
			"/api/json": listingTwoJobs("svc"),
			"/job/svc/api/json": buildsJSON(
				buildRow{Number: 10, Result: "FAILURE", Timestamp: now - int64(time.Hour/time.Millisecond)},
				buildRow{Number: 9, Result: "UNSTABLE", Timestamp: now - int64(2*time.Hour/time.Millisecond)},
				buildRow{Number: 8, Result: "ABORTED", Timestamp: now - int64(3*time.Hour/time.Millisecond)},
				buildRow{Number: 7, Result: "SUCCESS", Timestamp: now - int64(4*time.Hour/time.Millisecond)},
			),
		},
	})
	defer srv.Close()

	res, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{
		ResultFilter: "ANY_NON_SUCCESS",
	})
	if err != nil {
		t.Fatalf("FindRecentFailures: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{"FAILURE", "UNSTABLE", "ABORTED"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#7") {
		t.Errorf("expected SUCCESS build #7 excluded, got:\n%s", out)
	}
}

func TestFindRecentFailures_WindowExcludesOlder(t *testing.T) {
	now := msNow()
	d, srv := newRecentFailDeps(t, recentFailFixture{
		bodies: map[string]string{
			"/api/json": listingTwoJobs("svc"),
			"/job/svc/api/json": buildsJSON(
				buildRow{Number: 10, Result: "FAILURE", Timestamp: now - int64(30*time.Minute/time.Millisecond)},
				buildRow{Number: 9, Result: "FAILURE", Timestamp: now - int64(2*time.Hour/time.Millisecond)},
			),
		},
	})
	defer srv.Close()

	res, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{Since: "1h"})
	if err != nil {
		t.Fatalf("FindRecentFailures: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "#10") {
		t.Errorf("expected #10 (30m ago) in 1h window, got:\n%s", out)
	}
	if strings.Contains(out, "#9") {
		t.Errorf("expected #9 (2h ago) excluded from 1h window, got:\n%s", out)
	}
}

func TestFindRecentFailures_MaxResultsTruncates(t *testing.T) {
	now := msNow()
	d, srv := newRecentFailDeps(t, recentFailFixture{
		bodies: map[string]string{
			"/api/json": listingTwoJobs("svc"),
			"/job/svc/api/json": buildsJSON(
				buildRow{Number: 5, Result: "FAILURE", Timestamp: now - int64(1*time.Hour/time.Millisecond)},
				buildRow{Number: 4, Result: "FAILURE", Timestamp: now - int64(2*time.Hour/time.Millisecond)},
				buildRow{Number: 3, Result: "FAILURE", Timestamp: now - int64(3*time.Hour/time.Millisecond)},
			),
		},
	})
	defer srv.Close()

	res, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{MaxResults: 2})
	if err != nil {
		t.Fatalf("FindRecentFailures: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "stopped at max_results=2") {
		t.Errorf("expected truncation footer, got:\n%s", out)
	}
}

func TestFindRecentFailures_WideWindowEmitsHint(t *testing.T) {
	now := msNow()
	d, srv := newRecentFailDeps(t, recentFailFixture{
		bodies: map[string]string{
			"/api/json": listingTwoJobs("svc"),
			"/job/svc/api/json": buildsJSON(
				buildRow{Number: 1, Result: "FAILURE", Timestamp: now - int64(time.Hour/time.Millisecond)},
			),
		},
	})
	defer srv.Close()

	res, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{Since: "14d"})
	if err != nil {
		t.Fatalf("FindRecentFailures: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "wide window") {
		t.Errorf("expected wide-window note for 14d, got:\n%s", out)
	}
}

func TestFindRecentFailures_FoldersExcludedFromFanOut(t *testing.T) {
	now := msNow()
	d, srv := newRecentFailDeps(t, recentFailFixture{
		bodies: map[string]string{
			// Root has a folder + a leaf.
			"/api/json": `{"jobs":[
				{"name":"sub","_class":"com.cloudbees.hudson.plugins.folder.Folder"},
				{"name":"leaf","_class":"hudson.model.FreeStyleProject"}
			]}`,
			"/job/sub/api/json": `{"jobs":[{"name":"nested","_class":"hudson.model.FreeStyleProject"}]}`,
			"/job/sub/job/nested/api/json": buildsJSON(
				buildRow{Number: 1, Result: "FAILURE", Timestamp: now - int64(time.Hour/time.Millisecond)},
			),
			"/job/leaf/api/json": buildsJSON(
				buildRow{Number: 2, Result: "FAILURE", Timestamp: now - int64(time.Hour/time.Millisecond)},
			),
		},
	})
	defer srv.Close()

	res, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{})
	if err != nil {
		t.Fatalf("FindRecentFailures: %v", err)
	}
	out := resultText(t, res)
	// Folder "sub" should not appear as a job row; nested leaf should.
	if !strings.Contains(out, "sub/nested") {
		t.Errorf("expected sub/nested leaf in results, got:\n%s", out)
	}
	if !strings.Contains(out, "leaf") {
		t.Errorf("expected root leaf in results, got:\n%s", out)
	}
	// Footer should say 2 jobs scanned (folder excluded from fan-out).
	if !strings.Contains(out, "2 jobs scanned") {
		t.Errorf("expected '2 jobs scanned' (folder excluded), got:\n%s", out)
	}
}

func TestFindRecentFailures_InvalidSince(t *testing.T) {
	d := Deps{}
	_, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{Since: "garbage"})
	if err == nil {
		t.Fatal("expected error for invalid since")
	}
	if !strings.Contains(err.Error(), "since") {
		t.Errorf("expected error to mention 'since', got: %v", err)
	}
}

func TestFindRecentFailures_InvalidResultFilter(t *testing.T) {
	d := Deps{}
	_, _, err := d.FindRecentFailures(context.Background(), nil, FindRecentFailuresInput{ResultFilter: "BOGUS"})
	if err == nil {
		t.Fatal("expected error for invalid result_filter")
	}
	if !strings.Contains(err.Error(), "result_filter") {
		t.Errorf("expected error to mention 'result_filter', got: %v", err)
	}
}
