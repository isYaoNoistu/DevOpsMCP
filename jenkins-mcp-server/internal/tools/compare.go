package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// compareBuildTree pulls the diff inputs from each build in one call:
// result, duration, parameters, and the change sets reused from scm.go.
const compareBuildTree = "result,duration," +
	"actions[parameters[name,value]]," +
	"changeSets[kind,items[commitId,timestamp,author[fullName],msg]]"

// compareTestsTree keeps the test-case payload to identity + status —
// stack traces are not needed for a diff and would explode on large suites.
// Counts (failCount/passCount/skipCount) are not requested because the
// renderer reads only the suites payload.
const compareTestsTree = "suites[cases[className,name,status]]"

// compareTestsBucketCap caps how many test names render per bucket. The
// counts above the list are exact; only the printed list is truncated.
const compareTestsBucketCap = 100

// CompareBuildsInput is the schema for compare_builds.
type CompareBuildsInput struct {
	JobPath      string `json:"job_path" jsonschema:"Slash-separated job path."`
	BuildA       int64  `json:"build_a" jsonschema:"Older / baseline build number. Must be > 0; lastBuild is not accepted."`
	BuildB       int64  `json:"build_b" jsonschema:"Newer / candidate build number. Must be > 0 and != build_a."`
	IncludeTests *bool  `json:"include_tests,omitempty" jsonschema:"Include the per-test pass/fail diff. Default true. Set false to skip the testReport call on large suites."`
}

type compareParam struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type compareAction struct {
	Parameters []compareParam `json:"parameters"`
}

type compareBuildAPI struct {
	Result     string          `json:"result"`
	Duration   int64           `json:"duration"`
	Actions    []compareAction `json:"actions"`
	ChangeSets []scmChangeSet  `json:"changeSets"`
}

type compareSnapshot struct {
	BuildNumber int64
	Info        compareBuildAPI
	Stages      *wfapiDescribe // nil = not a pipeline build (HTTP 404)
	Tests       *junitReport   // nil = no JUnit publisher (HTTP 404) or include_tests=false
}

// CompareBuilds diffs two builds across header, parameters, SCM commits,
// pipeline stages, and JUnit tests. See docs/TOOLS.md for the output shape.
func (d Deps) CompareBuilds(ctx context.Context, _ *mcp.CallToolRequest, in CompareBuildsInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	if in.BuildA <= 0 || in.BuildB <= 0 {
		return nil, nil, fmt.Errorf("build_a and build_b must both be > 0 (lastBuild is not accepted)")
	}
	if in.BuildA == in.BuildB {
		return nil, nil, fmt.Errorf("build_a and build_b must be different")
	}

	includeTests := true
	if in.IncludeTests != nil {
		includeTests = *in.IncludeTests
	}

	// Load A and B in parallel — each snapshot is 1–3 sequential HTTP
	// calls (info + stages + tests), so two goroutines roughly halve wall
	// time. If one side errors, the other's in-flight calls share the
	// caller's ctx and complete naturally; the wasted work is bounded.
	var (
		snapA, snapB *compareSnapshot
		errA, errB   error
		wg           sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		snapA, errA = d.loadCompareSnapshot(ctx, in.JobPath, in.BuildA, includeTests)
	}()
	go func() {
		defer wg.Done()
		snapB, errB = d.loadCompareSnapshot(ctx, in.JobPath, in.BuildB, includeTests)
	}()
	wg.Wait()
	if errA != nil {
		return nil, nil, errA
	}
	if errB != nil {
		return nil, nil, errB
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Compare %s builds %d → %d\n\n", in.JobPath, in.BuildA, in.BuildB)
	renderHeaderDiff(&out, snapA, snapB)
	renderParamsDiff(&out, snapA, snapB)
	renderSCMDiff(&out, snapA, snapB)
	renderStagesDiff(&out, snapA, snapB)
	if includeTests {
		renderTestsDiff(&out, snapA, snapB)
	}

	return textResult(out.String()), nil, nil
}

// loadCompareSnapshot fetches one build's diff inputs. Stage and test 404s
// are recorded as nil rather than errors — the renderer prints a section
// hint instead of failing the whole comparison.
func (d Deps) loadCompareSnapshot(ctx context.Context, jobPath string, build int64, includeTests bool) (*compareSnapshot, error) {
	snap := &compareSnapshot{BuildNumber: build}
	buildPath := jenkins.JobAPIPath(jobPath) + "/" + jenkins.BuildRef(build)

	body, err := d.Client.Get(ctx, buildPath+"/api/json", map[string]string{"tree": compareBuildTree})
	if err != nil {
		return nil, fmt.Errorf("build %d info: %w", build, err)
	}
	if err := json.Unmarshal(body, &snap.Info); err != nil {
		return nil, fmt.Errorf("parse build %d JSON: %w", build, err)
	}

	body, err = d.Client.Get(ctx, buildPath+"/wfapi/describe", nil)
	if err == nil {
		var desc wfapiDescribe
		if err := json.Unmarshal(body, &desc); err != nil {
			return nil, fmt.Errorf("parse build %d stages: %w", build, err)
		}
		snap.Stages = &desc
	} else if !jenkins.IsHTTPStatus(err, http.StatusNotFound) {
		return nil, fmt.Errorf("build %d stages: %w", build, err)
	}

	if includeTests {
		body, err = d.Client.Get(ctx, buildPath+"/testReport/api/json", map[string]string{"tree": compareTestsTree})
		if err == nil {
			var rep junitReport
			if err := json.Unmarshal(body, &rep); err != nil {
				return nil, fmt.Errorf("parse build %d tests: %w", build, err)
			}
			snap.Tests = &rep
		} else if !jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return nil, fmt.Errorf("build %d tests: %w", build, err)
		}
	}

	return snap, nil
}

