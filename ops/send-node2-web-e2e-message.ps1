[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$TargetUserID,
    [Parameter(Mandatory = $true)][ValidateLength(1, 6000)][string]$Content,
    [ValidatePattern('^[A-Za-z0-9._-]{1,128}$')][string]$SshHost = 'openim-node2'
)

$ErrorActionPreference = 'Stop'
$contentBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Content))
$remoteDeployRoot = '/home/qsyy0921/MFL/deploy/node2-native'
$remoteHelper = "$remoteDeployRoot/ops/send-node2-web-e2e-message.sh"

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "bash '$remoteHelper' '$TargetUserID' '$contentBase64' '$remoteDeployRoot'"
if ($LASTEXITCODE -ne 0) {
    throw "Node2 E2E message send failed with exit code $LASTEXITCODE."
}
