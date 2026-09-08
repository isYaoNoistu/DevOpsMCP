package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

func newSCMDeps(t *testing.T, responses map[string]scmBuildAPI) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

// 2026-05-16 12:04 UTC = 1747396800000 ms (offset by ~4 mins inside the day
// to make rendered values deterministic across timezones).
const sampleTimestampMs int64 = 1747396800000

func TestGetSCMContext_SingleChangeSet(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/team/job/svc/86/api/json": {
			ChangeSets: []scmChangeSet{{
				Kind: "git",
				Items: []scmItem{
					{
						CommitID: "abc1234deadbeef", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "alice"},
						Msg:    "Fix flake in PaymentSpec",
						Paths: []scmPath{
							{File: "internal/payment/processor.go", EditType: "edit"},
							{File: "internal/payment/processor_test.go", EditType: "add"},
						},
					},
					{
						CommitID: "f00ba12f00ba12", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "bob"},
						Msg:    "Remove dead config",
						Paths:  []scmPath{{File: "config/old.yaml", EditType: "delete"}},
					},
				},
			}},
			Culprits: []scmCulprit{{FullName: "alice"}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:     "team/svc",
		BuildNumber: 86,
	})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"SCM context for team/svc build 86",
		"Culprits: alice",
		"abc1234",
		"alice",
		"\"Fix flake in PaymentSpec\"",
		"M  internal/payment/processor.go",
		"A  internal/payment/processor_test.go",
		"f00ba12",
		"bob",
		"D  config/old.yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Single-set should NOT print a per-set header.
	if strings.Contains(out, "change set 1") {
		t.Errorf("single change set should not print per-set header, got:\n%s", out)
	}
}

func TestGetSCMContext_MultiChangeSetPipeline(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{
				{
					Kind: "git",
					Items: []scmItem{{
						CommitID: "aaa1111", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "alice"},
						Msg:    "App change",
						Paths:  []scmPath{{File: "app/main.go", EditType: "edit"}},
					}},
				},
				{
					Kind: "git",
					Items: []scmItem{{
						CommitID: "bbb2222", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "bob"},
						Msg:    "Lib change",
						Paths:  []scmPath{{File: "vendor/lib.go", EditType: "edit"}},
					}},
				},
			},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"— change set 1 (git) — 1 commit(s)",
		"— change set 2 (git) — 1 commit(s)",
		"aaa1111",
		"bbb2222",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestGetSCMContext_PathFilter(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{{
				Items: []scmItem{
					{
						CommitID: "aaa1111", Author: scmAuthor{FullName: "alice"},
						Msg:   "Payment change",
						Paths: []scmPath{{File: "internal/payment/x.go", EditType: "edit"}},
					},
					{
						CommitID: "bbb2222", Author: scmAuthor{FullName: "bob"},
						Msg:   "Docs change",
						Paths: []scmPath{{File: "docs/README.md", EditType: "edit"}},
					},
					{
						CommitID: "ccc3333", Author: scmAuthor{FullName: "carol"},
						Msg: "Mixed change",
						Paths: []scmPath{
							{File: "docs/api.md", EditType: "edit"},
							{File: "internal/payment/y.go", EditType: "edit"},
						},
					},
				},
			}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:    "svc",
		PathFilter: "^internal/payment/",
	})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "aaa1111") {
		t.Errorf("expected aaa1111 (touches internal/payment) to remain, got:\n%s", out)
	}
	if !strings.Contains(out, "ccc3333") {
		t.Errorf("expected ccc3333 (touches internal/payment among other paths) to remain, got:\n%s", out)
	}
	if strings.Contains(out, "bbb2222") {
		t.Errorf("expected bbb2222 (docs only) to be filtered out, got:\n%s", out)
	}
	if !strings.Contains(out, `2 of 3 commits matched path_filter "^internal/payment/"`) {
		t.Errorf("expected path_filter summary line, got:\n%s", out)
	}
}

