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
SELECT SCHEMA_NAME AS schema_name,
       DEFAULT_CHARACTER_SET_NAME AS charset_name,
       DEFAULT_COLLATION_NAME AS collation_name
FROM information_schema.SCHEMATA
ORDER BY SCHEMA_NAME
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
SELECT TABLE_SCHEMA AS schema_name,
       TABLE_NAME AS name,
       TABLE_TYPE AS table_type,
       ENGINE AS engine,
       TABLE_ROWS AS table_rows,
       DATA_LENGTH AS data_bytes,
       INDEX_LENGTH AS index_bytes,
       AUTO_INCREMENT AS auto_increment,
       CREATE_TIME AS create_time,
       UPDATE_TIME AS update_time,
       TABLE_COMMENT AS table_comment
FROM information_schema.TABLES
WHERE 1=1
`
	var args []any
	if schema != "" {
		if err := checkIdent(schema); err != nil {
			return nil, nil, fmt.Errorf("schema: %w", err)
		}
		sql += " AND TABLE_SCHEMA = ?"
		args = append(args, schema)
	} else {
		sql += " AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')"
	}
	if q != "" {
		sql += " AND TABLE_NAME LIKE ?"
		args = append(args, "%"+q+"%")
	}
	sql += " ORDER BY TABLE_SCHEMA, TABLE_NAME"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "table_rows/data_bytes are InnoDB estimates", res))
}

type DescribeRelationInput struct {
	Target   string `json:"target" jsonschema:"Stable target name from list_targets."`
	Relation string `json:"relation" jsonschema:"table, schema.table, or view name."`
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func checkIdent(s string) error {
	if !identRe.MatchString(s) {
		return fmt.Errorf("invalid identifier %q; use unquoted names or query_mysql", s)
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
SELECT TABLE_SCHEMA AS schema_name,
       TABLE_NAME AS name,
       TABLE_TYPE AS table_type,
       ENGINE AS engine,
       ROW_FORMAT AS row_format,
       TABLE_ROWS AS table_rows,
       AVG_ROW_LENGTH AS avg_row_length,
       DATA_LENGTH AS data_bytes,
       INDEX_LENGTH AS index_bytes,
       DATA_FREE AS data_free_bytes,
       AUTO_INCREMENT AS auto_increment,
       TABLE_COLLATION AS collation_name,
       CREATE_TIME AS create_time,
       UPDATE_TIME AS update_time,
       TABLE_COMMENT AS table_comment
FROM information_schema.TABLES
WHERE TABLE_NAME = ?
  AND (? = '' OR TABLE_SCHEMA = ?)
  AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')
ORDER BY TABLE_SCHEMA
`
	meta, err := d.Pool.Query(ctx, t, metaSQL, []any{rel, schema, schema}, 10)
	if err != nil {
		return nil, nil, err
	}
	if meta.RowCount == 0 {
		return nil, nil, fmt.Errorf("relation %q not found on %s", in.Relation, t.Name)
	}
	if meta.RowCount > 1 {
		names := make([]string, 0, meta.RowCount)
		for _, row := range meta.Rows {
			names = append(names, fmt.Sprintf("%v.%v", row["schema_name"], row["name"]))
		}
		return nil, nil, fmt.Errorf("relation %q is ambiguous; candidates: %s", in.Relation, strings.Join(names, ", "))
	}
	foundSchema := fmt.Sprint(meta.Rows[0]["schema_name"])

	cols, err := d.Pool.Query(ctx, t, `
SELECT ORDINAL_POSITION AS ordinal,
       COLUMN_NAME AS name,
       COLUMN_TYPE AS type,
       IS_NULLABLE AS is_nullable,
       COLUMN_DEFAULT AS col_default,
       COLUMN_KEY AS column_key,
       EXTRA AS extra,
       CHARACTER_SET_NAME AS charset_name,
       COLLATION_NAME AS collation_name,
       COLUMN_COMMENT AS column_comment
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION
`, []any{foundSchema, rel}, 500)
	if err != nil {
		return nil, nil, err
	}
	idxs, err := d.Pool.Query(ctx, t, `
SELECT INDEX_NAME AS index_name,
       NON_UNIQUE AS non_unique,
       INDEX_TYPE AS index_type,
       SEQ_IN_INDEX AS seq_in_index,
       COLUMN_NAME AS column_name,
       SUB_PART AS sub_part,
       NULLABLE AS nullable,
       INDEX_COMMENT AS index_comment
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY INDEX_NAME, SEQ_IN_INDEX
`, []any{foundSchema, rel}, 200)
	if err != nil {
		return nil, nil, err
	}
	cons, err := d.Pool.Query(ctx, t, `
SELECT tc.CONSTRAINT_NAME AS name,
       tc.CONSTRAINT_TYPE AS type,
       kcu.COLUMN_NAME AS column_name,
       kcu.REFERENCED_TABLE_SCHEMA AS ref_schema,
       kcu.REFERENCED_TABLE_NAME AS ref_table,
       kcu.REFERENCED_COLUMN_NAME AS ref_column
FROM information_schema.TABLE_CONSTRAINTS tc
LEFT JOIN information_schema.KEY_COLUMN_USAGE kcu
  ON kcu.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
 AND kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
 AND kcu.TABLE_NAME = tc.TABLE_NAME
WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ?
ORDER BY tc.CONSTRAINT_TYPE, tc.CONSTRAINT_NAME, kcu.ORDINAL_POSITION
`, []any{foundSchema, rel}, 200)
	if err != nil {
		return nil, nil, err
	}
	parts, perr := optionalSection(ctx, d, t, `
SELECT PARTITION_NAME, PARTITION_METHOD, PARTITION_EXPRESSION,
       PARTITION_DESCRIPTION, TABLE_ROWS, DATA_LENGTH
FROM information_schema.PARTITIONS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
  AND PARTITION_NAME IS NOT NULL
ORDER BY PARTITION_ORDINAL_POSITION
`, []any{foundSchema, rel}, 200)
	out := map[string]any{
		"target":   t.Name,
		"relation": meta.Rows[0],
	}
	putSection(out, "columns", cols)
	putSection(out, "indexes", idxs)
	putSection(out, "constraints", cons)
	putSection(out, "partitions", parts)
	if perr != "" {
		out["partitions_error"] = perr
	}
	return jsonResult(out)
}
