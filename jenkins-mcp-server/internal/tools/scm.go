package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// scmContextTree pulls just enough of each change-set item to identify the
// commit and the files it touched. `kind` lets the per-set header tell git
// from svn/hg when a pipeline checks out from multiple SCMs.
const scmContextTree = "changeSets[kind,items[commitId,timestamp,author[fullName],msg,paths[file,editType]]]," +
	"culprits[fullName]"

// defaultMaxCommits bounds rendering when the caller doesn't pass an explicit
// max_commits. Picked to fit a typical "what changed in this build" answer
// without dumping a 500-commit force-push.
const defaultMaxCommits = 50

// GetSCMContextInput is the schema for get_scm_context.
type GetSCMContextInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path."`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
	MaxCommits  int    `json:"max_commits,omitempty" jsonschema:"Cap on commits returned (default 50). A footer notes if the cap was hit."`
	PathFilter  string `json:"path_filter,omitempty" jsonschema:"Case-insensitive RE2 regex; only commits that touch a matching path are returned."`
}

type scmPath struct {
	File     string `json:"file"`
	EditType string `json:"editType"`
}

type scmAuthor struct {
	FullName string `json:"fullName"`
}

type scmItem struct {
	CommitID  string    `json:"commitId"`
	Timestamp int64     `json:"timestamp"`
	Author    scmAuthor `json:"author"`
	Msg       string    `json:"msg"`
	Paths     []scmPath `json:"paths"`
}

type scmChangeSet struct {
	Kind  string    `json:"kind"`
	Items []scmItem `json:"items"`
}

type scmCulprit struct {
	FullName string `json:"fullName"`
}

type scmBuildAPI struct {
	ChangeSets []scmChangeSet `json:"changeSets"`
	Culprits   []scmCulprit   `json:"culprits"`
}

// GetSCMContext returns the per-commit change history for one build: commit
// id, author, timestamp, message subject, and each commit's touched paths
// with a single-letter edit code (A/M/D). Pipeline builds may produce
// multiple change sets (one per checkout step); they are flattened in order
// with a per-set header.
func (d Deps) GetSCMContext(ctx context.Context, _ *mcp.CallToolRequest, in GetSCMContextInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	maxCommits := in.MaxCommits
	if maxCommits <= 0 {
		maxCommits = defaultMaxCommits
	}
	pathRe, err := compileFilter("path_filter", in.PathFilter)
	if err != nil {
		return nil, nil, err
	}

	buildRef := jenkins.BuildRef(in.BuildNumber)
	apiPath := jenkins.JobAPIPath(in.JobPath) + "/" + buildRef + "/api/json"
	body, err := d.Client.Get(ctx, apiPath, map[string]string{"tree": scmContextTree})
	if err != nil {
		return nil, nil, fmt.Errorf("scm context for %s build %s: %w", in.JobPath, buildRef, err)
	}
	var b scmBuildAPI
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, nil, fmt.Errorf("parse build JSON: %w", err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "SCM context for %s build %s\n", in.JobPath, buildRef)

	if names := culpritNames(b.Culprits); len(names) > 0 {
		fmt.Fprintf(&out, "Culprits: %s\n", strings.Join(names, ", "))
	}

	totalCommits := 0
	for _, cs := range b.ChangeSets {
		totalCommits += len(cs.Items)
	}
	if totalCommits == 0 {
		out.WriteString("\n(no commits in change set)\n")
		return textResult(out.String()), nil, nil
	}

	rendered, matched := 0, 0
	truncated := false
	multiSet := len(b.ChangeSets) > 1
	for csIdx, cs := range b.ChangeSets {
		keep := filterCommitsByPath(cs.Items, pathRe)
		matched += len(keep)
		if len(keep) == 0 {
			continue
		}
		if multiSet {
			kind := cs.Kind
			if kind == "" {
				kind = "scm"
			}
			fmt.Fprintf(&out, "\n— change set %d (%s) — %d commit(s)\n", csIdx+1, kind, len(keep))
		} else {
			out.WriteByte('\n')
		}
		for _, it := range keep {
			if rendered >= maxCommits {
				truncated = true
				break
			}
			renderCommit(&out, it)
			rendered++
		}
		if truncated {
			break
		}
	}

	if pathRe != nil {
		fmt.Fprintf(&out, "\n%d of %d commits matched path_filter %q\n", matched, totalCommits, in.PathFilter)
	}
	if truncated {
		fmt.Fprintf(&out, "(stopped at max_commits=%d — pass max_commits to raise the cap)\n", maxCommits)
	}
	return textResult(out.String()), nil, nil
}

func culpritNames(cs []scmCulprit) []string {
	if len(cs) == 0 {
		return nil
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.FullName != "" {
			names = append(names, c.FullName)
		}
	}
	return names
}

