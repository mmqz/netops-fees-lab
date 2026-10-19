<#
.SYNOPSIS
  NetOps Lab 一键运行脚本（Windows / PowerShell）

.DESCRIPTION
  从零到浏览器可用，一步完成：
    ① 检查端口    ② 构建前端    ③ 构建二进制（嵌入 dist）
    ④ 按依赖序导入 32 工作簿 + 7 日性能 + 检测 + 物化工作台
    ⑤ 启动服务并打开浏览器

  默认使用真实公开演示数据（OSM + OpenCelliD，testdata/realdata）。
  数据库已存在时自动跳过导入（幂等，可反复运行）。

.PARAMETER Port
  HTTP 端口，默认 8080。

.PARAMETER Db
  数据库路径，默认 data\netops-real.db。

.PARAMETER SkipWeb
  跳过前端构建（dist 已是最新时可用，省 1-2 分钟）。

.PARAMETER SkipBuild
  跳过 Go 二进制构建。

.PARAMETER Rebuild
  强制重建数据库（删除后重新导入全部数据）。

.PARAMETER NoBrowser
  启动后不自动打开浏览器。

.EXAMPLE
  .\run.ps1
  .\run.ps1 -Port 9000
  .\run.ps1 -Rebuild
  .\run.ps1 -SkipWeb -SkipBuild