func renderHeaderDiff(w *strings.Builder, a, b *compareSnapshot) {
	fmt.Fprintf(w, "Result:   %s → %s\n", resultOrRunning(a.Info.Result), resultOrRunning(b.Info.Result))

	aDur := formatBuildDuration(a.Info.Duration)
	bDur := formatBuildDuration(b.Info.Duration)
	delta := b.Info.Duration - a.Info.Duration
	sign := "+"
	if delta < 0 {
		sign = "-"
		delta = -delta
	}
	fmt.Fprintf(w, "Duration: %s → %s (Δ %s%s)\n\n",
		aDur, bDur, sign, (time.Duration(delta) * time.Millisecond).Truncate(time.Second))
}

func resultOrRunning(s string) string {
	if s == "" {
		return "(in progress)"
	}
	return s
}

func renderParamsDiff(w *strings.Builder, a, b *compareSnapshot) {
	paramsA := collectParams(a.Info.Actions)
	paramsB := collectParams(b.Info.Actions)

	var added, removed, changed []string
	for k, v := range paramsB {
		if _, ok := paramsA[k]; !ok {
			added = append(added, fmt.Sprintf("  + %s=%s", k, paramString(v)))
		}
	}
	for k, v := range paramsA {
		if _, ok := paramsB[k]; !ok {
			removed = append(removed, fmt.Sprintf("  - %s=%s", k, paramString(v)))
		}
	}
	for k, vA := range paramsA {
		vB, ok := paramsB[k]
		if !ok {
			continue
		}
		if paramString(vA) != paramString(vB) {
			changed = append(changed, fmt.Sprintf("  ~ %s: %s → %s", k, paramString(vA), paramString(vB)))
		}
	}

	if len(added)+len(removed)+len(changed) == 0 {
		w.WriteString("Parameters: (no diff)\n\n")
		return
	}
	sort.Strings(removed)
	sort.Strings(added)
	sort.Strings(changed)
	w.WriteString("Parameters:\n")
	for _, l := range removed {
		w.WriteString(l + "\n")
	}
	for _, l := range added {
		w.WriteString(l + "\n")
	}
	for _, l := range changed {
		w.WriteString(l + "\n")
	}
	w.WriteString("\n")
}

func collectParams(actions []compareAction) map[string]any {
	m := map[string]any{}
	for _, a := range actions {
		for _, p := range a.Parameters {
			if p.Name != "" {
				m[p.Name] = p.Value
			}
		}
	}
	return m
}

// paramString renders a parameter value for the diff. JSON's unmarshal into
// `any` yields float64 for numbers; we collapse to %v but strip the trailing
// ".0" on integral floats so "X=3" doesn't render as "X=3.0".
func paramString(v any) string {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%v", v)
}

func renderSCMDiff(w *strings.Builder, a, b *compareSnapshot) {
	idsA := map[string]bool{}
	for _, cs := range a.Info.ChangeSets {
		for _, c := range cs.Items {
			if c.CommitID != "" {
				idsA[c.CommitID] = true
			}
		}
	}

	var newCommits []scmItem
	for _, cs := range b.Info.ChangeSets {
		for _, c := range cs.Items {
			if c.CommitID == "" || !idsA[c.CommitID] {
				newCommits = append(newCommits, c)
			}
		}
	}

	fmt.Fprintf(w, "SCM (commits in build %d not in build %d):\n", b.BuildNumber, a.BuildNumber)
	if len(newCommits) == 0 {
		w.WriteString("  (none)\n\n")
		return
	}
	for _, c := range newCommits {
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
		fmt.Fprintf(w, "  %s %s  %q\n", short, author, firstLine(c.Msg))
	}
	w.WriteString("\n")
}

