package broker

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"
	"kafka-mcp-server/internal/targets"
)

func trialClient(t *testing.T, partitions int32) (*Client, *kfake.Cluster) {
	t.Helper()
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(partitions, "orders", "other"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	cluster.ControlKey(15, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		r := kmsg.NewPtrDescribeGroupsResponse()
		r.SetVersion(req.GetVersion())
		r.Groups = []kmsg.DescribeGroupsResponseGroup{{Group: "reader", State: "Empty"}}
		return r, nil, true
	})
	c, err := New(targets.Target{Brokers: cluster.ListenAddrs(), Topics: []string{"*"}, Groups: []string{"reader"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, cluster
}

func scopedGroup(t *testing.T, c *Client, topics []string) (any, error) {
	t.Helper()
	api, ok := any(c).(interface {
		Group(context.Context, string, []string) (any, error)
	})
	if !ok {
		t.Fatal("Group does not accept explicit topic scope")
	}
	return api.Group(context.Background(), "reader", topics)
}

func TestTrialGroupExplicitScopeAndMissingCommit(t *testing.T) {
	c, cluster := trialClient(t, 1)
	c.target.Topics = []string{"orders", "other"}
	cluster.ControlKey(9, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		q := req.(*kmsg.OffsetFetchRequest)
		r := kmsg.NewPtrOffsetFetchResponse()
		r.SetVersion(q.GetVersion())
		for _, g := range q.Groups {
			rg := kmsg.OffsetFetchResponseGroup{Group: g.Group}
			for _, topic := range g.Topics {
				if topic.Topic != "orders" && topic.Topic != "" {
					t.Errorf("offset scope expanded to %q", topic.Topic)
				}
				rg.Topics = append(rg.Topics, kmsg.OffsetFetchResponseGroupTopic{Topic: topic.Topic, TopicID: topic.TopicID, Partitions: []kmsg.OffsetFetchResponseGroupTopicPartition{{Partition: 0, Offset: -1}}})
			}
			r.Groups = append(r.Groups, rg)
		}
		for _, topic := range q.Topics {
			if topic.Topic != "orders" {
				t.Errorf("offset scope expanded to %q", topic.Topic)
			}
			r.Topics = append(r.Topics, kmsg.OffsetFetchResponseTopic{Topic: topic.Topic, Partitions: []kmsg.OffsetFetchResponseTopicPartition{{Partition: 0, Offset: -1}}})
		}
		return r, nil, true
	})
	result, err := scopedGroup(t, c, []string{"orders"})
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["committed_offsets"].([]any)
	if len(rows) != 1 {
		t.Fatalf("explicit scope rows: %#v", result)
	}
	row := rows[0].(map[string]any)
	if row["committed_offset"] != int64(-1) || row["commit_status"] != "no_committed_offset" {
		t.Fatalf("missing commit misreported: %#v", row)
	}
}

func TestTrialGroupScopeLimitIsError(t *testing.T) {
	c, _ := trialClient(t, 501)
	_, err := scopedGroup(t, c, []string{"orders"})
	if err == nil || safeError(err) != "scope_limit_exceeded" {
		t.Fatalf("scope limit hidden: %v", err)
	}
}

func TestTrialGroupRejectsInvalidExplicitScopeBeforeRequest(t *testing.T) {
	c := &Client{target: targets.Target{Topics: []string{"orders*"}, Groups: []string{"reader"}}}
	for _, topics := range [][]string{{"orders*"}, {""}, {"private"}, make([]string, 21)} {
		if _, err := scopedGroup(t, c, topics); err == nil {
			t.Errorf("accepted scope %v", topics)
		}
	}
}

func TestTrialEmptyConfigKeysMeanAll(t *testing.T) {
	c, cluster := trialClient(t, 1)
	cluster.ControlKey(32, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		q := req.(*kmsg.DescribeConfigsRequest)
		if q.Resources[0].ConfigNames != nil {
			t.Errorf("empty keys encoded as non-null list")
		}
		return nil, nil, false
	})
	result, err := c.Configs(context.Background(), "topic", []string{"orders"}, []string{}, false)
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["resources"].([]any)
	if len(rows) != 1 || len(rows[0].(map[string]any)["configs"].([]any)) == 0 {
		t.Fatalf("all configs missing: %#v", result)
	}
}

func TestTrialConfigTransportFailureKeepsResources(t *testing.T) {
	c, _ := trialClient(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := c.Configs(ctx, "topic", []string{"orders", "other"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["resources"].([]any)
	if len(rows) != 2 {
		t.Fatalf("resource identity lost: %#v", result)
	}
	for _, r := range rows {
		row := r.(map[string]any)
		if row["resource_type"] != "topic" || row["name"] == nil || row["broker_id"] == nil || row["error_code"] != "cancelled" {
			t.Fatalf("missing safe details: %#v", row)
		}
	}
}

func TestTrialSafeNetworkErrorClassification(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: "private.example.com", Err: "password=secret", IsNotFound: true}}
	if safeError(err) != "dns_lookup_failed" {
		t.Fatalf("DNS classification lost: %s", safeError(err))
	}
	if safeError(errors.New("password=secret")) != "kafka_request_failed" {
		t.Fatal("unknown error changed")
	}
}

func TestTrialInactiveGroupHistoricalCommit(t *testing.T) {
	c, cluster := trialClient(t, 1)
	seed, err := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...), kgo.MaxVersions(kversion.V3_6_0()))
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	commit := kmsg.NewPtrOffsetCommitRequest()
	commit.Group = "reader"
	part := kmsg.NewOffsetCommitRequestTopicPartition()
	part.Partition = 0
	part.Offset = 13
	commit.Topics = []kmsg.OffsetCommitRequestTopic{{Topic: "orders", Partitions: []kmsg.OffsetCommitRequestTopicPartition{part}}}
	response, err := commit.RequestWith(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	if response.Topics[0].Partitions[0].ErrorCode != 0 {
		t.Fatal("fixture commit rejected")
	}
	result, err := c.Group(context.Background(), "reader", []string{"orders"})
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["committed_offsets"].([]any)
	if len(rows) != 1 {
		t.Fatalf("historical offset missing: %#v", result)
	}
	row := rows[0].(map[string]any)
	if row["committed_offset"] != int64(13) || row["commit_status"] != "available" {
		t.Fatalf("historical offset: %#v", row)
	}
}

func TestTrialConfigPartialBrokerFailures(t *testing.T) {
	c, _ := trialClient(t, 1)
	result, err := c.Configs(context.Background(), "broker", []string{"0", "999"}, []string{"log.retention.ms"}, false)
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["resources"].([]any)
	if len(rows) != 2 {
		t.Fatalf("partial result: %#v", result)
	}
	found := map[string]map[string]any{}
	for _, r := range rows {
		row := r.(map[string]any)
		found[row["name"].(string)] = row
	}
	if found["0"]["status"] != "ok" || found["999"]["status"] == "ok" || found["999"]["error_code"] == nil || found["999"]["broker_id"] == nil {
		t.Fatalf("partial identities: %#v", result)
	}
}

func TestTrialConfigProtocolResourceErrorDetails(t *testing.T) {
	c, cluster := trialClient(t, 1)
	cluster.ControlKey(32, func(req kmsg.Request) (kmsg.Response, error, bool) {
		cluster.KeepControl()
		r := kmsg.NewPtrDescribeConfigsResponse()
		r.SetVersion(req.GetVersion())
		r.Resources = []kmsg.DescribeConfigsResponseResource{{ResourceType: 2, ResourceName: "orders", ErrorCode: 29}}
		return r, nil, true
	})
	result, err := c.Configs(context.Background(), "topic", []string{"orders"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	row := result.(map[string]any)["resources"].([]any)[0].(map[string]any)
	if row["error_code"] != "TOPIC_AUTHORIZATION_FAILED" || row["kafka_error_code"] != int16(29) || row["category"] != "authorization" || row["retryable"] != false {
		t.Fatalf("resource error missing: %#v", row)
	}
}
