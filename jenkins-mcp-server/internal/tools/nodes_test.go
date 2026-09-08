package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

func newDepsAgainstHandler(t *testing.T, h http.Handler) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

func TestListNodes_Render(t *testing.T) {
	body := apiComputerListing{Computer: []apiComputer{
		{
			DisplayName:    "built-in",
			NumExecutors:   2,
			Idle:           true,
			AssignedLabels: []apiLabel{{Name: "master"}, {Name: "linux"}},
		},
		{
			DisplayName:        "agent-1",
			Offline:            true,
			OfflineCauseReason: "Disconnected by admin",
			NumExecutors:       4,
			AssignedLabels:     []apiLabel{{Name: "build"}},
		},
	}}
	d, srv := newDepsAgainstHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/computer/api/json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	res, _, err := d.ListNodes(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{"built-in", "agent-1", "offline", "Disconnected by admin", "linux,master"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestGetNode_EscapesParens(t *testing.T) {
	var seenURI string
	d, srv := newDepsAgainstHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURI = r.RequestURI
		_ = json.NewEncoder(w).Encode(apiComputer{
			DisplayName:  "controller",
			NumExecutors: 1,
			Executors:    []apiExecutor{{Idle: true}},
			MonitorData: map[string]any{
				"hudson.node_monitors.ResponseTimeMonitor": map[string]any{"average": 12},
			},
		})
	}))
	defer srv.Close()

	res, _, err := d.GetNode(context.Background(), nil, GetNodeInput{Name: "(built-in)"})
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !strings.Contains(seenURI, "%28built-in%29") {
		t.Errorf("expected parenthesized node name to be URL-escaped, got URI %q", seenURI)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "1 total, 1 idle") {
		t.Errorf("expected executor counts in output, got:\n%s", out)
	}
	if !strings.Contains(out, "ResponseTimeMonitor") {
		t.Errorf("expected monitor data in output, got:\n%s", out)
	}
}

func TestGetNode_RequiresName(t *testing.T) {
	d := Deps{}
	_, _, err := d.GetNode(context.Background(), nil, GetNodeInput{})
	if err == nil {
		t.Fatal("expected error when name is empty")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter passes through", "hello", 30, "hello"},
		{"exact length passes through", "abcde", 5, "abcde"},
		{"longer ASCII truncates with ellipsis", strings.Repeat("a", 40), 30,
			strings.Repeat("a", 29) + "…"},
		{"multi-byte content counted by rune, not byte", "日本語テスト", 4, "日本語…"},
		{"multi-byte under limit passes through", "日本語", 5, "日本語"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
	// Helper: count runes, not bytes — that's the whole point of the function.
	runeWidth := func(s string) int {
		n := 0
		for range s {
			n++
		}
		return n
	}

	cases := []struct {
		name  string
		in    string
		width int
		want  int // expected rune width of result
	}{
		{"shorter pads to width", "abc", 10, 10},
		{"exact width unchanged", "abcde", 5, 5},
		{"longer than width unchanged (no clip)", "abcdefghij", 5, 10},
		{"multi-byte input padded by rune count", "日本語", 6, 6},
		{"truncated input ending in ellipsis pads correctly",
			truncate(strings.Repeat("a", 40), 30), 30, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := padRight(tc.in, tc.width)
			if w := runeWidth(got); w != tc.want {
				t.Errorf("padRight(%q, %d) rune width = %d, want %d (result %q)",
					tc.in, tc.width, w, tc.want, got)
			}
		})
	}
}

func TestTruncatePadComposition_FixesColumnAlignment(t *testing.T) {
	// Regression for the issue this fix closes: a 40-byte ASCII name
	// truncated to 30 produced 32 bytes (29 ASCII + 3-byte "…"); when
	// formatted with %-30s, the column got no padding because fmt counts
	// bytes. padRight(truncate(...), 30) must yield exactly 30 runes.
	name := strings.Repeat("a", 40)
	cell := padRight(truncate(name, 30), 30)
	runes := 0
	for range cell {
		runes++
	}
	if runes != 30 {
		t.Errorf("composed cell rune width = %d, want 30", runes)
	}
}
