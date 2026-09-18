package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"
	"kafka-mcp-server/internal/targets"
)

func discoveryClient(t *testing.T, n int) (*Client, *kfake.Cluster) {
	t.Helper()
	cluster, err := kfake.NewCluster(kfake.NumBrokers(n))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	c, err := New(targets.Target{Brokers: cluster.ListenAddrs(), Topics: []string{"allowed-*"}, Groups: []string{"allowed-*"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, cluster
}

func TestDiscoveryTopicsFilterBeforePagingAndNoAutoCreate(t *testing.T) {
	c, cluster := discoveryClient(t, 1)
	var scans atomic.Int32
	cluster.ControlKey(3, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		md := req.(*kmsg.MetadataRequest)
		if md.Topics != nil {
			return nil, nil, false
		}
		scans.Add(1)
		if md.AllowAutoTopicCreation {
			t.Error("auto creation enabled")
		}
		r := kmsg.NewPtrMetadataResponse()
		r.SetVersion(req.GetVersion())
		for _, name := range []string{"denied-secret", "allowed-z", "allowed-b", "allowed-a", "allowed-internal"} {
			n := name
			r.Topics = append(r.Topics, kmsg.MetadataResponseTopic{Topic: &n, IsInternal: name == "allowed-internal", Partitions: []kmsg.MetadataResponseTopicPartition{{Partition: 0}}})
		}
		return r, nil, true
	})
	for _, tc := range []struct {
		query, after string
		internal     bool
		want, next   string
	}{
		{"", "", false, "allowed-a", "allowed-a"},
		{"", "allowed-a", false, "allowed-b", "allowed-b"},
		{"", "allowed-b", false, "allowed-z", ""},
		{"internal", "", true, "allowed-internal", ""},
		{"missing", "", true, "", ""},
	} {
		v, err := c.ListTopics(context.Background(), tc.query, tc.after, 1, tc.internal)
		if err != nil {
			t.Fatal(err)
		}
		out := v.(map[string]any)
		rows := out["topics"].([]any)
		if tc.want == "" {
			if len(rows) != 0 {
				t.Fatal(rows)
			}
		} else if len(rows) != 1 || rows[0].(map[string]any)["topic"] != tc.want {
			t.Fatal(rows)
		}
		if next, _ := out["next_after"].(string); next != tc.next {
			t.Fatalf("cursor %v", out)
		}
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), "denied-secret") {
			t.Fatal(string(b))
		}
	}
	if scans.Load() != 5 {
		t.Fatalf("metadata scans: %d", scans.Load())
	}
}

func TestDiscoveryGroupsDedupePartialAndPaging(t *testing.T) {
	c, cluster := discoveryClient(t, 3)
	cluster.ControlKey(16, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		r := kmsg.NewPtrListGroupsResponse()
		r.SetVersion(req.GetVersion())
		if cluster.CurrentNode() == 2 {
			r.ErrorCode = 31
		} else {
			r.Groups = []kmsg.ListGroupsResponseGroup{{Group: "allowed-z"}, {Group: "denied-secret"}, {Group: fmt.Sprintf("allowed-%d", cluster.CurrentNode())}}
		}
		return r, nil, true
	})
	v, err := c.ListGroups(context.Background(), "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	rows := out["groups"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["group"] != "allowed-0" || rows[1].(map[string]any)["group"] != "allowed-1" || out["next_after"] != "allowed-1" || out["partial"] != true {
		t.Fatalf("bad page: %v", out)
	}
	errs := out["broker_errors"].([]any)
	if len(errs) != 1 || errs[0].(map[string]any)["broker_id"] != int32(2) {
		t.Fatalf("errors: %v", errs)
	}
	v, err = c.ListGroups(context.Background(), "", "allowed-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	out = v.(map[string]any)
	rows = out["groups"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["group"] != "allowed-z" || out["has_more"] != false {
		t.Fatalf("duplicate or cursor: %v", out)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "denied-secret") {
		t.Fatal(string(b))
	}
}

func TestDiscoveryGroupsAllFailedIsNotEmptySuccess(t *testing.T) {
	c, cluster := discoveryClient(t, 1)
	cluster.ControlKey(16, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		r := kmsg.NewPtrListGroupsResponse()
		r.SetVersion(req.GetVersion())
		r.ErrorCode = 31
		return r, nil, true
	})
	v, err := c.ListGroups(context.Background(), "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["status"] != "failed" {
		t.Fatalf("failure hidden: %v", v)
	}
}

func TestDiscoveryLimitsBeforeNetwork(t *testing.T) {
	c := &Client{}
	for _, limit := range []int{-1, 201} {
		if _, err := c.ListTopics(context.Background(), "", "", limit, false); err == nil {
			t.Fatal("topics accepted invalid limit")
		}
		if _, err := c.ListGroups(context.Background(), "", "", limit); err == nil {
			t.Fatal("groups accepted invalid limit")
		}
	}
}

