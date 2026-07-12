[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$TargetUserID,
    [Parameter(Mandatory = $true)][ValidateSet('image', 'file')][string]$Type,
    [Parameter(Mandatory = $true)][ValidateLength(1, 2048)][string]$URL,
    [Parameter(Mandatory = $true)][ValidateLength(1, 255)][string]$FileName,
    [Parameter(Mandatory = $true)][ValidateRange(1, 104857600)][long]$FileSize,
    [ValidateRange(1, 32768)][int]$Width = 1,
    [ValidateRange(1, 32768)][int]$Height = 1,
    [string]$SshHost = 'openim-node2',
    [string]$Distribution = 'OpenIM-Ubuntu'
)

$ErrorActionPreference = 'Stop'
$uri = [Uri]$URL
if (-not $uri.IsAbsoluteUri -or $uri.Scheme -notin @('http', 'https') -or -not [string]::IsNullOrEmpty($uri.UserInfo)) {
    throw 'Media URL must be an absolute HTTP(S) URL without embedded credentials.'
}

$localHelper = Join-Path $PSScriptRoot 'send-node2-web-e2e-media.sh'
$remoteHelper = 'E:/MFL/ops/send-node2-web-e2e-media.sh'
scp -q -o BatchMode=yes -o StrictHostKeyChecking=yes $localHelper "${SshHost}:$remoteHelper"
if ($LASTEXITCODE -ne 0) {
    throw "E2E helper transfer failed with exit code $LASTEXITCODE."
}

$urlBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($URL))
$nameBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($FileName))
$remoteScriptTemplate = @'
$ErrorActionPreference = 'Stop'
& 'C:\Program Files\WSL\wsl.exe' -d '__DISTRIBUTION__' -u root -- bash /mnt/e/MFL/ops/send-node2-web-e2e-media.sh '__TARGET__' '__TYPE__' '__URL__' '__NAME__' '__SIZE__' '__WIDTH__' '__HEIGHT__'
exit $LASTEXITCODE
'@
$remoteScript = $remoteScriptTemplate.Replace('__DISTRIBUTION__', $Distribution.Replace("'", "''"))
$remoteScript = $remoteScript.Replace('__TARGET__', $TargetUserID)
$remoteScript = $remoteScript.Replace('__TYPE__', $Type)
$remoteScript = $remoteScript.Replace('__URL__', $urlBase64)
$remoteScript = $remoteScript.Replace('__NAME__', $nameBase64)
$remoteScript = $remoteScript.Replace('__SIZE__', [string]$FileSize)
$remoteScript = $remoteScript.Replace('__WIDTH__', [string]$Width)
$remoteScript = $remoteScript.Replace('__HEIGHT__', [string]$Height)
$encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($remoteScript))

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "powershell.exe -NoProfile -NonInteractive -EncodedCommand $encodedCommand"
if ($LASTEXITCODE -ne 0) {
    throw "Node2 E2E media send failed with exit code $LASTEXITCODE."
}