func TestGetSCMContext_MaxCommitsTruncates(t *testing.T) {
	items := make([]scmItem, 5)
	for i := range items {
		items[i] = scmItem{
			CommitID: "0000000", Author: scmAuthor{FullName: "alice"}, Msg: "commit",
			Paths: []scmPath{{File: "x.go", EditType: "edit"}},
		}
	}
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {ChangeSets: []scmChangeSet{{Items: items}}},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:    "svc",
		MaxCommits: 2,
	})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "stopped at max_commits=2") {
		t.Errorf("expected truncation footer, got:\n%s", out)
	}
	// Exactly 2 commit header lines should be rendered.
	if got := strings.Count(out, " alice "); got != 2 {
		t.Errorf("expected 2 commit lines rendered, got %d:\n%s", got, out)
	}
}

func TestGetSCMContext_EmptyChangeSet(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {ChangeSets: []scmChangeSet{}},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "(no commits in change set)") {
		t.Errorf("expected no-commits hint, got:\n%s", out)
	}
}

func TestGetSCMContext_GracefulMissingFields(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{{
				Items: []scmItem{{
					// No CommitID, no Timestamp, no Author, empty editType.
					Msg:   "Mystery commit",
					Paths: []scmPath{{File: "weird.txt", EditType: ""}},
				}},
			}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{"(no id)", "(unknown)", "(no timestamp)", "?  weird.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q placeholder in output, got:\n%s", want, out)
		}
	}
}

func TestGetSCMContext_InvalidPathFilter(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{})
	defer srv.Close()

	_, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:    "svc",
		PathFilter: "[invalid",
	})
	if err == nil {
		t.Fatal("expected error from invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "path_filter") {
		t.Errorf("expected error to mention path_filter, got: %v", err)
	}
}

func TestGetSCMContext_MissingJobPath(t *testing.T) {
	d := Deps{}
	_, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{})
	if err == nil {
		t.Fatal("expected error for empty job_path, got nil")
	}
}

func TestGetSCMContext_MultilineCommitMsg(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{{
				Items: []scmItem{{
					CommitID: "abc1234", Author: scmAuthor{FullName: "alice"},
					Msg:   "Subject line\n\nA longer body that the table\nshould not echo verbatim.",
					Paths: []scmPath{{File: "x.go", EditType: "edit"}},
				}},
			}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "\"Subject line\"") {
		t.Errorf("expected just the subject line in quotes, got:\n%s", out)
	}
	if strings.Contains(out, "longer body") {
		t.Errorf("commit body must not appear in the per-commit header line, got:\n%s", out)
	}
}

