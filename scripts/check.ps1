#Requires -Version 7
<#
.SYNOPSIS
  Runs the full Reusery.dev verification (Windows equivalent of `make check`).
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
Invoke-Step 'gofmt -l -w .'
Invoke-Step 'go vet ./...'
Invoke-Step 'go test ./...'
Invoke-Step 'golangci-lint run ./...'
Invoke-Step 'govulncheck ./...'
Invoke-Step 'go build ./cmd/reusery'

Write-Host ""
Write-Host "All checks passed."
