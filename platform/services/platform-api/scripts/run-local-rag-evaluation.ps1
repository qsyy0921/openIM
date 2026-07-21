[CmdletBinding()]
param(
    [ValidateSet("retrieval", "generation")]
    [string]$Mode = "retrieval",
    [Parameter(Mandatory = $true)]
    [string]$DatabaseURL,
    [Parameter(Mandatory = $true)]
    [string]$TenantID,
    [Parameter(Mandatory = $true)]
    [string]$MemberID,
    [int]$WorkerPort = 18082,
    [string]$EmbeddingBaseURL = "http://127.0.0.1:11434/v1",
    [string]$EmbeddingModel = "qwen3-embedding:4b",
    [int]$EmbeddingDimension = 2560,
    [int]$GenerationAnswerableCases = 20,
    [int]$GenerationUnanswerableCases = 20,
    [switch]$IndexMissing
)

$ErrorActionPreference = "Stop"
$platformRoot = Split-Path -Parent $PSScriptRoot
$servicesRoot = Split-Path -Parent $platformRoot
$repositoryRoot = Resolve-Path (Join-Path $platformRoot "..\..\..")
$workerRoot = Join-Path $servicesRoot "intelligence-worker"
$python = Join-Path $workerRoot ".venv\Scripts\python.exe"
$qaPath = Join-Path $repositoryRoot "datasets\enterprise-knowledge\v1\qa.jsonl"
$outputPath = if ($Mode -eq "generation") {
    Join-Path $platformRoot "eval\enterprise-rag-generation-report.json"
} else {
    Join-Path $platformRoot "eval\enterprise-rag-report.json"
}
if (-not (Test-Path -LiteralPath $python)) {
    throw "Run 'uv sync --extra test' in the intelligence worker before evaluation."
}
if (-not (Test-Path -LiteralPath $qaPath)) {
    throw "Enterprise QA dataset was not found."
}
if (Get-NetTCPConnection -LocalPort $WorkerPort -State Listen -ErrorAction SilentlyContinue) {
    throw "Port $WorkerPort is already in use."
}

# The bootstrap loads the local CLIProxyAPI key directly into the child process.
# Both evaluation modes preserve the single production generation route.
$env:INTELLIGENCE_HTTP_PORT = [string]$WorkerPort
$env:INTELLIGENCE_EMBEDDING_BASE_URL = $EmbeddingBaseURL
$env:INTELLIGENCE_EMBEDDING_API_KEY = "local-embedding"
$env:INTELLIGENCE_EMBEDDING_MODEL = $EmbeddingModel
$env:INTELLIGENCE_EMBEDDING_DIMENSION = [string]$EmbeddingDimension
$env:INTELLIGENCE_EMBEDDING_TIMEOUT_SECONDS = "120"
$env:INTELLIGENCE_ROUTING_DENSE_MIN_SIMILARITY = "0.2"

$stdout = Join-Path $env:TEMP "openim-rag-evaluation-worker.out.log"
$stderr = Join-Path $env:TEMP "openim-rag-evaluation-worker.err.log"
$worker = $null
try {
    $worker = Start-Process -FilePath $python `
        -ArgumentList @("-m", "intelligence_worker.local_bootstrap") `
        -WorkingDirectory $workerRoot `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr `
        -WindowStyle Hidden `
        -PassThru

    $deadline = (Get-Date).AddSeconds(30)
    do {
        try {
            $health = Invoke-RestMethod "http://127.0.0.1:$WorkerPort/healthz"
            break
        } catch {
            if ($worker.HasExited) {
                Get-Content -LiteralPath $stderr -Tail 80
                throw "Intelligence Worker exited before RAG evaluation."
            }
            Start-Sleep -Milliseconds 500
        }
    } while ((Get-Date) -lt $deadline)
    if ($health.status -ne "ok") {
        throw "Intelligence Worker did not become healthy."
    }

    Push-Location $platformRoot
    try {
        if ($IndexMissing) {
            & go run ./cmd/knowledge-rag-admin `
                -mode index `
                -database-url $DatabaseURL `
                -intelligence-url "http://127.0.0.1:$WorkerPort" `
                -model $EmbeddingModel `
                -dimension $EmbeddingDimension `
                -dense-minimum 0.45 `
                -max-candidates 4096 `
                -batch-size 32 `
                -timeout 120s | Out-Null
            if ($LASTEXITCODE -ne 0) {
                throw "Enterprise RAG indexing failed."
            }
        }
        $commandMode = if ($Mode -eq "generation") { "evaluate-generation" } else { "evaluate" }
        $arguments = @(
            "run", "./cmd/knowledge-rag-admin",
            "-mode", $commandMode,
            "-database-url", $DatabaseURL,
            "-intelligence-url", "http://127.0.0.1:$WorkerPort",
            "-model", $EmbeddingModel,
            "-dimension", $EmbeddingDimension,
            "-dense-minimum", "0.45",
            "-max-candidates", "4096",
            "-qa", $qaPath,
            "-tenant-id", $TenantID,
            "-member-id", $MemberID,
            "-limit", "8",
            "-output", $outputPath,
            "-timeout", "120s"
        )
        if ($Mode -eq "generation") {
            $arguments += @(
                "-generation-model", "gpt-5.6-luna",
                "-generation-seed", "enterprise-rag-generation-v1",
                "-generation-answerable", $GenerationAnswerableCases,
                "-generation-unanswerable", $GenerationUnanswerableCases
            )
        }
        & go @arguments | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "Enterprise RAG evaluation failed."
        }
    } finally {
        Pop-Location
    }

    $report = Get-Content -LiteralPath $outputPath -Raw | ConvertFrom-Json
    if ($Mode -eq "generation") {
        [PSCustomObject]@{
            cases = $report.cases
            model = $report.model
            sample_digest = $report.sample_digest
            candidate_contract_success_rate = $report.candidate_contract_success_rate
            grounding_decision_accuracy = $report.grounding_decision_accuracy
            abstention_accuracy = $report.abstention_accuracy
            required_fact_coverage = $report.required_fact_coverage
            generated_citation_precision = $report.generated_citation_precision
            generated_citation_recall = $report.generated_citation_recall
            citation_syntax_integrity = $report.citation_syntax_integrity
            end_to_end_success_rate = $report.end_to_end_success_rate
            production_gate_evaluated = $report.production_gate_evaluated
        } | ConvertTo-Json -Compress
    } else {
        [PSCustomObject]@{
            cases = $report.cases
            recall_at_k = $report.recall_at_k
            mrr = $report.mrr
            retrieval_precision_at_k = $report.retrieval_precision_at_k
            provenance_integrity = $report.provenance_integrity
            unanswerable_retrieval_empty_rate = $report.unanswerable_retrieval_empty_rate
            generation_abstention_evaluated = $report.generation_abstention_evaluated
        } | ConvertTo-Json -Compress
    }
} finally {
    if ($null -ne $worker -and -not $worker.HasExited) {
        Stop-Process -Id $worker.Id -Force
        $worker.WaitForExit()
    }
}