func filterCommitsByPath(items []scmItem, re *regexp.Regexp) []scmItem {
	if re == nil {
		return items
	}
	keep := make([]scmItem, 0, len(items))
	for _, it := range items {
		for _, p := range it.Paths {
			if re.MatchString(p.File) {
				keep = append(keep, it)
				break
			}
		}
	}
	return keep
}

func renderCommit(w *strings.Builder, c scmItem) {
	short := c.CommitID
	switch {
	case short == "":
		short = "(no id)"
	case len(short) > 7:
		short = short[:7]
	}
	author := c.Author.FullName
	if author == "" {
		author = "(unknown)"
	}
	when := "(no timestamp)"
	if c.Timestamp > 0 {
		when = time.UnixMilli(c.Timestamp).UTC().Format("2006-01-02 15:04")
	}
	msg := firstLine(c.Msg)
	fmt.Fprintf(w, "%s %s %s  %q\n", short, author, when, msg)
	for _, p := range c.Paths {
		fmt.Fprintf(w, "  %s  %s\n", editCode(p.EditType), p.File)
	}
}

// firstLine returns s up to (but not including) the first \r or \n. Commit
// messages are often multi-line with a subject + body; we want only the
// subject for the rendered table.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// LastGreenBuildInput is the schema for last_green_build.
type LastGreenBuildInput struct {
	JobPath string `json:"job_path" jsonschema:"Slash-separated job path."`
}

// LastGreenBuild reports the most recent successful build of a job via
// /<job>/lastSuccessfulBuild/api/json. Use as the "start point" for triage
// — pair with changes_since_last_green to see what's landed since.
func (d Deps) LastGreenBuild(ctx context.Context, _ *mcp.CallToolRequest, in LastGreenBuildInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/lastSuccessfulBuild/api/json"
	body, err := d.Client.Get(ctx, path, map[string]string{"tree": "number,url,timestamp"})
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return textResult(fmt.Sprintf("no successful build yet for %s", in.JobPath)), nil, nil
		}
		return nil, nil, fmt.Errorf("last green for %s: %w", in.JobPath, err)
	}
	var b struct {
		Number    int64  `json:"number"`
		URL       string `json:"url"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, nil, fmt.Errorf("parse last green JSON: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Last green build of %s: #%d\n", in.JobPath, b.Number)
	when := "(no timestamp)"
	if b.Timestamp > 0 {
		when = time.UnixMilli(b.Timestamp).UTC().Format("2006-01-02 15:04") + " (UTC)"
	}
	fmt.Fprintf(&out, "  Finished: %s\n", when)
	fmt.Fprintf(&out, "  URL:      %s\n", b.URL)
	return textResult(out.String()), nil, nil
}

// ChangesSinceLastGreenInput is the schema for changes_since_last_green.
type ChangesSinceLastGreenInput struct {
	JobPath    string `json:"job_path" jsonschema:"Slash-separated job path."`
	MaxCommits int    `json:"max_commits,omitempty" jsonschema:"Cap on commits returned (default 100). A footer notes if the cap was hit."`
	PathFilter string `json:"path_filter,omitempty" jsonschema:"Case-insensitive RE2 regex; only commits that touch a matching path are returned."`
}

// changesSinceDefaultMaxCommits bounds rendering when the caller doesn't
// pass an explicit max_commits. Wider than get_scm_context's 50 because the
// union spans multiple builds.
const changesSinceDefaultMaxCommits = 100

// wideWindowThreshold is the number of builds between greens above which
// the footer surfaces a "review carefully" warning — the agent likely
// asked the question on a job that has been red for a long time.
const wideWindowThreshold = 50

// ChangesSinceLastGreen unions commits across every completed build since
// the job's last successful one. The walk follows previousCompletedBuild
// from the latest completed build, skipping aborted/in-progress builds,
// and stops once the pointer hits (or crosses) the last green.
func (d Deps) ChangesSinceLastGreen(ctx context.Context, _ *mcp.CallToolRequest, in ChangesSinceLastGreenInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	maxCommits := in.MaxCommits
	if maxCommits <= 0 {
		maxCommits = changesSinceDefaultMaxCommits
	}
	pathRe, err := compileFilter("path_filter", in.PathFilter)
	if err != nil {
		return nil, nil, err
	}

	jobAPI := jenkins.JobAPIPath(in.JobPath)
	g, ok, err := fetchBuildNumber(ctx, d.Client, jobAPI+"/lastSuccessfulBuild/api/json")
	if err != nil {
		return nil, nil, fmt.Errorf("last green for %s: %w", in.JobPath, err)
	}
	if !ok {
		return textResult(fmt.Sprintf("no successful build yet for %s", in.JobPath)), nil, nil
	}
	c, ok, err := fetchBuildNumber(ctx, d.Client, jobAPI+"/lastCompletedBuild/api/json")
	if err != nil {
		return nil, nil, fmt.Errorf("last completed for %s: %w", in.JobPath, err)
	}
	if !ok {
		return textResult(fmt.Sprintf("no completed build yet for %s", in.JobPath)), nil, nil
	}
	if g == c {
		return textResult(fmt.Sprintf("all green — last completed build #%d is the same as last successful build for %s", c, in.JobPath)), nil, nil
	}

	commits, buildsWalked, err := walkChangesSinceLastGreen(ctx, d.Client, jobAPI, g, c)
	if err != nil {
		return nil, nil, err
	}

	kept := commits
	totalBefore := len(commits)
	if pathRe != nil {
		kept = filterCommitsByPath(commits, pathRe)
	}

	truncated := false
	if len(kept) > maxCommits {
		kept = kept[:maxCommits]
		truncated = true
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%d commits across %d builds since last green #%d (latest: #%d)\n", len(kept), buildsWalked, g, c)
	for _, commit := range kept {
		renderCommit(&out, commit)
	}
	if pathRe != nil {
		fmt.Fprintf(&out, "\n%d of %d commits matched path_filter %q\n", len(kept), totalBefore, in.PathFilter)
	}
	if truncated {
		fmt.Fprintf(&out, "(stopped at max_commits=%d — pass max_commits to raise the cap)\n", maxCommits)
	}
	if c-g > wideWindowThreshold {
		fmt.Fprintf(&out, "(wide window: %d builds between greens — review carefully)\n", c-g)
	}
	return textResult(out.String()), nil, nil
}

// fetchBuildNumber pulls just the build number from one of the job's
// well-known refs (lastSuccessfulBuild, lastCompletedBuild). The bool is
// false when Jenkins returns 404 — Jenkins' way of saying "no such build"
// — so callers can render a hint instead of erroring out.
func fetchBuildNumber(ctx context.Context, c *jenkins.Client, path string) (int64, bool, error) {
	body, err := c.Get(ctx, path, map[string]string{"tree": "number"})
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	var b struct {
		Number int64 `json:"number"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return 0, false, fmt.Errorf("parse build number: %w", err)
	}
	return b.Number, true, nil
}

