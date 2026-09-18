package tools

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twmb/franz-go/pkg/kerr"
	"kafka-mcp-server/internal/targets"
)

type Backend interface {
	Close()
	Capabilities(context.Context) (any, error)
	Configs(context.Context, string, []string, []string, bool) (any, error)
	Topic(context.Context, string) (any, error)
	Group(context.Context, string) (any, error)
	Offsets(context.Context, string, int32, *int64) (any, error)
	Peek(context.Context, string, int32, int64, *int64, int, int, bool, string) (any, error)
}
type Deps struct {
	Reg     *targets.Registry
	Factory func(targets.Target) (Backend, error)
}
type TargetInput struct {
	Target string `json:"target" jsonschema:"Exact target name from list_targets."`
}
type ListInput struct {
	Query string `json:"query,omitempty" jsonschema:"Optional name or description substring."`
}
type TopicInput struct {
	Target string `json:"target"`
	Topic  string `json:"topic"`
}
type GroupInput struct {
	Target string `json:"target"`
	Group  string `json:"group"`
}
type ConfigInput struct {
	Target          string   `json:"target"`
	ResourceType    string   `json:"resource_type" jsonschema:"topic or broker. Broker names are numeric node IDs."`
	ResourceNames   []string `json:"resource_names" jsonschema:"1 to 10 exact resource names. No wildcard queries."`
	ConfigKeys      []string `json:"config_keys,omitempty" jsonschema:"Up to 30 config keys; omitted returns bounded config entries."`
	IncludeSynonyms bool     `json:"include_synonyms,omitempty"`
}
type OffsetsInput struct {
	Target      string `json:"target"`
	Topic       string `json:"topic"`
	Partition   int32  `json:"partition"`
	TimestampMS *int64 `json:"timestamp_ms,omitempty" jsonschema:"Optional Unix milliseconds, not seconds. Returns timestamp lookup in addition to boundaries."`
}
type PeekInput struct {
	Target         string `json:"target"`
	Topic          string `json:"topic"`
	Partition      int32  `json:"partition"`
	StartOffset    *int64 `json:"start_offset,omitempty" jsonschema:"Exact nonnegative offset; choose this or timestamp_ms."`
	TimestampMS    *int64 `json:"timestamp_ms,omitempty" jsonschema:"Unix milliseconds; mutually exclusive with start_offset."`
	MaxRecords     int    `json:"max_records,omitempty" jsonschema:"Default 5, maximum 20."`
	MaxBytes       int    `json:"max_bytes,omitempty" jsonschema:"Payload output byte budget, default 16384, maximum 65536."`
	IncludeValue   bool   `json:"include_value,omitempty" jsonschema:"Opt in to key/header/value sampling; requires allow_payload in local target. Default metadata only."`
	IsolationLevel string `json:"isolation_level,omitempty" jsonschema:"read_committed (default) or read_uncommitted."`
}

