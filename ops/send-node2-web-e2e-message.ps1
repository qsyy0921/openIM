[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Za-z0-9_-]{1,128}$')][string]$TargetUserID,
    [Parameter(Mandatory = $true)][ValidateLength(1, 6000)][string]$Content,
    [string]$SshHost = 'openim-node2',
    [string]$Distribution = 'OpenIM-Ubuntu'
)

$ErrorActionPreference = 'Stop'
$localHelper = Join-Path $PSScriptRoot 'send-node2-web-e2e-message.sh'
$remoteHelper = 'E:/MFL/ops/send-node2-web-e2e-message.sh'

scp -q -o BatchMode=yes -o StrictHostKeyChecking=yes $localHelper "${SshHost}:$remoteHelper"
if ($LASTEXITCODE -ne 0) {
    throw "E2E helper transfer failed with exit code $LASTEXITCODE."
}

$contentBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Content))
$remoteScriptTemplate = @'
$ErrorActionPreference = 'Stop'
& 'C:\Program Files\WSL\wsl.exe' -d '__DISTRIBUTION__' -u root -- bash /mnt/e/MFL/ops/send-node2-web-e2e-message.sh '__TARGET__' '__CONTENT__'
exit $LASTEXITCODE
'@
$remoteScript = $remoteScriptTemplate.Replace('__DISTRIBUTION__', $Distribution.Replace("'", "''"))
$remoteScript = $remoteScript.Replace('__TARGET__', $TargetUserID)
$remoteScript = $remoteScript.Replace('__CONTENT__', $contentBase64)
$encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($remoteScript))

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "powershell.exe -NoProfile -NonInteractive -EncodedCommand $encodedCommand"
if ($LASTEXITCODE -ne 0) {
    throw "Node2 E2E message send failed with exit code $LASTEXITCODE."
}
