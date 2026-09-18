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
	"INSERT": {}, "UPDATE": {}, "DELETE": {}, "REPLACE": {}, "MERGE": {},
	"LOAD": {}, "TRUNCATE": {}, "ALTER": {}, "CREATE": {}, "DROP": {},
	"GRANT": {}, "REVOKE": {}, "CALL": {}, "DO": {}, "LOCK": {}, "UNLOCK": {},
	"FLUSH": {}, "RESET": {}, "PURGE": {}, "KILL": {}, "OPTIMIZE": {},
	"REPAIR": {}, "ANALYZE": {}, "CHECK": {}, "CHECKSUM": {}, "HANDLER": {},
	"PREPARE": {}, "EXECUTE": {}, "DEALLOCATE": {}, "SET": {}, "USE": {},
	"BEGIN": {}, "COMMIT": {}, "ROLLBACK": {}, "START": {}, "EXPLAIN": {},
	"INTO": {}, "BINLOG": {}, "CHANGE": {}, "CLONE": {}, "SHUTDOWN": {},
	"INSTALL": {}, "UNINSTALL": {}, "XA": {}, "SAVEPOINT": {},
	"RENAME": {}, "IMPORT": {}, "CACHE": {}, "RELOAD": {},
}

var forbiddenFuncs = map[string]struct{}{
	"GET_LOCK": {}, "RELEASE_LOCK": {}, "IS_USED_LOCK": {}, "IS_FREE_LOCK": {},
	"SLEEP": {}, "BENCHMARK": {}, "LOAD_FILE": {},
	"MASTER_POS_WAIT": {}, "SOURCE_POS_WAIT": {},
	"WAIT_FOR_EXECUTED_GTID_SET": {}, "WAIT_UNTIL_SQL_THREAD_AFTER_GTIDS": {},
	"EXECUTE_PREPARED_STMT": {}, "DIAGNOSTICS": {},
	"PS_SETUP_ENABLE_CONSUMER": {}, "PS_SETUP_DISABLE_CONSUMER": {},
	"PS_SETUP_ENABLE_INSTRUMENT": {}, "PS_SETUP_DISABLE_INSTRUMENT": {},
	"PS_SETUP_ENABLE_THREAD": {}, "PS_SETUP_DISABLE_THREAD": {},
	"PS_SETUP_RELOAD_SAVED": {}, "PS_SETUP_RESET_TO_DEFAULT": {},
	"PS_SETUP_SAVE": {}, "PS_TRUNCATE_ALL_TABLES": {},
	"PS_TRACE_THREAD": {}, "PS_TRACE_STATEMENT_DIGEST": {},
	"STATEMENT_PERFORMANCE_ANALYZER": {},
}

type ident struct {
	word   string
	quoted bool
}

func CheckReadQuery(sql string) (string, error) {
	if strings.TrimSpace(sql) == "" {
		return "", fmt.Errorf("sql is required")
	}
	if utf8.RuneCountInString(sql) > MaxSQLRunes {
		return "", fmt.Errorf("sql exceeds %d characters", MaxSQLRunes)
	}
	if strings.Contains(sql, "/*!") {
		return "", fmt.Errorf("read-only query rejects MySQL versioned comments")
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
	if err := rejectLockingReads(words); err != nil {
		return "", err
	}
	return stripped, nil
}

func CheckExplainSubject(sql string) (string, error) {
	return CheckReadQuery(sql)
}

func rejectLockingReads(words []ident) error {
	for i := 0; i < len(words); i++ {
		if words[i].quoted {
			continue
		}
		if words[i].word == "FOR" && i+1 < len(words) && !words[i+1].quoted {
			switch words[i+1].word {
			case "UPDATE", "SHARE":
				return fmt.Errorf("read-only query rejects FOR %s", words[i+1].word)
			}
		}
	}
	return nil
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
	cursor := 0
	err := scanSQL(sql, func(kind rune, text string, i int) error {
		switch kind {
		case '-', '#', '/':
			// Copy original spans: decoded quoted tokens must never become SQL.
			b.WriteString(sql[cursor:i])
			if kind != '/' || strings.ContainsAny(text, "\r\n") {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
			cursor = i + len(text)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	b.WriteString(sql[cursor:])
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
		case '"', '`':
			flush()
			words = append(words, ident{word: strings.ToUpper(text), quoted: true})
		case '\'', ';', '-', '/', '#':
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

// scanSQL walks MySQL-ish quoting: ', '', ", "", `...`, --, #, /* */.
// Comment callbacks carry their original text and starting offset so callers
// can remove them without re-encoding string literals or quoted identifiers.
func scanSQL(sql string, emit func(kind rune, text string, i int) error) error {
	inSingle, inDouble, inTick := false, false, false
	var q strings.Builder
	for i := 0; i < len(sql); i++ {
		c := sql[i]
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
			if c == '\\' && i+1 < len(sql) {
				q.WriteByte(sql[i+1])
				i++
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
			if c == '\\' && i+1 < len(sql) {
				q.WriteByte(sql[i+1])
				i++
				continue
			}
			q.WriteByte(c)
			continue
		}
		if inTick {
			if c == '`' {
				if i+1 < len(sql) && sql[i+1] == '`' {
					q.WriteByte('`')
					i++
					continue
				}
				if err := emit('`', q.String(), i); err != nil {
					return err
				}
				if err := emit(0, "`", i); err != nil {
					return err
				}
				inTick = false
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
		if c == '`' {
			if err := emit(0, "`", i); err != nil {
				return err
			}
			inTick = true
			q.Reset()
			continue
		}
		if c == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			if i+2 >= len(sql) || sql[i+2] == ' ' || sql[i+2] == '\t' || sql[i+2] == '\n' || sql[i+2] == '\r' {
				start := i
				for i < len(sql) && sql[i] != '\n' {
					i++
				}
				if err := emit('-', sql[start:i], start); err != nil {
					return err
				}
				continue
			}
		}
		if c == '#' {
			start := i
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			if err := emit('#', sql[start:i], start); err != nil {
				return err
			}
			continue
		}
		if c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			start := i
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 >= len(sql) {
				return fmt.Errorf("unclosed block comment")
			}
			i++
			if err := emit('/', sql[start:i+1], start); err != nil {
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
	if inTick && q.Len() > 0 {
		if err := emit('`', q.String(), len(sql)); err != nil {
			return err
		}
	}
	if inDouble && q.Len() > 0 {
		if err := emit('"', q.String(), len(sql)); err != nil {
			return err
		}
	}
	return nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || unicode.IsLetter(rune(c))
}

func isIdentPart(c byte) bool {
	return c == '_' || c == '$' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))
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
