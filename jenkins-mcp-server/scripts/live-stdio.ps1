# 作用：用本机 stdio MCP 对真实 Jenkins 做只读联调（健康检查、Job 列表）
# 运行主机：本机
# 调用方：人工验收
# 大概流程：
#   1) 读取 JENKINS_URL / JENKINS_USER / JENKINS_API_TOKEN
#   2) 拉起 jenkins-mcp-server.exe
#   3) 调用 health_check、list_jobs，打印摘要
# 勿放密钥：只走环境变量。可选 JENKINS_SAMPLE_FOLDER / JENKINS_SAMPLE_JOB 指定联调 Job。

param(
    [string]$Binary = (Join-Path $PSScriptRoot "..\jenkins-mcp-server.exe")
)

$ErrorActionPreference = "Stop"
if (-not $env:JENKINS_URL) { throw "JENKINS_URL is required" }
if (-not $env:JENKINS_USER) { throw "JENKINS_USER is required" }
if (-not $env:JENKINS_API_TOKEN) { throw "JENKINS_API_TOKEN is required" }

$Binary = (Resolve-Path $Binary).Path

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $Binary
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true
$psi.Environment["JENKINS_URL"] = $env:JENKINS_URL
$psi.Environment["JENKINS_USER"] = $env:JENKINS_USER
$psi.Environment["JENKINS_API_TOKEN"] = $env:JENKINS_API_TOKEN
$psi.Environment["JENKINS_MCP_TIMEOUT"] = $(if ($env:JENKINS_MCP_TIMEOUT) { $env:JENKINS_MCP_TIMEOUT } else { "90s" })

$proc = New-Object System.Diagnostics.Process
$proc.StartInfo = $psi
[void]$proc.Start()

$script:nextId = 1
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
function Invoke-Tool([string]$name, [hashtable]$arguments) {
    $id = $script:nextId
    $script:nextId++
    $payload = @{
        jsonrpc = "2.0"
        id = $id
        method = "tools/call"
        params = @{
            name = $name
            arguments = $arguments
        }
    } | ConvertTo-Json -Compress -Depth 8
    Send-Mcp $payload
    return Read-McpResult $id
}
function Get-ToolText($result) {
    if ($result.error) { return "RPC_ERROR $($result.error | ConvertTo-Json -Compress)" }
    $isErr = [bool]$result.result.isError
    $text = ($result.result.content | ForEach-Object { $_.text }) -join "`n"
    if ($isErr) { return "TOOL_ERROR $text" }
    return $text
}

try {
    Send-Mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"jenkins-live","version":"0.1.0"}}}'
    $init = Read-McpResult 1
    if (-not $init.result) { throw "initialize failed" }
    $script:nextId = 2
    Send-Mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}'

    Write-Output ("jenkins_url=" + $env:JENKINS_URL)
    Write-Output ("server=" + $init.result.serverInfo.name + " " + $init.result.serverInfo.version)

    $folder = $(if ($env:JENKINS_SAMPLE_FOLDER) { $env:JENKINS_SAMPLE_FOLDER } else { "team" })
    $job = $(if ($env:JENKINS_SAMPLE_JOB) { $env:JENKINS_SAMPLE_JOB } else { "team/prod/checkout-api" })
    $checks = @(
        @{ name = "health_check"; args = @{} },
        @{ name = "list_jobs"; args = @{ folder_path = $folder; recursive = $true } },
        @{ name = "list_queue"; args = @{} },
        @{ name = "list_nodes"; args = @{} },
        @{ name = "find_recent_failures"; args = @{ folder_path = $folder; since = "7d"; max_results = 10 } },
        @{ name = "get_build_info"; args = @{ job_path = $job } },
        @{ name = "get_pipeline_stages"; args = @{ job_path = $job } },
        @{ name = "get_console_log"; args = @{ job_path = $job; tail_lines = 40 } },
        @{ name = "search_console_log"; args = @{ job_path = $job; pattern = "Finished:|ERROR|FAILED" } },
        @{ name = "last_green_build"; args = @{ job_path = $job } }
    )

    $failed = @()
    foreach ($c in $checks) {
        $res = Invoke-Tool $c.name $c.args
        $text = Get-ToolText $res
        $ok = $text -notmatch '^(RPC_ERROR|TOOL_ERROR)'
        $preview = if ($text.Length -gt 900) { $text.Substring(0, 900) + "..." } else { $text }
        Write-Output ""
        Write-Output ("==== " + $c.name + " " + $(if ($ok) { "OK" } else { "FAIL" }) + " ====")
        Write-Output $preview
        if (-not $ok) { $failed += $c.name }
    }

    if ($failed.Count -gt 0) {
        throw ("live checks failed: " + ($failed -join ", "))
    }
    Write-Output ""
    Write-Output "LIVE_OK"
}
finally {
    if (-not $proc.HasExited) {
        $proc.Kill()
        [void]$proc.WaitForExit(3000)
    }
}
