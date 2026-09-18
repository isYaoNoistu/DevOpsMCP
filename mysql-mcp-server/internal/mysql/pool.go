package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"mysql-mcp-server/internal/cred"
	"mysql-mcp-server/internal/sqlguard"
	"mysql-mcp-server/internal/targets"
)

type Config struct {
	ConnectTimeout   time.Duration
	StatementTimeout time.Duration
	LockTimeout      time.Duration
	AllowAnalyze     bool
}

func (c Config) withDefaults() Config {
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.StatementTimeout <= 0 {
		c.StatementTimeout = 10 * time.Second
	}
	if c.LockTimeout <= 0 {
		c.LockTimeout = 2 * time.Second
	}
	return c
}

type Pooler struct {
	cfg   Config
	mu    sync.Mutex
	dbs   map[string]*sql.DB
	fp    map[string]string
}

func NewPooler(cfg Config) *Pooler {
	return &Pooler{
		cfg: cfg.withDefaults(),
		dbs: map[string]*sql.DB{},
		fp:  map[string]string{},
	}
}

func (p *Pooler) Config() Config { return p.cfg }

func (p *Pooler) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, db := range p.dbs {
		_ = db.Close()
		delete(p.dbs, name)
		delete(p.fp, name)
	}
}

func fingerprint(t targets.Target) string {
	return strings.Join([]string{
		t.Name, t.Host, fmt.Sprintf("%d", t.PortOrDefault()),
		t.DBName, t.User, t.SSLModeOrDefault(), t.CredentialRef,
	}, "|")
}

func tlsName(mode string) string {
	switch strings.ToLower(mode) {
	case "disable":
		return "false"
	case "prefer":
		return "preferred"
	case "require", "skip-verify":
		return "skip-verify"
	case "verify-ca", "verify-full":
		return "true"
	default:
		return "true"
	}
}

