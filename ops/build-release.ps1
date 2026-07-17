[CmdletBinding()]
param(
    [string]$Version = "0.1.0-dev",
    [string]$WebPublicOrigin = "",
    [string]$WebOIDCAuthority = "",
    [string]$WebOpenIMWSURL = ""
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$output = Join-Path $root ".runtime\release\$Version"
$api = Join-Path $root "platform\services\platform-api"
$worker = Join-Path $root "platform\services\intelligence-worker"
$webApp = Join-Path $root "platform\apps\web"

$webValues = @($WebPublicOrigin, $WebOIDCAuthority, $WebOpenIMWSURL)
$includeWeb = [bool]($webValues | Where-Object { $_ })
if ($includeWeb -and ($webValues | Where-Object { -not $_ }).Count -gt 0) {
    throw "WebPublicOrigin, WebOIDCAuthority, and WebOpenIMWSURL must be provided together"
}

if (Test-Path $output) {
    $resolvedOutput = (Resolve-Path $output).Path
    $runtimeRoot = (Resolve-Path (Join-Path $root ".runtime")).Path
    if (-not $resolvedOutput.StartsWith($runtimeRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "refusing to remove output outside .runtime"
    }
    Remove-Item -LiteralPath $resolvedOutput -Recurse -Force
}

$windows = New-Item -ItemType Directory -Force (Join-Path $output "windows-amd64")
$linux = New-Item -ItemType Directory -Force (Join-Path $output "linux-amd64")
$python = New-Item -ItemType Directory -Force (Join-Path $output "python")
$commands = @("platform-api", "platform-ingress", "platform-migrate", "agent-runtime", "action-executor", "agent-catalog-admin")

Push-Location $api
try {
    foreach ($command in $commands) {
        $env:GOOS = "windows"
        $env:GOARCH = "amd64"
        $env:CGO_ENABLED = "0"
        go build -trimpath -o (Join-Path $windows "$command.exe") "./cmd/$command"
        if ($LASTEXITCODE -ne 0) { throw "Windows build failed for $command" }

        $env:GOOS = "linux"
        go build -trimpath -o (Join-Path $linux $command) "./cmd/$command"
        if ($LASTEXITCODE -ne 0) { throw "Linux build failed for $command" }
    }
}
finally {
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

Push-Location $worker
try {
    python -m pip wheel . --no-deps --wheel-dir $python
    if ($LASTEXITCODE -ne 0) { throw "Python wheel build failed" }
}
finally {
    Pop-Location
}

if ($includeWeb) {
    $web = New-Item -ItemType Directory -Force (Join-Path $output "web")
    $webEnvironment = @{
        VITE_OIDC_AUTHORITY = $WebOIDCAuthority.TrimEnd("/")
        VITE_OIDC_CLIENT_ID = "platform-api"
        VITE_OIDC_REDIRECT_URI = "$($WebPublicOrigin.TrimEnd('/'))/auth/callback"
        VITE_OIDC_POST_LOGOUT_REDIRECT_URI = "$($WebPublicOrigin.TrimEnd('/'))/"
        VITE_PLATFORM_API_BASE_URL = "/platform-api"
        VITE_DEVICE_ID = "ubuntu-web"
        VITE_PLATFORM_API_PROXY_TARGET = "http://127.0.0.1:18080"
        VITE_OPENIM_API_URL = "$($WebPublicOrigin.TrimEnd('/'))/openim-api"
        VITE_OPENIM_API_PROXY_TARGET = "http://127.0.0.1:12002"
        VITE_OPENIM_WS_URL = $WebOpenIMWSURL
    }
    $previousWebEnvironment = @{}
    foreach ($entry in $webEnvironment.GetEnumerator()) {
        $previousWebEnvironment[$entry.Key] = [Environment]::GetEnvironmentVariable($entry.Key, "Process")
        [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, "Process")
    }
    Push-Location $webApp
    try {
        npm run build
        if ($LASTEXITCODE -ne 0) { throw "Web production build failed" }
        Copy-Item (Join-Path $webApp "dist\*") $web -Recurse -Force
    }
    finally {
        Pop-Location
        foreach ($entry in $previousWebEnvironment.GetEnumerator()) {
            [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, "Process")
        }
    }
}

Copy-Item (Join-Path $root "dependencies\openim.lock.yaml") (Join-Path $output "openim.lock.yaml")
Copy-Item (Join-Path $root "contracts") (Join-Path $output "contracts") -Recurse

$previousErrorPreference = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$sourceCommit = git -C $root rev-parse --verify HEAD 2>$null
if ($LASTEXITCODE -ne 0) {
    $sourceCommit = $null
}
$workingTreeState = git -C $root status --porcelain
$ErrorActionPreference = $previousErrorPreference

$metadata = [ordered]@{
    version = $Version
    source_commit = $sourceCommit
    working_tree_clean = -not [bool]$workingTreeState
    go_version = (go version)
    python_version = (python --version)
    web_public_origin = if ($includeWeb) { $WebPublicOrigin.TrimEnd("/") } else { $null }
    generated_at_utc = [DateTime]::UtcNow.ToString("o")
}
$metadata | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $output "release-metadata.json")

$hashes = Get-ChildItem $output -Recurse -File |
    Where-Object Name -ne "SHA256SUMS" |
    Sort-Object FullName |
    ForEach-Object {
        $relative = $_.FullName.Substring($output.Length).TrimStart("\", "/").Replace("\", "/")
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
        "$hash  $relative"
    }
$hashes | Set-Content -Encoding ascii (Join-Path $output "SHA256SUMS")

Write-Output $output
