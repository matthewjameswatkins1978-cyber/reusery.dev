#Requires -Version 7
<#
.SYNOPSIS
  Runs the full Reusery.dev verification (Windows equivalent of `make check`).

.DESCRIPTION
  Requires golangci-lint, govulncheck, sqlc and goose on PATH (see
  scripts/install-tools.ps1). The integration step requires Docker.
#>
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-Step {
    param([Parameter(Mandatory)][string]$Name)
    Write-Host ""
    Write-Host "==> $Name"
    Invoke-Expression $Name
    if ($LASTEXITCODE -ne 0) {
        throw "FAILED: $Name (exit code $LASTEXITCODE)"
    }
}

# Format first (idempotent write), so later steps always see formatted code.
Invoke-Step 'gofmt -s -l -w .'
Invoke-Step 'sqlc generate'
Invoke-Step 'git diff --exit-code -- internal/store/postgres/sqlc'
Invoke-Step 'go vet ./...'
Invoke-Step 'go test ./...'
Invoke-Step 'go test -tags=integration ./...'
Invoke-Step 'golangci-lint run ./...'
Invoke-Step 'govulncheck ./...'
Invoke-Step 'go build ./cmd/reusery'

Write-Host ""
Write-Host "All checks passed."
