param()
$ErrorActionPreference = 'Stop'
$moduleRoot = Split-Path -Parent $PSScriptRoot
$repoRoot = Split-Path -Parent $moduleRoot
$workspaceRoot = Split-Path -Parent $repoRoot
$outputDirectory = Join-Path $workspaceRoot 'dist/devopsmcp-dev-windows-amd64'
$outputFile = Join-Path $outputDirectory 'kafka-mcp-server.exe'
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
Push-Location $moduleRoot
try {
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    & go build -trimpath -o $outputFile .
    if ($LASTEXITCODE -ne 0) { throw "go build failed: $LASTEXITCODE" }
    $licenseDirectory = Join-Path $outputDirectory 'kafka-mcp-licenses'
    New-Item -ItemType Directory -Path $licenseDirectory -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $moduleRoot 'LICENSE'), (Join-Path $moduleRoot 'NOTICE') -Destination $licenseDirectory -Force
    Copy-Item -LiteralPath (Join-Path $moduleRoot 'licenses') -Destination $licenseDirectory -Recurse -Force
    Write-Output $outputFile
} finally {
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
