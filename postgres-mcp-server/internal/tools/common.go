package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"postgres-mcp-server/internal/pg"
	"postgres-mcp-server/internal/sqlguard"
	"postgres-mcp-server/internal/targets"
)

type Deps struct {
	Reg     *targets.Registry
	Pool    *pg.Pooler
	Version string
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(b)), v, nil
}

func (d Deps) resolve(query string) (targets.Target, error) {
	t, hits, err := d.Reg.Resolve(query)
	if err != nil {
		if len(hits) > 0 {
			names := make([]string, 0, len(hits))
			for _, h := range hits {
				names = append(names, h.Name)
			}
			return targets.Target{}, fmt.Errorf("%w; candidates: %s", err, strings.Join(names, ", "))
		}
		return targets.Target{}, err
	}
	if d.Pool != nil {
		d.Pool.Reap(d.Reg.Snapshot())
	}
	return *t, nil
}

func (d Deps) query(ctx context.Context, target, sql string, args []any, maxRows int) (*pg.Result, targets.Target, error) {
	t, err := d.resolve(target)
	if err != nil {
		return nil, targets.Target{}, err
	}
	res, err := d.Pool.Query(ctx, t, sql, args, maxRows)
	if err != nil {
		return nil, t, err
	}
	return res, t, nil
}

func wrapRows(target targets.Target, note string, res *pg.Result) map[string]any {
	out := map[string]any{
		"target":    target.Name,
		"dbname":    target.DBName,
		"columns":   res.Columns,
		"rows":      res.Rows,
		"row_count": res.RowCount,
	}
	if res.Truncated {
		out["truncated"] = true
		out["truncated_reason"] = res.TruncatedReason
	}
	if note != "" {
		out["note"] = note
	}
	return out
}

func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func defaultRows() int { return sqlguard.DefaultRows }
