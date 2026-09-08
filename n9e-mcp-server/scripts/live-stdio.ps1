# 作用：用本机 stdio MCP 对真实夜莺做只读联调（业务组、数据源、主机、告警、PromQL、日志索引）
# 运行主机：本机
# 调用方：人工验收
# 大概流程：
#   1) 读取 N9E_TOKEN / N9E_BASE_URL
#   2) 拉起 n9e-mcp-server.exe stdio（只读 toolset）
#   3) 依次 tools/call，打印摘要，不把 Token 写入仓库
# 勿放密钥：只走环境变量

param(
    [string]$Binary = (Join-Path $PSScriptRoot "..\n9e-mcp-server.exe")
)

$ErrorActionPreference = "Stop"
if (-not $env:N9E_TOKEN) { throw "N9E_TOKEN is required" }
if (-not $env:N9E_BASE_URL) { throw "N9E_BASE_URL is required" }

$Binary = (Resolve-Path $Binary).Path
$env:N9E_TOOLSETS = "alerts,targets,datasource,busi_groups,metrics,logs"
$env:N9E_READ_ONLY = "true"
$env:N9E_MCP_LOG_LEVEL = "info"

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $Binary
$psi.Arguments = "stdio"
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true
$psi.Environment["N9E_TOKEN"] = $env:N9E_TOKEN
$psi.Environment["N9E_BASE_URL"] = $env:N9E_BASE_URL
$psi.Environment["N9E_TOOLSETS"] = "alerts,targets,datasource,busi_groups,metrics,logs"
$psi.Environment["N9E_READ_ONLY"] = "true"
$psi.Environment["N9E_MCP_LOG_LEVEL"] = "info"

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
    Send-Mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"n9e-live","version":"0.1.0"}}}'
    $init = Read-McpResult 1
    if (-not $init.result) { throw "initialize failed" }
    $script:nextId = 2
    Send-Mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}'

    Write-Output ("base_url=" + $env:N9E_BASE_URL)
    Write-Output ("server=" + $init.result.serverInfo.name + " " + $init.result.serverInfo.version)

    $checks = @(
        @{ name = "list_busi_groups"; args = @{ limit = 20; p = 1 } },
        @{ name = "list_datasources"; args = @{ limit = 20; p = 1 } },
        @{ name = "list_targets"; args = @{ query = "mysql"; limit = 5; p = 1 } },
        @{ name = "list_active_alerts"; args = @{ hours = 6; limit = 5; p = 1 } },
        @{ name = "query_instant"; args = @{ ds_id = 2; query = 'mysql_up{job="mysqld_exporter"}' } }
    )

    $failed = @()
    foreach ($c in $checks) {
        $res = Invoke-Tool $c.name $c.args
        $text = Get-ToolText $res
        $ok = $text -notmatch '^(RPC_ERROR|TOOL_ERROR)'
        $preview = if ($text.Length -gt 400) { $text.Substring(0, 400) + "..." } else { $text }
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
