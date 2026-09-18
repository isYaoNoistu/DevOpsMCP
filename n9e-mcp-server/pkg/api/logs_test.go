package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n9e/n9e-mcp-server/pkg/client"
)

func TestPrepareLogQueryConstrainsForwardedPayload(t *testing.T) {
	body := map[string]any{"cate": "elasticsearch", "limit": float64(9999), "query": []any{map[string]any{"start": float64(100), "end": float64(200), "limit": float64(9999)}}}
	got, meta, err := prepareLogQuery(body, 50)
	if err != nil {
		t.Fatal(err)
	}
	q := got["query"].([]any)[0].(map[string]any)
	if q["limit"] != 50 {
		t.Fatalf("forwarded limit = %v, want 50", q["limit"])
	}
	if meta.Start != 100 || meta.End != 200 || meta.Limit != 50 {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestQueryLogsForwardsConstrainedBody(t *testing.T) {
	var forwarded map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&forwarded); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"data":[],"error":null}`))
	}))
	defer srv.Close()
	c, _ := client.NewClient("token", srv.URL, "test")
	res := callTool(t, queryLogsTool(func(context.Context) *client.Client { return c }),
		`{"body":{"cate":"elasticsearch","datasource_id":1,"limit":9999,"query":[{"start":100,"end":200,"limit":9999}]},"limit":9999,"start":100,"end":200}`)
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	query := forwarded["query"].([]any)[0].(map[string]any)
	if forwarded["limit"] != float64(maxLogLimit) || query["limit"] != float64(maxLogLimit) {
		t.Fatalf("unconstrained forwarded body: %#v", forwarded)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &output); err != nil {
		t.Fatal(err)
	}
	if output["start"] != float64(100) || output["end"] != float64(200) || output["limit"] != float64(maxLogLimit) {
		t.Fatalf("untruthful metadata: %#v", output)
	}
}

func TestPrepareLogQueryRejectsUnsafeWindows(t *testing.T) {
	tests := []map[string]any{
		{"cate": "elasticsearch", "query": []any{map[string]any{"start": float64(100), "end": float64(100 + maxLogRangeSeconds + 1)}}},
		{"cate": "elasticsearch", "query": []any{map[string]any{"from": float64(100), "to": float64(200)}}},
		{"cate": "elasticsearch", "query": []any{map[string]any{"start": float64(200), "end": float64(100)}}},
	}
	for _, body := range tests {
		if _, _, err := prepareLogQuery(body, defaultLogLimit); err == nil {
			t.Fatalf("expected rejection for %#v", body)
		}
	}
}

func TestPrepareLogQueryCapsRequestedLimit(t *testing.T) {
	body := map[string]any{"cate": "opensearch", "query": []any{map[string]any{"start": float64(100), "end": float64(200)}}}
	got, meta, err := prepareLogQuery(body, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Limit != maxLogLimit {
		t.Fatalf("limit = %d, want cap %d", meta.Limit, maxLogLimit)
	}
	if got["query"].([]any)[0].(map[string]any)["limit"] != maxLogLimit {
		t.Fatal("forwarded query was not capped")
	}
}

func TestPrepareLogQueryUsesSmallestActualLimit(t *testing.T) {
	body := map[string]any{"cate": "elasticsearch", "limit": float64(80), "query": []any{map[string]any{"start": float64(100), "end": float64(200), "limit": float64(25)}}}
	got, meta, err := prepareLogQuery(body, 100)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Limit != 25 || got["limit"] != 25 || got["query"].([]any)[0].(map[string]any)["limit"] != 25 {
		t.Fatalf("limits not normalized to actual constraint: meta=%+v body=%#v", meta, got)
	}
}
