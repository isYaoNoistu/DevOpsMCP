package jenkins

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// jenkinsFixture wires a fake Jenkins that serves the build-timestamp probe
// and the console body, with per-build state the test can swap between
// requests to simulate a same-number replay.
type jenkinsFixture struct {
	// Indexed by build number. If empty, that build returns 404.
	timestamps map[int64]int64
	bodies     map[int64]string

	// Counters so tests can assert how many round trips happened.
	timestampHits int
	consoleHits   int
}

func (f *jenkinsFixture) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path shape: /job/<jobPath segments>/<buildRef>/{api/json,consoleText}
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/api/json"):
			f.timestampHits++
			// Extract build number from the path between the last "/job/" segment chain.
			n := buildNumberFromPath(t, path, "/api/json")
			ts, ok := f.timestamps[n]
			if !ok {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"timestamp": %d}`, ts)
		case strings.HasSuffix(path, "/consoleText"):
			f.consoleHits++
			n := buildNumberFromPath(t, path, "/consoleText")
			body, ok := f.bodies[n]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	})
}

// buildNumberFromPath extracts the build number from a URL like
// "/job/some-job/7/api/json" by stripping the suffix and taking the last
// path segment.
func buildNumberFromPath(t *testing.T, urlPath, suffix string) int64 {
	t.Helper()
	trimmed := strings.TrimSuffix(urlPath, suffix)
	parts := strings.Split(trimmed, "/")
	last := parts[len(parts)-1]
	var n int64
	if _, err := fmt.Sscanf(last, "%d", &n); err != nil {
		t.Fatalf("could not parse build number from %q: %v", urlPath, err)
	}
	return n
}

func newCacheWithFixture(t *testing.T, fx *jenkinsFixture) (*ConsoleCache, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(fx.handler(t))
	c := newTestClient(t, srv)
	cc, err := NewConsoleCache(c, t.TempDir())
	if err != nil {
		t.Fatalf("NewConsoleCache: %v", err)
	}
	return cc, srv
}

// withFinishedMarker appends the Finished: line that the cache requires to
// persist a body to disk.
func withFinishedMarker(s string) string { return s + "\nFinished: SUCCESS\n" }

func TestConsoleCache_SameBuildNumberDifferentTimestamps(t *testing.T) {
	// The bug: deleting a build via the UI and replaying #7 yields a new
	// body under the same (job, buildNumber). The old cache key collided
	// and served the stale body.
	fx := &jenkinsFixture{
		timestamps: map[int64]int64{7: 1_000_000},
		bodies:     map[int64]string{7: withFinishedMarker("first build")},
	}
	cc, srv := newCacheWithFixture(t, fx)
	defer srv.Close()
	ctx := context.Background()

	body1, path1, err := cc.Fetch(ctx, "some-job", 7)
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if !strings.Contains(string(body1), "first build") {
		t.Fatalf("first body = %q, want to contain 'first build'", body1)
	}
	if path1 == "" {
		t.Fatal("first Fetch returned empty cachePath; expected the finished body to be cached")
	}

	// Simulate the operator deleting build #7 and replaying with new content.
	fx.timestamps[7] = 2_000_000
	fx.bodies[7] = withFinishedMarker("second build")

	body2, path2, err := cc.Fetch(ctx, "some-job", 7)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if strings.Contains(string(body2), "first build") {
		t.Fatalf("second Fetch served stale body: %q", body2)
	}
	if !strings.Contains(string(body2), "second build") {
		t.Fatalf("second body = %q, want to contain 'second build'", body2)
	}
	if path1 == path2 {
		t.Errorf("expected distinct cache paths for distinct timestamps; both were %q", path1)
	}
}

func TestConsoleCache_SameTimestampHitsCache(t *testing.T) {
	// Same (job, build, timestamp) on the second Fetch must short-circuit
	// the consoleText round trip — that's the whole point of the cache.
	fx := &jenkinsFixture{
		timestamps: map[int64]int64{7: 1_000_000},
		bodies:     map[int64]string{7: withFinishedMarker("only build")},
	}
	cc, srv := newCacheWithFixture(t, fx)
	defer srv.Close()
	ctx := context.Background()

	if _, _, err := cc.Fetch(ctx, "some-job", 7); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if _, _, err := cc.Fetch(ctx, "some-job", 7); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if fx.consoleHits != 1 {
		t.Errorf("consoleText hits = %d, want 1 (second Fetch should have hit cache)", fx.consoleHits)
	}
}

func TestConsoleCache_TimestampProbeFailureFallsThroughToNetwork(t *testing.T) {
	// If we can't probe the timestamp (transient 5xx on /api/json), we
	// must not error — we fall through and serve fresh from the network
	// without caching. Cache safety relies on stable identity; absent
	// that, we'd rather re-fetch than risk poisoning the cache.
	probeFailing := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/json"):
			if probeFailing {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `{"timestamp": 9999}`)
		case strings.HasSuffix(r.URL.Path, "/consoleText"):
			_, _ = w.Write([]byte(withFinishedMarker("body served")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	cc, err := NewConsoleCache(c, t.TempDir())
	if err != nil {
		t.Fatalf("NewConsoleCache: %v", err)
	}

	body, path, err := cc.Fetch(context.Background(), "some-job", 7)
	if err != nil {
		t.Fatalf("Fetch with failing probe: %v", err)
	}
	if !strings.Contains(string(body), "body served") {
		t.Errorf("body = %q, want to contain 'body served'", body)
	}
	if path != "" {
		t.Errorf("cachePath = %q, want empty when timestamp probe failed", path)
	}
}

func TestConsoleCachePath_DistinctTimestampsYieldDistinctPaths(t *testing.T) {
	cc := &ConsoleCache{Dir: filepath.Join(t.TempDir(), "x")}
	a := cc.Path("job", 7, 1000)
	b := cc.Path("job", 7, 2000)
	if a == b {
		t.Fatalf("expected distinct paths, both = %q", a)
	}
	if !strings.HasSuffix(a, ".log") || !strings.HasSuffix(b, ".log") {
		t.Errorf("expected .log suffix, got %q and %q", a, b)
	}
}
