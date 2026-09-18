param([string]$Version = '0.2.0-trial')
$ErrorActionPreference = 'Stop'
$moduleRoot = Split-Path -Parent $PSScriptRoot
$repoRoot = Split-Path -Parent $moduleRoot
$workspaceRoot = Split-Path -Parent $repoRoot
$outputDirectory = Join-Path $workspaceRoot 'dist/devopsmcp-windows-amd64'
$outputFile = Join-Path $outputDirectory 'kafka-mcp-server.exe'
$stagedFile = Join-Path $outputDirectory ('kafka-mcp-build-' + [guid]::NewGuid().ToString('N') + '.exe')
if ($Version -notmatch '^[0-9A-Za-z.+-]+$') { throw 'Version must contain only letters, digits, dots, plus and hyphens' }
function Get-SourceManifest {
    $paths = @(& git -C $repoRoot ls-files --cached --others --exclude-standard -- kafka-mcp-server)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate module source files' }
    @($paths | Sort-Object -Unique | ForEach-Object {
        $sourcePath = Join-Path $repoRoot $_
        if (Test-Path -LiteralPath $sourcePath -PathType Leaf) {
            [ordered]@{ path = $_; sha256 = (Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash.ToLowerInvariant() }
        }
    })
}
function Assert-BinaryNotRunning {
    $running = @(Get-Process -Name 'kafka-mcp-server' -ErrorAction SilentlyContinue | Where-Object {
        try { $_.Path -eq $outputFile } catch { $true }
    })
    if ($running.Count -gt 0) { throw 'The output binary is running. Disconnect its MCP client before rebuilding; the existing binary is preserved.' }
}
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
Push-Location $moduleRoot
try {
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $commit = (& git -C $repoRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot determine source commit' }
    $revision = (& git -C $repoRoot rev-parse --short HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot determine source revision' }
    $changes = @(& git -C $repoRoot status --porcelain --untracked-files=normal)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot determine source dirty state' }
    $dirty = $changes.Count -gt 0
    if ($dirty) { $revision += '-dirty' }
    $buildTime = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $sourceManifest = @(Get-SourceManifest)
    $sourceJSON = ConvertTo-Json -InputObject $sourceManifest -Depth 5 -Compress
    $goVersion = (& go version).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot determine Go version' }
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    $ldflags = "-X main.version=$Version -X main.revision=$revision -X main.buildTime=$buildTime"
    # Build to a unique path: Go must never rename the existing executable to .exe~.
    & go build -trimpath -ldflags $ldflags -o $stagedFile .
    if ($LASTEXITCODE -ne 0) { throw "go build failed: $LASTEXITCODE" }
    $currentSourceJSON = ConvertTo-Json -InputObject @(Get-SourceManifest) -Depth 5 -Compress
    if ($sourceJSON -cne $currentSourceJSON) { throw 'Module source changed during build; retry after edits finish' }
    $versionOutput = (& $stagedFile --version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or !$versionOutput.Contains($Version) -or !$versionOutput.Contains($revision) -or !$versionOutput.Contains($buildTime)) {
        throw 'Built executable did not report the requested version, revision and timestamp'
    }
    try {
        Assert-BinaryNotRunning
        Move-Item -LiteralPath $stagedFile -Destination $outputFile -Force
    } catch {
        # Keep a reviewable alternative if the active client has locked the old binary.
        $outputFile = Join-Path $outputDirectory ('kafka-mcp-server-' + $Version + '-' + $revision + '-' + [DateTime]::UtcNow.ToString('yyyyMMddHHmmss') + '.exe')
        Move-Item -LiteralPath $stagedFile -Destination $outputFile
        Write-Warning 'Default binary could not be replaced and was preserved. A versioned executable was produced; reconnect manually to use it.'
    }
    $binaryName = Split-Path -Leaf $outputFile
    $binaryHash = (Get-FileHash -LiteralPath $outputFile -Algorithm SHA256).Hash.ToLowerInvariant()
    [System.IO.File]::WriteAllText(($outputFile + '.sha256'), "$binaryHash  $binaryName`n", [System.Text.UTF8Encoding]::new($false))
    $licenseDirectory = Join-Path $outputDirectory 'kafka-mcp-licenses'
    New-Item -ItemType Directory -Path $licenseDirectory -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $moduleRoot 'LICENSE'), (Join-Path $moduleRoot 'NOTICE') -Destination $licenseDirectory -Force
    Copy-Item -LiteralPath (Join-Path $moduleRoot 'licenses') -Destination $licenseDirectory -Recurse -Force
    $docsRoot = Join-Path $outputDirectory 'kafka-mcp-docs'
    $moduleDocs = Join-Path $docsRoot 'kafka-mcp-server'
    $skillDocs = Join-Path $docsRoot '.cursor/skills/kafka'
    New-Item -ItemType Directory -Path $moduleDocs, $skillDocs -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $moduleRoot 'README.md') -Destination $moduleDocs -Force
    Copy-Item -LiteralPath (Join-Path $moduleRoot 'VALIDATION.md') -Destination $moduleDocs -Force
    $scriptDocs = Join-Path $moduleDocs 'scripts'
    New-Item -ItemType Directory -Path $scriptDocs -Force | Out-Null
    foreach ($script in @('smoke.py', 'acceptance.py')) {
        Copy-Item -LiteralPath (Join-Path $moduleRoot "scripts/$script") -Destination $scriptDocs -Force
    }
    # Explicit public example allowlist: never copy local target files or credentials.
    $exampleDocs = Join-Path $moduleDocs 'examples'
    New-Item -ItemType Directory -Path $exampleDocs -Force | Out-Null
    foreach ($example in @('targets.dev.json', 'targets.tls-scram.json', 'tool-calls.json')) {
        Copy-Item -LiteralPath (Join-Path $moduleRoot "examples/$example") -Destination $exampleDocs -Force
    }
    Copy-Item -LiteralPath (Join-Path $repoRoot '.cursor/skills/kafka/SKILL.md') -Destination $skillDocs -Force
    Copy-Item -LiteralPath (Join-Path $repoRoot '.cursor/skills/kafka/references') -Destination $skillDocs -Recurse -Force
    $buildInfo = [ordered]@{
        binary = $binaryName; sha256 = $binaryHash
        version = $Version; revision = $revision; build_time = $buildTime
        source = [ordered]@{ commit = $commit; dirty = $dirty; module = 'kafka-mcp-server'; files = $sourceManifest }
        build = [ordered]@{ go_version = $goVersion; goos = 'windows'; goarch = 'amd64'; cgo_enabled = '0'; trimpath = $true; ldflags = $ldflags }
        version_output = $versionOutput
    }
    [System.IO.File]::WriteAllText((Join-Path $outputDirectory 'kafka-mcp-server.build-info.json'), (ConvertTo-Json -InputObject $buildInfo -Depth 8) + "`n", [System.Text.UTF8Encoding]::new($false))
    Write-Output $outputFile
} finally {
    if (Test-Path -LiteralPath $stagedFile -PathType Leaf) { Remove-Item -LiteralPath $stagedFile -Force }
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
