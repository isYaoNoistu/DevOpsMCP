package broker

import (
	"context"
	"encoding/json"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"
	"kafka-mcp-server/internal/targets"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCapabilitiesFailover(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	c, err := New(targets.Target{Brokers: append([]string{dead}, cluster.ListenAddrs()...)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatalf("healthy second broker not used: %v", err)
	}
	if r.(map[string]any)["cluster_id"] == nil {
		t.Fatal("missing metadata")
	}
}

func TestModernConsumerGroup(t *testing.T) {
	for _, classicCode := range []int16{0, 69} {
		t.Run(strconv.Itoa(int(classicCode)), func(t *testing.T) {
			cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
			if err != nil {
				t.Fatal(err)
			}
			defer cluster.Close()
			cluster.ControlKey(15, func(req kmsg.Request) (kmsg.Response, error, bool) {
				cluster.KeepControl()
				r := kmsg.NewPtrDescribeGroupsResponse()
				r.SetVersion(req.GetVersion())
				r.Groups = []kmsg.DescribeGroupsResponseGroup{{Group: "reader", State: "Dead", ErrorCode: classicCode}}
				return r, nil, true
			})
			cluster.ControlKey(69, func(req kmsg.Request) (kmsg.Response, error, bool) {
				cluster.KeepControl()
				r := kmsg.NewPtrConsumerGroupDescribeResponse()
				r.SetVersion(req.GetVersion())
				m := kmsg.NewConsumerGroupDescribeResponseGroupMember()
				m.MemberID = "member-1"
				m.ClientID = "client-1"
				m.Assignment.TopicPartitions = []kmsg.AssignmentTopicPartition{{Topic: "orders", Partitions: []int32{0}}, {Topic: "private", Partitions: []int32{1}}}
				r.Groups = []kmsg.ConsumerGroupDescribeResponseGroup{{Group: "reader", State: "Stable", AssignorName: "uniform", Members: []kmsg.ConsumerGroupDescribeResponseGroupMember{m}}}
				return r, nil, true
			})
			c, err := New(targets.Target{Brokers: cluster.ListenAddrs(), Topics: []string{"orders"}, Groups: []string{"reader"}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r, err := c.Group(context.Background(), "reader")
			if err != nil {
				t.Fatal(err)
			}
			g := r.(map[string]any)
			if g["state"] != "Stable" || g["group_type"] != "consumer" || len(g["members"].([]any)) != 1 {
				t.Fatalf("modern group lost: %#v", g)
			}
			assigned := g["members"].([]any)[0].(map[string]any)["assignments"].([]any)
			if len(assigned) != 1 || assigned[0].(map[string]any)["topic"] != "orders" {
				t.Fatalf("assignment lost or allowlist bypassed: %v", assigned)
			}
		})
	}
}

func TestGroupProtocolFallbackBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		old         bool
		modernCode  int16
		wantError   bool
	}{
		{"classic-stable", "Stable", false, 0, false},
		{"old-broker-dead", "Dead", true, 0, false},
		{"modern-permission-denied", "Dead", false, 30, true},
		{"missing-both", "Dead", false, 69, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := []kfake.Opt{kfake.NumBrokers(1)}
			if tc.old {
				opts = append(opts, kfake.MaxVersions(kversion.V3_5_0()))
			}
			cluster, err := kfake.NewCluster(opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer cluster.Close()
			cluster.ControlKey(15, func(req kmsg.Request) (kmsg.Response, error, bool) {
				cluster.KeepControl()
				r := kmsg.NewPtrDescribeGroupsResponse()
				r.SetVersion(req.GetVersion())
				r.Groups = []kmsg.DescribeGroupsResponseGroup{{Group: "reader", State: tc.state}}
				return r, nil, true
			})
			cluster.ControlKey(69, func(req kmsg.Request) (kmsg.Response, error, bool) {
				cluster.KeepControl()
				if tc.old || tc.state == "Stable" {
					t.Error("unnecessary modern API request")
				}
				r := kmsg.NewPtrConsumerGroupDescribeResponse()
				r.SetVersion(req.GetVersion())
				r.Groups = []kmsg.ConsumerGroupDescribeResponseGroup{{Group: "reader", ErrorCode: tc.modernCode}}
				return r, nil, true
			})
			c, err := New(targets.Target{Brokers: cluster.ListenAddrs()})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			g, err := c.describeGroup(context.Background(), "reader")
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v wantError=%v", err, tc.wantError)
			}
			if !tc.wantError && (g.State != tc.state || g.GroupType != "classic") {
				t.Fatalf("classic result changed: %+v", g)
			}
		})
	}
}

func TestReadOnlyProtocolFixture(t *testing.T) {
	cluster, e := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
	if e != nil {
		t.Fatal(e)
	}
	defer cluster.Close()
	seed, e := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...), kgo.MaxVersions(kversion.V3_6_0()))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = seed.ProduceSync(ctx, &kgo.Record{Topic: "orders", Key: []byte("private-key"), Value: []byte("private-payload")}).FirstErr(); e != nil {
		t.Fatal(e)
	}
	seed.Close()
	var mu sync.Mutex
	var forbidden []int16
	cluster.Control(func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		mu.Lock()
		defer mu.Unlock()
		switch req.Key() {
		case 0, 8, 11, 12, 13, 14, 19, 20, 22, 24, 25, 26, 28, 33, 36, 37, 42:
			forbidden = append(forbidden, req.Key())
		}
		if md, ok := req.(*kmsg.MetadataRequest); ok && md.AllowAutoTopicCreation {
			t.Error("auto create enabled")
		}
		if f, ok := req.(*kmsg.OffsetFetchRequest); ok && f.Topics == nil && len(f.Groups) == 0 {
			t.Error("unbounded group offsets")
		}
		return nil, nil, false
	})
	c, e := New(targets.Target{Brokers: cluster.ListenAddrs(), Topics: []string{"orders", "missing"}, Groups: []string{"consumer"}, AllowPayload: true})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	caps, e := c.Capabilities(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if caps.(map[string]any)["cluster_id"] == nil {
		t.Fatal("missing cluster identity")
	}
	if _, e = c.Configs(ctx, "topic", []string{"orders"}, []string{"retention.ms"}, true); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Topic(ctx, "missing"); e != nil {
		t.Fatal(e)
	}
	if cluster.TopicInfo("missing") != nil {
		t.Fatal("topic auto-created")
	}
	o, e := c.Offsets(ctx, "orders", 0, nil)
	if e != nil {
		t.Fatal(e)
	}
	if o.(map[string]any)["high_watermark"].(map[string]any)["offset"] != int64(1) {
		t.Fatalf("offsets: %#v", o)
	}
	for _, payload := range []bool{false, true} {
		r, e := c.Peek(ctx, "orders", 0, 0, nil, 1, 65536, payload, "read_uncommitted")
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "private-") {
			t.Fatal("plaintext payload exposed")
		}
		if len(r.(map[string]any)["records"].([]any)) != 1 {
			t.Fatalf("missing records: %s", b)
		}
	}
	r, e := c.Peek(ctx, "orders", 0, 2, nil, 1, 65536, false, "read_uncommitted")
	if e != nil || r.(map[string]any)["status"] != "offset_out_of_range" {
		t.Fatalf("out of range: %v %v", r, e)
	}
	c.target.Topics = []string{"orders"}
	_, _ = c.Group(ctx, "consumer")
	mu.Lock()
	defer mu.Unlock()
	if len(forbidden) > 0 {
		t.Fatalf("mutating API requests: %v", forbidden)
	}
}
func TestPeekTransactionalVisibilityBoundary(t *testing.T) {
	cluster, e := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
	if e != nil {
		t.Fatal(e)
	}
	defer cluster.Close()
	cluster.ControlKey(2, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		q := req.(*kmsg.ListOffsetsRequest)
		r := kmsg.NewPtrListOffsetsResponse()
		r.SetVersion(q.GetVersion())
		for _, topic := range q.Topics {
			rt := kmsg.ListOffsetsResponseTopic{Topic: topic.Topic}
			for _, p := range topic.Partitions {
				o := int64(20)
				if p.Timestamp == -2 {
					o = 0
				} else if q.IsolationLevel == 1 {
					o = 10
				}
				rt.Partitions = append(rt.Partitions, kmsg.ListOffsetsResponseTopicPartition{Partition: p.Partition, Offset: o})
			}
			r.Topics = append(r.Topics, rt)
		}
		return r, nil, true
	})
	c, e := New(targets.Target{Brokers: cluster.ListenAddrs(), Topics: []string{"orders"}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	r, e := c.Peek(context.Background(), "orders", 0, 15, nil, 1, 65536, false, "read_committed")
	if e != nil {
		t.Fatal(e)
	}
	if r.(map[string]any)["status"] != "no_committed_data_visible" {
		t.Fatalf("valid offset above LSO misclassified: %v", r)
	}
}
func TestGroupOffsetsKeepValidTopicWhenAnotherMissing(t *testing.T) {
	cluster, e := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
	if e != nil {
		t.Fatal(e)
	}
	defer cluster.Close()
	seed, e := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...), kgo.MaxVersions(kversion.V3_6_0()))
	if e != nil {
		t.Fatal(e)
	}
	defer seed.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commit := kmsg.NewPtrOffsetCommitRequest()
	commit.Group = "consumer"
	p := kmsg.NewOffsetCommitRequestTopicPartition()
	p.Partition = 0
	p.Offset = 13
	commit.Topics = []kmsg.OffsetCommitRequestTopic{{Topic: "orders", Partitions: []kmsg.OffsetCommitRequestTopicPartition{p}}}
	r, e := commit.RequestWith(ctx, seed)
	if e != nil {
		t.Fatal(e)
	}
	if r.Topics[0].Partitions[0].ErrorCode != 0 {
		t.Fatalf("fixture commit: %v", r)
	}
	c, e := New(targets.Target{Brokers: cluster.ListenAddrs(), Topics: []string{"orders", "missing"}, Groups: []string{"consumer"}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	offsets, e := c.fetchScopedOffsets(ctx, "consumer", []string{"orders", "missing"})
	if e != nil {
		t.Fatal(e)
	}
	if offsets["orders"][0].At != 13 {
		t.Fatalf("valid offset lost: %v", offsets)
	}
	if offsets["missing"][-1].Err == nil {
		t.Fatal("missing topic error lost")
	}
}
func TestReadCommittedExcludesAbortedPayload(t *testing.T) {
	cluster, e := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
	if e != nil {
		t.Fatal(e)
	}
	defer cluster.Close()
	seed, e := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...), kgo.TransactionalID("fixture-only-transaction"), kgo.MaxVersions(kversion.V3_6_0()))
	if e != nil {
		t.Fatal(e)
	}
	defer seed.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, txn := range []struct {
		value  string
		commit bool
	}{{"aborted-private", false}, {"committed", true}} {
		if e = seed.BeginTransaction(); e != nil {
			t.Fatal(e)
		}
		if e = seed.ProduceSync(ctx, &kgo.Record{Topic: "orders", Value: []byte(txn.value)}).FirstErr(); e != nil {
			t.Fatal(e)
		}
		if e = seed.EndTransaction(ctx, kgo.TransactionEndTry(txn.commit)); e != nil {
			t.Fatal(e)
		}
	}
	c, e := New(targets.Target{Brokers: cluster.ListenAddrs(), Topics: []string{"orders"}, AllowPayload: true})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	r, e := c.Peek(ctx, "orders", 0, 0, nil, 20, 65536, true, "read_committed")
	if e != nil {
		t.Fatal(e)
	}
	rows := r.(map[string]any)["records"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["value"] != "Y29tbWl0dGVk" {
		t.Fatalf("unexpected committed records: %#v", r)
	}
}
