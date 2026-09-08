// Package sqlguard accepts only a single read-only SELECT/WITH statement.
package sqlguard

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxSQLRunes = 8000
	DefaultRows = 100
	MaxRows     = 500
	MaxBytes    = 256 * 1024
)

var forbiddenWords = map[string]struct{}{
	"INSERT": {}, "UPDATE": {}, "DELETE": {}, "MERGE": {}, "COPY": {},
	"TRUNCATE": {}, "ALTER": {}, "CREATE": {}, "DROP": {}, "GRANT": {},
	"REVOKE": {}, "CALL": {}, "DO": {}, "VACUUM": {}, "ANALYZE": {},
	"COMMENT": {}, "LISTEN": {}, "NOTIFY": {}, "LOAD": {}, "LOCK": {},
	"REINDEX": {}, "CLUSTER": {}, "CHECKPOINT": {}, "DISCARD": {},
	"RESET": {}, "REFRESH": {}, "PREPARE": {}, "EXECUTE": {},
	"DEALLOCATE": {}, "DECLARE": {}, "FETCH": {}, "MOVE": {}, "CLOSE": {},
	"UNLISTEN": {}, "SET": {}, "BEGIN": {}, "COMMIT": {}, "ROLLBACK": {},
	"START": {}, "EXPLAIN": {}, "SECURITY": {}, "INTO": {},
}

// Side-effect functions. Matched against unquoted and quoted identifiers
// (schema-qualified and "public"."dblink_exec" included). READ ONLY
// transactions do not stop dblink_exec on a remote database or session
// advisory locks that survive ROLLBACK on a pooled connection.
var forbiddenFuncs = map[string]struct{}{
	"PG_READ_FILE": {}, "PG_READ_BINARY_FILE": {}, "PG_LS_DIR": {},
	"PG_STAT_FILE": {}, "PG_WRITE_FILE": {}, "PG_FILE_WRITE": {},
	"PG_FILE_RENAME": {}, "PG_FILE_UNLINK": {},
	"LO_IMPORT": {}, "LO_EXPORT": {}, "LO_UNLINK": {}, "LO_CREATE": {}, "LO_PUT": {},
	"DBLINK": {}, "DBLINK_EXEC": {}, "DBLINK_CONNECT": {}, "DBLINK_CONNECT_U": {},
	"DBLINK_DISCONNECT": {},
	"PG_ADVISORY_LOCK": {}, "PG_ADVISORY_LOCK_SHARED": {},
	"PG_TRY_ADVISORY_LOCK": {}, "PG_TRY_ADVISORY_LOCK_SHARED": {},
	"PG_ADVISORY_UNLOCK": {}, "PG_ADVISORY_UNLOCK_SHARED": {},
	"PG_ADVISORY_UNLOCK_ALL": {},
	"PG_ADVISORY_XACT_LOCK": {}, "PG_ADVISORY_XACT_LOCK_SHARED": {},
	"PG_TRY_ADVISORY_XACT_LOCK": {}, "PG_TRY_ADVISORY_XACT_LOCK_SHARED": {},
	"PG_TERMINATE_BACKEND": {}, "PG_CANCEL_BACKEND": {},
	"PG_RELOAD_CONF": {}, "PG_ROTATE_LOGFILE": {}, "PG_PROMOTE": {},
	"SET_CONFIG": {},
	"PG_STAT_RESET": {}, "PG_STAT_RESET_SHARED": {},
	"PG_STAT_STATEMENTS_RESET": {},
	"PG_STAT_RESET_SINGLE_TABLE_COUNTERS": {},
	"PG_STAT_RESET_SINGLE_FUNCTION_COUNTERS": {},
	"PG_CREATE_PHYSICAL_REPLICATION_SLOT": {},
	"PG_CREATE_LOGICAL_REPLICATION_SLOT": {},
	"PG_DROP_REPLICATION_SLOT": {},
	"PG_REPLICATION_ORIGIN_ADVANCE": {}, "PG_REPLICATION_ORIGIN_DROP": {},
	"PG_SWITCH_WAL": {}, "PG_BACKUP_START": {}, "PG_BACKUP_STOP": {},
	"PG_WAL_REPLAY_PAUSE": {}, "PG_WAL_REPLAY_RESUME": {},
	"PG_LOG_BACKEND_MEMORY_CONTEXTS": {},
}

type ident struct {
	word   string
	quoted bool
}

// CheckReadQuery validates a single SELECT/WITH statement for query_postgres.
func CheckReadQuery(sql string) (string, error) {
	if strings.TrimSpace(sql) == "" {
		return "", fmt.Errorf("sql is required")
	}
	if utf8.RuneCountInString(sql) > MaxSQLRunes {
		return "", fmt.Errorf("sql exceeds %d characters", MaxSQLRunes)
	}
	stripped, err := stripComments(sql)
	if err != nil {
		return "", err
	}
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return "", fmt.Errorf("sql is empty after removing comments")
	}
	if err := rejectMultiStatement(stripped); err != nil {
		return "", err
	}
	stripped = strings.TrimRight(strings.TrimSpace(stripped), ";")
	words, err := scanWords(stripped)
	if err != nil {
		return "", err
	}
	if len(words) == 0 {
		return "", fmt.Errorf("sql has no tokens")
	}
	first := words[0]
	if first.quoted || (first.word != "SELECT" && first.word != "WITH" && first.word != "TABLE" && first.word != "VALUES") {
		return "", fmt.Errorf("only a single SELECT / WITH statement is allowed, got %s", first.word)
	}
	for _, w := range words {
		if !w.quoted {
			if _, bad := forbiddenWords[w.word]; bad {
				return "", fmt.Errorf("read-only query rejects keyword %s", w.word)
			}
		}
		if _, bad := forbiddenFuncs[w.word]; bad {
			return "", fmt.Errorf("read-only query rejects function %s", w.word)
		}
	}
	return stripped, nil
}

