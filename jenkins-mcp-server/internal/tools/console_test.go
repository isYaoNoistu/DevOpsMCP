package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// tailFixture seeds one progressiveText response. body is what the server
// returns starting at the requested `start` offset; textSize is what the
// X-Text-Size header reports; moreData is X-More-Data.
type tailFixture struct {
	body     string
	textSize int64
	moreData bool
}

func newTailDeps(t *testing.T, jobPath string, build int64, f tailFixture) (Deps, *httptest.Server) {
	t.Helper()
	path := jenkins.JobAPIPath(jobPath) + "/" + jenkins.BuildRef(build) + "/logText/progressiveText"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
		w.Header().Set("X-Text-Size", strconv.FormatInt(f.textSize, 10))
		if f.moreData {
			w.Header().Set("X-More-Data", "true")
		}
		// Body is the slice from `start` to end of the seeded buffer. The
		// fixture's body represents the wire body for start=0; later starts
		// just slice into it (mimicking real Jenkins progressive semantics).
		if start >= int64(len(f.body)) {
			return
		}
		_, _ = w.Write([]byte(f.body[start:]))
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli, Cache: &jenkins.ConsoleCache{Dir: "/tmp/x"}}, srv
}

func TestTailRunningBuild_HappyPathRunningBuild(t *testing.T) {
	body := "build log line 1\nbuild log line 2\n"
	d, srv := newTailDeps(t, "svc", 42, tailFixture{
		body:     body,
		textSize: int64(len(body)),
		moreData: true,
	})
	defer srv.Close()

	res, _, err := d.TailRunningBuild(context.Background(), nil, TailRunningBuildInput{
		JobPath: "svc", BuildNumber: 42,
	})
	if err != nil {
		t.Fatalf("TailRunningBuild: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"build log line 1",
		"build log line 2",
		"more=true",
		"Next since_byte=" + strconv.Itoa(len(body)),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestTailRunningBuild_BuildFinishedPointsAtConsoleTools(t *testing.T) {
	body := "final chunk\n"
	d, srv := newTailDeps(t, "svc", 42, tailFixture{
		body:     body,
		textSize: int64(len(body)),
		moreData: false,
	})
	defer srv.Close()

	res, _, err := d.TailRunningBuild(context.Background(), nil, TailRunningBuildInput{
		JobPath: "svc", BuildNumber: 42,
	})
	if err != nil {
		t.Fatalf("TailRunningBuild: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"final chunk",
		"more=false",
		"build finished",
		"get_console_log",
		"search_console_log",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestTailRunningBuild_SinceBytePaginates(t *testing.T) {
	// Wire body is the full log starting at byte 0 (fixture serves the
	// slice from `start` to end). With since_byte=20, the agent only sees
	// what came after byte 20.
	body := strings.Repeat("X", 100) + "PAGINATED_TAIL\n"
	d, srv := newTailDeps(t, "svc", 0, tailFixture{
		body:     body,
		textSize: int64(len(body)),
		moreData: false,
	})
	defer srv.Close()

	res, _, err := d.TailRunningBuild(context.Background(), nil, TailRunningBuildInput{
		JobPath: "svc", SinceByte: 100,
	})
	if err != nil {
		t.Fatalf("TailRunningBuild: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "PAGINATED_TAIL") {
		t.Errorf("expected tail after offset 100, got:\n%s", out)
	}
	if strings.Contains(out, "XXXXX") {
		t.Errorf("did not expect prefix bytes before offset 100, got:\n%s", out)
	}
	if !strings.Contains(out, "bytes 100..") {
		t.Errorf("expected footer to report bytes 100.., got:\n%s", out)
	}
}

func TestTailRunningBuild_MaxBytesTruncates(t *testing.T) {
	body := strings.Repeat("X", 200)
	d, srv := newTailDeps(t, "svc", 0, tailFixture{
		body:     body,
		textSize: int64(len(body)),
		moreData: true,
	})
	defer srv.Close()

	res, _, err := d.TailRunningBuild(context.Background(), nil, TailRunningBuildInput{
		JobPath: "svc", MaxBytes: 50,
	})
	if err != nil {
		t.Fatalf("TailRunningBuild: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "more=true") {
		t.Errorf("expected more=true on truncation, got:\n%s", out)
	}
	if !strings.Contains(out, "Next since_byte=50") {
		t.Errorf("expected next offset = since_byte + max_bytes, got:\n%s", out)
	}
	if strings.Count(out, "X") < 50 || strings.Count(out, "X") > 60 {
		t.Errorf("expected ~50 X's in body, got %d:\n%s", strings.Count(out, "X"), out)
	}
}

func TestTailRunningBuild_NoNewBytesYet(t *testing.T) {
	body := "earlier content"
	d, srv := newTailDeps(t, "svc", 0, tailFixture{
		body:     body,
		textSize: int64(len(body)),
		moreData: true,
	})
	defer srv.Close()

	res, _, err := d.TailRunningBuild(context.Background(), nil, TailRunningBuildInput{
		JobPath: "svc", SinceByte: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("TailRunningBuild: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "no new bytes") {
		t.Errorf("expected 'no new bytes' message, got:\n%s", out)
	}
	if !strings.Contains(out, "more=true") {
		t.Errorf("expected more=true (build still running), got:\n%s", out)
	}
}

func TestTailRunningBuild_MissingJobPath(t *testing.T) {
	d := Deps{}
	if _, _, err := d.TailRunningBuild(context.Background(), nil, TailRunningBuildInput{}); err == nil {
		t.Fatal("expected error for empty job_path")
	}
}

func TestTailRunningBuild_NegativeSinceByteErrors(t *testing.T) {
	d := Deps{}
	_, _, err := d.TailRunningBuild(context.Background(), nil, TailRunningBuildInput{
		JobPath: "svc", SinceByte: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative since_byte")
	}
}