func (p *Pooler) acquire(ctx context.Context, t targets.Target) (*sql.DB, error) {
	fp := fingerprint(t)
	p.mu.Lock()
	if db, ok := p.dbs[t.Name]; ok && p.fp[t.Name] == fp {
		p.mu.Unlock()
		return db, nil
	}
	if old, ok := p.dbs[t.Name]; ok {
		_ = old.Close()
		delete(p.dbs, t.Name)
		delete(p.fp, t.Name)
	}
	p.mu.Unlock()

	password, err := cred.Password(t)
	if err != nil {
		return nil, err
	}

	cfg := mysqldriver.NewConfig()
	cfg.User = t.User
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", t.Host, t.PortOrDefault())
	cfg.DBName = t.DBName
	cfg.Timeout = p.cfg.ConnectTimeout
	cfg.ReadTimeout = p.cfg.StatementTimeout + 2*time.Second
	cfg.WriteTimeout = p.cfg.StatementTimeout
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Params = map[string]string{
		"charset":   "utf8mb4",
		"collation": "utf8mb4_unicode_ci",
	}
	cfg.TLSConfig = tlsName(t.SSLModeOrDefault())
	cfg.RejectReadOnly = false

	connector, err := mysqldriver.NewConnector(cfg)
	if err != nil {
		return nil, sanitizeErr(err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(2 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, p.cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, sanitizeErr(err)
	}

	p.mu.Lock()
	if existing, ok := p.dbs[t.Name]; ok && p.fp[t.Name] == fp {
		p.mu.Unlock()
		_ = db.Close()
		return existing, nil
	}
	if old, ok := p.dbs[t.Name]; ok {
		_ = old.Close()
	}
	p.dbs[t.Name] = db
	p.fp[t.Name] = fp
	p.mu.Unlock()
	return db, nil
}

func (p *Pooler) Reap(active []targets.Target) {
	keep := make(map[string]string, len(active))
	for _, t := range active {
		keep[t.Name] = fingerprint(t)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, db := range p.dbs {
		if fp, ok := keep[name]; !ok || p.fp[name] != fp {
			_ = db.Close()
			delete(p.dbs, name)
			delete(p.fp, name)
		}
	}
}

func (p *Pooler) invalidate(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if db, ok := p.dbs[name]; ok {
		_ = db.Close()
		delete(p.dbs, name)
		delete(p.fp, name)
	}
}

type Result struct {
	Columns         []string         `json:"columns"`
	Rows            []map[string]any `json:"rows"`
	RowCount        int              `json:"row_count"`
	Truncated       bool             `json:"truncated,omitempty"`
	TruncatedReason string           `json:"truncated_reason,omitempty"`
}

func (p *Pooler) Query(ctx context.Context, t targets.Target, sqlText string, args []any, maxRows int) (*Result, error) {
	maxRows = sqlguard.ClampRows(maxRows)
	db, err := p.acquire(ctx, t)
	if err != nil {
		return nil, err
	}
	res, err := p.queryOnce(ctx, db, sqlText, args, maxRows)
	if err != nil && isAuthError(err) {
		p.invalidate(t.Name)
		db, err2 := p.acquire(ctx, t)
		if err2 != nil {
			return nil, err2
		}
		return p.queryOnce(ctx, db, sqlText, args, maxRows)
	}
	return res, err
}

func (p *Pooler) queryOnce(ctx context.Context, db *sql.DB, sqlText string, args []any, maxRows int) (*Result, error) {
	qctx, cancel := context.WithTimeout(ctx, p.cfg.ConnectTimeout+p.cfg.StatementTimeout+2*time.Second)
	defer cancel()

	tx, err := db.BeginTx(qctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, sanitizeErr(err)
	}
	defer func() { _ = tx.Rollback() }()

	stmtMs := p.cfg.StatementTimeout.Milliseconds()
	if stmtMs < 1 {
		stmtMs = 1
	}
	lockSec := int(p.cfg.LockTimeout.Seconds())
	if lockSec < 1 {
		lockSec = 1
	}
	_, _ = tx.ExecContext(qctx, fmt.Sprintf("SET SESSION max_execution_time = %d", stmtMs))
	_, _ = tx.ExecContext(qctx, fmt.Sprintf("SET SESSION innodb_lock_wait_timeout = %d", lockSec))
	_, _ = tx.ExecContext(qctx, fmt.Sprintf("SET SESSION lock_wait_timeout = %d", lockSec))

	rows, err := tx.QueryContext(qctx, sqlText, args...)
	if err != nil {
		return nil, sanitizeErr(err)
	}
	defer rows.Close()

	rawCols, err := rows.Columns()
	if err != nil {
		return nil, sanitizeErr(err)
	}
	cols := uniqueColumnNames(rawCols)
	out := &Result{Columns: cols, Rows: make([]map[string]any, 0, 8)}
	bytesUsed := 0
	for rows.Next() {
		if len(out.Rows) >= maxRows {
			out.Truncated = true
			out.TruncatedReason = fmt.Sprintf("row limit %d", maxRows)
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, sanitizeErr(err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalize(vals[i])
		}
		n, err := rowBytes(row)
		if err != nil {
			return nil, sanitizeErr(err)
		}
		if bytesUsed+n > sqlguard.MaxBytes {
			out.Truncated = true
			out.TruncatedReason = fmt.Sprintf("response size limit %d bytes", sqlguard.MaxBytes)
			break
		}
		bytesUsed += n
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, sanitizeErr(err)
	}
	out.RowCount = len(out.Rows)
	return out, nil
}

func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return t
	}
}

func uniqueColumnNames(names []string) []string {
	used := make(map[string]struct{}, len(names))
	out := make([]string, len(names))
	for i, n := range names {
		if n == "" {
			n = fmt.Sprintf("column_%d", i+1)
		}
		cand := n
		k := 2
		for {
			if _, ok := used[cand]; !ok {
				break
			}
			cand = fmt.Sprintf("%s_%d", n, k)
			k++
		}
		used[cand] = struct{}{}
		out[i] = cand
	}
	return out
}

func rowBytes(row map[string]any) (int, error) {
	b, err := json.Marshal(row)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

var passwordRe = regexp.MustCompile(`(?i)(password|passwd)=[^ \t]+`)

func sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", passwordRe.ReplaceAllString(err.Error(), "$1=***"))
}

func isAuthError(err error) bool {
	var me *mysqldriver.MySQLError
	if errors.As(err, &me) && me != nil {
		switch me.Number {
		case 1044, 1045, 1698, 3159:
			return true
		}
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "access denied") || strings.Contains(s, "no password")
}
