[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('accept', 'reset')][string]$Action,
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$WebUserID,
    [string]$SshHost = 'openim-node2',
    [string]$Distribution = 'OpenIM-Ubuntu'
)

$ErrorActionPreference = 'Stop'
$localHelper = Join-Path $PSScriptRoot 'manage-node2-web-e2e-friend.sh'
$remoteHelper = 'E:/MFL/ops/manage-node2-web-e2e-friend.sh'

scp -q -o BatchMode=yes -o StrictHostKeyChecking=yes $localHelper "${SshHost}:$remoteHelper"
if ($LASTEXITCODE -ne 0) {
    throw "E2E friend helper transfer failed with exit code $LASTEXITCODE."
}

$remoteScriptTemplate = @'
$ErrorActionPreference = 'Stop'
& 'C:\Program Files\WSL\wsl.exe' -d '__DISTRIBUTION__' -u root -- bash /mnt/e/MFL/ops/manage-node2-web-e2e-friend.sh '__ACTION__' '__WEB_USER_ID__'
exit $LASTEXITCODE
'@
$remoteScript = $remoteScriptTemplate.Replace('__DISTRIBUTION__', $Distribution.Replace("'", "''"))
$remoteScript = $remoteScript.Replace('__ACTION__', $Action)
$remoteScript = $remoteScript.Replace('__WEB_USER_ID__', $WebUserID)
$encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($remoteScript))

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "powershell.exe -NoProfile -NonInteractive -EncodedCommand $encodedCommand"
if ($LASTEXITCODE -ne 0) {
    throw "Node2 E2E friend action failed with exit code $LASTEXITCODE."
}
