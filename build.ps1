<#
.SYNOPSIS
    Cross-platform build script for Ciel Agent Go (PowerShell).
.DESCRIPTION
    Builds binaries for linux, darwin, and windows on amd64 + arm64.
.PARAMETER Version
    Version string to embed. Defaults to "dev".
.EXAMPLE
    .\build.ps1
    .\build.ps1 -Version "1.0.0"
#>
param(
    [string]$Version = "dev"
)

$ErrorActionPreference = "Stop"

$AppName = "ciel"
$BuildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$LdFlags = "-s -w -X main.version=$Version -X main.buildTime=$BuildTime"
$Dist = "dist"

Write-Host "Building $AppName v$Version" -ForegroundColor Cyan
Write-Host "   LDFLAGS: $LdFlags"
Write-Host ""

# Clean previous build
if (Test-Path $Dist) {
    Remove-Item -Recurse -Force $Dist
}
New-Item -ItemType Directory -Path $Dist | Out-Null

$Success = 0
$Fail = 0

function Format-FileSize {
    param([long]$Length)
    if ($Length -gt 1MB) { return "{0:N1} MB" -f ($Length / 1MB) }
    if ($Length -gt 1KB) { return "{0:N1} KB" -f ($Length / 1KB) }
    return "$Length bytes"
}

# Target list: OS, Arch, Extension
$Targets = @(
    @("linux",   "amd64", ""),
    @("linux",   "arm64", ""),
    @("darwin",  "amd64", ""),
    @("darwin",  "arm64", ""),
    @("windows", "amd64", ".exe"),
    @("windows", "arm64", ".exe")
)

foreach ($Target in $Targets) {
    $os   = $Target[0]
    $arch = $Target[1]
    $ext  = $Target[2]
    $output = Join-Path $Dist "$AppName-$os-$arch$ext"

    $label = "$os/$arch..."
    Write-Host ("  {0,-40}" -f $label) -NoNewline

    $env:GOOS = $os
    $env:GOARCH = $arch
    $env:CGO_ENABLED = "0"

    $buildResult = & go build -trimpath -ldflags $LdFlags -o $output ./cmd/hermes 2>&1
    $exitCode = $LASTEXITCODE

    if ($exitCode -eq 0) {
        $size = (Get-Item $output).Length
        $sizeStr = Format-FileSize -Length $size
        Write-Host " OK ($sizeStr)" -ForegroundColor Green
        $Success++
    } else {
        Write-Host " FAILED" -ForegroundColor Red
        $Fail++
    }
}

# Restore env
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "Build complete: $Success succeeded, $Fail failed" -ForegroundColor Cyan
Write-Host "   Output: $Dist/"
Write-Host ""
Get-ChildItem $Dist | ForEach-Object {
    $sizeStr = Format-FileSize -Length $_.Length
    Write-Host ("   {0,-42} {1}" -f $_.Name, $sizeStr)
}
Write-Host ""

if ($Fail -gt 0) { exit 1 }
