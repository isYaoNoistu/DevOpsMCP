// 作用：本机 stdio MCP，只读检索本机清单里主机上的白名单日志目录
// 运行主机：开发机（Cursor / WorkBuddy 拉起）
// 调用方：MCP 客户端（~/.cursor/mcp.json 等）
// 大概流程：
//   1) 读 HOST_LOGS_TARGETS_FILE（mtime 变化则下次调用自动 reload）
//   2) targets 可直接配置 password（内置 SSH）；密钥 / ssh_config 继续用本机 OpenSSH
//   3) 注册 list_targets / get_target_info / list_log_files / search_log / tail_log
//   4) 走 stdio MCP；无 exec、无写文件
// 勿放密钥：私钥路径可写在本机 targets，私钥本身和密码不进仓库
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"host-logs-mcp-server/internal/logop"
	"host-logs-mcp-server/internal/targets"
	"host-logs-mcp-server/internal/tools"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	path := strings.TrimSpace(os.Getenv("HOST_LOGS_TARGETS_FILE"))
	reg, err := targets.New(path)
	if err != nil {
		return err
	}

	readOnly := strings.TrimSpace(os.Getenv("HOST_LOGS_READ_ONLY"))
	if readOnly == "" {
		readOnly = "true"
	}
	if !parseBool(readOnly) {
		return fmt.Errorf("HOST_LOGS_READ_ONLY must stay true; this binary has no write tools and no exec")
	}

	deps := tools.Deps{
		Reg: reg,
		SSH: logop.SSHConfig{
			Timeout:        tools.ParseDuration(os.Getenv("HOST_LOGS_TIMEOUT"), 20*time.Second),
			StrictHostKey:  os.Getenv("HOST_LOGS_STRICT_HOST_KEY"),
			KnownHostsFile: strings.TrimSpace(os.Getenv("HOST_LOGS_KNOWN_HOSTS")),
		},
		Version: version,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "host-logs",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "Read-only host log MCP. Resolve a target, then list or search files under that target's path allowlist. " +
			"There is no shell/exec tool. Do not claim to have modified files or restarted services. SSH passwords and keys never appear in target metadata.",
	})

	log.Printf("host-logs-mcp %s (read-only) targets=%s", version, path)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_targets", "List targets",
		"List local host-log targets (name, host, allowlisted paths). Reloads the targets file if it changed. Filter with query. No SSH keys."), deps.ListTargets)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_target_info", "Get target info",
		"One target from the local registry. target is the stable name or a unique alias. Identity file is shown as basename only."), deps.GetTargetInfo)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_log_files", "List log files",
		"List files under an allowlisted directory (maxdepth 4). Empty path lists all roots on the target. Requires target."), deps.ListLogFiles)

	mcp.AddTool(srv, tools.ReadOnlyTool("search_log", "Search log",
		"Search one allowlisted log file. Optional start/end are lexicographic whole-line prefix bounds (PostgreSQL %m timestamps work as strings, not parsed times). Default regex: remote is grep -E (use [0-9] not \\d); local is Go regexp. Set fixed=true for literal. .gz is decompressed. Max 200 lines. Requires target and path."), deps.SearchLog)

	mcp.AddTool(srv, tools.ReadOnlyTool("tail_log", "Tail log",
		"Last N lines of one allowlisted log file (default 80, cap 200). .gz is decompressed. Requires target and path."), deps.TailLog)

	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
