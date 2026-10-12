# NetOps Lab 全面测试报告（Windows 平台）

> 测试时间：2026-10-12 · 环境：Windows / amd64 · Go 1.27.0 · Node 24.18.0 / npm 10.9.8
> 版本：v0.6.0（commit 2033de8）
> 测试范围：Go 测试套件、CLI、HTTP API、MCP、导入管线、仿真引擎、前端 2D/3D 画布

## 0. 结论摘要

系统核心引擎（导入/仿真/撤销/检查点/MCP）**功能正确、确定性达标**，实测通过。
但存在 **1 个 P0 阻断缺陷**（2D/3D 地图完全不可用）与 **15 个 Windows 平台测试失败**，
根因均为 **CI 仅覆盖 ubuntu-latest**（.github/workflows/ci.yml:17/38）。

| 层级 | 实测结果 |
| --- | --- |
| Go 测试 | 17 包 ok / **3 包 FAIL（15 个失败测试）** |
| CLI | 13 子命令，错误信封与退出码规范，**发现 3 处问题** |
| HTTP API | 全部端点正常，错误码规范，**发现 1 处问题** |
| MCP | 5 工具，安全边界严密，**未发现缺陷** |
| 导入管线 | 幂等/校验正确，**发现 1 个设计缺陷** |
| 仿真引擎 | 确定性/撤销/检查点**全部通过** |
| 前端画布 | 拓扑图正常，**2D/3D 地图 P0 阻断** |

---

## 1. P0 — 2D/3D 地图完全不可用（MIME 类型缺陷）

### 现象
切换到「2D 地图」或「3D 地图」后，底图不加载，地图区域一片空白。
浏览器控制台报错：
```
Error: Worker failed to load. Check that the worker URL is correct.
```
`window.__geomap.loaded()` 恒为 `false`。

### 根因
Go 嵌入式静态文件服务返回了错误的 Content-Type：

```
请求 /assets/maplibre-gl-worker-VaXr2Fck.mjs
实际 Content-Type: text/plain; charset=utf-8     ← 错误
期望 Content-Type: text/javascript 或 application/javascript
```

浏览器据此拒绝将该文件作为 ES Module Worker 加载。

复现（Go mime 表查询）：
```
.mjs  ->  "text/plain; charset=utf-8"   ← 缺陷
.js   ->  "application/javascript"      ← 正常
.css  ->  "text/css; charset=utf-8"
```

Windows 上 `mime.TypeByExtension` 从注册表读取，`.mjs` 未注册即回退 `text/plain`；
Linux 的 Go 内置表含 `.mjs`，故 CI 无法发现。

`internal/webui/webui.go:52` 仅硬编码了 HTML 的 Content-Type，其余依赖 Go 标准库推断。

### 影响
- **2D 地图、3D 地图视图完全不可用**（功能级失效，非降级）
- 数据源本身正常（`devices`/`links`/`towers`/`basemap`/`basemap-ref` 均已注册，zoom 已按数据定位）
- 拓扑图视图不受影响

### 证据
有地理坐标数据时（10 台带经纬度设备 + 10 条电路）：
```
MAP2D={"loaded":false,"zoom":3.14,
       "sources":["devices","links","towers","basemap","basemap-ref"]}
ERRS=["Error: Worker failed to load..."]
```
即 **数据完全正确，地图依然出不来**。

---

## 2. Windows 平台测试失败（15 个）

`go test -count=1 ./...` → 17 包 ok，3 包 FAIL。

### 2.1 E2E 二进制缺 .exe 后缀（11 个测试）
`internal/app/e2e_test.go:46` 硬编码无扩展名路径：
```go
bin := filepath.Join(tmp, "netopslab")
```
Windows 下 `exec.Command` 找不到该文件：
```
exec: "E:\temp\netopslab-e2e-3203553024\netopslab": executable file not found in %PATH%
```
受影响：TestCLIEndToEnd、TestCLIChangesFlow、TestCLIChangesRefRetargetFlow、
TestCLIExportRoundTrip、TestCLIExportUsage、TestCLIGovernanceSummary、
TestCLIGovernanceEmpty、TestCLIGovernanceUsage、TestCLILabFlow、
TestCLIRenamesFlow、TestCLIRenamesReject

