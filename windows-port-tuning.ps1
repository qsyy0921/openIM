$ErrorActionPreference = 'Stop'
$log = 'E:\development\OPENIM\windows-port-tuning.log'
"START $(Get-Date -Format o)" | Set-Content -Path $log -Encoding UTF8
netsh int ipv4 set dynamicport tcp start=10000 num=55535 | Tee-Object -FilePath $log -Append
netsh int ipv6 set dynamicport tcp start=10000 num=55535 | Tee-Object -FilePath $log -Append
"IPV4" | Tee-Object -FilePath $log -Append
netsh int ipv4 show dynamicport tcp | Tee-Object -FilePath $log -Append
"IPV6" | Tee-Object -FilePath $log -Append
netsh int ipv6 show dynamicport tcp | Tee-Object -FilePath $log -Append
"END $(Get-Date -Format o)" | Tee-Object -FilePath $log -Append
