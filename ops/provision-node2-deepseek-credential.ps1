[CmdletBinding()]
param(
    [string]$SshHost = 'openim-node2',
    [string]$Distribution = 'OpenIM-Ubuntu'
)

$ErrorActionPreference = 'Stop'
$key = (Get-Clipboard -Raw).Trim()
if ($key -notmatch '^sk-[A-Za-z0-9_-]{20,}$') {
    throw 'Clipboard does not contain a valid DeepSeek API key.'
}

$remoteScript = @'
$ErrorActionPreference = 'Stop'
$input | & 'C:\Program Files\WSL\wsl.exe' -d '__DISTRIBUTION__' -u root -- bash /mnt/e/MFL/ops/install-node2-deepseek-credential.sh
exit $LASTEXITCODE
'@.Replace('__DISTRIBUTION__', $Distribution.Replace("'", "''"))
$encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($remoteScript))

try {
    $key | ssh -o BatchMode=yes -o StrictHostKeyChecking=yes $SshHost "powershell.exe -NoProfile -NonInteractive -EncodedCommand $encodedCommand"
    if ($LASTEXITCODE -ne 0) {
        throw "Credential provisioning failed with SSH exit code $LASTEXITCODE."
    }
    Set-Clipboard -Value '[cleared by OpenIM provisioning]'
    Write-Output 'deepseek_credential=provisioned'
}
finally {
    $key = $null
}