### 2.2 TempDir 清理失败（3 个测试）
```
testing.go:1617: TempDir RemoveAll cleanup: unlinkat E:\temp\...: The directory is not empty.
```
受影响：TestIssueCountByBatchMatchesList、TestMarkRevertedStatusGuard、TestApplyOpsSemanticFailures

### 2.3 文件名含 Windows 非法字符（1 个测试）
`internal/repo/polish_test.go:137`：
```
MkdirAll: mkdir ...\scratch dir?with#hash: The filename, directory name,
or volume label syntax is incorrect.
```
`?` 与 `#` 在 Windows 文件名中非法。

---

## 3. 导入管线

### 3.1 类型识别仅依赖 sheet 名（设计缺陷）
`internal/mapping/suggest.go:88` 的 `SuggestType` 只用 sheet 名匹配 SheetHint：
```go
func SuggestType(sheetName string) (domain.ResourceType, bool) {
	for _, spec := range Specs {
		if strings.Contains(sheetName, spec.SheetHint) {
```
网管/系统导出通常名为 `Sheet1`/`导出结果`/`查询结果`，不含任何 hint →
`SuggestMapping` 判 `action: skip` → **整表被跳过，rowsTotal=0**。

实测：72 列表头齐备的工作簿，导入结果 `{rowsTotal:0, rowsImported:0}`；
手工指定 `resourceType: port` 后同文件立刻正常导入（5 行 → 2 导入/1 跳过/2 失败）。
**同一文件、同一运行，唯一变量是映射方式。**

### 3.2 已正确处理的部分（实测确认）
- 重复表头行（多段拼接）自动跳过，零 issue
- 复合自然键拼接正确（`B2|bvi2.1008`）
- 带宽 0 正常接受
- 幂等重导：0 新增 0 变更
- 错误信息精准（含行号、原始值、字段名）
- 畸形工作簿：空表/仅表头报 `empty_workbook`（rc=1），超大列数（300 列）正常

---

## 4. CLI

### 4.1 report --format json 无输出
`netopslab report --db <db> --format json --out <file>` 退出码 0 但 **stdout 为空**
（HTML 格式正常输出）。
```
### report-json
EXITCODE=0
（无任何输出）
```

### 4.2 无扩展名构建产物在 Windows 不可执行
`make build` 产出 `bin/netopslab`（无后缀），Windows 下无法直接运行，需手动改为 `.exe`。

### 4.3 服务与 CLI 可使用不同 DB，无任何提示
CLI 默认 `./data/netops.db`，服务 `--db` 亦可指定其他库。
用 CLI 建工作区后，Web 页面显示「工作台不存在或已被删除」——
因为服务连的是另一个库。**无任何警告或校验**，极易误判为数据丢失。

### 4.4 已正确实现（实测确认）
- 统一 JSON 错误信封 + 规范退出码（usage=2 / not_found=1）
- `--help` 在各子命令下输出用法
- 未知子命令、缺参数、非法枚举值均明确报错

---

## 5. HTTP API

### 5.1 limit/offset 负数未校验
`GET /api/resources?limit=-1`、`?offset=-5` 返回 **200** 而非 400，静默忽略负值。
（对比：`?type=bogus`、`?severity=bogus` 均正确返回 400）

### 5.2 已正确实现（实测确认）
- 全部端点 200，未知路径 404 带路径信息
- 非法枚举 400 且消息列出合法值
- 请求体非 JSON / 缺必填字段 → 400 `invalid_request`
- `/api/schema` 忠实投影 8 种资源类型

---

## 6. MCP（安全边界实测）

MCP 为**真实 JSON-RPC 协议服务**（标准 initialize / tools/list 握手），
server name `netops-lab` v0.6.0，恰好 5 个工具。