func renderStagesDiff(w *strings.Builder, a, b *compareSnapshot) {
	switch {
	case a.Stages == nil && b.Stages == nil:
		w.WriteString("Stages: (no pipeline data for either build)\n\n")
		return
	case a.Stages == nil:
		fmt.Fprintf(w, "Stages: build %d is not a pipeline (no /wfapi/describe data)\n\n", a.BuildNumber)
		return
	case b.Stages == nil:
		fmt.Fprintf(w, "Stages: build %d is not a pipeline (no /wfapi/describe data)\n\n", b.BuildNumber)
		return
	}

	aMap := map[string]string{}
	for _, s := range a.Stages.Stages {
		aMap[s.Name] = s.Status
	}
	bMap := map[string]string{}
	for _, s := range b.Stages.Stages {
		bMap[s.Name] = s.Status
	}

	seen := map[string]bool{}
	var ordered []string
	for _, s := range b.Stages.Stages {
		if !seen[s.Name] {
			ordered = append(ordered, s.Name)
			seen[s.Name] = true
		}
	}
	for _, s := range a.Stages.Stages {
		if !seen[s.Name] {
			ordered = append(ordered, s.Name)
			seen[s.Name] = true
		}
	}

	var changed []string
	for _, name := range ordered {
		sA, okA := aMap[name]
		sB, okB := bMap[name]
		switch {
		case okA && okB && sA != sB:
			changed = append(changed, fmt.Sprintf("  %s: %s → %s", name, sA, sB))
		case !okA && okB:
			changed = append(changed, fmt.Sprintf("  %s: (not in build %d) → %s", name, a.BuildNumber, sB))
		case okA && !okB:
			changed = append(changed, fmt.Sprintf("  %s: %s → (not in build %d)", name, sA, b.BuildNumber))
		}
	}

	w.WriteString("Stages (changed only):\n")
	if len(changed) == 0 {
		w.WriteString("  (none)\n\n")
		return
	}
	for _, line := range changed {
		w.WriteString(line + "\n")
	}
	w.WriteString("\n")
}

// collectTests folds a JUnit report into a {className.name → state} map.
// SKIPPED collapses to StateSkip; compare_builds itself doesn't split
// pass↔skip transitions (only pass↔fail), so the renderer treats Skip
// the same as Unknown when bucketing.
func collectTests(rep *junitReport) map[string]JUnitState {
	m := map[string]JUnitState{}
	if rep == nil {
		return m
	}
	for _, suite := range rep.Suites {
		for _, c := range suite.Cases {
			m[c.ClassName+"."+c.Name] = NormalizeJUnitStatus(c.Status)
		}
	}
	return m
}

func renderTestsDiff(w *strings.Builder, a, b *compareSnapshot) {
	switch {
	case a.Tests == nil && b.Tests == nil:
		w.WriteString("Tests: (no test report on either build)\n")
		return
	case a.Tests == nil:
		fmt.Fprintf(w, "Tests: no test report on build %d\n", a.BuildNumber)
		return
	case b.Tests == nil:
		fmt.Fprintf(w, "Tests: no test report on build %d\n", b.BuildNumber)
		return
	}

	tA := collectTests(a.Tests)
	tB := collectTests(b.Tests)

	var passToFail, failToPass, newTests, removed []string
	for k, sB := range tB {
		sA, ok := tA[k]
		if !ok {
			label := k
			switch sB {
			case StateFail:
				label = k + " [FAILED]"
			case StatePass:
				label = k + " [PASSED]"
			}
			newTests = append(newTests, label)
			continue
		}
		switch {
		case sA == StatePass && sB == StateFail:
			passToFail = append(passToFail, k)
		case sA == StateFail && sB == StatePass:
			failToPass = append(failToPass, k)
		}
	}
	for k := range tA {
		if _, ok := tB[k]; !ok {
			removed = append(removed, k)
		}
	}

	sort.Strings(passToFail)
	sort.Strings(failToPass)
	sort.Strings(newTests)
	sort.Strings(removed)

	w.WriteString("Tests:\n")
	renderTestBucket(w, "pass → fail", passToFail)
	renderTestBucket(w, "fail → pass", failToPass)
	renderTestBucket(w, "new", newTests)
	renderTestBucket(w, "removed", removed)
}

func renderTestBucket(w *strings.Builder, label string, names []string) {
	fmt.Fprintf(w, "  %s (%d):\n", label, len(names))
	if len(names) == 0 {
		return
	}
	shown := names
	if len(shown) > compareTestsBucketCap {
		shown = shown[:compareTestsBucketCap]
	}
	for _, n := range shown {
		fmt.Fprintf(w, "    %s\n", n)
	}
	if len(names) > compareTestsBucketCap {
		fmt.Fprintf(w, "    … (%d more, capped at %d)\n",
			len(names)-compareTestsBucketCap, compareTestsBucketCap)
	}
}
