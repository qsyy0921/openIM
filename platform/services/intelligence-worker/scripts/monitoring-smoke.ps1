[CmdletBinding()]
param(
    [int]$Port = 18082,
    [string]$PrometheusURL = "http://127.0.0.1:19091"
)

$ErrorActionPreference = "Stop"
$workerRoot = Split-Path -Parent $PSScriptRoot
$python = Join-Path $workerRoot ".venv\Scripts\python.exe"
if (-not (Test-Path -LiteralPath $python)) {
    throw "Run 'uv sync --extra test' before the monitoring smoke."
}
if (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) {
    throw "Port $Port is already in use."
}

# The smoke still verifies the only configured generation route at startup.
$env:INTELLIGENCE_HTTP_PORT = [string]$Port
$env:INTELLIGENCE_EMBEDDING_BASE_URL = "https://invalid.local"
$env:INTELLIGENCE_EMBEDDING_API_KEY = "metrics-smoke-not-a-credential"
$env:INTELLIGENCE_EMBEDDING_MODEL = "metrics-smoke"
$env:INTELLIGENCE_EMBEDDING_DIMENSION = "8"
$env:INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS = "1"
$env:INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY = "0.2"

$stdout = Join-Path $env:TEMP "openim-intelligence-metrics-smoke.out.log"
$stderr = Join-Path $env:TEMP "openim-intelligence-metrics-smoke.err.log"
$process = $null
try {
    $process = Start-Process -FilePath $python `
        -ArgumentList @("-m", "intelligence_worker.local_bootstrap") `
        -WorkingDirectory $workerRoot `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr `
        -WindowStyle Hidden `
        -PassThru

    $deadline = (Get-Date).AddSeconds(30)
    do {
        try {
            $health = Invoke-RestMethod "http://127.0.0.1:$Port/healthz"
            break
        } catch {
            if ($process.HasExited) {
                Get-Content -LiteralPath $stderr -Tail 80
                throw "Intelligence Worker exited before becoming healthy."
            }
            Start-Sleep -Milliseconds 500
        }
    } while ((Get-Date) -lt $deadline)
    if ($health.status -ne "ok") {
        throw "Intelligence Worker did not become healthy."
    }

    $metrics = (Invoke-WebRequest "http://127.0.0.1:$Port/metrics" -UseBasicParsing).Content
    if ($metrics -notmatch "openim_intelligence_http_requests_total") {
        throw "Intelligence Worker metrics are missing."
    }

    $scraped = $false
    try {
        $scrapeDeadline = (Get-Date).AddSeconds(25)
        do {
            $targets = (Invoke-RestMethod "$PrometheusURL/api/v1/targets").data.activeTargets
            $target = $targets | Where-Object { $_.labels.job -eq "openim-intelligence-worker" }
            if ($target.health -eq "up") {
                $scraped = $true
                break
            }
            Start-Sleep -Seconds 1
        } while ((Get-Date) -lt $scrapeDeadline)
    } catch {
        $scraped = $false
    }

    [PSCustomObject]@{
        worker_health = $health.status
        metrics_exposed = $true
        prometheus_scraped = $scraped
    } | ConvertTo-Json -Compress
} finally {
    if ($null -ne $process -and -not $process.HasExited) {
        Stop-Process -Id $process.Id -Force
        $process.WaitForExit()
    }
}