### 6.1 安全边界验证（全部通过）
| 尝试 | 结果 |
| --- | --- |
| `create_workspace`（MCP 建工作区） | 拒绝：not a forward op code |
| `restore_link`（内部操作码） | 拒绝：schema 严格校验 |
| `raw_sql`（SQL 注入） | 拒绝：schema 拒绝未知字段 |
| 空 ops `[]` | 拒绝：ops must contain at least one operation |
| 自环链路 | 拒绝：link endpoints must be distinct |
| 不存在实体 | 拒绝：link not found [changeset recorded as failed] |
| 非法 status 值 | 拒绝：invalid status "BROKEN" (want up or down) |

### 6.2 全闭环实测（通过）
```
基线        score=95  counts={error:0,warning:5,info:36}  run-1
注入故障     set_link_status ln-1 down -> cs-1 trigger=user applied=1
重算        score=94  run-3
影响分析     scoreDelta=-1
            connectivityLost=[DEV-001<->DEV-002, DEV-001<->DEV-003, ...]
撤销        cs-2
验证        hash 回到 aea27501fa59  ← 与基线逐字节相等
```
**哈希精确回环**证明这是带事务、状态机、逆操作的真引擎，而非表层改写。

---

## 7. 仿真引擎正确性（全部通过）

| 项目 | 实测结果 |
| --- | --- |
| 物化确定性 | 两次物化 hash 相同（`aea27501fa592c3f`） |
| 规模 | 27 设备 / 40 接口 / 20 链路 |
| 路径查询 | DEV-001→DEV-003 = 2 跳（真图 BFS） |
| 检查点恢复 | 恢复后 hash == 检查点 hash ✓ |
| 三层撤销 | 事务回滚 / 变更集逆向 / 检查点均正确 |
| 失败变更集 | `opsApplied=0`、状态 `failed`、不污染状态 ✓ |
| 状态哈希 | 断开再恢复精确回到基线 ✓ |

---

## 8. 能力差距（对照「本地云网运维系统」目标）

| 目标能力 | 现状 |
| --- | --- |
| xlsx 导入 + 幂等同步 | ✅ 已具备 |
| 资源拓扑物化 | ✅ 已具备 |
| 仿真操作 + 影响分析 | ✅ 已具备（拓扑连通性维度） |
| 2D/3D 地理展示 | ❌ P0 阻断（MIME 缺陷） |
| **数据异常检测** | ❌ **完全不存在** |
| **设备告警** | ❌ **完全不存在** |
| **趋势预测** | ❌ **完全不存在** |
| 网管周期同步 | ❌ 无调度层（内网限制，暂不可做） |
| 业务层影响（基站/客户） | ❌ relations 仅 assigned_to/a_end/z_end，基站与电路无关联 |
| 双库联动 | ❌ 工作区为物化快照，新批次不自动同步 |

### 8.1 关于告警/异常/预测的搜索结论
对 `docs/*.md`、`README.md`、`internal/**/*.go` 全文搜索
`告警|异常检测|预测|趋势|anomaly|predict|forecast|阈值`：
- 文档中唯一命中是「go vet 无告警」「ID 预测」等无关语义
- 代码中 `predict` 仅指检查点恢复的 **ID 分配预测**（checkpoint.go:195/353/433）
- **无任何业务告警、异常检测、趋势预测实现**

---

## 9. 根因总结

**所有 Windows 缺陷的共同根因：CI 仅覆盖 ubuntu-latest。**

```yaml
# .github/workflows/ci.yml
jobs:
  go:
    runs-on: ubuntu-latest   # ← 第 17 行
  web:
    runs-on: ubuntu-latest   # ← 第 38 行
```

因此：
- E2E 的 `.exe` 后缀问题 → 从未暴露
- `.mjs` MIME 问题 → Linux 持有正确映射，从不复现
- Windows 非法文件名字符 → 无从触发
- TempDir 句柄释放时序差异 → 无从触发

