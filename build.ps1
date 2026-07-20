# build.ps1 - PowerShell build script for the Skink compiler
# Replicates the functionality of Makefile for Windows.
#
# Usage:
#   .\build.ps1              - build the skink compiler (default)
#   .\build.ps1 build        - same as above
#   .\build.ps1 static       - build a statically linked binary
#   .\build.ps1 install      - install the compiler and standard library
#   .\build.ps1 uninstall    - remove installed files
#   .\build.ps1 test         - run standard library tests
#   .\build.ps1 test-pattern - run tests matching a pattern (-Pattern <name>)
#   .\build.ps1 clean        - remove build artifacts
#   .\build.ps1 fmt          - run go fmt
#   .\build.ps1 vet          - run go vet
#   .\build.ps1 dev          - build with race detector
#
# Override defaults:
#   .\build.ps1 install -Prefix "C:\Skink"
#   .\build.ps1 build -BuildFlags "-race -tags=foo"
#   .\build.ps1 test-pattern -Pattern "errors"

[CmdletBinding()]
param(
    [Parameter(Position=0)]
    [ValidateSet("build","static","install","install-deps","install-only","uninstall","test","test-pattern","test-std","clean","fmt","vet","dev","all","help")]
    [string]$Target = "build",

    [string]$Prefix = "$env:LOCALAPPDATA\Programs\Skink",
    [string]$Go = "go",
    [string]$BuildFlags = "",
    [string]$Pattern = "*"
)

$ErrorActionPreference = "Stop"

# --- Configuration ---
$BINDIR      = Join-Path $Prefix "bin"
$LIBDIR      = Join-Path $Prefix "lib\skink"
$TEST_DIR    = ".\std"
$SKINK_SRC   = ".\cmd\skink"
$SKINK_BIN   = "skink.exe"

