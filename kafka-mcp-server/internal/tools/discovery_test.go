package tools

import (
	"context"
	"kafka-mcp-server/internal/targets"
	"testing"
)

type listBackend struct {
	Backend
	limit  int
	after  string
	topics []string
}

func (b *listBackend) Close() {}
func (b *listBackend) ListTopics(_ context.Context, q, a string, l int, internal bool) (any, error) {
	b.limit = l
	b.after = a
	return map[string]any{"status": "ok"}, nil
}
func (b *listBackend) ListGroups(_ context.Context, q, a string, l int) (any, error) {
	b.limit = l
	b.after = a
	return map[string]any{"status": "ok"}, nil
}
func (b *listBackend) Group(_ context.Context, g string, topics []string) (any, error) {
	b.topics = topics
	return map[string]any{"commit_status": "no_committed_offset"}, nil
}
func TestDiscoveryValidationAndDefaults(t *testing.T) {
	b := &listBackend{}
	calls := 0
	d := Deps{Reg: fixtureRegistry(t), Factory: func(targets.Target) (Backend, error) { calls++; return b, nil }}
	r, _, _ := d.TopicsList(context.Background(), nil, TopicsListInput{Target: "test"})
	if r.IsError || b.limit != 50 {
		t.Fatal("topic defaults")
	}
	r, _, _ = d.GroupsList(context.Background(), nil, GroupsListInput{Target: "test", After: "worker:a"})
	if r.IsError || b.after != "worker:a" {
		t.Fatal("group cursor")
	}
	n := calls
	for _, limit := range []int{-1, 201} {
		r, _, _ = d.TopicsList(context.Background(), nil, TopicsListInput{Target: "test", Limit: limit})
		if !r.IsError {
			t.Fatal("invalid limit accepted")
		}
		r, _, _ = d.GroupsList(context.Background(), nil, GroupsListInput{Target: "test", Limit: limit})
		if !r.IsError {
			t.Fatal("invalid limit accepted")
		}
	}
	if calls != n {
		t.Fatal("invalid listing dialed")
	}
	r, _, _ = d.Group(context.Background(), nil, GroupInput{Target: "test", Group: "worker-a", Topics: []string{"orders-one"}})
	if r.IsError || len(b.topics) != 1 || b.topics[0] != "orders-one" {
		t.Fatal("explicit group scope not forwarded")
	}
}
