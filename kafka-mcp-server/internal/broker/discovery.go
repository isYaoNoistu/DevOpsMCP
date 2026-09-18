package broker

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const discoveryBrokerLimit = 100

func discoveryLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 200 {
		return 0, errors.New("limit must be between 1 and 200")
	}
	return limit, nil
}

// Retain only the smallest limit+1 distinct matching names: page size bounds
// retained state, not the metadata/ListGroups response received from Kafka.
type discoveryPage struct {
	limit int
	names []string
	rows  map[string]any
}

func newDiscoveryPage(limit int) *discoveryPage {
	return &discoveryPage{limit: limit, rows: make(map[string]any)}
}

func (p *discoveryPage) add(name string, row any) {
	if _, exists := p.rows[name]; exists {
		return
	}
	i := sort.SearchStrings(p.names, name)
	if i > p.limit {
		return
	}
	p.names = append(p.names, "")
	copy(p.names[i+1:], p.names[i:])
	p.names[i] = name
	p.rows[name] = row
	if len(p.names) > p.limit+1 {
		delete(p.rows, p.names[len(p.names)-1])
		p.names = p.names[:len(p.names)-1]
	}
}

func (p *discoveryPage) result(key, scope string) map[string]any {
	n := len(p.names)
	more := n > p.limit
	if more {
		n = p.limit
	}
	rows := make([]any, 0, n)
	for _, name := range p.names[:n] {
		rows = append(rows, p.rows[name])
	}
	out := map[string]any{
		key: rows, "status": "ok", "returned_count": n, "has_more": more,
		"partial": false, "truncated": false, "sample_atomic": false,
		"pagination": "exclusive after-name cursor over a fresh client-filtered snapshot; not broker-side pagination",
		"scan_scope": scope,
	}
	if more {
		out["next_after"] = p.names[n-1]
	}
	return out
}

func (c *Client) ListTopics(ctx context.Context, query, after string, limit int, includeInternal bool) (any, error) {
	limit, err := discoveryLimit(limit)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	// nil Topics explicitly requests an all-topic scan, including when the
	// configured allowlist contains only exact names. Filter before paging.
	req.Topics = nil
	md, err := req.RequestWith(ctx, c.kafka)
	if err != nil {
		return nil, &requestError{err}
	}
	page := newDiscoveryPage(limit)
	for _, topic := range md.Topics {
		if topic.Topic == nil {
			continue
		}
		name := *topic.Topic
		if !c.target.AllowsTopic(name) || !strings.Contains(name, query) || name <= after || topic.IsInternal && !includeInternal {
			continue
		}
		row := map[string]any{"topic": name, "status": status(topic.ErrorCode), "internal": topic.IsInternal, "partition_count": len(topic.Partitions)}
		if topic.ErrorCode != 0 {
			row["error"] = discoveryError(kerr.ErrorForCode(topic.ErrorCode))
		}
		partitionErrors := []any{}
		errorCount := 0
		for _, partition := range topic.Partitions {
			if partition.ErrorCode == 0 {
				continue
			}
			errorCount++
			if len(partitionErrors) < 20 {
				detail := discoveryError(kerr.ErrorForCode(partition.ErrorCode))
				detail["partition"] = partition.Partition
				partitionErrors = append(partitionErrors, detail)
			}
		}
		row["partition_error_count"] = errorCount
		row["partition_errors"] = partitionErrors
		row["partition_errors_truncated"] = errorCount > 20
		page.add(name, row)
	}
	out := page.result("topics", "all-topic metadata scan; only allowed matching topic rows and counts are returned")
	for _, value := range out["topics"].([]any) {
		row := value.(map[string]any)
		if row["status"] != "ok" || row["partition_error_count"].(int) > 0 {
			out["partial"] = true
			out["status"] = "partial"
		}
		if row["partition_errors_truncated"] == true {
			out["truncated"] = true
		}
	}
	return out, nil
}

func discoveryError(err error) map[string]any {
	out := ErrorDetails(err)
	out["status"] = safeError(err)
	return out
}

func (c *Client) ListGroups(ctx context.Context, query, after string, limit int) (any, error) {
	limit, err := discoveryLimit(limit)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	mdReq := kmsg.NewPtrMetadataRequest()
	mdReq.Topics = make([]kmsg.MetadataRequestTopic, 0)
	mdReq.AllowAutoTopicCreation = false
	md, err := mdReq.RequestWith(ctx, c.kafka)
	if err != nil {
		return nil, &requestError{err}
	}
	ids := make([]int32, 0, len(md.Brokers))
	seen := make(map[int32]bool)
	for _, b := range md.Brokers {
		if !seen[b.NodeID] {
			ids = append(ids, b.NodeID)
			seen[b.NodeID] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	truncated := len(ids) > discoveryBrokerLimit
	if truncated {
		ids = ids[:discoveryBrokerLimit]
	}
	if len(ids) == 0 {
		return nil, errors.New("broker metadata unavailable")
	}

	type shard struct {
		id       int32
		response *kmsg.ListGroupsResponse
		err      error
	}
	jobs := make(chan int32, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	results := make(chan shard, 4)
	var workers sync.WaitGroup
	for i := 0; i < 4 && i < len(ids); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				// Direct requests avoid RequestSharded's unbounded all-broker
				// fanout. Every response inherits the client's 8 MiB wire cap.
				brokerCtx, brokerCancel := context.WithTimeout(ctx, 3*time.Second)
				response, requestErr := c.kafka.Broker(int(id)).Request(brokerCtx, kmsg.NewPtrListGroupsRequest())
				brokerCancel()
				result := shard{id: id, err: requestErr}
				if requestErr == nil {
					result.response = response.(*kmsg.ListGroupsResponse)
					result.err = kerr.ErrorForCode(result.response.ErrorCode)
				}
				results <- result
			}
		}()
	}
	go func() { workers.Wait(); close(results) }()
	page := newDiscoveryPage(limit)
	brokerErrors, brokerResults := []any{}, []any{}
	for result := range results {
		if result.err != nil {
			detail := discoveryError(result.err)
			detail["broker_id"] = result.id
			brokerErrors = append(brokerErrors, detail)
			continue
		}
		brokerResults = append(brokerResults, map[string]any{"broker_id": result.id, "status": "ok"})
		for _, group := range result.response.Groups {
			if !c.target.AllowsGroup(group.Group) || !strings.Contains(group.Group, query) || group.Group <= after {
				continue
			}
			page.add(group.Group, map[string]any{"group": group.Group, "status": "ok", "protocol_type": group.ProtocolType, "state": group.GroupState, "group_type": group.GroupType})
		}
	}
	byBroker := func(rows []any) {
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].(map[string]any)["broker_id"].(int32) < rows[j].(map[string]any)["broker_id"].(int32)
		})
	}
	byBroker(brokerErrors)
	byBroker(brokerResults)
	out := page.result("groups", "ListGroups scan of up to 100 metadata brokers; only allowed matching groups are returned")
	out["broker_errors"], out["broker_results"] = brokerErrors, brokerResults
	out["truncated"] = truncated
	if truncated {
		out["incomplete_reason"] = "broker_scan_limit"
	}
	if len(brokerErrors) > 0 || truncated {
		out["partial"] = true
		out["status"] = "partial"
	}
	if len(brokerResults) == 0 {
		out["status"] = "failed"
	}
	return out, nil
}
