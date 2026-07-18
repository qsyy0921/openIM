[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('add', 'remove')][string]$Action,
    [Parameter(Mandatory = $true)][ValidatePattern('^node2-e2e-[A-Za-z0-9_-]{1,96}$')][string]$DeviceID,
    [ValidateRange(1, 11)][int]$PlatformID = 3,
    [string]$SshHost = 'openim-node2',
    [string]$Distribution = 'OpenIM-Ubuntu'
)

$ErrorActionPreference = 'Stop'
if ($PlatformID -eq 10) { throw 'OpenIM admin platform cannot be used for E2E devices.' }
$localHelper = Join-Path $PSScriptRoot 'manage-node2-web-e2e-device.sh'
$remoteHelper = 'E:/MFL/ops/manage-node2-web-e2e-device.sh'

scp -q -o BatchMode=yes -o StrictHostKeyChecking=yes $localHelper "${SshHost}:$remoteHelper"
if ($LASTEXITCODE -ne 0) { throw "E2E device helper transfer failed with exit code $LASTEXITCODE." }

$remoteScriptTemplate = @'
$ErrorActionPreference = 'Stop'
& 'C:\Program Files\WSL\wsl.exe' -d '__DISTRIBUTION__' -u root -- bash /mnt/e/MFL/ops/manage-node2-web-e2e-device.sh '__ACTION__' '__DEVICE_ID__' '__PLATFORM_ID__'
exit $LASTEXITCODE
'@
$remoteScript = $remoteScriptTemplate.Replace('__DISTRIBUTION__', $Distribution.Replace("'", "''"))
$remoteScript = $remoteScript.Replace('__ACTION__', $Action).Replace('__DEVICE_ID__', $DeviceID).Replace('__PLATFORM_ID__', [string]$PlatformID)
$encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($remoteScript))

ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "powershell.exe -NoProfile -NonInteractive -EncodedCommand $encodedCommand"
if ($LASTEXITCODE -ne 0) { throw "Node2 E2E device action failed with exit code $LASTEXITCODE." }
