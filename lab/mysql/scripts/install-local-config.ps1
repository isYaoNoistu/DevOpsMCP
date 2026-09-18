# 作用：把实验室 targets / mysqlpass 安装到本机，不覆盖已有生产清单
# 运行主机：本机
# 调用方：人工（lab/mysql 联调）
# 大概流程：
#   1) 复制 targets.lab.json → %USERPROFILE%\.cursor\mysql-targets.lab.json
#   2) 确保 %APPDATA%\mysql 存在
#   3) 若 mysqlpass 没有 mcp_lab 这一行则追加
# 勿放密钥：只复制仓库里标明的实验室口令，不读取生产 mysqlpass 明文

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

$cursorDir = Join-Path $env:USERPROFILE ".cursor"
New-Item -ItemType Directory -Force -Path $cursorDir | Out-Null
$destTargets = Join-Path $cursorDir "mysql-targets.lab.json"
Copy-Item -Force (Join-Path $Root "targets.lab.json") $destTargets
Write-Output "targets=$destTargets"

$myDir = Join-Path $env:APPDATA "mysql"
New-Item -ItemType Directory -Force -Path $myDir | Out-Null
$passfile = Join-Path $myDir "mysqlpass.conf"
$line = (Get-Content (Join-Path $Root "mysqlpass.example") | Where-Object { $_ -match '^[^#]' } | Select-Object -First 1).Trim()
if (-not $line) { throw "mysqlpass.example has no data line" }

if (Test-Path $passfile) {
    $existing = Get-Content $passfile -ErrorAction SilentlyContinue
    $has = $false
    foreach ($l in $existing) {
        if ($l.Trim() -eq $line) { $has = $true; break }
        if ($l -match '^127\.0\.0\.1:3306:mcp_lab:mcp_ro:') { $has = $true; break }
    }
    if (-not $has) {
        Add-Content -Path $passfile -Value $line
        Write-Output "mysqlpass_appended=$passfile"
    } else {
        Write-Output "mysqlpass_already_has_mcp_lab=$passfile"
    }
} else {
    $header = @"
# hostname:port:database:username:password
# appended by lab/mysql/scripts/install-local-config.ps1
"@
    Set-Content -Path $passfile -Value $header
    Add-Content -Path $passfile -Value $line
    Write-Output "mysqlpass_created=$passfile"
}

Write-Output ""
Write-Output "Next: set MYSQL_TARGETS_FILE in mcp.json to:"
Write-Output $destTargets.Replace('\', '/')
Write-Output "Windows command should point at mysql-mcp-server.exe, then reload the mysql MCP."
