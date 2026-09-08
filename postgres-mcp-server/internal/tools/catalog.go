package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListSchemasInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func (d Deps) ListSchemas(ctx context.Context, _ *mcp.CallToolRequest, in ListSchemasInput) (*mcp.CallToolResult, any, error) {
	const sql = `
SELECT n.nspname AS schema,
       pg_catalog.pg_get_userbyid(n.nspowner) AS owner
FROM pg_namespace n
WHERE n.nspname NOT LIKE 'pg_toast%'
  AND n.nspname NOT LIKE 'pg_temp_%'
ORDER BY 1
`
	res, t, err := d.query(ctx, in.Target, sql, nil, 200)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type ListTablesInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Schema string `json:"schema,omitempty" jsonschema:"Optional schema name. Empty = user schemas only."`
	Query  string `json:"query,omitempty" jsonschema:"Optional substring filter on table name."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 100, cap 500."`
}

func (d Deps) ListTables(ctx context.Context, _ *mcp.CallToolRequest, in ListTablesInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, defaultRows(), 500)
	schema := strings.TrimSpace(in.Schema)
	q := strings.TrimSpace(in.Query)
	sql := `
SELECT n.nspname AS schema,
       c.relname AS name,
       CASE c.relkind
         WHEN 'r' THEN 'table'
         WHEN 'p' THEN 'partitioned_table'
         WHEN 'v' THEN 'view'
         WHEN 'm' THEN 'materialized_view'
         WHEN 'f' THEN 'foreign_table'
         ELSE c.relkind::text
       END AS kind,
       pg_catalog.pg_get_userbyid(c.relowner) AS owner,
       pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f')
`
	var args []any
	n := 1
	if schema != "" {
		if err := checkIdent(schema); err != nil {
			return nil, nil, fmt.Errorf("schema: %w", err)
		}
		sql += fmt.Sprintf(" AND n.nspname = $%d", n)
		args = append(args, schema)
		n++
	} else {
		sql += " AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')"
	}
	if q != "" {
		sql += fmt.Sprintf(" AND c.relname ILIKE $%d", n)
		args = append(args, "%"+q+"%")
	}
	sql += " ORDER BY 1, 2"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type DescribeRelationInput struct {
	Target   string `json:"target" jsonschema:"Stable target name from list_targets."`
	Relation string `json:"relation" jsonschema:"table, schema.table, or view name."`
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const replicaIdentityExpr = `CASE c.relreplident
         WHEN 'd' THEN 'default'
         WHEN 'n' THEN 'nothing'
         WHEN 'f' THEN 'full'
         WHEN 'i' THEN 'index'
         ELSE c.relreplident::text
       END`

func checkIdent(s string) error {
	if !identRe.MatchString(s) {
		return fmt.Errorf("invalid identifier %q; use unquoted names or query_postgres", s)
	}
	return nil
}

func splitRelation(name string) (schema, rel string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("relation is required")
	}
	parts := strings.Split(name, ".")
	if len(parts) == 1 {
		if err := checkIdent(parts[0]); err != nil {
			return "", "", err
		}
		return "", parts[0], nil
	}
	if len(parts) == 2 {
		if err := checkIdent(parts[0]); err != nil {
			return "", "", err
		}
		if err := checkIdent(parts[1]); err != nil {
			return "", "", err
		}
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("relation must be table or schema.table")
}

func (d Deps) DescribeRelation(ctx context.Context, _ *mcp.CallToolRequest, in DescribeRelationInput) (*mcp.CallToolResult, any, error) {
	schema, rel, err := splitRelation(in.Relation)
	if err != nil {
		return nil, nil, err
	}
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}

	metaSQL := `
SELECT n.nspname AS schema,
       c.relname AS name,
       CASE c.relkind
         WHEN 'r' THEN 'table'
         WHEN 'p' THEN 'partitioned_table'
         WHEN 'v' THEN 'view'
         WHEN 'm' THEN 'materialized_view'
         WHEN 'f' THEN 'foreign_table'
         WHEN 'S' THEN 'sequence'
         ELSE c.relkind::text
       END AS kind,
       ` + replicaIdentityExpr + ` AS replica_identity,
       c.oid::bigint AS oid,
       pg_size_pretty(pg_relation_size(c.oid)) AS rel_size,
       pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relname = $1
  AND ($2 = '' OR n.nspname = $2)
  AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
ORDER BY 1
`
	meta, err := d.Pool.Query(ctx, t, metaSQL, []any{rel, schema}, 10)
	if err != nil {
		return nil, nil, err
	}
	if meta.RowCount == 0 {
		return nil, nil, fmt.Errorf("relation %q not found on %s", in.Relation, t.Name)
	}
	if meta.RowCount > 1 {
		names := make([]string, 0, meta.RowCount)
		for _, row := range meta.Rows {
			names = append(names, fmt.Sprintf("%v.%v", row["schema"], row["name"]))
		}
		return nil, nil, fmt.Errorf("relation %q is ambiguous; candidates: %s", in.Relation, strings.Join(names, ", "))
	}

	oid := meta.Rows[0]["oid"]
	colSQL := `
SELECT a.attnum AS ordinal,
       a.attname AS name,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS type,
       a.attnotnull AS not_null,
       pg_get_expr(ad.adbin, ad.adrelid) AS default
FROM pg_attribute a
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE a.attrelid = $1
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY a.attnum
`
	idxSQL := `
SELECT i.relname AS index_name,
       ix.indisunique AS unique,
       ix.indisprimary AS primary,
       pg_get_indexdef(ix.indexrelid) AS definition
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
WHERE ix.indrelid = $1
ORDER BY 1
`
	conSQL := `
SELECT conname AS name,
       CASE contype
         WHEN 'p' THEN 'primary'
         WHEN 'u' THEN 'unique'
         WHEN 'f' THEN 'foreign'
         WHEN 'c' THEN 'check'
         WHEN 'x' THEN 'exclusion'
         ELSE contype::text
       END AS type,
       pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = $1
ORDER BY 1
`
	cols, err := d.Pool.Query(ctx, t, colSQL, []any{oid}, 500)
	if err != nil {
		return nil, nil, err
	}
	idxs, err := d.Pool.Query(ctx, t, idxSQL, []any{oid}, 200)
	if err != nil {
		return nil, nil, err
	}
	cons, err := d.Pool.Query(ctx, t, conSQL, []any{oid}, 200)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]any{
		"target":      t.Name,
		"relation":    meta.Rows[0],
		"columns":     cols.Rows,
		"indexes":     idxs.Rows,
		"constraints": cons.Rows,
	})
}
