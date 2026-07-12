[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('ensure-peer', 'transfer-owner')][string]$Action,
    [ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$GroupID = '',
    [ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$OldOwnerUserID = '',
    [ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$PeerUserID = 'lifecyclePeer',
    [string]$SshHost = 'openim-node2',
    [string]$Distribution = 'OpenIM-Ubuntu'
)

$ErrorActionPreference = 'Stop'
if ($Action -eq 'transfer-owner' -and (-not $GroupID -or -not $OldOwnerUserID)) {
    throw 'GroupID and OldOwnerUserID are required for transfer-owner.'
}

$localHelper = Join-Path $PSScriptRoot 'manage-node2-web-e2e-group-lifecycle.sh'
$remoteHelper = 'E:/MFL/ops/manage-node2-web-e2e-group-lifecycle.sh'
scp -q -o BatchMode=yes -o StrictHostKeyChecking=yes $localHelper "${SshHost}:$remoteHelper"
if ($LASTEXITCODE -ne 0) {
    throw "E2E lifecycle helper transfer failed with exit code $LASTEXITCODE."
}

$remoteScriptTemplate = @'
$ErrorActionPreference = 'Stop'
& 'C:\Program Files\WSL\wsl.exe' -d '__DISTRIBUTION__' -u root -- bash /mnt/e/MFL/ops/manage-node2-web-e2e-group-lifecycle.sh '__ACTION__' '__GROUP_ID__' '__OLD_OWNER__' '__PEER_ID__'
exit $LASTEXITCODE
'@
$remoteScript = $remoteScriptTemplate.Replace('__DISTRIBUTION__', $Distribution.Replace("'", "''"))
$remoteScript = $remoteScript.Replace('__ACTION__', $Action)
$remoteScript = $remoteScript.Replace('__GROUP_ID__', $GroupID)
$remoteScript = $remoteScript.Replace('__OLD_OWNER__', $OldOwnerUserID)
$remoteScript = $remoteScript.Replace('__PEER_ID__', $PeerUserID)
$encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($remoteScript))

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "powershell.exe -NoProfile -NonInteractive -EncodedCommand $encodedCommand"
if ($LASTEXITCODE -ne 0) {
    throw "Node2 E2E lifecycle action failed with exit code $LASTEXITCODE."
}