func TestEditCode(t *testing.T) {
	cases := map[string]string{
		"add":     "A",
		"edit":    "M",
		"modify":  "M",
		"delete":  "D",
		"":        "?",
		"rename":  "R",
		"copy":    "C",
		"unknown": "U",
		"ñew":     "Ñ",
		"日本語":     "日",
	}
	for in, want := range cases {
		if got := editCode(in); got != want {
			t.Errorf("editCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// bisectFixture seeds a fake Jenkins for the last_green_build /
// changes_since_last_green tests. Paths in `bodies` get the literal JSON
// string back with HTTP 200; paths in `statuses` get that status with no
// body. Missing paths 404.
type bisectFixture struct {
	bodies   map[string]string
	statuses map[string]int
}

func newBisectDeps(t *testing.T, f bisectFixture) (Deps, *httptest.Server) {
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

func TestLastGreenBuild_HappyPath(t *testing.T) {
	d, srv := newBisectDeps(t, bisectFixture{
		bodies: map[string]string{
			"/job/team/job/svc/lastSuccessfulBuild/api/json": `{"number":42,"url":"https://jenkins.example.com/job/team/job/svc/42/","timestamp":1747396800000}`,
		},
	})
	defer srv.Close()

	res, _, err := d.LastGreenBuild(context.Background(), nil, LastGreenBuildInput{JobPath: "team/svc"})
	if err != nil {
		t.Fatalf("LastGreenBuild: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"Last green build of team/svc: #42",
		"2025-05-16 12:00 (UTC)",
		"https://jenkins.example.com/job/team/job/svc/42/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestLastGreenBuild_NoGreenEverRendersHint(t *testing.T) {
	d, srv := newBisectDeps(t, bisectFixture{
		statuses: map[string]int{
			"/job/svc/lastSuccessfulBuild/api/json": http.StatusNotFound,
		},
	})
	defer srv.Close()

	res, _, err := d.LastGreenBuild(context.Background(), nil, LastGreenBuildInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("expected hint, not error: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "no successful build yet for svc") {
		t.Errorf("expected no-green hint, got:\n%s", out)
	}
}

func TestLastGreenBuild_MissingJobPath(t *testing.T) {
	d := Deps{}
	if _, _, err := d.LastGreenBuild(context.Background(), nil, LastGreenBuildInput{}); err == nil {
		t.Fatal("expected error for empty job_path")
	}
}

// bisectBuildBody builds the JSON shape returned for /job/.../<N>/api/json
// with previousCompletedBuild pointer.
func bisectBuildBody(prev int64, commits ...scmItem) string {
	b := struct {
		ChangeSets             []scmChangeSet `json:"changeSets"`
		PreviousCompletedBuild *struct {
			Number int64 `json:"number"`
		} `json:"previousCompletedBuild"`
	}{
		ChangeSets: []scmChangeSet{{Kind: "git", Items: commits}},
	}
	if prev > 0 {
		b.PreviousCompletedBuild = &struct {
			Number int64 `json:"number"`
		}{Number: prev}
	}
	out, _ := json.Marshal(b)
	return string(out)
}

func commit(id, author, msg, file string) scmItem {
	return scmItem{
		CommitID: id, Timestamp: sampleTimestampMs,
		Author: scmAuthor{FullName: author}, Msg: msg,
		Paths: []scmPath{{File: file, EditType: "edit"}},
	}
}

func TestChangesSinceLastGreen_HappyPathUnionsAndDedupes(t *testing.T) {
	// G=40, C=42. Build 42 → prev 41 → prev 40 (stops since prev <= G).
	// Commit "yyy2222" appears in both 42 and 41 — must render once.
	d, srv := newBisectDeps(t, bisectFixture{
		bodies: map[string]string{
			"/job/svc/lastSuccessfulBuild/api/json": `{"number":40}`,
			"/job/svc/lastCompletedBuild/api/json":  `{"number":42}`,
			"/job/svc/42/api/json": bisectBuildBody(41,
				commit("xxx1111", "alice", "Fix payment", "internal/payment/x.go"),
				commit("yyy2222", "bob", "Refactor cache", "internal/jenkins/cache.go"),
			),
			"/job/svc/41/api/json": bisectBuildBody(40,
				commit("yyy2222", "bob", "Refactor cache", "internal/jenkins/cache.go"),
				commit("zzz3333", "carol", "Tighten retries", "internal/jenkins/client.go"),
			),
		},
	})
	defer srv.Close()

	res, _, err := d.ChangesSinceLastGreen(context.Background(), nil, ChangesSinceLastGreenInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("ChangesSinceLastGreen: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"3 commits across 2 builds since last green #40 (latest: #42)",
		"xxx1111",
		"yyy2222",
		"zzz3333",
		"M  internal/payment/x.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "yyy2222"); got != 1 {
		t.Errorf("expected yyy2222 to render exactly once, got %d:\n%s", got, out)
	}
}

func TestChangesSinceLastGreen_AllGreen(t *testing.T) {
	d, srv := newBisectDeps(t, bisectFixture{
		bodies: map[string]string{
			"/job/svc/lastSuccessfulBuild/api/json": `{"number":42}`,
			"/job/svc/lastCompletedBuild/api/json":  `{"number":42}`,
		},
	})
	defer srv.Close()

	res, _, err := d.ChangesSinceLastGreen(context.Background(), nil, ChangesSinceLastGreenInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("ChangesSinceLastGreen: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "all green") {
		t.Errorf("expected 'all green' message, got:\n%s", out)
	}
	if !strings.Contains(out, "#42") {
		t.Errorf("expected build #42 mentioned, got:\n%s", out)
	}
}

func TestChangesSinceLastGreen_NoGreenEverRendersHint(t *testing.T) {
	d, srv := newBisectDeps(t, bisectFixture{
		statuses: map[string]int{
			"/job/svc/lastSuccessfulBuild/api/json": http.StatusNotFound,
		},
	})
	defer srv.Close()

	res, _, err := d.ChangesSinceLastGreen(context.Background(), nil, ChangesSinceLastGreenInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("expected hint, not error: %v", err)
	}
	if !strings.Contains(resultText(t, res), "no successful build yet for svc") {
		t.Errorf("expected no-green hint, got:\n%s", resultText(t, res))
	}
}

func TestChangesSinceLastGreen_MaxCommitsTruncates(t *testing.T) {
	// 5 commits across 2 builds, max_commits=2 → render 2 + footer.
	d, srv := newBisectDeps(t, bisectFixture{
		bodies: map[string]string{
			"/job/svc/lastSuccessfulBuild/api/json": `{"number":40}`,
			"/job/svc/lastCompletedBuild/api/json":  `{"number":41}`,
			"/job/svc/41/api/json": bisectBuildBody(40,
				commit("aaa", "alice", "one", "a.go"),
				commit("bbb", "alice", "two", "b.go"),
				commit("ccc", "alice", "three", "c.go"),
				commit("ddd", "alice", "four", "d.go"),
				commit("eee", "alice", "five", "e.go"),
			),
		},
	})
	defer srv.Close()

	res, _, err := d.ChangesSinceLastGreen(context.Background(), nil, ChangesSinceLastGreenInput{
		JobPath: "svc", MaxCommits: 2,
	})
	if err != nil {
		t.Fatalf("ChangesSinceLastGreen: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "stopped at max_commits=2") {
		t.Errorf("expected truncation footer, got:\n%s", out)
	}
}

func TestChangesSinceLastGreen_WideWindowEmitsWarning(t *testing.T) {
	// G=0, C=52 → 52 > 50 → warning footer fires. Use a dynamic handler
	// rather than enumerating 52 fixture entries.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/svc/lastSuccessfulBuild/api/json":
			_, _ = w.Write([]byte(`{"number":0}`))
		case "/job/svc/lastCompletedBuild/api/json":
			_, _ = w.Write([]byte(`{"number":52}`))
		default:
			// any /job/svc/<N>/api/json — point each at <N-1>.
			var n int64
			if _, err := fmt.Sscanf(r.URL.Path, "/job/svc/%d/api/json", &n); err != nil || n <= 0 {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(bisectBuildBody(n-1,
				commit(fmt.Sprintf("c%02d", n), "alice", "msg", "f.go"),
			)))
		}
	}))
	defer srv.Close()
	cli, _ := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	d := Deps{Client: cli}

	res, _, err := d.ChangesSinceLastGreen(context.Background(), nil, ChangesSinceLastGreenInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("ChangesSinceLastGreen: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "wide window") {
		t.Errorf("expected wide-window warning footer, got:\n%s", out)
	}
}

func TestChangesSinceLastGreen_PathFilter(t *testing.T) {
	d, srv := newBisectDeps(t, bisectFixture{
		bodies: map[string]string{
			"/job/svc/lastSuccessfulBuild/api/json": `{"number":40}`,
			"/job/svc/lastCompletedBuild/api/json":  `{"number":41}`,
			"/job/svc/41/api/json": bisectBuildBody(40,
				commit("aaa", "alice", "payment", "internal/payment/x.go"),
				commit("bbb", "bob", "docs", "docs/README.md"),
			),
		},
	})
	defer srv.Close()

	res, _, err := d.ChangesSinceLastGreen(context.Background(), nil, ChangesSinceLastGreenInput{
		JobPath:    "svc",
		PathFilter: "^internal/payment/",
	})
	if err != nil {
		t.Fatalf("ChangesSinceLastGreen: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "aaa") {
		t.Errorf("expected aaa to remain, got:\n%s", out)
	}
	if strings.Contains(out, "bbb") {
		t.Errorf("expected bbb (docs only) to be filtered out, got:\n%s", out)
	}
	if !strings.Contains(out, `matched path_filter "^internal/payment/"`) {
		t.Errorf("expected path_filter summary, got:\n%s", out)
	}
}

func TestChangesSinceLastGreen_MissingJobPath(t *testing.T) {
	d := Deps{}
	if _, _, err := d.ChangesSinceLastGreen(context.Background(), nil, ChangesSinceLastGreenInput{}); err == nil {
		t.Fatal("expected error for empty job_path")
	}
}
