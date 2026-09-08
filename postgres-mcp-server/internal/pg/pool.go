package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"postgres-mcp-server/internal/cred"
	"postgres-mcp-server/internal/sqlguard"
	"postgres-mcp-server/internal/targets"
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
	pools map[string]*pgxpool.Pool
	fp    map[string]string
}

func NewPooler(cfg Config) *Pooler {
	return &Pooler{
		cfg:   cfg.withDefaults(),
		pools: map[string]*pgxpool.Pool{},
		fp:    map[string]string{},
	}
}

func (p *Pooler) Config() Config { return p.cfg }

func (p *Pooler) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, pool := range p.pools {
		pool.Close()
		delete(p.pools, name)
		delete(p.fp, name)
	}
}

func fingerprint(t targets.Target) string {
	return strings.Join([]string{
		t.Name, t.Host, fmt.Sprintf("%d", t.PortOrDefault()),
		t.DBName, t.User, t.SSLModeOrDefault(), t.CredentialRef,
	}, "|")
}

func (p *Pooler) acquire(ctx context.Context, t targets.Target) (*pgxpool.Pool, error) {
	fp := fingerprint(t)
	p.mu.Lock()
	if pool, ok := p.pools[t.Name]; ok && p.fp[t.Name] == fp {
		p.mu.Unlock()
		return pool, nil
	}
	if old, ok := p.pools[t.Name]; ok {
		old.Close()
		delete(p.pools, t.Name)
		delete(p.fp, t.Name)
	}
	p.mu.Unlock()

	password, err := cred.Password(t)
	if err != nil {
		return nil, err
	}

	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(t.User, password),
		Host:   fmt.Sprintf("%s:%d", t.Host, t.PortOrDefault()),
		Path:   "/" + t.DBName,
	}
	q := u.Query()
	q.Set("sslmode", t.SSLModeOrDefault())
	q.Set("connect_timeout", fmt.Sprintf("%d", int(p.cfg.ConnectTimeout.Seconds())))
	q.Set("application_name", "postgres-mcp")
	u.RawQuery = q.Encode()

	pcfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, sanitizeErr(err)
	}
	pcfg.MaxConns = 2
	pcfg.MinConns = 0
	pcfg.MaxConnIdleTime = 2 * time.Minute
	pcfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pcfg.AfterRelease = func(c *pgx.Conn) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := c.Exec(ctx, "DISCARD ALL"); err != nil {
			return false
		}
		return true
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, sanitizeErr(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, sanitizeErr(err)
	}

	p.mu.Lock()
	if existing, ok := p.pools[t.Name]; ok && p.fp[t.Name] == fp {
		p.mu.Unlock()
		pool.Close()
		return existing, nil
	}
	if old, ok := p.pools[t.Name]; ok {
		old.Close()
	}
	p.pools[t.Name] = pool
	p.fp[t.Name] = fp
	p.mu.Unlock()
	return pool, nil
}

// Reap closes pools whose target was deleted, renamed, or whose connection fingerprint changed.
func (p *Pooler) Reap(active []targets.Target) {
	keep := make(map[string]string, len(active))
	for _, t := range active {
		keep[t.Name] = fingerprint(t)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, pool := range p.pools {
		if fp, ok := keep[name]; !ok || p.fp[name] != fp {
			pool.Close()
			delete(p.pools, name)
			delete(p.fp, name)
		}
	}
}

func (p *Pooler) invalidate(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pool, ok := p.pools[name]; ok {
		pool.Close()
		delete(p.pools, name)
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

func (p *Pooler) Query(ctx context.Context, t targets.Target, sql string, args []any, maxRows int) (*Result, error) {
	maxRows = sqlguard.ClampRows(maxRows)
	pool, err := p.acquire(ctx, t)
	if err != nil {
		return nil, err
	}
	res, err := p.queryOnce(ctx, pool, sql, args, maxRows)
	if err != nil && isAuthError(err) {
		p.invalidate(t.Name)
		pool, err2 := p.acquire(ctx, t)
		if err2 != nil {
			return nil, err2
		}
		return p.queryOnce(ctx, pool, sql, args, maxRows)
	}
	return res, err
}

func (p *Pooler) queryOnce(ctx context.Context, pool *pgxpool.Pool, sql string, args []any, maxRows int) (*Result, error) {
	qctx, cancel := context.WithTimeout(ctx, p.cfg.ConnectTimeout+p.cfg.StatementTimeout+2*time.Second)
	defer cancel()

	tx, err := pool.BeginTx(qctx, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, sanitizeErr(err)
	}
	defer func() { _ = tx.Rollback(qctx) }()

	if _, err := tx.Exec(qctx, fmt.Sprintf("SET LOCAL statement_timeout = '%dms'", p.cfg.StatementTimeout.Milliseconds())); err != nil {
		return nil, sanitizeErr(err)
	}
	if _, err := tx.Exec(qctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", p.cfg.LockTimeout.Milliseconds())); err != nil {
		return nil, sanitizeErr(err)
	}

	rows, err := tx.Query(qctx, sql, args...)
	if err != nil {
		return nil, sanitizeErr(err)
	}
	defer rows.Close()

	fds := rows.FieldDescriptions()
	rawCols := make([]string, len(fds))
	for i, fd := range fds {
		rawCols[i] = string(fd.Name)
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
		vals, err := rows.Values()
		if err != nil {
			return nil, sanitizeErr(err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			v := normalize(vals[i])
			row[col] = v
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
	case [16]byte:
		return fmt.Sprintf("%x", t[:])
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

var passwordRe = regexp.MustCompile(`(?i)password=[^ \t]+`)

func sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", passwordRe.ReplaceAllString(err.Error(), "password=***"))
}

func isAuthError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr != nil {
		return pgErr.Code == "28P01" || pgErr.Code == "28000"
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "password authentication failed") || strings.Contains(s, "no password")
}