.NOTES
  费用分析查询：Windows 原生构建不含 DuckDB 分析引擎（go-duckdb 需链接的
  rstrtmgr 库 mingw-w64 未提供，go build -tags duckdb 链接失败）——导入/快照/
  Parquet 写入不受影响，仅费用查询答 501 engine_not_built。需要查询时：
    ① docker build -t netops-lab:local .   （镜像默认含 duckdb 引擎）
    ② 或在 WSL2 内 make build-duckdb
  详见 docs/fees-layer.md §5。

  重要：dist 被 go:embed 在【构建时】嵌入二进制。
  顺序必须是「先建前端、再建二进制」——只重建前端而不重建二进制无效，
  会导致新增地图图层静默缺失（GET /mapdata/*.geojson.gz 返回 200 + text/html）。
#>

[CmdletBinding()]
param(
    [int]$Port = 8080,
    [string]$Database = "",
    [switch]$SkipWeb,
    [switch]$SkipBuild,
    [switch]$Rebuild,
    [switch]$NoBrowser
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# 注意：参数名用 -Database 而非 -Db ——
# PowerShell 把 -Db 视为内置 -Debug 的缩写，绑定时会报冲突。
# ── 路径 ─────────────────────────────────────────────
$Root = $PSScriptRoot
if ([string]::IsNullOrEmpty($Database)) { $Database = Join-Path $Root "data\netops-real.db" }
elseif (-not [System.IO.Path]::IsPathRooted($Database)) { $Database = Join-Path $Root $Database }
$Db = $Database   # 脚本内部统一用 $Db

$Bin      = Join-Path $Root "bin\netopslab.exe"
$WebDir   = Join-Path $Root "web"
$DistDir  = Join-Path $Root "internal\webui\dist"
$RealData = Join-Path $Root "testdata\realdata"

# ── 输出 helpers ─────────────────────────────────────
$script:StepNo = 0
function Step($msg) {
    $script:StepNo++
    Write-Host ""
    Write-Host ("[{0}/6] {1}" -f $script:StepNo, $msg) -ForegroundColor Cyan
}
function Ok($msg)   { Write-Host "      [OK] $msg" -ForegroundColor Green }
function Info($msg) { Write-Host "      ·    $msg" -ForegroundColor DarkGray }
function Warn($msg) { Write-Host "      [!]  $msg" -ForegroundColor Yellow }
function Fail($msg) {
    Write-Host ""
    Write-Host "[X] $msg" -ForegroundColor Red
    exit 1
}

# ── 环境检查 ─────────────────────────────────────────
Write-Host ""
Write-Host "NetOps Lab 一键运行" -ForegroundColor White
Write-Host "工作区 $Root" -ForegroundColor DarkGray

foreach ($tool in @("go", "node", "npm")) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        Fail "未找到 $tool，请先安装并加入 PATH"
    }
}
$goVer = (& go version) -split " " | Select-Object -Index 2
$nodeVer = (& node --version)
Info "$goVer / node $nodeVer"

# ── ① 端口 ───────────────────────────────────────────
Step "检查端口 $Port"
$listener = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
if ($listener) {
    $procIds = $listener.OwningProcess | Sort-Object -Unique
    foreach ($procId in $procIds) {
        $p = Get-Process -Id $procId -ErrorAction SilentlyContinue
        if ($p -and $p.ProcessName -eq "netopslab") {
            Info "停止旧实例 PID $procId"
            Stop-Process -Id $procId -Force
            Start-Sleep -Milliseconds 600
        } else {
            Fail "端口 $Port 被 $($p.ProcessName)（PID $procId）占用，请换端口：.\run.ps1 -Port 9000"
        }
    }
}
Ok "端口 $Port 可用"

# ── ② 前端（必须先于二进制）──────────────────────────
Step "构建前端（React SPA -> internal\webui\dist）"
if ($SkipWeb) {
    Warn "-SkipWeb：跳过（请确保 dist 已最新）"
} else {
    Push-Location $WebDir
    try {
        if (-not (Test-Path (Join-Path $WebDir "node_modules"))) {
            Info "npm ci（首次较慢）..."
            & npm ci 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0) { Fail "npm ci 失败" }
        } else {
            Info "node_modules 已存在，跳过 npm ci"
        }
        Info "npm run build ..."
        & npm run build 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail "前端构建失败" }
    } finally { Pop-Location }
    Ok "dist 已更新"
}

# ── ③ 二进制（嵌入 dist）─────────────────────────────
Step "构建二进制（go:embed 嵌入 dist）"
if ($SkipBuild) {
    Warn "-SkipBuild：跳过"
    if (-not (Test-Path $Bin)) { Fail "二进制不存在，且指定了 -SkipBuild" }
} else {
    Push-Location $Root
    try {
        & go build -o $Bin ./cmd/netopslab
        if ($LASTEXITCODE -ne 0) { Fail "go build 失败" }
    } finally { Pop-Location }
    $distTime = (Get-Item (Join-Path $DistDir "index.html")).LastWriteTime
    $binTime  = (Get-Item $Bin).LastWriteTime
    if ($binTime -lt $distTime) {
        Warn "二进制早于 dist，图层可能缺失（建议去掉 -SkipBuild 重跑）"
    } else {
        Ok ("netopslab.exe 已构建（{0:HH:mm:ss}）" -f $binTime)
    }
}
# ── ④ 数据导入（按引用依赖序）─────────────────────────
Step "准备数据"
if ($Rebuild -and (Test-Path $Db)) {
    Info "删除旧库（-Rebuild）"
    foreach ($suf in @("", "-wal", "-shm")) {
        Remove-Item "$Db$suf" -Force -ErrorAction SilentlyContinue
    }
}

# 库存在时检查「数据厚度」是否达标——旧版本（如 v0.8.0 时代）的库
# 字段薄、关系少，直接复用会让新版修复（环/上联/客户引用）失效。
$needsRebuild = $false
if (Test-Path $Db) {
    $probe = & $Bin query --db $Db stats 2>$null | ConvertFrom-Json
    $resN = if ($probe) { [int]$probe.totalResources } else { 0 }
    $relN = 0
    if ($probe -and $probe.byRelationType) {
        $relN = [int](($probe.byRelationType.PSObject.Properties | Measure-Object -Property Value -Sum).Sum)
    }
    # 判据用「关系数」而非资源数：
    #   v0.8.0 旧库 = 1,965 资源 /   491 关系（基站全孤立、无 uplink_to/serves）
    #   v0.8.1 新库 = 1,991 资源 / 3,193 关系
    # 资源数几乎相同（+26），只有关系数能区分——用资源数会误判为「够新」。
    if ($relN -ge 2000) {
        Info "库已存在：$Db（$resN 资源 / $relN 关系）"
        Ok "跳过导入（需强制重建请加 -Rebuild）"
    } else {
        Warn "库已存在但关系厚度不足（$relN 关系，期望 >= 2000）"
        Info "多为旧版本（v0.8.0 及更早）生成的库——缺少 uplink_to/serves 等关系"
        Info "自动重建以应用当前版本的数据修复"
        $needsRebuild = $true
    }
}

if ((-not (Test-Path $Db)) -or $needsRebuild) {
    foreach ($suf in @("", "-wal", "-shm")) {
        Remove-Item "$Db$suf" -Force -ErrorAction SilentlyContinue
    }
    if (-not (Test-Path $RealData)) { Fail "找不到演示数据目录：$RealData" }
    New-Item -ItemType Directory -Force -Path (Split-Path $Db -Parent) | Out-Null

    # 引用依赖序：被引用者必须先导入。
    # v0.8.1 起「所属环/上联设备/服务客户」为引用列，顺序错误会报 E003 并级联失败。
    $Order = @(
        "customers", "rings", "igp_domains", "vpn_instances",
        "sites", "machine_rooms", "bbus", "olts",
        "devices", "boards", "racks", "ports", "pon_ports",
        "base_stations", "rrus", "onus",
        "optical_cables", "cable_segments", "ducts", "manholes", "poles",
        "odf", "odf_terminals", "splice_boxes",
        "fibers", "otn_ochs", "sdh_timeslots", "circuits",
        "protection_groups", "ippools", "services", "leased_lines"
    )

    $mapDir = Join-Path $env:TEMP ("netopslab-map-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $mapDir | Out-Null

    $totalRows = 0; $imported = 0; $failed = @()
    foreach ($wb in $Order) {
        $file = Join-Path $RealData "$wb.xlsx"
        if (-not (Test-Path $file)) { Warn "缺少 $wb.xlsx，跳过"; continue }

        $analysis = & $Bin analyze -file $file 2>$null | ConvertFrom-Json
        $mapFile = Join-Path $mapDir "$wb.json"
        $analysis.suggestedMapping | ConvertTo-Json -Depth 20 | Set-Content -Path $mapFile -Encoding UTF8

        $res = & $Bin import --file $file --map $mapFile --db $Db 2>$null | ConvertFrom-Json
        $s = if ($res.summary) { $res.summary } else { $res }
        $totalRows += [int]$s.rowsImported
        if ([int]$s.rowsFailed -gt 0) { $failed += "$wb($($s.rowsFailed)行)" }
        $imported++
    }
    Remove-Item $mapDir -Recurse -Force -ErrorAction SilentlyContinue

    if ($failed.Count -gt 0) {
        Warn "部分工作簿有失败行：$($failed -join ", ")"
        Info "多为导入顺序问题，检查后可用 -Rebuild 重试"
    }
    Ok "$imported 本工作簿，$totalRows 行（零失败为佳）"

    # 7 日性能指标
    $ingestMap = Join-Path $RealData "ingest-mapping.json"
    if (Test-Path $ingestMap) {
        $days = 0
        foreach ($d in 1..7) {
            $pf = Join-Path $RealData ("perf-day-{0:D2}.xlsx" -f $d)
            if (Test-Path $pf) {
                & $Bin ingest --file $pf --map $ingestMap --db $Db 2>$null | Out-Null
                if ($LASTEXITCODE -eq 0) { $days++ }
            }
        }
        Ok "性能数据 $days/7 天";
    }

    # 检测 -> 告警 -> 运行状态联动
    $det = & $Bin detect --db $Db 2>$null | ConvertFrom-Json
    Ok ("检测：{0} 条发现，{1} 个告警，{2} 个状态联动" -f $det.findings, $det.alertsCreated, $det.runStatesWritten)

    # 物化仿真工作台
    $ws = & $Bin lab workspace create -db $Db -name "黑龙江真实数据演示" -desc "v0.8.1 真实公开数据（OSM + OpenCelliD）" 2>$null | ConvertFrom-Json
    if ($ws.workspaceId) {
        Ok ("工作台：设备 {0} / 接口 {1} / 链路 {2}" -f $ws.counts.devices, $ws.counts.interfaces, $ws.counts.links)
        $ws.workspaceId | Set-Content -Path (Join-Path $Root ".last-workspace") -Encoding UTF8
    }
}

# ── ⑤ 启动服务 ───────────────────────────────────────
Step "启动服务"
$url = "http://127.0.0.1:$Port"
$proc = Start-Process -FilePath $Bin -ArgumentList @("serve", "--addr", "127.0.0.1:$Port", "--db", $Db) `
    -WorkingDirectory $Root -PassThru -WindowStyle Hidden

$ready = $false; $health = $null
foreach ($i in 1..40) {
    Start-Sleep -Milliseconds 400
    try { $health = Invoke-RestMethod "$url/api/health" -TimeoutSec 2; $ready = $true; break } catch { }
}
if (-not $ready) { Fail "服务启动超时（PID $($proc.Id)）" }
Ok ("$url 已就绪（{0}，PID {1}）" -f $health.version, $proc.Id)

# 图层自检：捕获「只重建前端未重建二进制」的陷阱
try {
    $resp = Invoke-WebRequest "$url/mapdata/hlj-cables.geojson.gz" -TimeoutSec 10 -UseBasicParsing
    if ($resp.Headers["Content-Type"] -like "*geo+json*") {
        Ok "地图图层正常（application/geo+json）"
    } else {
        Warn "图层 Content-Type=$($resp.Headers["Content-Type"])，应重建二进制（去掉 -SkipBuild 重跑）"
    }
} catch { Warn "地图图层不可达" }

# 数据概览
$stats = & $Bin query --db $Db stats 2>$null | ConvertFrom-Json
if ($stats) {
    $typeCount = ($stats.byType.PSObject.Properties | Measure-Object).Count
    $relCount = 0
    if ($stats.byRelationType) {
        $relCount = ($stats.byRelationType.PSObject.Properties | Measure-Object -Property Value -Sum).Sum
    }
    Ok ("资源 {0}（{1} 类）/ 关系 {2}" -f $stats.totalResources, $typeCount, $relCount)
}

# ── ⑥ 浏览器 ─────────────────────────────────────────
Step "打开浏览器"
if ($NoBrowser) { Warn "-NoBrowser：跳过" }
else { Start-Process $url; Ok "已在默认浏览器打开" }

Write-Host ""
Write-Host "========================================================" -ForegroundColor DarkGray
Write-Host "  NetOps Lab 运行中" -ForegroundColor Green
Write-Host ""
Write-Host "  地址    $url" -ForegroundColor White
Write-Host "  数据库  $Db" -ForegroundColor DarkGray
Write-Host "  进程    PID $($proc.Id)" -ForegroundColor DarkGray
Write-Host ""
Write-Host "  停止    Stop-Process -Id $($proc.Id)" -ForegroundColor DarkGray
Write-Host "  或      Get-Process netopslab | Stop-Process -Force" -ForegroundColor DarkGray
Write-Host "========================================================" -ForegroundColor DarkGray
Write-Host ""