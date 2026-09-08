package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// folderClasses lists the Jenkins `_class` values that contain nested jobs.
// When recursive=true, list_jobs descends into entries whose _class matches.
// Anything else is treated as a leaf job (FreeStyle, WorkflowJob, Maven, etc.).
var folderClasses = map[string]bool{
	"com.cloudbees.hudson.plugins.folder.Folder":                            true,
	"jenkins.branch.OrganizationFolder":                                     true,
	"org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject": true,
}

// listJobsTree is the `tree` selector for folder/root listings. Kept explicit
// so the response stays small and stable across Jenkins versions.
const listJobsTree = "jobs[name,url,color,_class,lastBuild[number,result,timestamp]]"

// listJobsCap bounds the response size. Large folders are surfaced via a
// trailing note that points the caller at `folder_path` / `name_filter` as the
// workaround — pagination of /api/json is not supported by Jenkins.
const listJobsCap = 500

type apiBuild struct {
	Number    int64  `json:"number"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
}

type apiJob struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Color     string    `json:"color"`
	Class     string    `json:"_class"`
	LastBuild *apiBuild `json:"lastBuild"`
}

type apiListing struct {
	Jobs []apiJob `json:"jobs"`
}

// ListJobsInput is the schema for list_jobs.
type ListJobsInput struct {
	FolderPath string `json:"folder_path,omitempty" jsonschema:"Slash-separated folder path (e.g. 'team/integration'). Omit or empty for the Jenkins root."`
	Recursive  bool   `json:"recursive,omitempty" jsonschema:"Walk into sub-folders. Default false."`
	NameFilter string `json:"name_filter,omitempty" jsonschema:"Case-insensitive RE2 regex (a plain substring works too) matched against each job name."`
}

type listingEntry struct {
	JobPath    string
	URL        string
	Color      string
	LastNumber int64
	LastResult string
	IsFolder   bool
}

// ListJobs enumerates jobs and folders under folder_path (root when empty).
// With recursive=true it descends into Jenkins folder types. name_filter is
// matched against each job's leaf name only — folders are always traversed
// even when their own name does not match, since matching jobs may live
// inside them.
func (d Deps) ListJobs(ctx context.Context, _ *mcp.CallToolRequest, in ListJobsInput) (*mcp.CallToolResult, any, error) {
	nameRe, err := compileFilter("name_filter", in.NameFilter)
	if err != nil {
		return nil, nil, err
	}

	var (
		entries []listingEntry
		hitCap  bool
	)
	root := strings.Trim(in.FolderPath, "/")
	if err := d.walkFolder(ctx, root, in.Recursive, nameRe, &entries, &hitCap); err != nil {
		return nil, nil, err
	}

	var out strings.Builder
	label := root
	if label == "" {
		label = "(root)"
	}
	fmt.Fprintf(&out, "Listing under %s (recursive=%v", label, in.Recursive)
	if in.NameFilter != "" {
		fmt.Fprintf(&out, ", name_filter=%q", in.NameFilter)
	}
	fmt.Fprintf(&out, ") — %d entries\n\n", len(entries))

	if len(entries) == 0 {
		out.WriteString("(no entries matched)\n")
		return textResult(out.String()), nil, nil
	}

	fmt.Fprintf(&out, "%-6s  %-9s  %-7s  %-9s  %-40s  %s\n",
		"type", "status", "last#", "result", "job_path", "url")
	fmt.Fprintf(&out, "%-6s  %-9s  %-7s  %-9s  %-40s  %s\n",
		"------", "---------", "-------", "---------",
		"----------------------------------------", "---")
	for _, e := range entries {
		typ := "job"
		if e.IsFolder {
			typ = "folder"
		}
		status := e.Color
		if status == "" {
			status = "-"
		}
		lastNum := "-"
		if e.LastNumber > 0 {
			lastNum = fmt.Sprintf("%d", e.LastNumber)
		}
		result := e.LastResult
		if result == "" {
			result = "-"
		}
		fmt.Fprintf(&out, "%-6s  %-9s  %-7s  %-9s  %-40s  %s\n",
			typ, status, lastNum, result, e.JobPath, e.URL)
	}

	if hitCap {
		fmt.Fprintf(&out,
			"\n(stopped at %d entries — narrow the listing with folder_path or name_filter; "+
				"Jenkins /api/json does not paginate)\n",
			listJobsCap)
	}
	out.WriteString(
		"\nUse the listed job_path values with the other tools (e.g. get_build_info, get_console_log).\n",
	)
	return textResult(out.String()), nil, nil
}

// walkFolder fetches one folder listing and, when recursive, descends into
// folder-typed entries. The cap is enforced across the whole walk: once
// hitCap flips true, additional rows are skipped but folders already in
// progress finish unwinding cleanly.
func (d Deps) walkFolder(
	ctx context.Context,
	folderPath string,
	recursive bool,
	nameRe *regexp.Regexp,
	entries *[]listingEntry,
	hitCap *bool,
) error {
	apiPath := "/api/json"
	if folderPath != "" {
		apiPath = jenkins.JobAPIPath(folderPath) + "/api/json"
	}
	body, err := d.Client.Get(ctx, apiPath, map[string]string{"tree": listJobsTree})
	if err != nil {
		label := folderPath
		if label == "" {
			label = "(root)"
		}
		return fmt.Errorf("list jobs under %s: %w", label, err)
	}
	var listing apiListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return fmt.Errorf("parse listing for %q: %w", folderPath, err)
	}

	for _, j := range listing.Jobs {
		childPath := j.Name
		if folderPath != "" {
			childPath = folderPath + "/" + j.Name
		}
		isFolder := folderClasses[j.Class]
		if !*hitCap && (nameRe == nil || nameRe.MatchString(j.Name)) {
			row := listingEntry{
				JobPath:  childPath,
				URL:      j.URL,
				Color:    j.Color,
				IsFolder: isFolder,
			}
			if j.LastBuild != nil {
				row.LastNumber = j.LastBuild.Number
				row.LastResult = j.LastBuild.Result
			}
			*entries = append(*entries, row)
			if len(*entries) >= listJobsCap {
				*hitCap = true
			}
		}
		if recursive && isFolder && !*hitCap {
			if err := d.walkFolder(ctx, childPath, recursive, nameRe, entries, hitCap); err != nil {
				return err
			}
		}
	}
	return nil
}
