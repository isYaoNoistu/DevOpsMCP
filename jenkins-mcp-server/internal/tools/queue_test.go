package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestListQueue_RenderAndFilter(t *testing.T) {
	now := time.Now().UnixMilli()
	listing := apiQueueListing{Items: []apiQueueItem{
		{
			ID:           42,
			Task:         apiQueueTask{Name: "team-build", URL: "https://j/job/team/job/build/"},
			InQueueSince: now - 65_000,
			Why:          "Waiting for next available executor on linux",
			Buildable:    true,
		},
		{
			ID:           43,
			Task:         apiQueueTask{Name: "other-build", URL: "https://j/job/other/job/build/"},
			InQueueSince: now - 5_000,
			Blocked:      true,
		},
	}}
	d, srv := newDepsAgainstHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/queue/api/json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(listing)
	}))
	defer srv.Close()

	res, _, err := d.ListQueue(context.Background(), nil, ListQueueInput{})
	if err != nil {
		t.Fatalf("ListQueue: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{"team-build", "other-build", "Waiting for next available executor", "buildable", "blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}

	filtered, _, err := d.ListQueue(context.Background(), nil, ListQueueInput{JobPathPrefix: "/job/team/"})
	if err != nil {
		t.Fatalf("ListQueue filtered: %v", err)
	}
	fout := resultText(t, filtered)
	if !strings.Contains(fout, "team-build") {
		t.Errorf("expected team-build in filtered output, got:\n%s", fout)
	}
	if strings.Contains(fout, "other-build") {
		t.Errorf("did not expect other-build in filtered output, got:\n%s", fout)
	}
}
