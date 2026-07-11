[CmdletBinding()]
param(
    [switch]$KeepAlive
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$distro = 'OpenIM-Ubuntu'
$clashTask = 'OpenIM-Clash-Core'
$wslExecutable = 'C:\Program Files\WSL\wsl.exe'
$bridgePort = 17893
$clashPort = 7893
$lanAddress = '172.31.50.2'
$controllerAddress = '172.31.50.1'
$publishedPorts = @(12001, 12002, 12005, 12008, 12009, 18080, 18081)
$stateDir = 'E:\MFL\state'
$logDir = 'E:\MFL\logs'
$logFile = Join-Path $logDir 'start-node2-runtime.log'

New-Item -ItemType Directory -Path $stateDir, $logDir -Force | Out-Null

function Write-Log {
    param([string]$Message)
    $line = '{0:o} {1}' -f [DateTimeOffset]::Now, $Message
    Add-Content -Path $logFile -Value $line -Encoding utf8
    Write-Output $line
}

function Wait-Listener {
    param(
        [int]$Port,
        [int]$TimeoutSeconds
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $listener = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue
        if ($listener) {
            return
        }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)

    throw "listener $Port did not become ready"
}

try {
    Write-Log 'starting node2 runtime recovery'

    if (-not (Test-Path $wslExecutable)) {
        throw "modern WSL executable is missing: $wslExecutable"
    }

    $task = Get-ScheduledTask -TaskName $clashTask -ErrorAction Stop
    if ($task.State -ne 'Running') {
        Start-ScheduledTask -TaskName $clashTask
    }
    Wait-Listener -Port $clashPort -TimeoutSeconds 30
    Write-Log "Clash is listening on 127.0.0.1:$clashPort"

    & $wslExecutable -d $distro -- /bin/true
    if ($LASTEXITCODE -ne 0) {
        throw "failed to start WSL distribution $distro"
    }

    $deadline = (Get-Date).AddSeconds(30)
    $wslAddress = $null
    do {
        $wslAddress = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
            Where-Object { $_.InterfaceAlias -match 'WSL' } |
            Select-Object -First 1
        if (-not $wslAddress) {
            Start-Sleep -Seconds 1
        }
    } while (-not $wslAddress -and (Get-Date) -lt $deadline)

    if (-not $wslAddress) {
        throw 'WSL virtual adapter did not become ready'
    }

    $hostAddress = $wslAddress.IPAddress
    $prefixLength = $wslAddress.PrefixLength
    $connectedRoute = Get-NetRoute -AddressFamily IPv4 -InterfaceIndex $wslAddress.InterfaceIndex |
        Where-Object {
            $_.NextHop -eq '0.0.0.0' -and
            $_.DestinationPrefix -like "*/$prefixLength"
        } |
        Select-Object -First 1

    if (-not $connectedRoute) {
        throw "could not determine the WSL /$prefixLength connected subnet"
    }

    $wslSubnet = $connectedRoute.DestinationPrefix
    Write-Log "WSL host address is $hostAddress; subnet is $wslSubnet"

    $portProxyKey = 'HKLM:\SYSTEM\CurrentControlSet\Services\PortProxy\v4tov4\tcp'
    if (Test-Path $portProxyKey) {
        $valueNames = (Get-Item $portProxyKey).GetValueNames()
        foreach ($valueName in $valueNames) {
            $parts = $valueName -split '/'
            if ($parts.Count -eq 2 -and $parts[1] -eq [string]$bridgePort) {
                & netsh.exe interface portproxy delete v4tov4 `
                    listenaddress=$($parts[0]) listenport=$bridgePort | Out-Null
            }
        }
    }

    & netsh.exe interface portproxy add v4tov4 `
        listenaddress=$hostAddress listenport=$bridgePort `
        connectaddress=127.0.0.1 connectport=$clashPort
    if ($LASTEXITCODE -ne 0) {
        throw 'failed to create the WSL-only Clash bridge'
    }

    Get-NetFirewallRule -DisplayName 'OpenIM WSL Clash Bridge' -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    New-NetFirewallRule `
        -DisplayName 'OpenIM WSL Clash Bridge' `
        -Direction Inbound `
        -Action Allow `
        -Protocol TCP `
        -LocalAddress $hostAddress `
        -LocalPort $bridgePort `
        -RemoteAddress $wslSubnet `
        -Profile Any | Out-Null

    $linuxAddressOutput = & $wslExecutable -d $distro -- hostname -I
    if ($LASTEXITCODE -ne 0) {
        throw 'failed to determine the WSL Linux address'
    }
    $linuxAddress = ($linuxAddressOutput -split '\s+' | Where-Object { $_ })[0]
    $parsedLinuxAddress = $null
    if (-not [Net.IPAddress]::TryParse($linuxAddress, [ref]$parsedLinuxAddress) -or
        $parsedLinuxAddress.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {
        throw "invalid WSL Linux address: $linuxAddress"
    }

    foreach ($port in $publishedPorts) {
        & netsh.exe interface portproxy delete v4tov4 `
            listenaddress=$lanAddress listenport=$port | Out-Null
        & netsh.exe interface portproxy add v4tov4 `
            listenaddress=$lanAddress listenport=$port `
            connectaddress=$linuxAddress connectport=$port
        if ($LASTEXITCODE -ne 0) {
            throw "failed to publish WSL port $port"
        }
    }

    Get-NetFirewallRule -DisplayName 'OpenIM Node2 Published Services' -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    New-NetFirewallRule `
        -DisplayName 'OpenIM Node2 Published Services' `
        -Direction Inbound `
        -Action Allow `
        -Protocol TCP `
        -LocalAddress $lanAddress `
        -LocalPort $publishedPorts `
        -RemoteAddress $controllerAddress `
        -Profile Any | Out-Null
    Write-Log "published WSL $linuxAddress ports $($publishedPorts -join ',') to $lanAddress for $controllerAddress"

    $proxyUrl = "http://${hostAddress}:$bridgePort"
    $linuxScript = @"
set -eu
install -d -m 0755 /etc/apt/apt.conf.d /etc/openim
cat >/etc/apt/apt.conf.d/90openim-proxy <<'EOF'
Acquire::http::Proxy "$proxyUrl";
Acquire::https::Proxy "$proxyUrl";
Acquire::Retries "3";
EOF
cat >/etc/openim/proxy.env <<'EOF'
HTTP_PROXY=$proxyUrl
HTTPS_PROXY=$proxyUrl
NO_PROXY=127.0.0.1,localhost
EOF
chmod 0600 /etc/openim/proxy.env
if [ "`$(ps -p 1 -o comm=)" != "systemd" ]; then
  echo "modern WSL systemd is required" >&2
  exit 1
fi
install -d -m 0755 /etc/systemd/system/docker.service.d
cat >/etc/systemd/system/docker.service.d/openim-proxy.conf <<'EOF'
[Service]
EnvironmentFile=/etc/openim/proxy.env
EOF
systemctl daemon-reload
systemctl enable docker
systemctl restart docker
docker info >/dev/null
iptables -t nat -C OUTPUT -d $lanAddress/32 -p tcp --dport 18081 -j REDIRECT --to-ports 18081 2>/dev/null || \
  iptables -t nat -A OUTPUT -d $lanAddress/32 -p tcp --dport 18081 -j REDIRECT --to-ports 18081
"@
    $linuxEncoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($linuxScript))
    & $wslExecutable -d $distro -- sh -lc "echo $linuxEncoded | base64 -d | sh"
    if ($LASTEXITCODE -ne 0) {
        throw 'failed to update the WSL proxy or start Docker'
    }

    Set-Content -Path (Join-Path $stateDir 'wsl-host-address.txt') -Value $hostAddress -Encoding ascii
    Write-Log 'node2 runtime recovery completed'

    if ($KeepAlive) {
        Write-Log 'entering WSL keepalive'
        & $wslExecutable -d $distro -- /bin/sleep infinity
        throw 'WSL keepalive exited unexpectedly'
    }
} catch {
    Write-Log "node2 runtime recovery failed: $($_.Exception.Message)"
    throw
}
