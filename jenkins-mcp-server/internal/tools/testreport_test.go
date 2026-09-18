package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

func TestGetTestReportRejectsNegativeStackTraceLinesBeforeFetching(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	d := Deps{Client: client}

	_, _, err = d.GetTestReport(context.Background(), nil, GetTestReportInput{
		JobPath: "svc", StackTraceLines: -1,
	})
	if err == nil || !strings.Contains(err.Error(), "stack_trace_lines must be >= 0") {
		t.Fatalf("expected stack_trace_lines validation error, got %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("validation made %d HTTP requests, want 0", got)
	}
}

func TestGetTestReportCapsStackTraceLines(t *testing.T) {
	stack := strings.Repeat("line\n", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"failCount":1,"suites":[{"name":"suite","cases":[{"name":"case","status":"FAILED","errorStackTrace":` + quotedJSON(stack) + `}]}]}`))
	}))
	defer srv.Close()

	client, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	res, _, err := (Deps{Client: client}).GetTestReport(context.Background(), nil, GetTestReportInput{
		JobPath: "svc", StackTraceLines: 10000,
	})
	if err != nil {
		t.Fatalf("GetTestReport: %v", err)
	}
	if out := resultText(t, res); !strings.Contains(out, "first 200 + last 200 lines") {
		t.Fatalf("expected capped stack trace size, got:\n%s", out)
	}
}

func quotedJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
