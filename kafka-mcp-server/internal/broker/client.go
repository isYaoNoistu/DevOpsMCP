package broker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sort"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"kafka-mcp-server/internal/targets"
	"strconv"
	"strings"
	"time"
)

type requestError struct{ cause error }

func (e *requestError) Error() string { return safeError(e.cause) }
func (e *requestError) Unwrap() error { return e.cause }

type Client struct {
	target targets.Target
	kafka  *kgo.Client
	opts   []kgo.Opt
}

func New(t targets.Target) (*Client, error) {
	opts := []kgo.Opt{kgo.SeedBrokers(t.Brokers...), kgo.ClientID("kafka-readonly-mcp"), kgo.DialTimeout(5 * time.Second), kgo.RequestTimeoutOverhead(5 * time.Second), kgo.RetryTimeout(8 * time.Second), kgo.RequestRetries(2), kgo.BrokerMaxReadBytes(8 << 20), kgo.MaxDecompressBatchBytes(8 << 20), kgo.FetchMaxBytes(65536), kgo.FetchMaxPartitionBytes(65536)}
	if len(t.Brokers) == 0 {
		return nil, errors.New("bootstrap brokers required")
	}
	if t.TLS.Enabled {
		conf := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.TLS.ServerName}
		if t.TLS.CAFile != "" || t.TLS.CAPEM != "" {
			pem := []byte(t.TLS.CAPEM)
			var e error
			if t.TLS.CAFile != "" {
				pem, e = os.ReadFile(t.TLS.CAFile)
			}
			if e != nil {
				return nil, errors.New("TLS CA unavailable")
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("invalid TLS CA")
			}
			conf.RootCAs = pool
		}
		if t.TLS.CertFile != "" || t.TLS.KeyFile != "" || t.TLS.CertPEM != "" || t.TLS.KeyPEM != "" {
			var cert tls.Certificate
			var e error
			if t.TLS.CertPEM != "" || t.TLS.KeyPEM != "" {
				cert, e = tls.X509KeyPair([]byte(t.TLS.CertPEM), []byte(t.TLS.KeyPEM))
			} else {
				cert, e = tls.LoadX509KeyPair(t.TLS.CertFile, t.TLS.KeyFile)
			}
			if e != nil {
				return nil, errors.New("TLS client certificate unavailable")
			}
			conf.Certificates = []tls.Certificate{cert}
		}
		opts = append(opts, kgo.DialTLSConfig(conf))
	}
	switch strings.ToUpper(t.SASL.Mechanism) {
	case "", "NONE":
	case "PLAIN":
		opts = append(opts, kgo.SASL(plain.Auth{User: t.SASL.Username, Pass: t.SASL.Password}.AsMechanism()))
	case "SCRAM-SHA-256":
		opts = append(opts, kgo.SASL(scram.Auth{User: t.SASL.Username, Pass: t.SASL.Password}.AsSha256Mechanism()))
	case "SCRAM-SHA-512":
		opts = append(opts, kgo.SASL(scram.Auth{User: t.SASL.Username, Pass: t.SASL.Password}.AsSha512Mechanism()))
	default:
		return nil, errors.New("unsupported SASL mechanism")
	}
	cl, e := kgo.NewClient(opts...)
	if e != nil {
		return nil, errors.New("invalid Kafka client configuration")
	}
	return &Client{t, cl, opts}, nil
}
func (c *Client) Close() { c.kafka.Close() }
func safeError(e error) string {
	return ErrorDetails(e)["error_code"].(string)
}
func status(code int16) string { return safeError(kerr.ErrorForCode(code)) }
func (c *Client) Capabilities(ctx context.Context) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, e := c.kafka.Request(ctx, kmsg.NewPtrApiVersionsRequest())
	if e != nil {
		return nil, &requestError{e}
	}
	v := r.(*kmsg.ApiVersionsResponse)
	versions := []any{}
	for _, a := range v.ApiKeys {
		if len(versions) >= 200 {
			break
		}
		versions = append(versions, map[string]any{"api_key": a.ApiKey, "min_version": a.MinVersion, "max_version": a.MaxVersion})
	}
	out := map[string]any{"status": status(v.ErrorCode), "scope": "one reachable broker API versions and cluster identity; no topic enumeration", "api_versions": versions, "truncated": len(v.ApiKeys) > 200}
	md := kmsg.NewPtrMetadataRequest()
	md.Topics = make([]kmsg.MetadataRequestTopic, 0)
	md.AllowAutoTopicCreation = false
	meta, e := md.RequestWith(ctx, c.kafka)
	if e != nil {
		out["metadata_status"] = safeError(e)
	} else {
		out["cluster_id"] = meta.ClusterID
		out["controller_id"] = meta.ControllerID
		ids := []int32{}
		for _, b := range meta.Brokers {
			if len(ids) >= 100 {
				out["truncated"] = true
				break
			}
			ids = append(ids, b.NodeID)
		}
		out["broker_ids"] = ids
		out["broker_count"] = len(meta.Brokers)
	}
	return out, nil
}
func sensitive(name string) bool {
	name = strings.ToLower(name)
	for _, s := range []string{"password", "secret", "credential", "token", "jaas", "private.key"} {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}
func configView(v kmsg.DescribeConfigsResponseResourceConfig, syn bool) map[string]any {
	hidden := v.IsSensitive || sensitive(v.Name)
	var value *string
	if !hidden {
		value = v.Value
	}
	out := map[string]any{"name": v.Name, "value": value, "sensitive": hidden, "source": v.Source.String(), "read_only": v.ReadOnly}
	if syn {
		rows := []any{}
		for _, s := range v.ConfigSynonyms {
			if len(rows) >= 20 {
				break
			}
			var sv *string
			if !hidden && !sensitive(s.Name) {
				sv = s.Value
			}
			rows = append(rows, map[string]any{"name": s.Name, "value": sv, "source": s.Source.String()})
		}
		out["synonyms"] = rows
		out["synonyms_truncated"] = len(v.ConfigSynonyms) > 20
	}
	return out
}
func (c *Client) Configs(ctx context.Context, typ string, names, keys []string, syn bool) (any, error) {
	if len(names) < 1 || len(names) > 20 || len(keys) > 100 {
		return nil, errors.New("invalid resource or key count")
	}
	rt := kmsg.ConfigResourceType(2)
	if typ == "broker" {
		rt = 4
	} else if typ != "topic" {
		return nil, errors.New("unsupported resource type")
	}
	req := kmsg.NewPtrDescribeConfigsRequest()
	req.IncludeSynonyms = syn
	if len(keys) == 0 {
		keys = nil
	}
	for _, n := range names {
		if rt == 4 {
			id, e := strconv.ParseInt(n, 10, 32)
			if e != nil || id < 0 {
				return nil, errors.New("broker ID must be a nonnegative integer")
			}
		} else if !c.target.AllowsTopic(n) {
			return nil, errors.New("topic not allowed")
		}
		req.Resources = append(req.Resources, kmsg.DescribeConfigsRequestResource{ResourceType: rt, ResourceName: n, ConfigNames: keys})
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows := []any{}
	truncated := false
	for _, sh := range c.kafka.RequestSharded(ctx, req) {
		if sh.Err != nil {
			if failed, ok := sh.Req.(*kmsg.DescribeConfigsRequest); ok {
				for _, resource := range failed.Resources {
					row := ErrorDetails(sh.Err)
					row["status"], row["resource_type"], row["name"], row["broker_id"] = safeError(sh.Err), typ, resource.ResourceName, sh.Meta.NodeID
					rows = append(rows, row)
				}
			} else {
				row := ErrorDetails(sh.Err)
				row["status"], row["broker_id"] = safeError(sh.Err), sh.Meta.NodeID
				rows = append(rows, row)
			}
			continue
		}
		for _, r := range sh.Resp.(*kmsg.DescribeConfigsResponse).Resources {
			configs := []any{}
			for _, v := range r.Configs {
				if len(configs) >= 200 {
					truncated = true
					break
				}
				if len(keys) > 0 {
					found := false
					for _, k := range keys {
						if v.Name == k {
							found = true
						}
					}
					if !found {
						continue
					}
				}
				configs = append(configs, configView(v, syn))
			}
			row := ErrorDetails(kerr.ErrorForCode(r.ErrorCode))
			row["name"], row["resource_type"], row["broker_id"] = r.ResourceName, typ, sh.Meta.NodeID
			row["status"], row["configs"], row["kafka_error_code"] = status(r.ErrorCode), configs, r.ErrorCode
			rows = append(rows, row)
		}
	}
	return map[string]any{"resources": rows, "truncated": truncated}, nil
}
func (c *Client) Topic(ctx context.Context, topic string) (any, error) {
	if !c.target.AllowsTopic(topic) {
		return nil, errors.New("topic not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	req.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
	r, e := c.kafka.Request(ctx, req)
	if e != nil {
		return nil, &requestError{e}
	}
	rows := []any{}
	truncated := false
	for _, t := range r.(*kmsg.MetadataResponse).Topics {
		if t.Topic == nil || *t.Topic != topic {
			continue
		}
		parts := []any{}
		for _, p := range t.Partitions {
			if len(parts) >= 500 {
				truncated = true
				break
			}
			parts = append(parts, map[string]any{"partition": p.Partition, "status": status(p.ErrorCode), "leader": p.Leader, "replicas": p.Replicas, "isr": p.ISR, "offline_replicas": p.OfflineReplicas})
		}
		rows = append(rows, map[string]any{"topic": topic, "status": status(t.ErrorCode), "internal": t.IsInternal, "partitions": parts})
	}
	return map[string]any{"topics": rows, "truncated": truncated}, nil
}
func (c *Client) offset(ctx context.Context, topic string, p int32, stamp int64, isolation int8) (map[string]any, error) {
	req := kmsg.NewPtrListOffsetsRequest()
	req.IsolationLevel = isolation
	part := kmsg.NewListOffsetsRequestTopicPartition()
	part.Partition = p
	part.Timestamp = stamp
	req.Topics = []kmsg.ListOffsetsRequestTopic{{Topic: topic, Partitions: []kmsg.ListOffsetsRequestTopicPartition{part}}}
	r, e := c.kafka.Request(ctx, req)
	if e != nil {
		return nil, &requestError{e}
	}
	for _, t := range r.(*kmsg.ListOffsetsResponse).Topics {
		for _, v := range t.Partitions {
			if t.Topic == topic && v.Partition == p {
				return map[string]any{"status": status(v.ErrorCode), "offset": v.Offset, "timestamp_ms": v.Timestamp}, nil
			}
		}
	}
	return nil, errors.New("partition response missing")
}
func (c *Client) Offsets(ctx context.Context, topic string, p int32, stamp *int64) (any, error) {
	if p < 0 || !c.target.AllowsTopic(topic) {
		return nil, errors.New("invalid partition or topic not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out := map[string]any{"topic": topic, "partition": p, "sample_atomic": false}
	for _, q := range []struct {
		name  string
		stamp int64
		iso   int8
	}{{"earliest", -2, 0}, {"high_watermark", -1, 0}, {"last_stable_offset", -1, 1}} {
		v, e := c.offset(ctx, topic, p, q.stamp, q.iso)
		if e != nil {
			out[q.name] = map[string]any{"status": safeError(e)}
		} else {
			out[q.name] = v
		}
	}
	if stamp != nil {
		v, e := c.offset(ctx, topic, p, *stamp, 0)
		if e != nil {
			out["timestamp_offset"] = map[string]any{"status": safeError(e)}
		} else {
			out["timestamp_offset"] = v
		}
	}
	return out, nil
}

type groupMember struct {
	MemberID, ClientID, ClientHost string
	Assignments                    []kmsg.ConsumerMemberAssignmentTopic
}
type groupDetail struct {
	kadm.DescribedGroup
	Members   []groupMember
	GroupType string
}

func (c *Client) describeGroup(ctx context.Context, group string) (groupDetail, error) {
	admin := kadm.NewClient(c.kafka)
	groups, err := admin.DescribeGroups(ctx, group)
	if err != nil {
		return groupDetail{}, err
	}
	g, ok := groups[group]
	if !ok {
		return groupDetail{}, errors.New("group response missing")
	}
	out := groupDetail{DescribedGroup: g, GroupType: "classic"}
	for _, m := range g.Members {
		member := groupMember{MemberID: m.MemberID, ClientID: m.ClientID, ClientHost: m.ClientHost}
		if a, ok := m.Assigned.AsConsumer(); ok {
			member.Assignments = a.Topics
		}
		out.Members = append(out.Members, member)
	}
	// Older DescribeGroups versions report new-protocol groups as Dead with
	// no error; newer versions report GROUP_ID_NOT_FOUND. Do not fall back
	// on permission or transient failures.
	if !(errors.Is(g.Err, kerr.GroupIDNotFound) || g.Err == nil && g.State == "Dead") {
		return out, nil
	}
	// Check the actual coordinator, preserving classic-only broker support.
	versionRequest := kmsg.NewPtrApiVersionsRequest()
	versionRequest.ClientSoftwareName, versionRequest.ClientSoftwareVersion = "kafka-readonly-mcp", "dev"
	versions, err := c.kafka.Broker(int(g.Coordinator.NodeID)).RetriableRequest(ctx, versionRequest)
	if err != nil {
		return groupDetail{}, err
	}
	api := versions.(*kmsg.ApiVersionsResponse)
	if api.ErrorCode != 0 {
		return groupDetail{}, kerr.ErrorForCode(api.ErrorCode)
	}
	supported := false
	for _, v := range api.ApiKeys {
		if v.ApiKey == 69 {
			supported = true
			break
		}
	}
	if !supported {
		return out, nil
	}
	modern, err := admin.DescribeConsumerGroups(ctx, group)
	if err != nil {
		return groupDetail{}, err
	}
	mg, ok := modern[group]
	if !ok {
		return groupDetail{}, errors.New("consumer group response missing")
	}
	if errors.Is(mg.Err, kerr.GroupIDNotFound) {
		return out, nil
	}
	if mg.Err != nil {
		return groupDetail{}, mg.Err
	}
	out.GroupType = "consumer"
	out.State, out.ProtocolType, out.Protocol, out.Err = mg.State, "consumer", mg.AssignorName, nil
	out.Members = nil
	for _, m := range mg.Members {
		member := groupMember{MemberID: m.MemberID, ClientID: m.ClientID, ClientHost: m.ClientHost}
		for _, tp := range m.Assignment.Sorted() {
			member.Assignments = append(member.Assignments, kmsg.ConsumerMemberAssignmentTopic{Topic: tp.Topic, Partitions: tp.Partitions})
		}
		out.Members = append(out.Members, member)
	}
	return out, nil
}

func (c *Client) Group(ctx context.Context, group string, requestedTopics []string) (any, error) {
	if !c.target.AllowsGroup(group) {
		return nil, errors.New("group not allowed")
	}
	if len(requestedTopics) > 20 {
		return nil, errors.New("too many topics")
	}
	for _, topic := range requestedTopics {
		if topic == "" || strings.ContainsAny(topic, "*?[") || !c.target.AllowsTopic(topic) {
			return nil, errors.New("invalid topic or topic not allowed")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	g, e := c.describeGroup(ctx, group)
	if e != nil {
		return nil, &requestError{e}
	}
	members := []any{}
	topics := map[string]bool{}
	truncated := false
	for _, t := range c.target.Topics {
		if !strings.ContainsAny(t, "*?[") {
			topics[t] = true
		}
	}
	for _, m := range g.Members {
		if len(members) >= 200 {
			truncated = true
			break
		}
		assignments := []any{}
		{
			for _, t := range m.Assignments {
				if !c.target.AllowsTopic(t.Topic) {
					continue
				}
				topics[t.Topic] = true
				for _, p := range t.Partitions {
					if len(assignments) >= 500 {
						truncated = true
						break
					}
					assignments = append(assignments, map[string]any{"topic": t.Topic, "partition": p})
				}
			}
		}
		members = append(members, map[string]any{"member_id": m.MemberID, "client_id": m.ClientID, "client_host": m.ClientHost, "assignments": assignments})
	}
	names := []string{}
	if len(requestedTopics) > 0 {
		topics = map[string]bool{}
		for _, topic := range requestedTopics {
			topics[topic] = true
		}
	}
	for t := range topics {
		if len(names) >= 100 {
			truncated = true
			break
		}
		names = append(names, t)
	}
	sort.Strings(names)
	offsets := []any{}
	offsetStatus := "not_queried_no_explicit_topics"
	if len(names) > 0 {
		os, e := c.fetchScopedOffsets(ctx, group, names)
		var scope *ScopeLimitError
		if errors.As(e, &scope) {
			return nil, e
		}
		offsetStatus = safeError(e)
		for topic, ps := range os {
			if !c.target.AllowsTopic(topic) {
				continue
			}
			for p, v := range ps {
				if len(offsets) >= 500 {
					truncated = true
					break
				}
				commitStatus := "available"
				if v.Err != nil {
					commitStatus = "query_failed"
				} else if v.At < 0 {
					commitStatus = "no_committed_offset"
				}
				offsets = append(offsets, map[string]any{"topic": topic, "partition": p, "committed_offset": v.At, "status": safeError(v.Err), "commit_status": commitStatus})
			}
		}
	}
	offsetScope, incompleteReason := "allowed exact topics and observed allowed member assignments", "wildcard-only inactive topic offsets are not enumerated"
	if len(requestedTopics) > 0 {
		offsetScope, incompleteReason = "explicit requested topics", ""
	}
	return map[string]any{"group": group, "group_type": g.GroupType, "status": safeError(g.Err), "state": g.State, "protocol_type": g.ProtocolType, "protocol": g.Protocol, "members": members, "committed_offsets": offsets, "offsets_status": offsetStatus, "offset_scope": offsetScope, "offset_topics": names, "incomplete_reason": incompleteReason, "application_position_available": false, "truncated": truncated}, nil
}
func recordView(r *kgo.Record, payload bool, budget int) (map[string]any, int, bool) {
	v := map[string]any{"topic": r.Topic, "partition": r.Partition, "offset": r.Offset, "timestamp_ms": r.Timestamp.UnixMilli(), "key_bytes": len(r.Key), "value_bytes": len(r.Value), "header_count": len(r.Headers)}
	size := len(r.Key) + len(r.Value)
	for _, h := range r.Headers {
		size += len(h.Key) + len(h.Value)
	}
	if payload && size <= 16384 {
		v["encoding"] = "base64"
		v["key"] = base64.StdEncoding.EncodeToString(r.Key)
		v["value"] = base64.StdEncoding.EncodeToString(r.Value)
		v["tombstone"] = r.Value == nil
		hs := []any{}
		for _, h := range r.Headers {
			hs = append(hs, map[string]any{"key": base64.StdEncoding.EncodeToString([]byte(h.Key)), "value": base64.StdEncoding.EncodeToString(h.Value)})
		}
		v["headers"] = hs
	} else if payload {
		v["payload_omitted"] = true
		v["reason"] = "per_record_byte_limit"
	}
	b, _ := json.Marshal(v)
	if payload && len(b) > 16384 {
		v, _, _ = recordView(r, false, budget)
		v["payload_omitted"] = true
		v["reason"] = "per_record_byte_limit"
		b, _ = json.Marshal(v)
	}
	return v, len(b), len(b) <= budget
}
func (c *Client) Peek(ctx context.Context, topic string, p int32, offset int64, stamp *int64, maxRecords, maxBytes int, payload bool, isolation string) (any, error) {
	if !c.target.AllowsTopic(topic) || p < 0 || maxRecords < 1 || maxRecords > 20 || maxBytes < 1 || maxBytes > 65536 || payload && !c.target.AllowPayload {
		return nil, errors.New("invalid peek limits or access denied")
	}
	if isolation != "read_committed" && isolation != "read_uncommitted" {
		return nil, errors.New("invalid isolation")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	iso := int8(0)
	if isolation == "read_committed" {
		iso = 1
	}
	if stamp != nil {
		v, e := c.offset(ctx, topic, p, *stamp, iso)
		if e != nil {
			return nil, e
		}
		if v["status"] != "ok" {
			return v, nil
		}
		offset = v["offset"].(int64)
		if offset < 0 {
			return map[string]any{"status": "timestamp_not_found", "records": []any{}}, nil
		}
	}
	if offset < 0 {
		return nil, errors.New("explicit nonnegative offset required")
	}
	start, e := c.offset(ctx, topic, p, -2, 0)
	if e != nil {
		return nil, e
	}
	end, e := c.offset(ctx, topic, p, -1, iso)
	if e != nil {
		return nil, e
	}
	if start["status"] != "ok" || end["status"] != "ok" {
		return map[string]any{"status": "offset_bounds_unavailable", "earliest": start, "end": end}, nil
	}
	high := end
	if iso == 1 {
		high, e = c.offset(ctx, topic, p, -1, 0)
		if e != nil {
			return nil, e
		}
		if high["status"] != "ok" {
			return map[string]any{"status": "offset_bounds_unavailable", "high_watermark": high}, nil
		}
	}
	if offset < start["offset"].(int64) || offset > high["offset"].(int64) {
		return map[string]any{"status": "offset_out_of_range", "records": []any{}, "earliest": start, "end": end}, nil
	}
	if iso == 1 && offset >= end["offset"].(int64) && offset < high["offset"].(int64) {
		return map[string]any{"status": "no_committed_data_visible", "records": []any{}, "last_stable_offset": end, "high_watermark": high}, nil
	}
	if offset == high["offset"].(int64) {
		return map[string]any{"status": "at_end", "records": []any{}}, nil
	}
	opts := append([]kgo.Opt{}, c.opts...)
	opts = append(opts, kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: {p: kgo.NewOffset().At(offset)}}), kgo.ConsumeResetOffset(kgo.NoResetOffset()), kgo.FetchMaxBytes(65536), kgo.FetchMaxPartitionBytes(65536), kgo.FetchMaxWait(500*time.Millisecond))
	if iso == 1 {
		opts = append(opts, kgo.FetchIsolationLevel(kgo.ReadCommitted()))
	}
	cl, e := kgo.NewClient(opts...)
	if e != nil {
		return nil, errors.New("invalid peek configuration")
	}
	defer cl.Close()
	fetch := cl.PollRecords(ctx, maxRecords)
	rows := []any{}
	used := 0
	truncated := false
	for _, r := range fetch.Records() {
		if r.Topic != topic || r.Partition != p {
			continue
		}
		v, n, ok := recordView(r, payload, maxBytes-used)
		if !ok {
			truncated = true
			break
		}
		rows = append(rows, v)
		used += n
	}
	errs := []any{}
	for _, e := range fetch.Errors() {
		errs = append(errs, map[string]any{"topic": e.Topic, "partition": e.Partition, "status": safeError(e.Err)})
	}
	return map[string]any{"records": rows, "errors": errs, "isolation": isolation, "payload_included": payload, "record_json_bytes": used, "truncated": truncated || len(rows) >= maxRecords, "sampling": "single bounded poll; may return fewer records", "earliest": start, "end": end}, nil
}

// FetchOffsetsForTopics in kadm fetches all group offsets then filters locally.
// Use explicit partitions on the wire instead.
func (c *Client) fetchScopedOffsets(ctx context.Context, group string, names []string) (kadm.OffsetResponses, error) {
	requested := make(map[string]bool, len(names))
	for _, name := range names {
		requested[name] = true
	}
	md := kmsg.NewPtrMetadataRequest()
	md.AllowAutoTopicCreation = false
	for _, name := range names {
		n := name
		md.Topics = append(md.Topics, kmsg.MetadataRequestTopic{Topic: &n})
	}
	mr, e := md.RequestWith(ctx, c.kafka)
	if e != nil {
		return nil, e
	}
	req := kmsg.NewPtrOffsetFetchRequest()
	req.Group = group
	req.Topics = make([]kmsg.OffsetFetchRequestTopic, 0)
	count := 0
	out := kadm.OffsetResponses{}
	for _, t := range mr.Topics {
		if t.Topic == nil || !requested[*t.Topic] || !c.target.AllowsTopic(*t.Topic) {
			continue
		}
		if t.ErrorCode != 0 {
			out[*t.Topic] = map[int32]kadm.OffsetResponse{-1: {Offset: kadm.Offset{Topic: *t.Topic, Partition: -1, At: -1}, Err: kerr.ErrorForCode(t.ErrorCode)}}
			continue
		}
		rt := kmsg.OffsetFetchRequestTopic{Topic: *t.Topic}
		for _, p := range t.Partitions {
			count++
			if count > 500 {
				return nil, &ScopeLimitError{}
			}
			rt.Partitions = append(rt.Partitions, p.Partition)
		}
		req.Topics = append(req.Topics, rt)
	}
	if len(req.Topics) == 0 {
		return out, nil
	}
	r, e := req.RequestWith(ctx, c.kafka)
	if e != nil {
		return nil, e
	}
	if r.ErrorCode != 0 {
		return nil, kerr.ErrorForCode(r.ErrorCode)
	}
	add := func(topic string, p int32, offset int64, code int16) {
		if !requested[topic] || !c.target.AllowsTopic(topic) {
			return
		}
		if out[topic] == nil {
			out[topic] = map[int32]kadm.OffsetResponse{}
		}
		out[topic][p] = kadm.OffsetResponse{Offset: kadm.Offset{Topic: topic, Partition: p, At: offset}, Err: kerr.ErrorForCode(code)}
	}
	if len(r.Groups) > 0 {
		for _, g := range r.Groups {
			if g.Group != group {
				continue
			}
			if g.ErrorCode != 0 {
				return nil, kerr.ErrorForCode(g.ErrorCode)
			}
			for _, t := range g.Topics {
				for _, p := range t.Partitions {
					add(t.Topic, p.Partition, p.Offset, p.ErrorCode)
				}
			}
		}
	} else {
		for _, t := range r.Topics {
			for _, p := range t.Partitions {
				add(t.Topic, p.Partition, p.Offset, p.ErrorCode)
			}
		}
	}
	return out, nil
}
