[CmdletBinding()]
param(
    [ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$PeerUserID = 'lifecyclePeer',
    [ValidateRange(1, 11)][int]$PlatformID = 5,
    [string]$SshHost = 'openim-node2',
    [string]$Distribution = 'OpenIM-Ubuntu'
)

$ErrorActionPreference = 'Stop'
if ($PlatformID -eq 10) { throw 'OpenIM admin platform cannot be used for an E2E peer.' }
$localHelper = Join-Path $PSScriptRoot 'issue-node2-web-e2e-peer-token.sh'
$remoteHelper = 'E:/MFL/ops/issue-node2-web-e2e-peer-token.sh'
scp -q -o BatchMode=yes -o StrictHostKeyChecking=yes $localHelper "${SshHost}:$remoteHelper"
if ($LASTEXITCODE -ne 0) {
    throw "E2E peer-token helper transfer failed with exit code $LASTEXITCODE."
}

$remoteScriptTemplate = @'
$ErrorActionPreference = 'Stop'
& 'C:\Program Files\WSL\wsl.exe' -d '__DISTRIBUTION__' -u root -- bash /mnt/e/MFL/ops/issue-node2-web-e2e-peer-token.sh '__PEER_USER__' '__PLATFORM_ID__'
exit $LASTEXITCODE
'@
$remoteScript = $remoteScriptTemplate.Replace('__DISTRIBUTION__', $Distribution.Replace("'", "''"))
$remoteScript = $remoteScript.Replace('__PEER_USER__', $PeerUserID)
$remoteScript = $remoteScript.Replace('__PLATFORM_ID__', [string]$PlatformID)
$encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($remoteScript))

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "powershell.exe -NoProfile -NonInteractive -EncodedCommand $encodedCommand"
if ($LASTEXITCODE -ne 0) {
    throw "Node2 E2E peer-token issue failed with exit code $LASTEXITCODE."
}
