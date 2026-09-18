package tools

import (
	"context"
	"kafka-mcp-server/internal/targets"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectBeforeDial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.json")
	os.WriteFile(path, []byte(`{"targets":[{"name":"test","brokers":["localhost:9092"],"topics":["orders-*"],"groups":["worker-*"],"allow_payload":false}]}`), 0600)
	d := Deps{Reg: targets.New(path), Factory: func(targets.Target) (Backend, error) { t.Fatal("invalid request dialed broker"); return nil, nil }}
	cases := []func() bool{
		func() bool {
			r, _, _ := d.Topic(context.Background(), nil, TopicInput{Target: "test", Topic: "private"})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Group(context.Background(), nil, GroupInput{Target: "test", Group: "private"})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Configs(context.Background(), nil, ConfigInput{Target: "test", ResourceType: "topic", ResourceNames: []string{"private"}})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Configs(context.Background(), nil, ConfigInput{Target: "test", ResourceType: "broker", ResourceNames: []string{"not-id"}})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Offsets(context.Background(), nil, OffsetsInput{Target: "test", Topic: "orders-a", Partition: -1})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Peek(context.Background(), nil, PeekInput{Target: "test", Topic: "orders-a", IncludeValue: true, StartOffset: ptr(0)})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Peek(context.Background(), nil, PeekInput{Target: "test", Topic: "orders-a", MaxRecords: 21, StartOffset: ptr(0)})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Peek(context.Background(), nil, PeekInput{Target: "test", Topic: "orders-a"})
			return r.IsError
		},
		func() bool {
			r, _, _ := d.Peek(context.Background(), nil, PeekInput{Target: "test", Topic: "orders-a", StartOffset: ptr(0), TimestampMS: ptr(1)})
			return r.IsError
		},
	}
	for i, call := range cases {
		if !call() {
			t.Errorf("case %d accepted", i)
		}
	}
}
func ptr(v int64) *int64 { return &v }
