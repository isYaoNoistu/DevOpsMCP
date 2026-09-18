# Package all six MCP servers into deploy/dist; no arguments.
if ($args.Count -ne 0) { throw "This script takes no arguments" }
function Resolve-Python {
    foreach ($name in @("python", "python3")) {
        $cmd = Get-Command $name -ErrorAction SilentlyContinue
        if ($cmd) {
            & $cmd.Source -c "import sys; sys.exit(sys.version_info < (3, 10))" 2>$null
            if ($LASTEXITCODE -eq 0) { return @{ File = $cmd.Source; Prefix = @() } }
        }
    }
    $py = Get-Command py -ErrorAction SilentlyContinue
    if ($py) {
        & $py.Source -3 -c "import sys; sys.exit(sys.version_info < (3, 10))" 2>$null
        if ($LASTEXITCODE -eq 0) { return @{ File = $py.Source; Prefix = @("-3") } }
    }
    throw "need python3, python, or the Windows py launcher"
}

$ErrorActionPreference = "Stop"
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$Py = Resolve-Python
$compileArgs = $Py.Prefix + @((Join-Path $Here "compile.py"), "windows")
& $Py.File @compileArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