function Build {
    Write-Host "Building skink compiler..."
    Push-Location "compiler"
    try {
        $goArgs = @("build")
        if ($BuildFlags) {
            $goArgs += $BuildFlags -split '\s+'
        }
        $goArgs += @("-o", "..\$SKINK_BIN", $SKINK_SRC)
        & $Go @goArgs
        if ($LASTEXITCODE -ne 0) { throw "Build failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
    Write-Host "Built: $SKINK_BIN"
}

function Build-Static {
    Write-Host "Building statically linked skink compiler..."
    $oldCGO = $env:CGO_ENABLED
    $env:CGO_ENABLED = "1"
    Push-Location "compiler"
    try {
        $ldflags = "-linkmode external -extldflags '-static'"
        & $Go build -ldflags $ldflags -o "..\$SKINK_BIN" $SKINK_SRC
        if ($LASTEXITCODE -ne 0) { throw "Static build failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
        if ($null -ne $oldCGO) {
            $env:CGO_ENABLED = $oldCGO
        } else {
            Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
        }
    }
    Write-Host "Built static binary: $SKINK_BIN"
}

function Install-Deps {
    Write-Host "Installing external C library dependencies for Windows..."
    Write-Host ""

    $installed = @()
    $failed = @()

    # Check if winget is available
    if (!(Get-Command winget -ErrorAction SilentlyContinue)) {
        Write-Warning "winget not found. Please install dependencies manually."
        Write-Host "Required libraries:"
        Write-Host "  - SQLite3 (for std/db)"
        Write-Host "  - OpenSSL (for std/crypto)"
        Write-Host "  - Eclipse Mosquitto (for std/mqtt)"
        Write-Host "  - zlib (for std/compress)"
        Write-Host "  - llama.cpp (for std/llm)"
        return
    }

    # SQLite3 - Note: winget package is CLI tools, not dev library
    Write-Host "Installing SQLite3..."
    try {
        winget install --id SQLite.SQLite -e --accept-source-agreements --accept-package-agreements 2>&1 | Out-Null
        $installed += "SQLite3 (CLI tools)"
        Write-Host "  [OK] SQLite3 installed"
        Write-Warning "  Note: winget installs SQLite CLI tools. For development, you may need the amalgamation source from https://sqlite.org/download.html"
    } catch {
        $failed += "SQLite3"
        Write-Host "  [FAILED] SQLite3 installation failed"
    }

    # OpenSSL (development version)
    Write-Host "Installing OpenSSL (development)..."
    try {
        winget install --id ShiningLight.OpenSSL.Dev -e --accept-source-agreements --accept-package-agreements 2>&1 | Out-Null
        $installed += "OpenSSL"
        Write-Host "  [OK] OpenSSL installed"
    } catch {
        $failed += "OpenSSL"
        Write-Host "  [FAILED] OpenSSL installation failed"
    }

    # Eclipse Mosquitto (MQTT)
    Write-Host "Installing Eclipse Mosquitto (MQTT)..."
    try {
        winget install --id EclipseFoundation.Mosquitto -e --accept-source-agreements --accept-package-agreements 2>&1 | Out-Null
        $installed += "Mosquitto"
        Write-Host "  [OK] Mosquitto installed"
    } catch {
        $failed += "Mosquitto"
        Write-Host "  [FAILED] Mosquitto installation failed"
    }

    # zlib - No winget package available
    Write-Host "zlib (for std/compress)..."
    Write-Host "  [SKIP] zlib not available via winget"
    Write-Host "  Install manually from: https://gnuwin32.sourceforge.io/packages/zlib.htm or build from source"

    # llama.cpp - User may have already installed
    Write-Host "llama.cpp (for std/llm)..."
    if (Get-Command llama-cli -ErrorAction SilentlyContinue) {
        Write-Host "  [OK] llama.cpp already installed"
    } else {
        Write-Host "  [SKIP] llama.cpp not found"
        Write-Host "  Install with: winget install llama.cpp"
    }

    Write-Host ""
    Write-Host "Dependency installation summary:"
    if ($installed.Count -gt 0) {
        Write-Host "  Installed: $($installed -join ', ')"
    }
    if ($failed.Count -gt 0) {
        Write-Host "  Failed: $($failed -join ', ')"
    }
    Write-Host ""
    Write-Host "Note: After installation, you may need to add library paths to your compiler's include/lib search paths."
    Write-Host "See manual/35-external-dependencies.md for details."
}

function Install-Only {
    if (!(Test-Path $SKINK_BIN)) {
        Write-Error "skink.exe not found. Run '.\build.ps1 build' first."
        exit 1
    }
    Write-Host "Installing skink to $BINDIR ..."
    New-Item -ItemType Directory -Force -Path $BINDIR | Out-Null
    Copy-Item $SKINK_BIN (Join-Path $BINDIR "skink.exe") -Force

    Write-Host "Installing standard library to $LIBDIR ..."
    $stdDir = Join-Path $LIBDIR "std"
    New-Item -ItemType Directory -Force -Path $stdDir | Out-Null
    foreach ($file in (Get-ChildItem -Path $TEST_DIR -Filter "*.skink")) {
        Copy-Item $file.FullName $stdDir -Force
    }

    Write-Host "Installing runtime to $LIBDIR ..."
    $rtDir = Join-Path $LIBDIR "runtime"
    New-Item -ItemType Directory -Force -Path $rtDir | Out-Null
    foreach ($file in (Get-ChildItem -Path ".\compiler\runtime" -Filter "*.c")) {
        Copy-Item $file.FullName $rtDir -Force
    }

    Write-Host "Installing lib support files to $(Join-Path $LIBDIR 'lib') ..."
    $libDir = Join-Path $LIBDIR "lib"
    New-Item -ItemType Directory -Force -Path $libDir | Out-Null
    foreach ($file in (Get-ChildItem -Path ".\compiler\lib" -Filter "*.skink")) {
        Copy-Item $file.FullName $libDir -Force
    }

    Write-Host ""
    Write-Host "Installation complete."
    Write-Host "Add $BINDIR to your PATH if not already present."
    Write-Host "Set SKINK_HOME=$LIBDIR to use the installed standard library."
}

function Install {
    Install-Deps
    Build
    Install-Only
}

function Uninstall {
    Write-Host "Removing skink from $BINDIR ..."
    Remove-Item (Join-Path $BINDIR "skink.exe") -ErrorAction SilentlyContinue
    Write-Host "Removing $LIBDIR ..."
    Remove-Item $LIBDIR -Recurse -Force -ErrorAction SilentlyContinue
}

function Test-Stdlib {
    Build
    Write-Host "Running standard library tests in $TEST_DIR ..."
    Push-Location $TEST_DIR
    try {
        & "..\$SKINK_BIN" test
        if ($LASTEXITCODE -ne 0) { throw "Tests failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
}

function Test-Pattern {
    Build
    Write-Host "Running tests matching pattern '$Pattern' ..."
    Push-Location $TEST_DIR
    try {
        & "..\$SKINK_BIN" test $Pattern
        if ($LASTEXITCODE -ne 0) { throw "Tests failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
}

function Clean {
    Write-Host "Removing build artifacts ..."
    # Resolve binary path relative to script location so clean works regardless of cwd
    $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { "." }
    $skinkPath = Join-Path $scriptDir $SKINK_BIN
    if (Test-Path $skinkPath) {
        Remove-Item $skinkPath
        Write-Host "  Removed: $skinkPath"
    }
    $removed = 0
    Get-ChildItem -Path $scriptDir -Recurse -Filter "*.o"   -ErrorAction SilentlyContinue | ForEach-Object { Remove-Item $_ -ErrorAction SilentlyContinue; $removed++ }
    Get-ChildItem -Path $scriptDir -Recurse -Filter "*.ll"  -ErrorAction SilentlyContinue | ForEach-Object { Remove-Item $_ -ErrorAction SilentlyContinue; $removed++ }
    Get-ChildItem -Path $scriptDir -Recurse -Filter "*.s"   -ErrorAction SilentlyContinue | ForEach-Object { Remove-Item $_ -ErrorAction SilentlyContinue; $removed++ }
    Get-ChildItem -Path $scriptDir -Recurse -Filter "main" -File -ErrorAction SilentlyContinue | ForEach-Object { Remove-Item $_ -ErrorAction SilentlyContinue; $removed++ }
    Get-ChildItem -Path $scriptDir -Recurse -Directory -Filter ".skink-test-*" -ErrorAction SilentlyContinue | ForEach-Object { Remove-Item $_ -Recurse -Force -ErrorAction SilentlyContinue; $removed++ }
    if ($removed -gt 0) {
        Write-Host "  Removed $removed intermediate artifact(s)."
    }
    Write-Host "Clean complete."
}

function Fmt {
    Write-Host "Running go fmt ..."
    Push-Location "compiler"
    try {
        & $Go fmt ./...
    } finally {
        Pop-Location
    }
}

function Vet {
    Write-Host "Running go vet ..."
    Push-Location "compiler"
    try {
        & $Go vet ./...
    } finally {
        Pop-Location
    }
}

function Build-Dev {
    Write-Host "Building development skink compiler (with race detector)..."
    Push-Location "compiler"
    try {
        & $Go build -race -o "..\$SKINK_BIN" $SKINK_SRC
        if ($LASTEXITCODE -ne 0) { throw "Dev build failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
    Write-Host "Built: $SKINK_BIN (with race detector)"
}

function Show-Help {
    @"
Usage: .\build.ps1 [Target] [Options]

Targets:
  build        Build the skink compiler (default)
  static       Build a statically linked binary
  install      Install compiler, stdlib, runtime, and C library dependencies
  install-deps Install external C library dependencies only (SQLite, OpenSSL, Mosquitto, etc.)
  install-only Install compiler, stdlib, and runtime (skip dependency installation and build)
  uninstall    Remove installed files
  test         Run standard library tests
  test-pattern Run tests matching -Pattern (e.g., -Pattern "errors")
  test-std     Alias for 'test'
  clean        Remove build artifacts
  fmt          Run 'go fmt' on the compiler
  vet          Run 'go vet' on the compiler
  dev          Build with Go race detector
  all          Alias for 'build'
  help         Show this message

Options:
  -Prefix <path>      Installation prefix (default: %LOCALAPPDATA%\Programs\Skink)
  -Go <path>          Go command to use (default: go)
  -BuildFlags <flags> Extra flags passed to 'go build'
  -Pattern <name>     Test pattern for 'test-pattern' (appended with '_test.skink')

Examples:
  .\build.ps1
  .\build.ps1 install -Prefix "C:\Tools\Skink"
  .\build.ps1 install-deps
  .\build.ps1 test-pattern -Pattern "errors"
"@
}

switch ($Target) {
    "all"          { Build }
    "build"        { Build }
    "static"       { Build-Static }
    "install"      { Install }
    "install-deps" { Install-Deps }
    "install-only" { Install-Only }
    "uninstall"    { Uninstall }
    "test"         { Test-Stdlib }
    "test-std"     { Test-Stdlib }
    "test-pattern" { Test-Pattern }
    "clean"        { Clean }
    "fmt"          { Fmt }
    "vet"          { Vet }
    "dev"          { Build-Dev }
    "help"         { Show-Help }
    default        { Show-Help }
}
