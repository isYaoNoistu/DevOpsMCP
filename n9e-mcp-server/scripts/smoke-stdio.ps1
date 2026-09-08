# 作用：对本机编译的 n9e-mcp-server 做 stdio 握手，核对只读 toolset 是否含指标和日志
# 运行主机：本机（开发机 / Cursor 所在机）
# 调用方：人工或配置后的验收
# 大概流程：
#   1) 用环境变量拉起 stdio 进程
#   2) initialize → tools/list
#   3) 断言指标/日志工具在，写工具不在
# 勿放密钥：Token 只走环境变量

param(
    [string]$Binary = (Join-Path $PSScriptRoot "..\n9e-mcp-server.exe"),
    [string]$Token = $(if ($env:N9E_TOKEN) { $env:N9E_TOKEN } else { "smoke-dummy-token" }),
    [string]$BaseUrl = $(if ($env:N9E_BASE_URL) { $env:N9E_BASE_URL } else { "http://127.0.0.1:17000" })
)

$ErrorActionPreference = "Stop"
$Binary = (Resolve-Path $Binary).Path

$env:N9E_TOKEN = $Token
$env:N9E_BASE_URL = $BaseUrl
$env:N9E_TOOLSETS = "alerts,targets,datasource,busi_groups,metrics,logs"
$env:N9E_READ_ONLY = "true"

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $Binary
$psi.Arguments = "stdio"
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true
$psi.Environment["N9E_TOKEN"] = $Token
$psi.Environment["N9E_BASE_URL"] = $BaseUrl
$psi.Environment["N9E_TOOLSETS"] = "alerts,targets,datasource,busi_groups,metrics,logs"
$psi.Environment["N9E_READ_ONLY"] = "true"

$proc = New-Object System.Diagnostics.Process
$proc.StartInfo = $psi
[void]$proc.Start()

function Send-Mcp([string]$json) {
    $proc.StandardInput.WriteLine($json)
    $proc.StandardInput.Flush()
}

function Read-McpResult([int]$wantId) {
    while (-not $proc.StandardOutput.EndOfStream) {
        $line = $proc.StandardOutput.ReadLine()
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $obj = $line | ConvertFrom-Json
        if ($obj.id -eq $wantId) { return $obj }
    }
    throw "no MCP response for id=$wantId"
}

try {
    Send-Mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"n9e-smoke","version":"0.1.0"}}}'
    $init = Read-McpResult 1
    if (-not $init.result) { throw "initialize failed: $($init | ConvertTo-Json -Compress)" }

    Send-Mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    Send-Mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
    $listed = Read-McpResult 2
    $names = @($listed.result.tools | ForEach-Object { $_.name }) | Sort-Object

    $required = @(
        "list_active_alerts", "get_active_alert",
        "list_history_alerts", "get_history_alert",
        "list_alert_rules", "get_alert_rule",
        "list_targets",
        "list_datasources", "get_datasource", "list_datasource_plugins",
        "list_busi_groups",
        "query_instant", "query_range",
        "query_logs", "list_log_indices", "list_log_fields"
    )
    $forbidden = @(
        "create_alert_rule", "update_alert_rule", "create_mute", "upsert_datasource",
        "list_datasources_full", "list_users", "list_dashboards", "list_mutes", "list_notify_rules"
    )

    $missing = @($required | Where-Object { $names -notcontains $_ })
    $leaked = @($forbidden | Where-Object { $names -contains $_ })

    Write-Output ("server=" + $init.result.serverInfo.name + " " + $init.result.serverInfo.version)
    Write-Output ("tool_count=" + $names.Count)
    Write-Output ("tools=" + ($names -join ","))

    if ($missing.Count -gt 0) { throw "missing required tools: $($missing -join ', ')" }
    if ($leaked.Count -gt 0) { throw "read-only leaked write/extra tools: $($leaked -join ', ')" }

    Write-Output "SMOKE_OK"
}
finally {
    if (-not $proc.HasExited) {
        $proc.Kill()
        [void]$proc.WaitForExit(3000)
    }
}
