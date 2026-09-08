# 作用：把实验室 targets / pgpass 安装到本机，不覆盖已有生产清单
# 运行主机：本机
# 调用方：人工（lab/postgres 联调）
# 大概流程：
#   1) 复制 targets.lab.json → %USERPROFILE%\.cursor\postgres-targets.lab.json
#   2) 确保 %APPDATA%\postgresql 存在
#   3) 若 pgpass 没有 mcp_lab 这一行则追加
# 勿放密钥：只复制仓库里标明的实验室口令，不读取生产 pgpass 明文

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

$cursorDir = Join-Path $env:USERPROFILE ".cursor"
New-Item -ItemType Directory -Force -Path $cursorDir | Out-Null
$destTargets = Join-Path $cursorDir "postgres-targets.lab.json"
Copy-Item -Force (Join-Path $Root "targets.lab.json") $destTargets
Write-Output "targets=$destTargets"

$pgDir = Join-Path $env:APPDATA "postgresql"
New-Item -ItemType Directory -Force -Path $pgDir | Out-Null
$pgpass = Join-Path $pgDir "pgpass.conf"
$line = (Get-Content (Join-Path $Root "pgpass.example") | Where-Object { $_ -match '^[^#]' } | Select-Object -First 1).Trim()
if (-not $line) { throw "pgpass.example has no data line" }

if (Test-Path $pgpass) {
    $existing = Get-Content $pgpass -ErrorAction SilentlyContinue
    $has = $false
    foreach ($l in $existing) {
        if ($l.Trim() -eq $line) { $has = $true; break }
        if ($l -match '^127\.0\.0\.1:5432:mcp_lab:mcp_ro:') { $has = $true; break }
    }
    if (-not $has) {
        Add-Content -Path $pgpass -Value $line
        Write-Output "pgpass_appended=$pgpass"
    } else {
        Write-Output "pgpass_already_has_mcp_lab=$pgpass"
    }
} else {
    $header = @"
# hostname:port:database:username:password
# appended by lab/postgres/scripts/install-local-config.ps1
"@
    Set-Content -Path $pgpass -Value $header
    Add-Content -Path $pgpass -Value $line
    Write-Output "pgpass_created=$pgpass"
}

Write-Output ""
Write-Output "Next: set PG_TARGETS_FILE in mcp.json to:"
Write-Output $destTargets.Replace('\', '/')
Write-Output "Windows command should point at postgres-mcp-server.exe, then reload the postgres MCP."
