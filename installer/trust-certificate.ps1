[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Server,
    [switch]$Machine,
    [switch]$VerifyOnly
)

$ErrorActionPreference = "Stop"

if ($Machine -and -not $VerifyOnly) {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "使用 -Machine 需要管理员权限。普通员工可直接使用默认的当前用户安装。"
    }
}

if ($Server -match "^https?://") {
    $base = $Server.TrimEnd("/")
} else {
    if ($Server -match ":\d+$") {
        $base = "https://$Server"
    } else {
        $base = "https://${Server}:9443"
    }
}
$uri = [Uri]$base
if ($uri.Scheme -ne "https") {
    throw "请使用 https 地址，例如 https://192.168.1.100:9443"
}
$url = "$base/api/cert"
$leafUrl = "$base/api/server-cert"

function Download-Url([string]$address, [string]$path) {
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($curl) {
        & $curl.Source -k -sS --fail --max-time 20 $address -o $path
        if ($LASTEXITCODE -ne 0) {
            throw "下载失败：$address。请确认服务器地址、端口和防火墙。"
        }
        return
    }
    if ($PSVersionTable.PSVersion.Major -ge 7) {
        Invoke-WebRequest -Uri $address -OutFile $path -SkipCertificateCheck
        return
    }
    throw "未找到 curl.exe。请使用 Windows 10/11 自带的 curl，或改用 PowerShell 7 运行本脚本。"
}

$tempCa = Join-Path $env:TEMP ("office-files-ca-" + [Guid]::NewGuid().ToString("N") + ".cer")
$tempLeaf = Join-Path $env:TEMP ("office-files-server-" + [Guid]::NewGuid().ToString("N") + ".cer")
try {
    Download-Url $url $tempCa
    Download-Url $leafUrl $tempLeaf
    $trustCert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($tempCa)
    $leaf = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($tempLeaf)
} finally {
    Remove-Item $tempCa, $tempLeaf -Force -ErrorAction SilentlyContinue
}

$sanExt = $leaf.Extensions | Where-Object { $_.Oid.Value -eq "2.5.29.17" } | Select-Object -First 1
$sanText = if ($sanExt) { $sanExt.Format($false) } else { "" }
$hostName = $uri.Host
$ip = $null
if ([Net.IPAddress]::TryParse($hostName, [ref]$ip)) {
    $covered = $sanText -match ("IP Address=" + [regex]::Escape($hostName))
} else {
    $covered = $sanText -match ("DNS Name=" + [regex]::Escape($hostName))
}
if (-not $covered) {
    throw "服务器证书不包含访问地址 $hostName。当前证书 SAN：$sanText。请管理员在主机上停止服务，删除数据目录下 certs 文件夹后重启服务，再重新执行本脚本。"
}

Write-Host "服务器地址：$base"
Write-Host "证书名称：$($trustCert.Subject)"
Write-Host "SHA-256 指纹：$($trustCert.Thumbprint)"
Write-Host "服务器证书 SAN：$sanText"
Write-Host ""

if ($VerifyOnly) {
    Write-Host "验证通过：服务器证书包含访问地址 $hostName。" -ForegroundColor Green
    Write-Host "未导入证书（-VerifyOnly 模式）。" -ForegroundColor Yellow
    return
}

$location = if ($Machine) {
    [System.Security.Cryptography.X509Certificates.StoreLocation]::LocalMachine
} else {
    [System.Security.Cryptography.X509Certificates.StoreLocation]::CurrentUser
}
$store = New-Object System.Security.Cryptography.X509Certificates.X509Store(
    [System.Security.Cryptography.X509Certificates.StoreName]::Root, $location)
$store.Open([System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
try {
    $exists = @($store.Certificates | Where-Object { $_.Thumbprint -eq $trustCert.Thumbprint }).Count -gt 0
    if (-not $exists) {
        $store.Add($trustCert)
    }
    $installed = @($store.Certificates | Where-Object { $_.Thumbprint -eq $trustCert.Thumbprint }).Count -gt 0
} finally {
    $store.Close()
}
if (-not $installed) {
    throw "证书写入受信任根存储失败。"
}

$chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
$chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
$chainOk = $chain.Build($leaf)
if (-not $chainOk) {
    $details = ($chain.ChainStatus | ForEach-Object { "$($_.Status)：$($_.StatusInformation)" }) -join "；"
    throw "证书已导入，但服务器证书链验证失败：$details。请核对上方指纹是否与管理员后台一致。"
}

$storeLabel = if ($Machine) { "本地计算机\受信任的根证书颁发机构" } else { "当前用户\受信任的根证书颁发机构" }
Write-Host "证书已导入：$storeLabel" -ForegroundColor Green
Write-Host "证书链验证：通过" -ForegroundColor Green
Write-Host ""
Write-Host "请完全退出所有 Edge/Chrome 窗口（包括后台进程）后重新打开：" -ForegroundColor Yellow
Write-Host "  $base"
Write-Host "也可以在地址栏打开 edge://restart 或 chrome://restart 重启浏览器。"
Write-Host "Firefox 使用独立证书库，需要在 Firefox 设置中单独导入证书。"
