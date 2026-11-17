#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [string]$ServiceName = "LanFilePlatform",
    [int]$Port = 9443,
    [string]$InstallDir = "$env:ProgramFiles\LanFilePlatform",
    [switch]$RemoveProgram
)

$ErrorActionPreference = "Stop"
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "请右键选择「以管理员身份运行」。"
}

$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc) {
    if ($svc.Status -ne "Stopped") {
        Stop-Service -Name $ServiceName -Force
        Start-Sleep -Seconds 2
    }
    & sc.exe delete $ServiceName | Out-Null
    Write-Host "服务已删除：$ServiceName"
} else {
    Write-Host "未找到服务：$ServiceName"
}

$ruleName = "办公室文件中心 (TCP $Port)"
Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
Write-Host "防火墙规则已移除。"

if ($RemoveProgram -and (Test-Path $InstallDir)) {
    Remove-Item -Path $InstallDir -Recurse -Force
    Write-Host "程序目录已删除：$InstallDir"
} else {
    Write-Host "程序目录保留：$InstallDir"
}
Write-Host ""
Write-Host "业务数据目录默认位于 D:\LanFileData，卸载不会删除任何文件或历史版本。" -ForegroundColor Yellow
Write-Host "如确认不再需要数据，请自行在资源管理器中删除数据目录。"