func TestDiscoveryStructuredErrors(t *testing.T) {
	err := discoveryError(context.DeadlineExceeded)
	if err["status"] != "timeout" || err["category"] != "timeout" || err["retryable"] != true {
		t.Fatalf("missing safe transport details: %v", err)
	}
}

func TestDiscoveryBrokerCap(t *testing.T) {
	c, cluster := discoveryClient(t, 1)
	host, portText, err := net.SplitHostPort(cluster.ListenAddrs()[0])
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	cluster.ControlKey(3, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		r := kmsg.NewPtrMetadataResponse()
		r.SetVersion(req.GetVersion())
		for id := int32(0); id < 101; id++ {
			r.Brokers = append(r.Brokers, kmsg.MetadataResponseBroker{NodeID: id, Host: host, Port: int32(port)})
		}
		return r, nil, true
	})
	var calls atomic.Int32
	cluster.ControlKey(16, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		calls.Add(1)
		r := kmsg.NewPtrListGroupsResponse()
		r.SetVersion(req.GetVersion())
		r.Groups = []kmsg.ListGroupsResponseGroup{{Group: "allowed-a"}}
		return r, nil, true
	})
	v, err := c.ListGroups(context.Background(), "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	if calls.Load() != 100 || out["truncated"] != true || out["partial"] != true || out["incomplete_reason"] != "broker_scan_limit" {
		t.Fatalf("unbounded or silent scan (%d): %v", calls.Load(), out)
	}
	if _, ok := out["next_after"]; ok {
		t.Fatal("broker truncation fabricated a row cursor")
	}
}

func TestDiscoveryDefaultAndMaximumPage(t *testing.T) {
	c, cluster := discoveryClient(t, 1)
	cluster.ControlKey(16, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		r := kmsg.NewPtrListGroupsResponse()
		r.SetVersion(req.GetVersion())
		for i := 202; i >= 0; i-- {
			r.Groups = append(r.Groups, kmsg.ListGroupsResponseGroup{Group: fmt.Sprintf("allowed-%03d", i)})
		}
		return r, nil, true
	})
	for _, tc := range []struct{ limit, want int }{{0, 50}, {200, 200}} {
		v, err := c.ListGroups(context.Background(), "", "", tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		out := v.(map[string]any)
		if len(out["groups"].([]any)) != tc.want || out["next_after"] != fmt.Sprintf("allowed-%03d", tc.want-1) {
			t.Fatalf("bad bounded page: %v", out)
		}
	}
	v, err := c.ListGroups(context.Background(), "201", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.(map[string]any)["groups"].([]any)) != 1 {
		t.Fatal("substring filter not applied")
	}
}

func TestDiscoveryCancellationReturnsPromptly(t *testing.T) {
	c, _ := discoveryClient(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if _, err := c.ListGroups(ctx, "", "", 50); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancelled scan waited")
	}
}

func TestDiscoveryTopicPartitionErrorsAreBounded(t *testing.T) {
	c, cluster := discoveryClient(t, 1)
	cluster.ControlKey(3, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		if req.(*kmsg.MetadataRequest).Topics != nil {
			return nil, nil, false
		}
		r := kmsg.NewPtrMetadataResponse()
		r.SetVersion(req.GetVersion())
		name := "allowed-a"
		topic := kmsg.MetadataResponseTopic{Topic: &name}
		for p := int32(0); p < 25; p++ {
			topic.Partitions = append(topic.Partitions, kmsg.MetadataResponseTopicPartition{Partition: p, ErrorCode: 5})
		}
		r.Topics = []kmsg.MetadataResponseTopic{topic}
		return r, nil, true
	})
	v, err := c.ListTopics(context.Background(), "", "", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	row := out["topics"].([]any)[0].(map[string]any)
	if out["partial"] != true || out["truncated"] != true || row["partition_count"] != 25 || row["partition_error_count"] != 25 || len(row["partition_errors"].([]any)) != 20 {
		t.Fatalf("partition errors unbounded or hidden: %v", out)
	}
	detail := row["partition_errors"].([]any)[0].(map[string]any)
	if detail["kafka_error_code"] != int16(5) || detail["retryable"] != true {
		t.Fatal(detail)
	}
}

func TestDiscoveryEmptySuccessfulBrokerAndFailureRemainPartial(t *testing.T) {
	c, cluster := discoveryClient(t, 2)
	cluster.ControlKey(16, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		r := kmsg.NewPtrListGroupsResponse()
		r.SetVersion(req.GetVersion())
		if cluster.CurrentNode() == 1 {
			r.ErrorCode = 31
		}
		return r, nil, true
	})
	v, err := c.ListGroups(context.Background(), "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	out := v.(map[string]any)
	if out["status"] != "partial" || len(out["groups"].([]any)) != 0 || len(out["broker_results"].([]any)) != 1 || len(out["broker_errors"].([]any)) != 1 {
		t.Fatalf("empty success was lost: %v", out)
	}
}