### 建议
1. **引入 windows-latest CI job**（至少覆盖 `go test`），这是消除整类缺陷的根本手段；
2. 对 `.mjs`/`.wasm` 等现代资源显式注册 MIME，不依赖平台注册表
   （`internal/webui/webui.go` 已硬编码 HTML，扩展该处即可）；
3. E2E 用 `filepath.Join(tmp, "netopslab"+exeSuffix())`（`runtime.GOOS` 判定）。

---

## 10. 未复现 / 无法验证项（诚实声明）

- 网管周期同步：需内网接口，本次不涉及
- 真实运营商数据：按仓库红线未使用
- 多工作区并发写：文档已声明不支持，未测试
- Linux 平台行为：本报告全部基于 Windows，未做跨平台对比

---

*本报告所有结论均来自实测复现。测试过程中仓库代码未被修改（`git status` 干净）。*

---

## 11. 更正与补充（Docker/Linux 验证后）

### 11.1 更正：地图不显示的主因判断有误

本报告初版将 2D/3D 地图失效归因于 Windows 特有的 `.mjs` MIME 缺陷，
并推测「Linux 上地图应正常」。**Docker 实测证明该推断错误。**

### 11.2 Docker 实测结果

构建 Linux 镜像并运行后：

| 检查项 | Windows | Linux (Docker) |
| --- | --- | --- |
| `.mjs` Content-Type | `text/plain` ❌ | `text/javascript` ✅ |
| MapLibre Worker | 加载失败 | 正常 |
| `.geo-map` 高度 | 0 | **0** ❌ |
| 地图是否显示 | 否 | **否** ❌ |

**结论**：MIME 缺陷确实仅存在于 Windows，但地图在 Linux 上同样不显示。
真正的主因是 `.geo-map` 被 `.maplibregl-map` 覆盖导致的高度塌陷（与平台无关）。

### 11.3 两个缺陷的叠加关系

```
issue-01  CSS 高度塌陷  →  所有平台，主因
issue-02  MIME 错误     →  仅 Windows，叠加
```

- 只修 issue-01 → Linux 恢复正常，Windows 仍黑屏
- 只修 issue-02 → 地图仍黑屏

### 11.4 修复验证

对 `web/src/styles/global.css` 提升 `.geo-map` 选择器特异性后重建：

```
2D: mapRect 756x0 → 756x644   canvas 756x300 → 756x644   loaded=true
3D: mapRect 756x0 → 756x644   pitch=58°                  loaded=true
控制台: 零错误
```

2D 地图正常显示底图与设备落点；3D 地图斜视正常。

### 11.5 修复后新发现：3D 节点光柱问题

地图可见后暴露新的渲染质量缺陷（issue-09）：
3D 节点底面实测宽 39.9px（设计目标 7px），高 52.3px，
宽高比 1:1.31，呈「矮胖光柱」而非「细高塔」，遮挡底图且顶部被裁切。

该问题此前被黑屏掩盖，**与平台无关**。

### 11.6 方法论启示

本案例说明：**跨平台验证不可替代**。
仅在 Windows 上测试会误判主因（归咎于平台特有缺陷），
仅靠 Linux/CI 则完全发现不了地图路径的问题（CI 无该路径覆盖）。


### 11.7 3D 视图的两个额外实测结论

修复 issue-01 后，3D 视图可见，暴露出 issue-09 的两个问题（均已实测量化）：

**A. 节点尺寸失控**

| zoom | 底面宽 | 实际高度 |
| --- | --- | --- |
| 8.41 | 19–42 px | 15,383 m |
| 7.20 | 25–40 px | 35,502 m |
| 6.40 | **109–217 px** | **62,001 m** |

- 宽度随缩放变化达 11 倍，与「恒定 7px」的设计注释不符
- 真实高度随缩小暴涨至 62 公里，无物理约束

**B. 3D 底图为平面投影，无地形**

```
basemap:  type = raster   （平面瓦片）
terrain:  null            （无 DEM/terrain source）
maxPitch: 60°
```

「3D 地图」实为 **2D 平面底图 + 58° 倾斜相机**，不含任何地形起伏。


