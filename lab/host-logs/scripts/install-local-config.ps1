# 作用：把实验室 host-logs targets 安装到本机，写入夹具目录的绝对路径
# 运行主机：本机
# 调用方：人工（lab/host-logs 联调）
# 大概流程：
#   1) 解析 lab/host-logs/fixtures 绝对路径
#   2) 写 %USERPROFILE%\.cursor\host-logs-targets.lab.json（local transport）
# 勿放密钥：实验室无 SSH、无 password

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$fixtures = Join-Path $Root "fixtures"
if (-not (Test-Path $fixtures)) { throw "missing $fixtures" }
$fixturesAbs = (Resolve-Path $fixtures).Path

$cursorDir = Join-Path $env:USERPROFILE ".cursor"
New-Item -ItemType Directory -Force -Path $cursorDir | Out-Null
$destTargets = Join-Path $cursorDir "host-logs-targets.lab.json"

$payload = @{
    targets = @(
        @{
            name         = "mcp-lab-host-logs"
            aliases      = @("实验室日志", "lab host logs")
            description  = "DevOpsMCP lab/host-logs fixtures (local, no SSH)"
            environment  = "dev"
            host         = "local"
            transport    = "local"
            paths        = @($fixturesAbs)
            tags         = @("local", "lab", "postgres")
        }
    )
}
$payload | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $destTargets -Encoding utf8
Write-Output "targets=$destTargets"
Write-Output "fixtures=$fixturesAbs"
Write-Output ""
Write-Output "Next: set HOST_LOGS_TARGETS_FILE in mcp.json to:"
Write-Output $destTargets.Replace('\', '/')
Write-Output "Windows command should point at host-logs-mcp-server.exe, then reload the host-logs MCP."
