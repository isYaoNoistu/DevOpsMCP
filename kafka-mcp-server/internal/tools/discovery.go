package tools

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"kafka-mcp-server/internal/targets"
	"unicode/utf8"
)

type TopicsListInput struct {
	Target          string `json:"target"`
	Query           string `json:"query,omitempty" jsonschema:"Optional case-sensitive name substring, at most 256 bytes."`
	After           string `json:"after,omitempty" jsonschema:"Exclusive name cursor from next_after. Fresh snapshot; not server-side pagination."`
	Limit           int    `json:"limit,omitempty" jsonschema:"Default 50, maximum 200 returned allowed names. Does not limit broker metadata scan."`
	IncludeInternal bool   `json:"include_internal,omitempty" jsonschema:"Include allowed internal topics, default false."`
}
type GroupsListInput struct {
	Target string `json:"target"`
	Query  string `json:"query,omitempty" jsonschema:"Optional case-sensitive name substring, at most 256 bytes."`
	After  string `json:"after,omitempty" jsonschema:"Exclusive group name cursor from next_after. Fresh snapshot; may change between calls."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Default 50, maximum 200 returned allowed names. Does not limit broker group scan."`
}

func (d Deps) TopicsList(ctx context.Context, _ *mcp.CallToolRequest, in TopicsListInput) (*mcp.CallToolResult, any, error) {
	if in.Limit < 0 || in.Limit > 200 || len(in.Query) > 256 || !utf8.ValidString(in.Query) || (in.After != "" && !validTopic(in.After)) {
		return invalid(in.Target, "kafka_topics_list")
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	return d.invoke(ctx, in.Target, "kafka_topics_list", in, nil, func(ctx context.Context, b Backend) (any, error) {
		return b.ListTopics(ctx, in.Query, in.After, in.Limit, in.IncludeInternal)
	})
}
func (d Deps) GroupsList(ctx context.Context, _ *mcp.CallToolRequest, in GroupsListInput) (*mcp.CallToolResult, any, error) {
	if in.Limit < 0 || in.Limit > 200 || len(in.Query) > 256 || !utf8.ValidString(in.Query) || (in.After != "" && !targets.ValidGroupName(in.After)) {
		return invalid(in.Target, "kafka_groups_list")
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	return d.invoke(ctx, in.Target, "kafka_groups_list", in, nil, func(ctx context.Context, b Backend) (any, error) {
		return b.ListGroups(ctx, in.Query, in.After, in.Limit)
	})
}
