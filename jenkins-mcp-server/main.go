// 作用：本机 stdio MCP，只读查询 Jenkins Job / 构建 / 控制台 / Pipeline stage / 队列
// 运行主机：开发机（Cursor 拉起）
// 调用方：Cursor MCP（~/.cursor/mcp.json）
// 大概流程：
//   1) 读 JENKINS_URL / JENKINS_USER / JENKINS_API_TOKEN
//   2) 注册只读排障工具
//   3) 走 stdio MCP
// 勿放密钥：只走环境变量，不写进仓库
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
	"github.com/2001adarsh/jenkins-mcp-go/internal/tools"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	client, err := jenkins.NewClient(jenkins.Config{
		BaseURL: cfg.URL,
		User:    cfg.User,
		Token:   cfg.Token,
		Timeout: cfg.Timeout,
	})
	if err != nil {
		return err
	}

	cache, err := jenkins.NewConsoleCache(client, cfg.CacheDir)
	if err != nil {
		return err
	}
	if cfg.CacheMax > 0 {
		cache.MaxBytes = cfg.CacheMax
	}

	deps := tools.Deps{
		Client:   client,
		Cache:    cache,
		Version:  version,
		ReadOnly: true,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "jenkins",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "Read-only Jenkins MCP for deploy diagnosis. List jobs, inspect builds, " +
			"pipeline stages, console tails/search, queue and nodes. Do not claim to have " +
			"triggered, stopped, canceled, or changed any Jenkins job.",
	})

	log.Printf("jenkins-mcp %s (read-only)", version)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_check",
		Description: "Probe Jenkins reachability, version, authenticated user, and plugin presence.",
	}, deps.HealthCheck)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_jobs",
		Description: "List jobs/folders. folder_path empty = root; recursive + name_filter (RE2) to narrow. Capped at 500.",
	}, deps.ListJobs)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_build_info",
		Description: "Build status, duration, parameters, and change set. job_path required; build_number 0 = lastBuild.",
	}, deps.GetBuildInfo)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_build_environment",
		Description: "Cause, parameters (secrets masked), and injected env vars. Do not echo secret values.",
	}, deps.GetBuildEnvironment)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_scm_context",
		Description: "Commits and touched paths for one build.",
	}, deps.GetSCMContext)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "last_green_build",
		Description: "Most recent successful build of a job.",
	}, deps.LastGreenBuild)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "changes_since_last_green",
		Description: "Commits since the job's last successful build.",
	}, deps.ChangesSinceLastGreen)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "compare_builds",
		Description: "Diff two builds: result, duration, parameters, SCM, stages, tests.",
	}, deps.CompareBuilds)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_console_log",
		Description: "Tail console output. Default last 500 lines. Negative tail_lines is rejected; use search_console_log instead of dumping the full log.",
	}, deps.GetConsoleLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_console_log",
		Description: "RE2 search over a build console log with context lines.",
	}, deps.SearchConsoleLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tail_running_build",
		Description: "Incremental console of an in-flight build via progressiveText. Pass Next since_byte to continue.",
	}, deps.TailRunningBuild)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_test_report",
		Description: "JUnit test report for a build, focused on failed cases.",
	}, deps.GetTestReport)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "find_recent_failures",
		Description: "Survey recent failed builds under a folder. Default since=24h, result_filter=FAILURE.",
	}, deps.FindRecentFailures)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_pipeline_stages",
		Description: "Pipeline stages with status and duration via /wfapi/describe. Use this before reading the console.",
	}, deps.GetPipelineStages)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_stage_log",
		Description: "One pipeline stage log via wfapi. If empty, fall back to get_console_log / search_console_log.",
	}, deps.GetStageLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_nodes",
		Description: "Jenkins agents: online/offline, executors, labels.",
	}, deps.ListNodes)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_node",
		Description: "One node detail. Use \"(built-in)\" or \"(master)\" for the controller.",
	}, deps.GetNode)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_queue",
		Description: "Pending queue items and block reasons. Optional job_path_prefix.",
	}, deps.ListQueue)

	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

type config struct {
	URL      string
	User     string
	Token    string
	CacheDir string
	CacheMax int64
	Timeout  time.Duration
}

func loadConfig() (config, error) {
	var cfg config
	cfg.URL = strings.TrimRight(os.Getenv("JENKINS_URL"), "/")
	cfg.User = os.Getenv("JENKINS_USER")
	cfg.Token = os.Getenv("JENKINS_API_TOKEN")
	if cfg.URL == "" || cfg.User == "" || cfg.Token == "" {
		return cfg, fmt.Errorf("JENKINS_URL, JENKINS_USER, and JENKINS_API_TOKEN must all be set")
	}

	dir := os.Getenv("JENKINS_MCP_CACHE_DIR")
	if dir == "" {
		if userCache, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(userCache, "jenkins-mcp")
		} else {
			dir = filepath.Join(os.TempDir(), "jenkins-mcp")
		}
	}
	cfg.CacheDir = dir

	if v := os.Getenv("JENKINS_MCP_CACHE_MAX"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("JENKINS_MCP_CACHE_MAX must be a positive integer, got %q", v)
		}
		cfg.CacheMax = n
	}

	if v := os.Getenv("JENKINS_MCP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("JENKINS_MCP_TIMEOUT must be a positive Go duration, got %q", v)
		}
		cfg.Timeout = d
	}

	return cfg, nil
}
