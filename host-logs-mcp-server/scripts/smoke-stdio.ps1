# 作用：对本机编译的 host-logs-mcp-server 做 stdio 握手，核对只读工具并对夹具 search/tail
# 运行主机：本机
# 调用方：人工验收
# 大概流程：
#   1) 若无二进制则 go build
#   2) 写临时 local target + PostgreSQL 风格夹具日志
#   3) initialize → tools/list（5 个只读，无 exec）
#   4) list_targets / search_log / tail_log；越界 path 必须失败
# 勿放密钥：夹具不使用真实密码或私钥

param(
    [string]$Binary = ""
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot

if (-not $Binary) {
    $exe = Join-Path $Root "host-logs-mcp-server.exe"
    $plain = Join-Path $Root "host-logs-mcp-server"
    if (Test-Path $exe) { $Binary = $exe }
    elseif (Test-Path $plain) { $Binary = $plain }
}

if (-not $Binary -or -not (Test-Path $Binary)) {
    Push-Location $Root
    try {
        $out = Join-Path $Root "host-logs-mcp-server.exe"
        go build -o $out .
        if ($LASTEXITCODE -ne 0) { throw "go build failed" }
        $Binary = $out
    }
    finally {
        Pop-Location
    }
}

$Binary = (Resolve-Path $Binary).Path

$work = Join-Path $env:TEMP ("host-logs-mcp-smoke-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$logFile = Join-Path $work "postgresql-16-main-2026-09-17.log"
$logBody = @"
2026-09-17 03:00:00.000 CST [1] LOG:  checkpoint
2026-09-17 05:40:37.000 CST [688365] shop@shop_prod 203.0.113.10(56306) app=shop-web xid=0 vxid=25/64532 LOG:  unexpected EOF on client connection with an open transaction
2026-09-17 06:00:00.000 CST [2] LOG:  later
"@
[System.IO.File]::WriteAllText($logFile, $logBody)

$targets = Join-Path $work "targets.json"
$testPassword = "smoke-only-placeholder-password"
$targetsObj = @{
    targets = @(
        @{
            name        = "smoke-local"
            aliases     = @("smoke")
            environment = "dev"
            host        = "local"
            transport   = "local"
            paths       = @($work)
            tags        = @("smoke", "lab")
        },
        @{
            name = "smoke-password"
            host = "127.0.0.1"
            user = "reader"
            password = $testPassword
            paths = @("/var/log/nginx")
        }
    )
}
$targetsObj | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $targets -Encoding utf8

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $Binary
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true
$psi.EnvironmentVariables["HOST_LOGS_TARGETS_FILE"] = $targets
$psi.EnvironmentVariables["HOST_LOGS_READ_ONLY"] = "true"
if ($psi.Environment) {
    $psi.Environment["HOST_LOGS_TARGETS_FILE"] = $targets
    $psi.Environment["HOST_LOGS_READ_ONLY"] = "true"
}

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

function Text-Of($resp) {
    if ($resp.result.content -and $resp.result.content.Count -gt 0) {
        return [string]$resp.result.content[0].text
    }
    return ($resp | ConvertTo-Json -Compress -Depth 8)
}

try {
    Send-Mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"host-logs-smoke","version":"0.1.0"}}}'
    $init = Read-McpResult 1
    if (-not $init.result) { throw "initialize failed: $($init | ConvertTo-Json -Compress)" }

    Send-Mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    Send-Mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
    $listed = Read-McpResult 2
    $names = @($listed.result.tools | ForEach-Object { $_.name }) | Sort-Object

    $required = @(
        "list_targets", "get_target_info",
        "list_log_files", "search_log", "tail_log"
    )
    $forbidden = @(
        "exec", "run_command", "ssh_exec", "execute_command",
        "write_file", "delete_file", "read_file",
        "put_file", "get_file"
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

    Send-Mcp '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_targets","arguments":{}}}'
    $listedTargets = Read-McpResult 3
    if ($listedTargets.error) { throw "list_targets error: $($listedTargets | ConvertTo-Json -Compress)" }
    $listText = Text-Of $listedTargets
    if ($listText -notmatch "smoke-local") { throw "list_targets missing smoke-local: $listText" }
    if ($listText -notmatch "smoke-password") { throw "password target not loaded" }
    if (($listedTargets | ConvertTo-Json -Depth 20) -match [regex]::Escape($testPassword)) { throw "list_targets exposed password" }
    Send-Mcp '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"get_target_info","arguments":{"target":"smoke-password"}}}'
    $passwordInfo = Read-McpResult 8
    if ($passwordInfo.error -or $passwordInfo.result.isError) { throw "password target metadata failed" }
    if (($passwordInfo | ConvertTo-Json -Depth 20) -match [regex]::Escape($testPassword)) { throw "get_target_info exposed password" }
    Write-Output "PASSWORD_CONFIG_OK (no SSH connection)"


    $searchArgs = @{
        target   = "smoke-local"
        path     = $logFile
        pattern  = "unexpected EOF"
        fixed    = $true
        start    = "2026-09-17 05:38:00"
        end      = "2026-09-17 05:45:00"
        max_lines = 20
    } | ConvertTo-Json -Compress
    Send-Mcp ("{`"jsonrpc`":`"2.0`",`"id`":4,`"method`":`"tools/call`",`"params`":{`"name`":`"search_log`",`"arguments`":$searchArgs}}")
    $searched = Read-McpResult 4
    if ($searched.error -or $searched.result.isError) { throw "search_log failed: $($searched | ConvertTo-Json -Compress -Depth 8)" }
    $searchText = Text-Of $searched
    if ($searchText -notmatch "shop-web") { throw "search_log missed fixture: $searchText" }

    $tailArgs = @{
        target = "smoke-local"
        path   = $logFile
        lines  = 2
    } | ConvertTo-Json -Compress
    Send-Mcp ("{`"jsonrpc`":`"2.0`",`"id`":5,`"method`":`"tools/call`",`"params`":{`"name`":`"tail_log`",`"arguments`":$tailArgs}}")
    $tailed = Read-McpResult 5
    if ($tailed.error -or $tailed.result.isError) { throw "tail_log failed: $($tailed | ConvertTo-Json -Compress -Depth 8)" }

    $badPath = Join-Path $env:TEMP "host-logs-smoke-outside.log"
    $badArgs = @{
        target  = "smoke-local"
        path    = $badPath
        pattern = "x"
        fixed   = $true
    } | ConvertTo-Json -Compress
    Send-Mcp ("{`"jsonrpc`":`"2.0`",`"id`":6,`"method`":`"tools/call`",`"params`":{`"name`":`"search_log`",`"arguments`":$badArgs}}")
    $denied = Read-McpResult 6
    $deniedText = ($denied | ConvertTo-Json -Compress -Depth 8)
    $okDeny = $false
    if ($denied.error) { $okDeny = $true }
    if ($denied.result.isError) { $okDeny = $true }
    if ($deniedText -match "allowlist" -or $deniedText -match "outside") { $okDeny = $true }
    if (-not $okDeny) { throw "expected allowlist deny, got $deniedText" }

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
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
