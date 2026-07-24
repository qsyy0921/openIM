[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('bootstrap', 'denied', 'revoke', 'version', 'cleanup')]
    [string]$Phase,

    [Parameter(Mandatory = $true)]
    [string]$CredentialPath,

    [Parameter(Mandatory = $true)]
    [string]$FixtureDirectory,

    [Parameter(Mandatory = $true)]
    [string]$StatePath,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory,

    [string]$BaseURL = 'https://172.31.50.2:3443',

    [switch]$ContractOnly
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Get-RequiredString {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Value,
        [Parameter(Mandatory = $true)]
        [string]$Label
    )
    if ($Value -isnot [string] -or [string]::IsNullOrWhiteSpace($Value)) {
        throw "$Label is missing"
    }
    return $Value
}

function Assert-UnderRuntime {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [Parameter(Mandatory = $true)]
        [string]$RuntimeRoot,
        [Parameter(Mandatory = $true)]
        [string]$Label
    )
    $prefix = $RuntimeRoot.TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    ) + [IO.Path]::DirectorySeparatorChar
    if (-not $Path.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "$Label must stay under the ignored .runtime directory"
    }
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repositoryRoot = Split-Path -Parent $scriptRoot
$webRoot = Join-Path $repositoryRoot 'platform\apps\web'
$runtimeRoot = [IO.Path]::GetFullPath((Join-Path $repositoryRoot '.runtime'))
New-Item -ItemType Directory -Path $runtimeRoot -Force | Out-Null

$credentialPathResolved = (Resolve-Path -LiteralPath $CredentialPath).Path
$fixtureDirectoryResolved = (Resolve-Path -LiteralPath $FixtureDirectory).Path
$manifestPath = Join-Path $fixtureDirectoryResolved 'manifest.json'
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw 'enterprise RAG fixture manifest is missing'
}

$stateParent = Split-Path -Parent ([IO.Path]::GetFullPath($StatePath))
if (-not (Test-Path -LiteralPath $stateParent -PathType Container)) {
    throw 'enterprise RAG state parent directory is missing'
}
$statePathResolved = [IO.Path]::GetFullPath($StatePath)
if ($Phase -ne 'bootstrap' -and -not (Test-Path -LiteralPath $statePathResolved -PathType Leaf)) {
    throw "enterprise RAG state is required for phase $Phase"
}

$outputDirectoryResolved = [IO.Path]::GetFullPath($OutputDirectory)
Assert-UnderRuntime -Path $credentialPathResolved -RuntimeRoot $runtimeRoot -Label 'CredentialPath'
Assert-UnderRuntime -Path $fixtureDirectoryResolved -RuntimeRoot $runtimeRoot -Label 'FixtureDirectory'
Assert-UnderRuntime -Path $statePathResolved -RuntimeRoot $runtimeRoot -Label 'StatePath'
Assert-UnderRuntime -Path $outputDirectoryResolved -RuntimeRoot $runtimeRoot -Label 'OutputDirectory'
New-Item -ItemType Directory -Path $outputDirectoryResolved -Force | Out-Null

$baseUri = $null
if (-not [Uri]::TryCreate($BaseURL, [UriKind]::Absolute, [ref]$baseUri) -or
    $baseUri.Scheme -ne 'https' -or
    $baseUri.Host -ne '172.31.50.2' -or
    $baseUri.Port -ne 3443 -or
    $baseUri.AbsolutePath -ne '/') {
    throw 'BaseURL must be the canonical Node2 HTTPS origin'
}

$credentials = Get-Content -LiteralPath $credentialPathResolved -Raw | ConvertFrom-Json
$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
if ($credentials.schema_version -ne 1 -or
    $manifest.schema_version -ne 1 -or
    $credentials.batch_id -ne $manifest.batch_id) {
    throw 'identity and fixture contracts do not belong to the same acceptance batch'
}

$authorized = $credentials.members.authorized
$denied = $credentials.members.denied
$environmentValues = [ordered]@{
    OPENIM_E2E_BASE_URL = $baseUri.AbsoluteUri.TrimEnd('/')
    OPENIM_E2E_PHASE = $Phase
    OPENIM_E2E_FIXTURE_DIR = $fixtureDirectoryResolved
    OPENIM_E2E_STATE_PATH = $statePathResolved
    OPENIM_E2E_OUTPUT_DIR = $outputDirectoryResolved
    OPENIM_E2E_USERNAME = (Get-RequiredString -Value $authorized.username -Label 'authorized username')
    OPENIM_E2E_PASSWORD = (Get-RequiredString -Value $authorized.password -Label 'authorized password')
    OPENIM_E2E_MEMBER_DISPLAY_NAME = (Get-RequiredString -Value $authorized.display_name -Label 'authorized display name')
    OPENIM_E2E_DENIED_USERNAME = (Get-RequiredString -Value $denied.username -Label 'denied username')
    OPENIM_E2E_DENIED_PASSWORD = (Get-RequiredString -Value $denied.password -Label 'denied password')
}

if ($ContractOnly) {
    Write-Output "enterprise_rag_web_contract=valid phase=$Phase batch_id=$($credentials.batch_id)"
    $credentials = $null
    $authorized = $null
    $denied = $null
    $environmentValues = $null
    return
}

$previousValues = @{}
try {
    foreach ($entry in $environmentValues.GetEnumerator()) {
        $previousValues[$entry.Key] = [Environment]::GetEnvironmentVariable(
            $entry.Key,
            [EnvironmentVariableTarget]::Process
        )
        [Environment]::SetEnvironmentVariable(
            $entry.Key,
            [string]$entry.Value,
            [EnvironmentVariableTarget]::Process
        )
    }
    & npm.cmd --prefix $webRoot run test:e2e:node2:enterprise-rag
    if ($LASTEXITCODE -ne 0) {
        throw "enterprise RAG Web phase $Phase failed"
    }
    Write-Output "enterprise_rag_web_phase=$Phase status=passed"
} finally {
    foreach ($key in $environmentValues.Keys) {
        [Environment]::SetEnvironmentVariable(
            $key,
            $previousValues[$key],
            [EnvironmentVariableTarget]::Process
        )
    }
    $credentials = $null
    $authorized = $null
    $denied = $null
    $environmentValues = $null
}
