package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

func newPipelineDeps(t *testing.T, handler http.HandlerFunc) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

func TestGetPipelineStages_Table(t *testing.T) {
	d, srv := newPipelineDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/svc/lastBuild/wfapi/describe" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"status":"FAILED",
			"durationMillis":1500,
			"stages":[{"id":"54","name":"Deploy","status":"FAILED","durationMillis":1200}]
		}`))
	}))
	defer srv.Close()

	res, _, err := d.GetPipelineStages(context.Background(), nil, GetPipelineStagesInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetPipelineStages: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{"FAILED", "Deploy", "54", "get_stage_log", "search_console_log"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "get_console_log_path") {
		t.Errorf("did not expect get_console_log_path in:\n%s", out)
	}
}

func TestGetStageLog_EmptyPointsAtConsoleSearch(t *testing.T) {
	d, srv := newPipelineDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/svc/42/execution/node/54/wfapi/log" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"nodeId":"54","nodeStatus":"FAILED","length":0,"hasMore":false,"text":""}`))
	}))
	defer srv.Close()

	res, _, err := d.GetStageLog(context.Background(), nil, GetStageLogInput{
		JobPath: "svc", BuildNumber: 42, StageID: "54",
	})
	if err != nil {
		t.Fatalf("GetStageLog: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "length=0") || !strings.Contains(out, "search_console_log") {
		t.Errorf("expected empty-log hint, got:\n%s", out)
	}
}

func TestGetStageLog_RejectsTraversalID(t *testing.T) {
	d := Deps{}
	_, _, err := d.GetStageLog(context.Background(), nil, GetStageLogInput{
		JobPath: "svc", StageID: "../script",
	})
	if err == nil {
		t.Fatal("expected invalid stage_id")
	}
}
