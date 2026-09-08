package jenkins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultMaxCacheBytes is the soft cap applied by Cache.Fetch when no override
// is configured. 1 GiB strikes a balance between holding enough finished-build
// logs to be useful and not surprising operators on smaller machines.
const DefaultMaxCacheBytes int64 = 1 * 1024 * 1024 * 1024

// finishedMarker matches the last line Jenkins emits when a build is done.
// We only persist a log to disk after seeing this marker, so a cache hit can
// never serve a partial / mid-flight log.
var finishedMarker = regexp.MustCompile(`(?m)^Finished: (SUCCESS|FAILURE|ABORTED|UNSTABLE|NOT_BUILT)\s*$`)

var unsafeSlugChar = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// ConsoleCache serves console logs from disk when the build has finished, and
// transparently falls back to the network when it has not.
//
// The cache is LRU by mtime, capped at MaxBytes. Writes are serialized.
type ConsoleCache struct {
	Client   *Client
	Dir      string
	MaxBytes int64

	mu sync.Mutex
}

// NewConsoleCache constructs a cache rooted at dir. The directory is created
// if missing.
func NewConsoleCache(c *Client, dir string) (*ConsoleCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %s: %w", dir, err)
	}
	return &ConsoleCache{
		Client:   c,
		Dir:      dir,
		MaxBytes: DefaultMaxCacheBytes,
	}, nil
}

// Path returns the deterministic on-disk path for a finished build's log.
// The buildTimestampMs fragment ensures that a re-run reusing the same build
// number (operator-deleted-then-replayed slot, "keep forever" toggled off,
// etc.) maps to a distinct filename rather than silently colliding with the
// previous body. The slug is sanitized to a flat filename so directory
// traversal cannot escape the cache root.
func (cc *ConsoleCache) Path(jobPath string, buildNumber, buildTimestampMs int64) string {
	slug := strings.ReplaceAll(strings.Trim(jobPath, "/"), "/", "__")
	slug = unsafeSlugChar.ReplaceAllString(slug, "_")
	if len(slug) > 180 {
		slug = slug[:180]
	}
	return filepath.Join(cc.Dir, fmt.Sprintf("%s-%d-%d.log", slug, buildNumber, buildTimestampMs))
}

// Fetch returns the full consoleText body for a build and, if cached on disk,
// the cache path.
//
// Behavior:
//   - buildNumber == 0 (lastBuild) is never cached — its identity is unstable.
//   - For positive build numbers, the build's timestamp is probed via a tiny
//     `api/json?tree=timestamp` call and folded into the cache filename. This
//     guarantees that a same-number replay (operator-deleted slot, "keep
//     forever" toggle, etc.) misses the cache cleanly instead of serving the
//     prior body. If the probe fails, the body is served from the network
//     without being cached — better to refetch than to risk a poisoned cache.
//   - A network response is written to disk only if the body contains the
//     `Finished: <result>` marker, guaranteeing the cached file is complete.
func (cc *ConsoleCache) Fetch(ctx context.Context, jobPath string, buildNumber int64) (body []byte, cachePath string, err error) {
	var (
		ts      int64
		probeOK bool
	)
	if buildNumber > 0 {
		var probeErr error
		ts, probeErr = cc.buildTimestamp(ctx, jobPath, buildNumber)
		if probeErr != nil {
			debugf("cache identity probe failed for %s build %d: %v", jobPath, buildNumber, probeErr)
		} else {
			probeOK = true
			cachePath = cc.Path(jobPath, buildNumber, ts)
			if b, e := os.ReadFile(cachePath); e == nil {
				now := time.Now()
				_ = os.Chtimes(cachePath, now, now)
				debugf("cache hit  %s (%d bytes)", cachePath, len(b))
				return b, cachePath, nil
			}
			debugf("cache miss %s", cachePath)
			cachePath = ""
		}
	}

	url := JobAPIPath(jobPath) + "/" + BuildRef(buildNumber) + "/consoleText"
	body, err = cc.Client.Get(ctx, url, nil)
	if err != nil {
		return nil, "", err
	}

	if probeOK && finishedMarker.Match(tailBytes(body, 256)) {
		path := cc.Path(jobPath, buildNumber, ts)
		cc.mu.Lock()
		defer cc.mu.Unlock()
		if writeErr := os.WriteFile(path, body, 0o644); writeErr == nil {
			cachePath = path
			debugf("cache write %s (%d bytes)", path, len(body))
			cc.evictIfOverCap()
		} else {
			debugf("cache write failed %s: %v", path, writeErr)
		}
	} else if probeOK {
		debugf("cache skip %s (build not finished)", cc.Path(jobPath, buildNumber, ts))
	}
	return body, cachePath, nil
}

// buildTimestamp probes the build's start timestamp (ms since epoch) via the
// minimal `tree=timestamp` selector. Used to scope the cache key to a specific
// run rather than just the build number — the latter can be reused after a
// delete-and-replay.
func (cc *ConsoleCache) buildTimestamp(ctx context.Context, jobPath string, buildNumber int64) (int64, error) {
	path := JobAPIPath(jobPath) + "/" + BuildRef(buildNumber) + "/api/json"
	body, err := cc.Client.Get(ctx, path, map[string]string{"tree": "timestamp"})
	if err != nil {
		return 0, err
	}
	var payload struct {
		Timestamp int64 `json:"timestamp"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("parse build timestamp from %s: %w", path, err)
	}
	return payload.Timestamp, nil
}

// evictIfOverCap deletes oldest-mtime files until total cache size <= MaxBytes.
// Caller must hold cc.mu.
func (cc *ConsoleCache) evictIfOverCap() {
	entries, err := os.ReadDir(cc.Dir)
	if err != nil {
		return
	}
	type item struct {
		path  string
		size  int64
		mtime time.Time
	}
	items := make([]item, 0, len(entries))
	var total int64
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		items = append(items, item{filepath.Join(cc.Dir, de.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= cc.MaxBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime.Before(items[j].mtime) })
	startTotal := total
	evicted := 0
	for _, it := range items {
		if total <= cc.MaxBytes {
			break
		}
		if err := os.Remove(it.path); err == nil {
			debugf("cache evict %s (%d bytes, mtime %s)", it.path, it.size, it.mtime.Format(time.RFC3339))
			total -= it.size
			evicted++
		}
	}
	if evicted > 0 {
		debugf("cache eviction freed %d bytes across %d files (%d → %d, cap %d)",
			startTotal-total, evicted, startTotal, total, cc.MaxBytes)
	}
}

func tailBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}