func reply(target, operation string, scope, data any, code string) (*mcp.CallToolResult, any, error) {
	v := map[string]any{"target": target, "operation": operation, "sampled_at": time.Now().UTC().Format(time.RFC3339Nano), "scope": scope, "data": data, "status": "ok", "truncated": false}
	good, bad, cut := resultState(data)
	v["truncated"] = cut
	if bad > 0 {
		if good > 0 {
			v["status"] = "partial"
		} else {
			v["status"] = "query_failed"
		}
	}
	if code != "" {
		v["status"] = code
		v["data"] = nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	if len(b) > 256*1024 {
		v["data"] = nil
		v["status"] = "response_too_large"
		v["truncated"] = true
		v["next_step"] = "Narrow resources or config_keys; no partial JSON is returned."
		code = "response_too_large"
		b, _ = json.Marshal(v)
	}
	return &mcp.CallToolResult{IsError: code != "" || v["status"] == "query_failed", Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

// Preserve object-level failures and bounded results in the outer envelope.
func resultState(data any) (good, bad int, truncated bool) {
	switch v := data.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && (k == "status" || strings.HasSuffix(k, "_status")) {
				switch s {
				case "ok", "at_end", "timestamp_not_found", "not_visible_yet", "no_committed_data_visible":
					good++
				default:
					bad++
				}
			}
			if flag, ok := x.(bool); ok && flag && (k == "truncated" || strings.HasSuffix(k, "_truncated")) {
				truncated = true
			}
			if k == "scope" {
				continue
			}
			g, b, t := resultState(x)
			good += g
			bad += b
			truncated = truncated || t
		}
	case []any:
		for _, x := range v {
			g, b, t := resultState(x)
			good += g
			bad += b
			truncated = truncated || t
		}
	}
	return
}
func invalid(target, op string) (*mcp.CallToolResult, any, error) {
	return reply(target, op, nil, nil, "invalid_arguments")
}
func classify(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var ke *kerr.Error
	if errors.As(err, &ke) {
		switch ke.Code {
		case 29, 30, 31, 53, 65:
			return "permission_denied"
		case 33, 35:
			return "unsupported"
		case 58:
			return "authentication_failed"
		case 3, 69:
			return "not_found"
		case 1:
			return "offset_out_of_range"
		}
		return "broker_error"
	}
	return "connection_or_query_failed"
}
func (d Deps) invoke(ctx context.Context, target, op string, scope any, gate func(targets.Target) bool, fn func(context.Context, Backend) (any, error)) (*mcp.CallToolResult, any, error) {
	t, err := d.Reg.Resolve(target)
	if err != nil {
		return reply(target, op, scope, nil, "target_unavailable")
	}
	if gate != nil && !gate(t) {
		return reply(target, op, scope, nil, "not_allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cl, err := d.Factory(t)
	if err != nil {
		return reply(target, op, scope, nil, "client_configuration_error")
	}
	defer cl.Close()
	data, err := fn(ctx, cl)
	if err != nil {
		return reply(target, op, scope, nil, classify(err))
	}
	return reply(target, op, scope, data, "")
}
func (d Deps) ListTargets(_ context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, any, error) {
	ts, err := d.Reg.List()
	if err != nil {
		return reply("", "list_targets", nil, nil, "targets_configuration_error")
	}
	views := []any{}
	for _, t := range ts {
		if strings.Contains(strings.ToLower(t.Name+" "+t.Description), strings.ToLower(in.Query)) {
			views = append(views, t.PublicView())
		}
	}
	return reply("", "list_targets", nil, views, "")
}
func (d Deps) GetTargetInfo(_ context.Context, _ *mcp.CallToolRequest, in TargetInput) (*mcp.CallToolResult, any, error) {
	t, err := d.Reg.Resolve(in.Target)
	if err != nil {
		return reply(in.Target, "get_target_info", nil, nil, "target_unavailable")
	}
	return reply(in.Target, "get_target_info", nil, t.PublicView(), "")
}
func (d Deps) Capabilities(ctx context.Context, _ *mcp.CallToolRequest, in TargetInput) (*mcp.CallToolResult, any, error) {
	return d.invoke(ctx, in.Target, "kafka_capabilities", nil, nil, func(c context.Context, b Backend) (any, error) { return b.Capabilities(c) })
}

var topicName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,249}$`)

func validTopic(s string) bool { return topicName.MatchString(s) && s != "." && s != ".." }
func (d Deps) Topic(ctx context.Context, _ *mcp.CallToolRequest, in TopicInput) (*mcp.CallToolResult, any, error) {
	if !validTopic(in.Topic) {
		return invalid(in.Target, "kafka_topic_inspect")
	}
	return d.invoke(ctx, in.Target, "kafka_topic_inspect", in, func(t targets.Target) bool { return t.AllowsTopic(in.Topic) }, func(c context.Context, b Backend) (any, error) { return b.Topic(c, in.Topic) })
}
func (d Deps) Group(ctx context.Context, _ *mcp.CallToolRequest, in GroupInput) (*mcp.CallToolResult, any, error) {
	if !targets.ValidGroupName(in.Group) {
		return invalid(in.Target, "kafka_group_inspect")
	}
	return d.invoke(ctx, in.Target, "kafka_group_inspect", in, func(t targets.Target) bool { return t.AllowsGroup(in.Group) }, func(c context.Context, b Backend) (any, error) { return b.Group(c, in.Group) })
}
func (d Deps) Configs(ctx context.Context, _ *mcp.CallToolRequest, in ConfigInput) (*mcp.CallToolResult, any, error) {
	if (in.ResourceType != "topic" && in.ResourceType != "broker") || len(in.ResourceNames) < 1 || len(in.ResourceNames) > 10 || len(in.ConfigKeys) > 30 {
		return invalid(in.Target, "kafka_configs_query")
	}
	for _, s := range in.ResourceNames {
		if in.ResourceType == "topic" {
			if !validTopic(s) {
				return invalid(in.Target, "kafka_configs_query")
			}
		} else {
			id, err := strconv.ParseInt(s, 10, 32)
			if err != nil || id < 0 {
				return invalid(in.Target, "kafka_configs_query")
			}
		}
	}
	for _, k := range in.ConfigKeys {
		if len(k) == 0 || len(k) > 200 {
			return invalid(in.Target, "kafka_configs_query")
		}
	}
	return d.invoke(ctx, in.Target, "kafka_configs_query", in, func(t targets.Target) bool {
		for _, s := range in.ResourceNames {
			if in.ResourceType == "topic" && !t.AllowsTopic(s) {
				return false
			}
		}
		return true
	}, func(c context.Context, b Backend) (any, error) {
		return b.Configs(c, in.ResourceType, in.ResourceNames, in.ConfigKeys, in.IncludeSynonyms)
	})
}
func (d Deps) Offsets(ctx context.Context, _ *mcp.CallToolRequest, in OffsetsInput) (*mcp.CallToolResult, any, error) {
	if !validTopic(in.Topic) || in.Partition < 0 || (in.TimestampMS != nil && *in.TimestampMS < 0) {
		return invalid(in.Target, "kafka_offsets_query")
	}
	return d.invoke(ctx, in.Target, "kafka_offsets_query", in, func(t targets.Target) bool { return t.AllowsTopic(in.Topic) }, func(c context.Context, b Backend) (any, error) {
		return b.Offsets(c, in.Topic, in.Partition, in.TimestampMS)
	})
}
func (d Deps) Peek(ctx context.Context, _ *mcp.CallToolRequest, in PeekInput) (*mcp.CallToolResult, any, error) {
	if !validTopic(in.Topic) || in.Partition < 0 || (in.StartOffset == nil) == (in.TimestampMS == nil) || (in.StartOffset != nil && *in.StartOffset < 0) || (in.TimestampMS != nil && *in.TimestampMS < 0) || in.MaxRecords < 0 || in.MaxRecords > 20 || in.MaxBytes < 0 || in.MaxBytes > 65536 {
		return invalid(in.Target, "kafka_records_peek")
	}
	if in.MaxRecords == 0 {
		in.MaxRecords = 5
	}
	if in.MaxBytes == 0 {
		in.MaxBytes = 16384
	}
	if in.IsolationLevel == "" {
		in.IsolationLevel = "read_committed"
	}
	if in.IsolationLevel != "read_committed" && in.IsolationLevel != "read_uncommitted" {
		return invalid(in.Target, "kafka_records_peek")
	}
	offset := int64(0)
	if in.StartOffset != nil {
		offset = *in.StartOffset
	}
	return d.invoke(ctx, in.Target, "kafka_records_peek", in, func(t targets.Target) bool { return t.AllowsTopic(in.Topic) && (!in.IncludeValue || t.AllowPayload) }, func(c context.Context, b Backend) (any, error) {
		return b.Peek(c, in.Topic, in.Partition, offset, in.TimestampMS, in.MaxRecords, in.MaxBytes, in.IncludeValue, in.IsolationLevel)
	})
}
func Register(s *mcp.Server, d Deps) {
	tool := func(n, desc string) *mcp.Tool {
		return &mcp.Tool{Name: n, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	}
	mcp.AddTool(s, tool("list_targets", "List configured Kafka targets without credentials. Local only."), d.ListTargets)
	mcp.AddTool(s, tool("get_target_info", "Local target metadata, allowlists and message sampling policy; no credentials."), d.GetTargetInfo)
	mcp.AddTool(s, tool("kafka_capabilities", "Read cluster metadata and broker protocol capabilities; support is not proof of authorization."), d.Capabilities)
	mcp.AddTool(s, tool("kafka_configs_query", "Read effective topic/broker config with sources and optional synonyms. No changes; sensitive values hidden."), d.Configs)
	mcp.AddTool(s, tool("kafka_topic_inspect", "Inspect one allowlisted topic's partition leaders, replica and ISR IDs. No auto-create."), d.Topic)
	mcp.AddTool(s, tool("kafka_group_inspect", "Read one allowed group's members, assignments and committed offsets. Not consumer processing position or business stack traces."), d.Group)
	mcp.AddTool(s, tool("kafka_offsets_query", "Read one partition's earliest offset, high watermark, last stable offset and optional timestamp lookup. Boundaries are not message counts."), d.Offsets)
	mcp.AddTool(s, tool("kafka_records_peek", "Bounded manual partition read, no group join or commits. Metadata only unless local policy and include_value allow payload. Empty sample is not proof of no data."), d.Peek)
}
