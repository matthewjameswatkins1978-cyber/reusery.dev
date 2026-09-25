#Requires -Version 7
<#
.SYNOPSIS
  Installs the pinned Go developer tools into GOPATH/bin.
#>
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pinned versions — keep in sync with docs/engineering.md and .github/workflows/ci.yml.
$GolangciLintVersion = 'v2.14.0'
$GovulncheckVersion = 'v1.8.0' # golang.org/x/vuln release; keep pinned
$SqlcVersion = 'v1.31.1'
$GooseVersion = 'v3.28.0'

Write-Host "==> Installing golangci-lint $GolangciLintVersion (binary download)"
$triplet = 'windows-amd64'
$archive = "golangci-lint-$($GolangciLintVersion.TrimStart('v'))-$triplet.zip"
$url = "https://github.com/golangci/golangci-lint/releases/download/$GolangciLintVersion/$archive"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) $archive
Invoke-WebRequest -Uri $url -OutFile $tmp
$dest = Join-Path ([System.IO.Path]::GetTempPath()) 'golangci-lint-install'
if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
New-Item -ItemType Directory -Path $dest | Out-Null
Expand-Archive -Path $tmp -DestinationPath $dest
$binDir = Join-Path (go env GOPATH) 'bin'
New-Item -ItemType Directory -Path $binDir -Force | Out-Null
$exe = Get-ChildItem -Path $dest -Recurse -Filter 'golangci-lint.exe' | Select-Object -First 1
Copy-Item -Force $exe.FullName (Join-Path $binDir 'golangci-lint.exe')
Remove-Item -Force $tmp
Remove-Item -Recurse -Force $dest

Write-Host "==> Installing govulncheck $GovulncheckVersion"
go install "golang.org/x/vuln/cmd/govulncheck@$GovulncheckVersion"

Write-Host "==> Installing sqlc $SqlcVersion"
go install "github.com/sqlc-dev/sqlc/cmd/sqlc@$SqlcVersion"

Write-Host "==> Installing goose $GooseVersion"
go install "github.com/pressly/goose/v3/cmd/goose@$GooseVersion"

Write-Host ""
Write-Host "Done. Verify with: golangci-lint version; govulncheck -version; sqlc version; goose -version"
