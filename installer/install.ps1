#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [string]$InstallDir = "$env:ProgramFiles\LanFilePlatform",
    [string]$DataDir = "D:\LanFileData",
    [int]$Port = 9443,
    [string]$ServiceName = "LanFilePlatform",
    [switch]$NonInteractive
)

$ErrorActionPreference = "Stop"

function Write-Step([string]$Text) {
    Write-Host ""
    Write-Host "==> $Text" -ForegroundColor Cyan
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "请右键选择「以管理员身份运行」。"
}

if (-not $NonInteractive) {
    $answer = Read-Host "数据目录（建议独立磁盘，回车使用 $DataDir）"
    if ($answer.Trim() -ne "") { $DataDir = $answer.Trim() }
    $answer = Read-Host "访问端口（回车使用 $Port）"
    if ($answer.Trim() -ne "") { $Port = [int]$answer.Trim() }
}

if (-not [System.IO.Path]::IsPathRooted($DataDir)) {
    throw "数据目录必须是绝对路径，例如 D:\LanFileData"
}
if ($Port -lt 1 -or $Port -gt 65535) {
    throw "端口无效"
}
$dataFull = [System.IO.Path]::GetFullPath($DataDir)
$installFull = [System.IO.Path]::GetFullPath($InstallDir)
$sep = [System.IO.Path]::DirectorySeparatorChar
$dataTrimmed = $dataFull.TrimEnd($sep)
$installTrimmed = $installFull.TrimEnd($sep)
if ($dataTrimmed -eq $installTrimmed -or $dataTrimmed.StartsWith($installTrimmed + $sep, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "数据目录不能位于程序目录内，请选择独立的数据目录。"
}

$sourceExe = Join-Path $PSScriptRoot "LanFileServer.exe"
if (-not (Test-Path $sourceExe)) {
    throw "安装包中缺少 LanFileServer.exe，请确认解压完整。"
}

Write-Step "准备目录"
New-Item -ItemType Directory -Force -Path $InstallDir, $DataDir | Out-Null
$targetExe = Join-Path $InstallDir "LanFileServer.exe"

Write-Step "停止并移除旧服务（数据会保留）"
$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existing) {
    if ($existing.Status -ne "Stopped") {
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    }
    Start-Sleep -Seconds 2
    & sc.exe delete $ServiceName | Out-Null
    Start-Sleep -Seconds 2
}

Write-Step "复制程序文件"
Copy-Item -Path $sourceExe -Destination $targetExe -Force
Copy-Item -Path (Join-Path $PSScriptRoot "trust-certificate.ps1") -Destination $InstallDir -Force -ErrorAction SilentlyContinue
Copy-Item -Path (Join-Path $PSScriptRoot "一键信任证书.cmd") -Destination $InstallDir -Force -ErrorAction SilentlyContinue

Write-Step "设置服务账号权限"
& icacls $InstallDir /grant "*S-1-5-19:(OI)(CI)RX" /T | Out-Null
& icacls $DataDir /grant "*S-1-5-19:(OI)(CI)M" /T | Out-Null

Write-Step "配置防火墙（仅专用/域网络，本地子网）"
$ruleName = "办公室文件中心 (TCP $Port)"
Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Action Allow -Protocol TCP `
    -LocalPort $Port -Profile Domain,Private -RemoteAddress LocalSubnet | Out-Null

Write-Step "注册 Windows 服务"
$binPath = "`"$targetExe`" -data `"$DataDir`" -addr `":$Port`""
New-Service -Name $ServiceName -BinaryPathName $binPath -DisplayName "办公室文件中心" -StartupType Automatic | Out-Null
& sc.exe config $ServiceName obj= "NT AUTHORITY\LocalService" | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "服务账号配置失败。"
}
& sc.exe description $ServiceName "办公室局域网文件版本管理平台" | Out-Null
& sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/10000/restart/30000 | Out-Null
& sc.exe start $ServiceName | Out-Null

$svc = Get-Service -Name $ServiceName
$deadline = (Get-Date).AddSeconds(25)
while ($svc.Status -ne "Running" -and (Get-Date) -lt $deadline) {
    Start-Sleep -Seconds 1
    $svc.Refresh()
}
if ($svc.Status -ne "Running") {
    Write-Host "服务未能启动，请查看日志：$DataDir\logs\server.log" -ForegroundColor Red
    throw "服务启动失败"
}

Write-Step "完成"
$portSuffix = if ($Port -eq 443) { "" } else { ":$Port" }
Write-Host "服务名称：$ServiceName" -ForegroundColor Green
Write-Host "本机初始化地址：https://localhost$portSuffix" -ForegroundColor Green
Write-Host "数据目录：$DataDir"
Write-Host "日志文件：$DataDir\logs\server.log"
$ips = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
    Where-Object { $_.IPAddress -notlike "127.*" -and $_.PrefixOrigin -in @("Dhcp", "Manual") } |
    Select-Object -ExpandProperty IPAddress -Unique
foreach ($ip in $ips) {
    Write-Host "员工访问地址：https://${ip}${portSuffix}" -ForegroundColor Green
}
Write-Host ""
Write-Host "首次使用：" -ForegroundColor Yellow
Write-Host "1. 在主机上打开 https://localhost$portSuffix ，浏览器提示证书风险时选择「继续访问」。"
Write-Host "2. 创建管理员账号并登录。"
Write-Host "3. 每位员工的电脑需一次性信任服务器证书："
Write-Host "   powershell -ExecutionPolicy Bypass -File .\trust-certificate.ps1 -Server https://<主机IP>$portSuffix"
Write-Host "   或直接双击安装目录中的「一键信任证书.cmd」，无需输入命令。"
Write-Host "   证书也可在「管理后台 → 备份与系统」中下载。"
Write-Host "4. 在「系统设置」中配置独立备份目录，并完成一次手动备份。"
Write-Host ""
if (-not $NonInteractive) {
    Start-Process "explorer.exe" -ArgumentList "https://localhost$portSuffix"
}