// CheckExplainSubject is the inner query for explain_query (same rules as CheckReadQuery).
func CheckExplainSubject(sql string) (string, error) {
	return CheckReadQuery(sql)
}

func rejectMultiStatement(sql string) error {
	return scanSQL(sql, func(kind rune, _ string, i int) error {
		if kind == ';' {
			rest := strings.TrimSpace(sql[i+1:])
			if rest != "" {
				return fmt.Errorf("only one SQL statement is allowed")
			}
		}
		return nil
	})
}

func stripComments(sql string) (string, error) {
	var b strings.Builder
	err := scanSQL(sql, func(kind rune, text string, _ int) error {
		switch kind {
		case '-':
			b.WriteByte('\n')
		case '/':
			b.WriteByte(' ')
		default:
			b.WriteString(text)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

func scanWords(sql string) ([]ident, error) {
	var words []ident
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		words = append(words, ident{word: strings.ToUpper(cur.String()), quoted: false})
		cur.Reset()
	}
	err := scanSQL(sql, func(kind rune, text string, _ int) error {
		switch kind {
		case '"':
			flush()
			words = append(words, ident{word: strings.ToUpper(text), quoted: true})
		case '\'', '$', ';', '-', '/':
			flush()
		default:
			if text == "" {
				flush()
				return nil
			}
			c := text[0]
			if isIdentStart(c) || (cur.Len() > 0 && isIdentPart(c)) {
				cur.WriteByte(c)
				return nil
			}
			flush()
		}
		return nil
	})
	flush()
	return words, err
}

// scanSQL walks SQL with PostgreSQL-ish quoting: ', '', ", "", $tag$...$tag$.
// Callback kinds:
//
//	'i'  identifier fragment (unquoted run) — not used; raw chars come as kind 0
//	'"'  quoted identifier content
//	'\'' string literal (skipped)
//	'$'  dollar-quoted string (skipped)
//	'-'  line comment
//	'/'  block comment
//	';'  semicolon at i
//	0    raw character in text
func scanSQL(sql string, emit func(kind rune, text string, i int) error) error {
	inSingle, inDouble, inDollar := false, false, false
	var dollarTag string
	var q strings.Builder
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if inDollar {
			if strings.HasPrefix(sql[i:], dollarTag) {
				if err := emit('$', q.String(), i); err != nil {
					return err
				}
				if err := emit(0, dollarTag, i); err != nil {
					return err
				}
				i += len(dollarTag) - 1
				inDollar = false
				dollarTag = ""
				q.Reset()
			} else {
				q.WriteByte(c)
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					q.WriteByte('\'')
					i++
					continue
				}
				if err := emit('\'', q.String(), i); err != nil {
					return err
				}
				if err := emit(0, "'", i); err != nil {
					return err
				}
				inSingle = false
				q.Reset()
				continue
			}
			q.WriteByte(c)
			continue
		}
		if inDouble {
			if c == '"' {
				if i+1 < len(sql) && sql[i+1] == '"' {
					q.WriteByte('"')
					i++
					continue
				}
				if err := emit('"', q.String(), i); err != nil {
					return err
				}
				if err := emit(0, `"`, i); err != nil {
					return err
				}
				inDouble = false
				q.Reset()
				continue
			}
			q.WriteByte(c)
			continue
		}
		if c == '\'' {
			if err := emit(0, "'", i); err != nil {
				return err
			}
			inSingle = true
			q.Reset()
			continue
		}
		if c == '"' {
			if err := emit(0, `"`, i); err != nil {
				return err
			}
			inDouble = true
			q.Reset()
			continue
		}
		if c == '$' {
			tag, ok := readDollarTag(sql[i:])
			if ok {
				if err := emit(0, tag, i); err != nil {
					return err
				}
				inDollar = true
				dollarTag = tag
				q.Reset()
				i += len(tag) - 1
				continue
			}
		}
		if c == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			if err := emit('-', "", i); err != nil {
				return err
			}
			continue
		}
		if c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 >= len(sql) {
				return fmt.Errorf("unclosed block comment")
			}
			i++
			if err := emit('/', "", i); err != nil {
				return err
			}
			continue
		}
		if c == ';' {
			if err := emit(';', ";", i); err != nil {
				return err
			}
			continue
		}
		if err := emit(0, sql[i:i+1], i); err != nil {
			return err
		}
	}
	if inSingle || inDouble || inDollar {
		// Leave unclosed quotes to the database; still emit leftover quoted ident
		// so "dblink_exec" without a closer cannot sneak through as "not a token".
		if inDouble && q.Len() > 0 {
			if err := emit('"', q.String(), len(sql)); err != nil {
				return err
			}
		}
	}
	return nil
}

func readDollarTag(s string) (string, bool) {
	if s == "" || s[0] != '$' {
		return "", false
	}
	j := 1
	for j < len(s) && (s[j] == '_' || isIdentPart(s[j])) {
		j++
	}
	if j < len(s) && s[j] == '$' {
		return s[:j+1], true
	}
	return "", false
}

func isIdentStart(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c))
}

func isIdentPart(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))
}

func ClampRows(n int) int {
	if n <= 0 {
		return DefaultRows
	}
	if n > MaxRows {
		return MaxRows
	}
	return n
}
