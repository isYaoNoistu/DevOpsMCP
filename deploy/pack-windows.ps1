# 作用：默认 Windows 打包。编三个 .exe，带无密钥样例，打成 zip
# 运行主机：Windows 开发机 / 构建机。有 Go 本机编，否则 Docker Desktop + golang 镜像
# 调用方：人工（pack-windows.cmd）或 CI。接到月弦是 Linux 上的 attach.sh
# 大概流程：
# 1) compile.py --os windows
# 2) deploy/dist/devopsmcp-windows-<arch>/
# 3) Compress-Archive 为 zip
# 勿放密钥：不要把 .env / pgpass 打进包

[CmdletBinding()]
param(
    [string]$Arch = $(if ($env:GOARCH) { $env:GOARCH } else { "amd64" }),
    [string]$OutDir = "",
    [switch]$NoArchive,
    [switch]$Docker
)

function Resolve-Python {
    foreach ($name in @("python3", "python")) {
        $cmd = Get-Command $name -ErrorAction SilentlyContinue
        if ($cmd) {
            return @{ File = $cmd.Source; Prefix = @() }
        }
    }
    $py = Get-Command py -ErrorAction SilentlyContinue
    if ($py) {
        return @{ File = $py.Source; Prefix = @("-3") }
    }
    throw "need python3, python, or the Windows py launcher"
}

$ErrorActionPreference = "Stop"
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$Dist = Join-Path $Here "dist"
$Py = Resolve-Python

New-Item -ItemType Directory -Force -Path $Dist | Out-Null
if (-not $OutDir) {
    $OutDir = Join-Path $Dist "devopsmcp-windows-$Arch"
}
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$compileArgs = $Py.Prefix + @(
    (Join-Path $Here "compile.py"),
    "--os", "windows",
    "--arch", $Arch,
    "--out", $OutDir
)
if ($Docker) { $compileArgs += "--docker" }

& $Py.File @compileArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "windows pack dir: $OutDir"
if ($NoArchive) { exit 0 }

$Archive = Join-Path $Dist "devopsmcp-windows-$Arch.zip"
if (Test-Path $Archive) { Remove-Item -Force $Archive }
Compress-Archive -Path $OutDir -DestinationPath $Archive -Force
Write-Host "windows archive : $Archive"
