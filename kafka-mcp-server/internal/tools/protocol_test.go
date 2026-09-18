package tools

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
)

func TestPartialResultsAndOutputBudget(t *testing.T) {
	r, _, _ := reply("demo", "op", nil, map[string]any{"resources": []any{map[string]any{"status": "ok"}, map[string]any{"status": "TOPIC_AUTHORIZATION_FAILED"}}, "truncated": true}, "")
	var v map[string]any
	json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &v)
	if v["status"] != "partial" || v["truncated"] != true {
		t.Fatalf("partial failure hidden: %v", v)
	}
	r, _, _ = reply("demo", "op", nil, map[string]any{"value": strings.Repeat("x", 300000)}, "")
	if !r.IsError || len(r.Content[0].(*mcp.TextContent).Text) > 256*1024 {
		t.Fatal("output cap failed")
	}
}

func TestNamedSubqueryStatus(t *testing.T) {
	for _, key := range []string{"offsets_status", "metadata_status"} {
		r, _, _ := reply("demo", "op", nil, map[string]any{"status": "ok", key: "permission_denied"}, "")
		var v map[string]any
		json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &v)
		if v["status"] != "partial" {
			t.Fatalf("%s failure hidden", key)
		}
	}
	r, _, _ := reply("demo", "op", nil, map[string]any{"status": "at_end", "records": []any{}}, "")
	if r.IsError {
		t.Fatal("empty end boundary is not request failure")
	}
}

func TestRegisteredToolsOverMCP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Register(srv, Deps{})
	st, ct := mcp.NewInMemoryTransports()
	ss, e := srv.Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, e := client.Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	listed, e := cs.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(listed.Tools) != 8 {
		t.Fatalf("tools=%d", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("missing readonly %s", tool.Name)
		}
	}
	if _, e = cs.CallTool(ctx, &mcp.CallToolParams{Name: "kafka_offsets_query", Arguments: map[string]any{"topic": "orders-a"}}); e == nil {
		t.Fatal("schema accepted missing target and partition")
	}
}