// walkChangesSinceLastGreen walks completed builds from c down toward
// (but not including) g, following previousCompletedBuild and unioning
// each build's change sets. Commits are deduped by commitId, keeping the
// first sighting's metadata. Builds are visited at most once.
func walkChangesSinceLastGreen(ctx context.Context, client *jenkins.Client, jobAPI string, g, c int64) ([]scmItem, int, error) {
	type buildAPI struct {
		ChangeSets             []scmChangeSet `json:"changeSets"`
		PreviousCompletedBuild *struct {
			Number int64 `json:"number"`
		} `json:"previousCompletedBuild"`
	}

	commits := []scmItem{}
	seen := map[string]bool{}
	visited := map[int64]bool{}
	buildsWalked := 0
	cur := c
	for cur > g {
		if visited[cur] {
			break
		}
		visited[cur] = true
		path := fmt.Sprintf("%s/%d/api/json", jobAPI, cur)
		body, err := client.Get(ctx, path, map[string]string{
			"tree": scmContextTree + ",previousCompletedBuild[number]",
		})
		if err != nil {
			return nil, 0, fmt.Errorf("fetch build %d: %w", cur, err)
		}
		var b buildAPI
		if err := json.Unmarshal(body, &b); err != nil {
			return nil, 0, fmt.Errorf("parse build %d: %w", cur, err)
		}
		buildsWalked++
		for _, cs := range b.ChangeSets {
			for _, it := range cs.Items {
				if seen[it.CommitID] {
					continue
				}
				seen[it.CommitID] = true
				commits = append(commits, it)
			}
		}
		if b.PreviousCompletedBuild == nil || b.PreviousCompletedBuild.Number <= g {
			break
		}
		cur = b.PreviousCompletedBuild.Number
	}
	return commits, buildsWalked, nil
}

// editCode maps Jenkins EditType strings ("add"/"edit"/"delete") to the
// single-letter codes git callers expect (A/M/D). Unknown values fall back
// to the upper-cased first rune so plugin-specific types remain visible.
func editCode(s string) string {
	switch strings.ToLower(s) {
	case "add":
		return "A"
	case "edit", "modify":
		return "M"
	case "delete":
		return "D"
	case "":
		return "?"
	default:
		r, _ := utf8.DecodeRuneInString(s)
		return strings.ToUpper(string(r))
	}
}
