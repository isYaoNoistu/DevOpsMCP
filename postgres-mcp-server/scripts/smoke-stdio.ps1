# 作用：对本机编译的 postgres-mcp-server 做 stdio 握手，核对接手和只读工具清单
# 运行主机：本机
# 调用方：人工验收
# 大概流程：
#   1) 用临时 targets 文件拉起 stdio（不连真实 PostgreSQL）
#   2) initialize → tools/list
#   3) 断言 19 个只读工具在，写工具不在
# 勿放密钥：targets 里不放密码

param(
    [string]$Binary = (Join-Path $PSScriptRoot "..\postgres-mcp-server.exe")
)

$ErrorActionPreference = "Stop"
$Binary = (Resolve-Path $Binary).Path

$targets = Join-Path $env:TEMP "postgres-mcp-smoke-targets.json"
$targetsJson = @'
{
  "targets": [
    {
      "name": "smoke-local",
      "aliases": ["smoke"],
      "environment": "dev",
      "host": "127.0.0.1",
      "port": 5432,
      "dbname": "postgres",
      "user": "mcp_ro",
      "credential_ref": "postgres/smoke-local",
      "tags": ["smoke"]
    }
  ]
}
'@
[System.IO.File]::WriteAllText($targets, $targetsJson)

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $Binary
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true
if ($psi.Environment) {
    $psi.Environment["PG_TARGETS_FILE"] = $targets
    $psi.Environment["PG_MCP_READ_ONLY"] = "true"
}
$psi.EnvironmentVariables["PG_TARGETS_FILE"] = $targets
$psi.EnvironmentVariables["PG_MCP_READ_ONLY"] = "true"

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
    Send-Mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"postgres-smoke","version":"0.1.0"}}}'
    $init = Read-McpResult 1
    if (-not $init.result) { throw "initialize failed: $($init | ConvertTo-Json -Compress)" }

    Send-Mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    Send-Mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
    $listed = Read-McpResult 2
    $names = @($listed.result.tools | ForEach-Object { $_.name }) | Sort-Object

    $required = @(
        "list_targets", "get_target_info",
        "get_server_overview", "get_database_stats", "get_settings",
        "list_schemas", "list_tables", "describe_relation",
        "list_sessions", "list_long_transactions", "list_idle_transactions", "get_blocking_tree",
        "list_slow_queries", "get_table_stats", "get_index_stats",
        "get_vacuum_status", "get_replication_status",
        "query_postgres", "explain_query"
    )
    $forbidden = @(
        "execute_sql", "run_sql", "write_query",
        "update_row", "delete_row", "insert_row",
        "vacuum_table", "drop_slot", "kill_session",
        "create_index", "alter_table"
    )

    $missing = @($required | Where-Object { $names -notcontains $_ })
    $leaked = @($forbidden | Where-Object { $names -contains $_ })

    Write-Output ("server=" + $init.result.serverInfo.name + " " + $init.result.serverInfo.version)
    Write-Output ("tool_count=" + $names.Count)
    Write-Output ("tools=" + ($names -join ","))

    if ($missing.Count -gt 0) { throw "missing required tools: $($missing -join ', ')" }
    if ($leaked.Count -gt 0) { throw "read-only leaked write/extra tools: $($leaked -join ', ')" }
    if ($names.Count -ne $required.Count) { throw "tool_count=$($names.Count) want $($required.Count)" }

    foreach ($tool in $listed.result.tools) {
        if (-not $tool.annotations.readOnlyHint) {
            throw "missing ReadOnlyHint on $($tool.name)"
        }
        if (-not $tool.outputSchema) {
            throw "missing OutputSchema on $($tool.name)"
        }
    }

    Write-Output "SMOKE_OK"
}
catch {
    if (-not $proc.HasExited) {
        $proc.Kill()
        [void]$proc.WaitForExit(3000)
    }
    $stderr = ""
    try { $stderr = $proc.StandardError.ReadToEnd() } catch { }
    if ($stderr) { Write-Output ("stderr=" + $stderr) }
    throw
}
finally {
    if (-not $proc.HasExited) {
        $proc.Kill()
        [void]$proc.WaitForExit(3000)
    }
    Remove-Item -LiteralPath $targets -ErrorAction SilentlyContinue
}
