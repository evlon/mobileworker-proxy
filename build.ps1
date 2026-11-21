# 一键构建 mobilework-proxy（Go 版）
#
#   .\build.ps1            构建当前平台
#   .\build.ps1 -Tidy      先跑 go mod tidy
#
# 依赖：Go 1.21+。默认走 goproxy.cn（公司网络下 proxy.golang.org 不通）。

[CmdletBinding()]
param(
    [switch]$Tidy,
    [string]$OutDir = "dist",
    # 版本号，会注入到 --version；留空则取 git describe，再退回 dev
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"

# ---- 定位 go ----
$goExe = "go"
if (Test-Path "D:\go\bin\go.exe") {
    $env:Path = "D:\go\bin;" + $env:Path
    $goExe = "D:\go\bin\go.exe"
}
try {
    $ver = & $goExe version
    Write-Host "使用 $ver" -ForegroundColor Cyan
} catch {
    throw "找不到 go 命令。请安装 Go 并把 go.exe 放进 PATH（或装到 D:\go）。"
}

# ---- Go 环境：国内代理 ----
if (-not $env:GOPROXY)  { $env:GOPROXY  = "https://goproxy.cn,direct" }
if (-not $env:GOSUMDB)  { $env:GOSUMDB  = "off" }
if (-not $env:GOFLAGS)  { $env:GOFLAGS  = "-mod=mod" }

# 若默认缓存目录不可写（受限环境），退回到工作区内的缓存
function Test-Writable([string]$p) {
    try {
        New-Item -ItemType Directory -Force -Path $p | Out-Null
        $f = Join-Path $p (".wtest-" + [guid]::NewGuid().ToString("N"))
        Set-Content -Path $f -Value "x" -ErrorAction Stop
        Remove-Item $f -Force
        return $true
    } catch { return $false }
}

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
if (-not $env:GOCACHE -and -not (Test-Writable (Join-Path $env:LOCALAPPDATA "go-build"))) {
    $env:GOCACHE = Join-Path $root ".gocache"
    Write-Host "GOCACHE 回退到 $env:GOCACHE" -ForegroundColor Yellow
}
if (-not $env:GOMODCACHE -and -not (Test-Writable (Join-Path $HOME "go"))) {
    $env:GOMODCACHE = Join-Path $root ".gomodcache"
    Write-Host "GOMODCACHE 回退到 $env:GOMODCACHE" -ForegroundColor Yellow
}

Push-Location $root
try {
    if ($Tidy) {
        Write-Host "`n[1/3] go mod tidy" -ForegroundColor Cyan
        & $goExe mod tidy
        if ($LASTEXITCODE -ne 0) { throw "go mod tidy 失败" }
    }

    Write-Host "`n[2/3] 构建" -ForegroundColor Cyan
    $out = Join-Path $root $OutDir
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    $exe = Join-Path $out "mobilework-proxy.exe"

    # 版本号：命令行指定 > git describe > dev
    if (-not $Version) {
        try {
            $Version = (& git describe --tags --always --dirty 2>$null | Out-String).Trim()
        } catch { }
        if (-not $Version) { $Version = "dev" }
    }
    Write-Host "  版本: $Version"

    & $goExe build -trimpath -ldflags "-s -w -X main.version=$Version" -o $exe .
    if ($LASTEXITCODE -ne 0) { throw "构建失败" }

    $sizeMB = [math]::Round((Get-Item $exe).Length / 1MB, 2)
    Write-Host "`n[3/3] 完成" -ForegroundColor Green
    Write-Host "  产物: $exe"
    Write-Host "  大小: $sizeMB MB"
    Write-Host "`n双击即可运行。" -ForegroundColor Green
} finally {
    Pop-Location
}
