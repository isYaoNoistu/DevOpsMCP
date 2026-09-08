package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient builds a Client pointed at the given test server. Tests
// should defer srv.Close() in the caller.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestHTTPError_PreservesMessageFormat(t *testing.T) {
	// The historical error message is asserted on by other tests
	// (queue_test.go) and shows up in operator logs, so the refactor must
	// not change the rendered string.
	e := &HTTPError{
		Method:     http.MethodGet,
		Path:       "/api/json",
		StatusCode: 503,
		Snippet:    "Service Unavailable",
	}
	want := "jenkins /api/json returned HTTP 503: Service Unavailable"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestClientGet_ReturnsHTTPErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not here"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.Get(context.Background(), "/anything", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// errors.As must recover the typed payload.
	var herr *HTTPError
	if !errors.As(err, &herr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if herr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", herr.StatusCode)
	}
	if herr.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", herr.Method)
	}
	if herr.Path != "/anything" {
		t.Errorf("Path = %q, want /anything", herr.Path)
	}
	if !strings.Contains(herr.Snippet, "not here") {
		t.Errorf("Snippet = %q, want to contain %q", herr.Snippet, "not here")
	}
}

func TestIsHTTPStatus(t *testing.T) {
	notFound := &HTTPError{StatusCode: http.StatusNotFound, Path: "/x"}
	wrapped := fmt.Errorf("context: %w", notFound)
	other := errors.New("not an HTTPError")

	cases := []struct {
		name string
		err  error
		code int
		want bool
	}{
		{"direct match", notFound, http.StatusNotFound, true},
		{"wrapped match", wrapped, http.StatusNotFound, true},
		{"status mismatch", notFound, http.StatusInternalServerError, false},
		{"unrelated error", other, http.StatusNotFound, false},
		{"nil error", nil, http.StatusNotFound, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsHTTPStatus(tc.err, tc.code); got != tc.want {
				t.Errorf("IsHTTPStatus(%v, %d) = %v, want %v", tc.err, tc.code, got, tc.want)
			}
		})
	}
}

func TestClientGet_SnippetTruncated(t *testing.T) {
	// Bodies > 300 bytes get truncated with a "..." suffix; the cap is
	// load-bearing in operator logs (no megabyte error lines).
	big := strings.Repeat("x", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.Get(context.Background(), "/x", nil)
	var herr *HTTPError
	if !errors.As(err, &herr) {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if !strings.HasSuffix(herr.Snippet, "...") {
		t.Errorf("expected truncated snippet ending in '...', got len=%d, tail=%q",
			len(herr.Snippet), tail(herr.Snippet, 10))
	}
	if len(herr.Snippet) > 320 {
		t.Errorf("snippet too long: %d bytes", len(herr.Snippet))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestJobAPIPath(t *testing.T) {
	got := JobAPIPath("team/prod/checkout-api")
	want := "/job/team/job/prod/job/checkout-api"
	if got != want {
		t.Errorf("JobAPIPath = %q, want %q", got, want)
	}
	got = JobAPIPath("foo/../bar")
	if strings.Contains(got, "..") {
		t.Errorf("JobAPIPath leaked ..: %q", got)
	}
	if got != "/job/foo/job/_/job/bar" {
		t.Errorf("JobAPIPath traversal = %q", got)
	}
}

func TestClientGet_RefusesCrossHostRedirect(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the off-host server")
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Get(context.Background(), "/api/json", nil)
	if err == nil {
		t.Fatal("expected redirect error")
	}
	if !strings.Contains(err.Error(), "refusing redirect") {
		t.Errorf("expected refusing redirect, got %v", err)
	}
}

