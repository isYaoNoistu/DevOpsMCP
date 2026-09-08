# 作用：对本机编译的 jenkins-mcp-server 做 stdio 握手，核对接手和只读工具清单
# 运行主机：本机
# 调用方：人工验收
# 大概流程：
#   1) 用占位凭据拉起 stdio（不连真实 Jenkins）
#   2) initialize → tools/list
#   3) 断言排障工具在，写工具不在
# 勿放密钥：占位 Token 即可

param(
    [string]$Binary = (Join-Path $PSScriptRoot "..\jenkins-mcp-server.exe")
)

$ErrorActionPreference = "Stop"
$Binary = (Resolve-Path $Binary).Path

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $Binary
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true
$psi.Environment["JENKINS_URL"] = "http://127.0.0.1:8080"
$psi.Environment["JENKINS_USER"] = "smoke"
$psi.Environment["JENKINS_API_TOKEN"] = "smoke-dummy-token"

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
    Send-Mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"jenkins-smoke","version":"0.1.0"}}}'
    $init = Read-McpResult 1
    if (-not $init.result) { throw "initialize failed: $($init | ConvertTo-Json -Compress)" }

    Send-Mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    Send-Mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
    $listed = Read-McpResult 2
    $names = @($listed.result.tools | ForEach-Object { $_.name }) | Sort-Object

    $required = @(
        "health_check", "list_jobs",
        "get_build_info", "get_build_environment",
        "get_scm_context", "last_green_build", "changes_since_last_green", "compare_builds",
        "get_console_log", "search_console_log", "tail_running_build",
        "get_test_report", "find_recent_failures",
        "get_pipeline_stages", "get_stage_log",
        "list_nodes", "get_node", "list_queue"
    )
    $forbidden = @(
        "trigger_build", "stop_build", "cancel_queue_item",
        "get_console_log_path", "get_pipeline_script",
        "get_plugin_versions", "whoami_can", "run_groovy_script"
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
