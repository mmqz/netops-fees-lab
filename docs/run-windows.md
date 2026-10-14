# 一键运行（Windows）

> 面向 Windows 用户的零配置启动脚本。
> 现有 `scripts/*.sh` 与 `make demo*` 均为 bash，在 Windows 上无法直接运行——
> `run.ps1` 是它们的 PowerShell 等价物。

## 用法

```powershell
# 最简：全流程 + 自动开浏览器
.\run.ps1

# 换端口
.\run.ps1 -Port 9000

# 强制重建数据库（重新导入全部数据）
.\run.ps1 -Rebuild

# 跳过构建（前端与二进制都已最新时，省 1-3 分钟）
.\run.ps1 -SkipWeb -SkipBuild

# 不开浏览器（适合脚本化/远程）
.\run.ps1 -NoBrowser
```

### 参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-Port` | `8080` | HTTP 端口 |
| `-Database` | `data\netops-real.db` | 数据库路径（相对路径基于仓库根） |
| `-SkipWeb` | 关 | 跳过 `npm ci && npm run build` |
| `-SkipBuild` | 关 | 跳过 `go build` |
| `-Rebuild` | 关 | 删除旧库后重新导入 |
| `-NoBrowser` | 关 | 不自动打开浏览器 |

## 流程（6 步）

```
① 检查端口        占用者是 netopslab 则自动停止；否则报错并提示换端口
② 构建前端        npm ci（缺 node_modules 时）+ npm run build → internal/webui/dist
③ 构建二进制      go build -o bin/netopslab.exe（go:embed 嵌入 dist）
④ 准备数据        按引用依赖序导入 32 工作簿 → 7 日性能 → 检测 → 物化工作台
⑤ 启动服务        等待 /api/health 就绪 + 地图图层自检 + 打印资源/关系数
⑥ 打开浏览器      http://127.0.0.1:<Port>
```

### 数据库复用与自动重建

脚本检查已有数据库的**关系厚度**：

| 情况 | 行为 |
| --- | --- |
| 库不存在 | 全量导入 |
| 关系数 ≥ 2000 | **跳过导入**（幂等，秒级启动） |
| 关系数 < 2000 | **自动重建**（旧版本/残缺库，输出会说明原因） |

**判据为什么用关系数而非资源数**：

```
v0.8.0 旧库  1,965 资源 /   491 关系   ← 基站全孤立，无 uplink_to/serves
v0.8.1 新库  1,991 资源 / 3,193 关系
```

两者**资源数几乎相同**（仅差 26），只有关系数能区分。
若按资源数判断，旧库会被误判为「够新」而跳过导入——
使用者在界面上就会看到一堆孤立节点，却不知道原因。

需要无条件重建时加 `-Rebuild`。

## 两个关键设计（避免踩坑）

### 1. 前端必须先于二进制构建

`internal/webui/dist` 被 `go:embed` 在**构建时**嵌入二进制。
顺序颠倒（或只重建前端）会导致**新增地图图层静默缺失**：

```
GET /mapdata/hlj-cables.geojson.gz
  → 200 text/html     ← SPA 回退返回 index.html，控制台无报错
  期望 application/geo+json
```

脚本按正确顺序执行，并在第 ⑤ 步**主动自检图层 Content-Type**，
发现异常会提示「应重建二进制」。

### 2. 导入必须按引用依赖序

v0.8.1 起，「所属环 / 上联设备 / 服务客户」等列从**文本升级为引用**
（如 `device.ring_code` → `ring` 资源）。

顺序错误会报 `E003 引用不存在`，且**级联失败**：

```
device.ring_code="RING-HDQ" 引用不存在（目标类型 ring）
  → devices 全失败
  → boards / ports / base_stations 随之全失败（一次丢 1,198 行）
```

脚本内置了正确的依赖序（被引用者在前）：

```
customers rings igp_domains vpn_instances
sites machine_rooms bbus olts
devices boards racks ports pon_ports
base_stations rrus onus
optical_cables cable_segments ducts manholes poles
odf odf_terminals splice_boxes
fibers otn_ochs sdh_timeslots circuits
protection_groups ippools services leased_lines
```

## 停止服务

```powershell
Get-Process netopslab | Stop-Process -Force
```

脚本结束时也会打印本次启动的 PID。

## 数据来源

默认演示数据为 `testdata/realdata/`（32 工作簿 + 7 日性能），
真实/模拟字段边界见 `testdata/realdata/manifest.json` 与 [data-sources.md](data-sources.md)：

- **真实**：局站（OSM 地名/坐标）、基站（OpenCelliD 1,198 站）、光缆走廊几何与里程
- **模拟**：机房/纤芯占用/波道/电路/客户/业务等台账细节

导入自己的数据：把工作簿放到任意目录，用 `netopslab analyze / import` 手工导入，
或参考 `-Database` 指定另一个库。

## 常见问题

**Q：提示 `未找到 go/node/npm`**
安装对应工具并加入 PATH。Go 1.27+、Node 24+。

**Q：`npm ci` 很慢 / 失败**
国内网络可设镜像：`npm config set registry https://registry.npmmirror.com`。
已装过依赖可加 `-SkipWeb`。

**Q：端口被占用但不是 netopslab**
脚本会报错并给出占用进程；换端口：`.\run.ps1 -Port 9000`。

**Q：图层不显示**
去掉 `-SkipBuild` 重跑（二进制里嵌的 dist 过期）。

**Q：导入有失败行**
看输出里的 `[!]` 行。多为依赖顺序问题；`-Rebuild` 可重试。