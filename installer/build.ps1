param(
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent

Write-Host "==> 构建前端" -ForegroundColor Cyan
Push-Location (Join-Path $root "web")
try {
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "前端构建失败" }
} finally {
    Pop-Location
}

Write-Host "==> 构建服务端" -ForegroundColor Cyan
$goExe = $null
$goCommand = Get-Command go -ErrorAction SilentlyContinue
if ($goCommand) {
    $goExe = $goCommand.Source
} else {
    $candidate = "C:\Program Files\Go\bin\go.exe"
    if (Test-Path $candidate) { $goExe = $candidate } else { throw "未找到 Go，请安装 Go 1.22 或更高版本" }
}
$env:GOPROXY = "https://goproxy.cn,direct"
$distDir = Join-Path $root "dist"
New-Item -ItemType Directory -Force -Path $distDir | Out-Null
Push-Location $root
try {
    & $goExe build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $distDir "LanFileServer.exe") ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "服务端构建失败" }
} finally {
    Pop-Location
}

Write-Host "==> 生成发布包" -ForegroundColor Cyan
$releaseRoot = Join-Path $root "release"
$releaseDir = Join-Path $releaseRoot "LanFilePlatform-$Version"
if (Test-Path $releaseDir) { Remove-Item $releaseDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path $releaseDir | Out-Null

Copy-Item (Join-Path $distDir "LanFileServer.exe") $releaseDir
Copy-Item (Join-Path $PSScriptRoot "install.ps1") $releaseDir
Copy-Item (Join-Path $PSScriptRoot "uninstall.ps1") $releaseDir
Copy-Item (Join-Path $PSScriptRoot "trust-certificate.ps1") $releaseDir

Write-Host "==> 生成一键信任脚本" -ForegroundColor Cyan
$trustScript = Get-Content (Join-Path $PSScriptRoot "trust-certificate.ps1") -Raw -Encoding UTF8
$trustScript = $trustScript.TrimStart([char]0xFEFF)
$trustCmdHeader = @'
@echo off
chcp 65001 >nul
title 一键信任办公室文件中心证书
setlocal
set "SELF=%~f0"
set "SERVER=https://192.168.1.100:9443"
if not "%~1"=="" set "SERVER=%~1"
echo.
echo ============================================
echo  办公室文件中心 - 一键信任 HTTPS 证书
echo  服务器: %SERVER%
echo  普通双击：仅为当前用户安装
echo  右键“以管理员身份运行”：为所有用户安装
echo ============================================
echo.
powershell -NoProfile -ExecutionPolicy Bypass -Command "$t=Get-Content -LiteralPath $env:SELF -Raw -Encoding UTF8; $i=$t.LastIndexOf('#===PS==='); if($i -lt 0){throw '脚本内容不完整'}; $s=$t.Substring($i+9); $f=Join-Path $env:TEMP ('office-trust-'+[guid]::NewGuid().ToString('N')+'.ps1'); [IO.File]::WriteAllText($f,$s,(New-Object Text.UTF8Encoding($true))); $a=([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator); try { & $f -Server $env:SERVER -Machine:$a } finally { Remove-Item $f -Force -ErrorAction SilentlyContinue }"
set "RC=%ERRORLEVEL%"
echo.
if /i "%~2"=="/quiet" exit /b %RC%
pause
exit /b %RC%
#===PS===
'@
$trustCmdText = $trustCmdHeader + "`r`n" + $trustScript
$trustCmdPath = Join-Path $releaseDir "一键信任证书.cmd"
[System.IO.File]::WriteAllText($trustCmdPath, $trustCmdText, (New-Object System.Text.UTF8Encoding($false)))
Copy-Item (Join-Path $PSScriptRoot "安装.cmd") $releaseDir
Copy-Item (Join-Path $PSScriptRoot "卸载服务.cmd") $releaseDir
Copy-Item (Join-Path $root "README.md") $releaseDir
Copy-Item (Join-Path $root "docs") (Join-Path $releaseDir "docs") -Recurse
Set-Content -Path (Join-Path $releaseDir "VERSION.txt") -Value $Version -Encoding UTF8

$exeHash = Get-FileHash (Join-Path $releaseDir "LanFileServer.exe") -Algorithm SHA256
Set-Content -Path (Join-Path $releaseDir "SHA256SUMS.txt") -Value "$($exeHash.Hash)  LanFileServer.exe" -Encoding ASCII

$zip = Join-Path $releaseRoot "LanFilePlatform-$Version.zip"
if (Test-Path $zip) { Remove-Item $zip -Force }
Compress-Archive -Path (Join-Path $releaseDir "*") -DestinationPath $zip -CompressionLevel Optimal

Write-Host ""
Write-Host "发布包已生成：" -ForegroundColor Green
Write-Host "  $zip"
Write-Host "解压后双击「安装.cmd」即可安装。"
