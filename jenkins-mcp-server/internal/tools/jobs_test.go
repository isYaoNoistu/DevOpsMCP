package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// newDepsAgainstMock spins up an httptest server that serves the given
// canned /api/json listings keyed by request path, and returns a Deps value
// pointing at it.
func newDepsAgainstMock(t *testing.T, listings map[string]apiListing) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listing, ok := listings[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(listing)
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("empty tool result")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func TestListJobs_Root(t *testing.T) {
	d, srv := newDepsAgainstMock(t, map[string]apiListing{
		"/api/json": {Jobs: []apiJob{
			{Name: "alpha", Class: "hudson.model.FreeStyleProject", Color: "blue",
				LastBuild: &apiBuild{Number: 42, Result: "SUCCESS"}},
			{Name: "beta-folder", Class: "com.cloudbees.hudson.plugins.folder.Folder"},
		}},
	})
	defer srv.Close()

	res, _, err := d.ListJobs(context.Background(), nil, ListJobsInput{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected alpha in output, got:\n%s", out)
	}
	if !strings.Contains(out, "beta-folder") {
		t.Errorf("expected beta-folder in output, got:\n%s", out)
	}
	if !strings.Contains(out, "folder") {
		t.Errorf("expected 'folder' row type for beta-folder, got:\n%s", out)
	}
	if !strings.Contains(out, "SUCCESS") {
		t.Errorf("expected SUCCESS result for alpha, got:\n%s", out)
	}
}

func TestListJobs_RecursiveAndNameFilter(t *testing.T) {
	d, srv := newDepsAgainstMock(t, map[string]apiListing{
		"/api/json": {Jobs: []apiJob{
			{Name: "team", Class: "com.cloudbees.hudson.plugins.folder.Folder"},
		}},
		"/job/team/api/json": {Jobs: []apiJob{
			{Name: "api-tests", Class: "hudson.model.FreeStyleProject", Color: "blue"},
			{Name: "ui-tests", Class: "hudson.model.FreeStyleProject", Color: "red"},
		}},
	})
	defer srv.Close()

	res, _, err := d.ListJobs(context.Background(), nil, ListJobsInput{
		Recursive:  true,
		NameFilter: "api",
	})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "team/api-tests") {
		t.Errorf("expected team/api-tests in output, got:\n%s", out)
	}
	if strings.Contains(out, "ui-tests") {
		t.Errorf("did not expect filtered-out ui-tests, got:\n%s", out)
	}
	// The folder itself doesn't match "api" by name, so it must not appear in
	// the rendered output even though we recursed into it.
	if strings.Contains(out, "  team\n") || strings.Contains(out, "  team ") {
		t.Errorf("did not expect the folder row 'team' itself (filtered out by name_filter), got:\n%s", out)
	}
}

func TestListJobs_NonRecursiveSkipsFolderContents(t *testing.T) {
	d, srv := newDepsAgainstMock(t, map[string]apiListing{
		"/api/json": {Jobs: []apiJob{
			{Name: "team", Class: "com.cloudbees.hudson.plugins.folder.Folder"},
		}},
		"/job/team/api/json": {Jobs: []apiJob{
			{Name: "inner", Class: "hudson.model.FreeStyleProject"},
		}},
	})
	defer srv.Close()

	res, _, err := d.ListJobs(context.Background(), nil, ListJobsInput{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "team") {
		t.Errorf("expected team folder in output, got:\n%s", out)
	}
	if strings.Contains(out, "inner") {
		t.Errorf("did not expect nested job without recursive=true, got:\n%s", out)
	}
}

func TestListJobs_InvalidNameFilter(t *testing.T) {
	d, srv := newDepsAgainstMock(t, map[string]apiListing{
		"/api/json": {Jobs: []apiJob{}},
	})
	defer srv.Close()

	_, _, err := d.ListJobs(context.Background(), nil, ListJobsInput{NameFilter: "[invalid"})
	if err == nil {
		t.Fatal("expected error from invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "name_filter") {
		t.Errorf("expected error to mention name_filter, got: %v", err)
	}
}
