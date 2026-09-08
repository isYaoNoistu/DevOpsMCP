// Package jenkins is a small REST client for Jenkins that only talks to the
// single host configured at startup.
package jenkins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HTTPError is the error type returned by Client when Jenkins responds with a
// non-2xx status. Callers can recover the status code and a short body
// snippet via errors.As, or use IsHTTPStatus for the common
// "is this a 404?" branch.
type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
	Snippet    string
}

// Error preserves the historical message format ("jenkins <path> returned
// HTTP <code>: <snippet>") so existing assertions and operator-facing logs
// keep working after the typed-error refactor.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("jenkins %s returned HTTP %d: %s", e.Path, e.StatusCode, e.Snippet)
}

// IsHTTPStatus reports whether err is (or wraps) an *HTTPError with the
// given status code. Use this at call sites that need to special-case a
// specific Jenkins response (404 from /crumbIssuer, /testReport, /wfapi).
func IsHTTPStatus(err error, status int) bool {
	var herr *HTTPError
	return errors.As(err, &herr) && herr.StatusCode == status
}

// DefaultTimeout is the HTTP timeout used when Config.Timeout is zero.
const DefaultTimeout = 90 * time.Second

// debugEnabled is sampled once at process start. When set, debugf emits a
// single stderr line for each outbound Jenkins request, cache hit, and cache
// write. Stderr only — stdout is reserved for MCP protocol frames.
var debugEnabled = os.Getenv("JENKINS_MCP_DEBUG") != ""

// debugf prints a single stderr line when JENKINS_MCP_DEBUG is set. Callers
// must never pass response bodies or request headers — the Authorization
// header carries the Jenkins API token, and bodies can be megabytes.
func debugf(format string, args ...any) {
	if !debugEnabled {
		return
	}
	log.Printf("jenkins: "+format, args...)
}

// Client is a Jenkins REST client with HTTP Basic auth.
//
// All requests target a single base URL. There is no host switching at request
// time — an MCP process talks to exactly one Jenkins.
type Client struct {
	baseURL string
	user    string
	token   string
	http    *http.Client
}

// Config carries the inputs needed to construct a Client.
type Config struct {
	BaseURL string
	User    string
	Token   string
	Timeout time.Duration
}

// Timeout returns the HTTP client timeout the Jenkins client is using.
// Useful for tools that want to surface the effective timeout (e.g.
// health_check) without snapshotting it elsewhere at startup.
func (c *Client) Timeout() time.Duration { return c.http.Timeout }

// NewClient validates the configuration and returns a ready-to-use Client.
//
// BaseURL trailing slashes are trimmed so callers can join paths starting with
// "/". A zero Timeout falls back to DefaultTimeout.
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.User == "" || cfg.Token == "" {
		return nil, fmt.Errorf("jenkins: BaseURL, User, and Token are all required")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("jenkins: invalid BaseURL")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: base,
		user:    cfg.User,
		token:   cfg.Token,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("stopped after 5 redirects")
				}
				if req.URL.Scheme != parsed.Scheme || req.URL.Host != parsed.Host {
					return fmt.Errorf("refusing redirect off Jenkins host to %s://%s", req.URL.Scheme, req.URL.Host)
				}
				return nil
			},
		},
	}, nil
}

// Get issues an authenticated GET to baseURL+path with optional query params
// and returns the response body. Non-2xx responses are returned as errors with
// a short body snippet for context.
func (c *Client) Get(ctx context.Context, path string, query map[string]string) ([]byte, error) {
	body, _, err := c.doGet(ctx, path, query)
	return body, err
}

// GetWithHeaders is like Get but also returns the response headers. Use it
// for endpoints whose useful information lives in a header (e.g. the
// X-Jenkins version banner on /api/json).
func (c *Client) GetWithHeaders(ctx context.Context, path string, query map[string]string) ([]byte, http.Header, error) {
	return c.doGet(ctx, path, query)
}

func (c *Client) doGet(ctx context.Context, path string, query map[string]string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, nil, err
	}
	if len(query) > 0 {
		q := req.URL.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	req.SetBasicAuth(c.user, c.token)
	debugf("req  GET %s", req.URL.Path)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		debugf("err  GET %s after %s: %v", req.URL.Path, time.Since(start), err)
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		debugf("err  GET %s read body after %s: %v", req.URL.Path, time.Since(start), err)
		return nil, nil, err
	}
	debugf("resp %d GET %s in %s (%d bytes)", resp.StatusCode, req.URL.Path, time.Since(start), len(body))
	if resp.StatusCode/100 != 2 {
		return nil, nil, &HTTPError{
			Method:     http.MethodGet,
			Path:       req.URL.Path,
			StatusCode: resp.StatusCode,
			Snippet:    snippetOf(body),
		}
	}
	return body, resp.Header, nil
}

// snippetOf returns a short body snippet suitable for error messages.
// Long bodies are truncated; the cap mirrors what the previous inline
// formatter used.
func snippetOf(body []byte) string {
	const max = 300
	if len(body) <= max {
		return string(body)
	}
	return string(body[:max]) + "..."
}

// JobAPIPath converts a slash-separated job path like
// "folder/subfolder/job-name" into Jenkins' nested form
// "/job/folder/job/subfolder/job/job-name".
// "." / ".." segments are replaced so they cannot walk off /job/.
func JobAPIPath(jobPath string) string {
	var sb strings.Builder
	for _, p := range strings.Split(strings.Trim(jobPath, "/"), "/") {
		if p == "" {
			continue
		}
		if p == "." || p == ".." {
			p = "_"
		}
		sb.WriteString("/job/")
		sb.WriteString(url.PathEscape(p))
	}
	return sb.String()
}

// BuildRef returns "lastBuild" for n<=0, otherwise the decimal build number.
func BuildRef(n int64) string {
	if n <= 0 {
		return "lastBuild"
	}
	return fmt.Sprintf("%d", n)
}
