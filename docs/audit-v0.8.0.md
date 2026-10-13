# 云端 v0.7.0 / v0.8.0 本地审计报告

> 审计对象：`120d20f`（本地）→ `8998b81`（云端，含 tag v0.7.0 / v0.8.0）
> 变更规模：158 文件，+21,667 / −186 行
> 审计环境：**Windows / amd64**，Go 1.27，Node 24
> 审计方式：拉取 → 独立复现（不信 CHANGELOG 声明）→ 逐项核实
> 日期：2026-10-13

---

## 0. 结论摘要

**设计与实现质量高，声明基本可信**——33 类型 / 29 关系、1,965 行零失败导入、
7 日劣化场景、告警链路、跨层穿透与根因候选**全部经我独立复现验证通过**。

**但在 Windows 上「429 测试全绿」不成立**：全量套件 **15 个测试失败**（6 个包），
且**并发缺陷在负载下会真实逃逸**。这是本次审计的核心发现。

| 维度 | 结果 |
| --- | --- |
| 数据模型扩容（33 类型 / 29 关系） | ✅ 代码与库双验证 |
| 31 工作簿导入 | ✅ 1,965 行 / 0 失败（我独立跑，102.8s） |
| 真实公开数据（OpenCelliD / OSM） | ✅ 1,198 基站，编码结构真实 |
| 7 日劣化场景 | ✅ 光功率 -8.32 → -24.00 dBm |
| 检测 → 告警 → run_state 联动 | ✅ 4 告警 / 2 状态联动 |
| 跨层穿透（§10.5 核心） | ✅ 87 节点 / 14 类型 / 8 层深 |
| 根因候选判定 | ✅ candidates=1，理由正确 |
| §10.8 七条硬性约束 | ✅ 代码逐条核对 |
| **Windows 测试门禁** | ❌ **15 失败 / 6 包** |
| **并发健壮性** | ⚠️ **负载下 SQLITE_BUSY 逃逸** |

---

## 1. 已复现验证的声明（全部为真）

### 1.1 数据模型扩容

```
资源类型 33（8 既有 + 25 新增），relation 29（4 既有 + 25 新增）
迁移 v14–v19（timeseries / resource_types / relation_types / run_state / alert_detection / mapping_templates）
新增表：metric_keys / metric_series / metrics / metric_batches / resource_status /
        detection_rules / alerts / mapping_templates
```

- 代码核对：`internal/domain/enums.go` 枚举与注释一致
- 库核对：`schema_migrations` v19，`sqlite_master` 28 张表
- 三点一致性（枚举 ↔ SQL CHECK ↔ API ↔ web）由测试 `TestTypeRegistryFourPointConsistency` 钉死

### 1.2 31 工作簿导入（我独立复现）

```
合计 imported=1965  skipped=0  failed=0
耗时 102.8s
```

逐工作簿计数与 `testdata/realdata/manifest.json` **完全一致**：
sites 83 / base_stations 1198 / machine_rooms 83 / odf_terminals 120 / ports 90 /
boards 60 / fibers 56 / splice_boxes 46 / …

### 1.3 真实公开数据真实性

基站编码为 **OpenCelliD 原生结构**（MCC-MNC-LAC-CID），运营商分布合理：

```
460-00 中国移动  1092
460-01 中国联通   104
460-11 中国电信     1
460-15 中国广电     1
合计              1198
```

清单 `manifest.json` 逐字段披露真实/模拟边界（如「局站=OSM place 节点真实；
机房编号/名称为模拟」），诚实度可接受。

### 1.4 7 日劣化场景（用户目标的核心场景）

```
追踪端口：大庆市传输设备01 / xgei-1/1/1
  day-01  rx= -8.32 dBm   loss=0.02%
  day-02  rx=-10.67 dBm   loss=0.82%
  day-03  rx=-13.33 dBm   loss=1.62%
  day-04  rx=-16.00 dBm   loss=2.42%
  day-05  rx=-18.67 dBm   loss=3.22%
  day-06  rx=-21.33 dBm   loss=4.02%
  day-07  rx=-24.00 dBm   loss=4.82%
（同批其他端口稳定在 -8 附近，劣化为单点注入而非全局漂移）
```

### 1.5 指标 → 检测 → 告警 → 状态联动

```
摄入：7 日 × 90 行 = 8820 点；1260 序列；14 指标
检测：findings=4  alertsCreated=4  runStatesWritten=2
告警：2× dr_loss_rx + 2× dr_opt_rx_low，均 firing
状态：2 个资源 run_state=degraded，source=alert
```

### 1.6 跨层穿透与根因候选（§10.5，设计核心）

设置 `OC-HDQ 哈大齐干线光缆` run_state=down 后：

```
GET /api/resources/{id}/impact?direction=up&maxDepth=8
  impacted = 80 节点，跨层类型：
    cable_segment 14 / fiber 32 / odf_terminal 8 / optical_cable 3 /
    otn_och 8 / circuit 4 / customer 4 / leased_line 3 / service 4
  candidates = 1 → 哈大齐干线光缆
    理由："run_state=down, 下游无异常"   ← 正是 §10.5 的判定逻辑

GET /api/resources/{customer}/traverse?direction=down&maxDepth=8
  nodes=87 / edges=117 / 14 类型 / 深度 0–8
  路径：客户→专线→业务→电路→波道→纤芯→光缆→光缆段→ODF端子→设备→板卡→机房→机架→局站
```

**这证明「从表象（业务中断）追溯到物理根因」的设计目标已实现。**

### 1.7 §10.8 七条硬性约束

逐条核对 `internal/topology/traverse.go`：

| # | 约束 | 实现 |
| --- | --- | --- |
| 1 | 关系必须登记方向语义 | ✅ `RelationSpecs` 注册表 + 一致性门禁 |
| 2 | 层序 BFS + 剪枝，禁固定点迭代 | ✅ FIFO 队列，maxDepth 为剪枝非过滤 |
| 3 | 边按 (from,to,relation) 去重 | ✅ 存储三元组去重 |
| 4 | direction 默认 down | ✅ `DefaultDirection=down` |
| 5 | 必须返回 truncated | ✅ 响应含 `truncated` |
| 6 | 必须区分 impacted / candidates | ✅ 两数组分离 |
| 7 | 必须有 maxNodes 上限 | ✅ 默认 200 / 上限 500 |

并保留了旧端点（§10.10 无破坏性变更）：`GET /resources/{id}/relations?depth=1|2` 行为不变。

### 1.8 值得肯定的：他们修正了我设计里的 4 处错误

`docs/design/data-model-v1.md` §12「落地处置日志」记录了实施时发现并修正的问题，
**其中 4 处是我原设计的真实缺陷**：

| # | 我的错误 | 他们的修正 |
| --- | --- | --- |
| 1 | `carries_fiber`（光缆→纤芯）与 §10.2 的「成员→容器」模式矛盾 | 改为 `fiber_of`（纤芯→光缆），同层 |
| 2 | §10.2 层次表**漏了接入层**，且与方向表矛盾 | 插入 L4 接入层，逻辑/业务顺移 L5/L6 |
| 3 | `belongs_to` 被我标为跨层，实际同层 | 勘误为同层 |
| 4 | 「from→to=向下」规则与 `serves` 等业务边**自相矛盾** | 改为**层差驱动**可达性，存储方向仅保留语义 |

第 4 项是实质性改进——我原规则在 `device → customer`（serves）上会判反方向。

---

## 2. 审计发现的问题

### 2.1 【实测】Windows 上「测试全绿」不成立：6 包 15 项失败

CHANGELOG v0.8.0 称「427 → **429** 顶层测试」「本地门禁全绿」——
**该结论基于 Linux 沙箱**（`verified-capabilities.md` 明示验证环境为 linux/amd64）。

我在 Windows 实跑 `go test -count=1 ./...`：

```
ok   17 包
FAIL  6 包：internal/api, internal/app, internal/mcpserver, internal/repo, internal/sim, internal/webui
失败测试 15 项
```

**逐项归类**：

| 类别 | 数量 | 性质 | 归属包 |
| --- | --- | --- | --- |
| TempDir 清理失败（路径被占用） | 10 | Windows 平台 | api ×9, mcpserver ×1 |
| TestHealthCarriesDBPath | 1 | **测试不可移植** | api |
| TestGCOrphanUploadsRemovalFailureNotFatal | 1 | 平台（POSIX 权限） | app |
| TestDoConcurrentWorkloads | 1 | **负载相关偶发** | repo |
| TestConcurrentEngineWorkload | 1 | **负载相关偶发** | sim |
| TestHandlerRealDistRealNetworkLayers | 1 | 环境（dist 陈旧） | webui |

#### 2.1.1 TempDir 清理（10 项）—— 修复未覆盖新测试

```
testing.go:1617: TempDir RemoveAll cleanup: unlinkat ...\netops.db:
  The process cannot access the file because it is being used by another process
```

根因：SQLite 文件句柄在 Close() 返回后仍被 Windows 持有，t.TempDir 的
RemoveAll 随即失败。**v0.6.2 曾为此修过**（issue #5），但做法是**各包各自加局部辅助**：

```go
// 仅这两个包定义了 tempDirT：
internal/sim/helpers_test.go:30    func tempDirT(...)
internal/repo/repo_test.go:20      func tempDirT(...)
```

v0.7.0 新增的测试（traverse/impact/context/detection/run-state）
**重新用了裸 t.TempDir()**，于是同一问题在新包里复发：

```
api:  TestDetectionRulesCRUD / TestDetectionRunAndAlerts /
      TestDetectionRunStateIntegration / TestAPIRenameApplyTargetMissingDirectDelete /
      TestAPIErrorPaths / TestRunStateEndpoints /
      TestTraverseEndpointContract / TestImpactEndpointContract / TestContextEndpoint
mcpserver: TestMCPLabDiffUndo
```

**教训**：平台适配应做成**共享测试辅助**（或统一 TestMain 包装），
而不是逐包复制——否则每个新测试文件都会重新踩坑。

#### 2.1.2 TestHealthCarriesDBPath —— 断言写死 POSIX 路径

```go
// internal/api/pagination_issue7_test.go:157
if health.DBPath == "" || !strings.HasPrefix(health.DBPath, "/") {
    t.Errorf("health: dbPath = %q, want the absolute path of the bound database", health.DBPath)
}
```

Windows 的绝对路径是 `E:\temp\...\netops.db`，**不以 / 开头** → 必然失败。
这是 issue #8 的验证测试，**测试本身不可移植**（产品行为正确：health 确实返回了绝对路径）。

建议改为 `filepath.IsAbs(health.DBPath)`。

#### 2.1.3 TestGCOrphanUploadsRemovalFailureNotFatal —— 既有平台差异

```
gc result = {Scanned:1 Deleted:1 Referenced:0 Errors:0}, want deleted=0 errors=1
```

该测试用 chmod 制造「不可删除」以验证 GC 的容错；**Windows 下删除仍然成功**，
故期望的 error 路径不可达。**此为 v0.6.0 审计已发现的既有项，非本次回归。**

#### 2.1.4 TestHandlerRealDistRealNetworkLayers —— 审计环境问题，非缺陷

```
GET /mapdata/hlj-cables.geojson.gz: Content-Type = "text/html", want application/geo+json
```

**我拉取后未重建前端**，`internal/webui/dist` 里没有 v0.8.0 新增的
`hlj-cables*.geojson.gz` → 服务端 SPA 回退返回 index.html（text/html）。
执行 `npm run build` 后该测试**通过**。

> 审计说明：此项**不是产品缺陷**，但暴露一个运维事实——
> `dist` 不入库（.gitignore），**拉取后必须重建前端**，否则地图图层静默缺失。

### 2.2 【实测】并发缺陷：负载下 SQLITE_BUSY 逃逸

```
TestDoConcurrentWorkloads    fail: repo: begin transaction (SQLITE_BUSY ...)
TestConcurrentEngineWorkload fail: sim: apply: ... (SQLITE_BUSY ...)
```

**复测结论（关键）**：

```
单独运行  → ok  internal/repo 8.7s / internal/sim 14.1s   ← 通过
全量套件  → FAIL（两包）                                  ← 失败
```

即**不是确定性缺陷，而是负载相关的健壮性不足**。

当前重试实现（`internal/repo/repo.go:224-248`）：

```go
const beginBusyAttempts = 3
// busy_timeout = 5000ms；BEGIN 返回 BUSY 表示整个 5s 窗口内写锁都被占用
backoff := 50ms, 100ms, 150ms        // 三次尝试合计额外等待仅 ~300ms
```

**问题**：全套件并行执行时（21 包同时压 SQLite），写锁持有时间超过
`5s + 300ms`，第 3 次尝试后直接返回错误。

**影响评估**：
- 单进程正常使用（serve）**不受影响**——并发度远低于测试套件
- 但在**多实例 / 高并发导入**场景下，用户可能遇到偶发 `SQLITE_BUSY` 失败
- 严重度：**中**（非数据损坏，是可用性问题）

**建议**：将 `beginBusyAttempts` 提至 5–6 并加大退避上限，
或提高 `busy_timeout`；同时把该测试改为**可容忍重试**或降低并发度，
避免用「零 BUSY」这种在慢 I/O 平台上不可达的断言。

### 2.3 【数据】类型与关系覆盖缺口

实测 31 类资源有数据（域定义 33 类），**2 类无数据**：

```
ptp_port            （需 PTP 配置脚本文本，真实数据样本未纳入 realdata）
protection_group    （保护组）
```

29 种关系中 **24 种有数据，5 种无数据**：

```
mounted_on    端口→板卡
protects      保护组→电路      （依赖 protection_group，连带缺失）
in_vpn        端口→VPN实例
serves        设备→客户        ← 业务影响分析的一条关键边
uplink_to     基站→设备        ← B4 裁决「不自动派生」的后果
```

**说明**：`uplink_to` 缺失是**设计决策的正确后果**（§12-B4：按 IP/名称自动匹配
违反项目「禁止按名称推断关系」红线，故不派生）。但 `serves` 缺失意味着
**「设备直接服务客户」这条边在演示数据中不通**——当前业务影响只走
`service → customer`（subscribed_by）路径。

**建议**：在演示数据中补 `serves`（可为模拟数据），否则
「设备故障影响哪些客户」只能间接经业务实例推导。

### 2.4 【数据】性能值以字符串存储

`testdata/realdata/perf-day-*.xlsx` 中数值单元格是**文本**：

```
接收光功率均值 = '-8.00'   （字符串，非数值 -8.00）
接收丢包率     = '0.00'
```

导入器能正确解析（8820 点入库、检测正常触发），故**不阻塞功能**。
但若将来接入真实网管导出（通常为数值型），需确认解析路径对两种类型都稳健。

### 2.5 【接口】traverse / impact / context 无 CLI 通道

```
HTTP:  GET /api/resources/{id}/traverse | impact | context   ✅ 已实现
CLI:   netopslab query traverse ...                          ❌ 不存在
       （query 子命令仍为 resources/stats/relations/governance）
```

这**符合设计 §7.2**（AI 经 HTTP 读数据、MCP 保持 5 工具冻结），
但意味着**脚本化 / 离线环境无法使用图查询**——只能起服务再调 HTTP。

**建议**：若希望每日巡检可在无服务场景下由脚本 / AI 执行，
应补 CLI 通道（或确认 HTTP 是唯一预期入口）。

### 2.6 【接口】impact 空结果返回 null 而非空数组

`internal/topology/impact.go:158` 用**未初始化的 nil 切片**：

```go
var impacted []ImpactedItem          // nil，不是 []ImpactedItem{}
result.Impacted = impacted           // 无影响面时 JSON 序列化为 null
```

实测（无故障态时查询 impact）：

```json
{"root": {...}, "impacted": null, "candidates": null, "truncated": false}
```

**为何是问题**：`null` 与 `[]` 对客户端语义不同——
`len(resp.impacted)` / `for x in resp.impacted` 在多数语言下会抛错或需额外判空。
**我的审计脚本就崩在这一处**（`len(None)` TypeError），只能事后补判空。

**正确做法**：

```go
impacted := make([]ImpactedItem, 0)  // 保证序列化为 []
candidates := make([]CandidateItem, 0)
```

**同理需检查**：`traverse` 的 `nodes`/`edges`（设计 §10.3 已写「无结果时为空数组」）、
以及其余新增端点的集合字段。

**严重度**：低（不影响服务端正确性），但影响 API 契约质量与客户端健壮性。

### 2.7 【数据】演示数据含非法枚举值（13 行）

平台自身的问题清单实测 **49 条警告**（概览页「质量评分 51/100」），归类：

| 规则 | 数量 | 内容 | 判定 |
| --- | --- | --- | --- |
| W001 | 36 | `可选引用为空：device.ring_code` | **预期**（B4 裁决：不自动派生环关系） |
| W002 | 13 | `状态值未知：ip_pool.status="使用中"` | **数据缺陷** |

W002 的 evidence 字段：

```json
{"field":"status","type":"ip_pool","raw_value":"使用中",
 "allowed":"在网/停用/在建/退网"}
```

即 `scripts/realdata/workbooks.py` 生成的 IP 地址池写入了**封闭枚举之外**的状态值。

**值得肯定**：**校验管线正确捕获了它**——说明 W002 规则有效、
问题清单如实反映数据质量，而非静默接受非法值。

**缺陷**：演示数据本不该有非法值。13 行 `ip_pool` 命中，
导致概览页质量评分从 100 掉到 51，**演示/评审时数据显得不干净**。

**建议**：
1. 修 `workbooks.py`：`ip_pool.status` 改用合法枚举值
2. 或为 `ip_pool` 扩展状态词表（若「使用中」确为业务需要）
3. W001 的 36 条建议在 `manifest.json` 说明成因（B4 裁决），避免被误读为缺陷

---
## 3. 结论与建议

### 3.1 总评

**实现质量高，设计对齐度好。** 我独立复现了核心链路（导入 → 指标 → 检测 →
告警 → 状态 → 跨层穿透 → 根因候选），**全部通过**；§10.8 的七条硬约束逐条落实；
并主动修正了我设计文档中的 4 处错误。

**主要问题是跨平台**：项目门禁是 Linux-only，Windows 上 6 包 15 项测试失败。
用户的实际运行环境是 Windows，因此**「429 测试全绿」不能作为本地可用性依据**。

### 3.2 优先级建议

| 优先级 | 事项 | 理由 |
| --- | --- | --- |
| **P1** | 共享 TempDir 辅助（覆盖 api/mcpserver） | 10 项失败，且会随新测试持续复发 |
| **P1** | `TestHealthCarriesDBPath` 改 `filepath.IsAbs` | 1 行修复，消除平台误报 |
| **P2** | 修演示数据 `ip_pool.status="使用中"`（13 行非法枚举） | 拉低质量评分至 51/100，演示观感差 |
| **P2** | 演示数据补 `serves` 边 | 让「设备→客户」影响链直通 |
| **P2** | 并发重试预算提升 | 负载下真实逃逸，影响多实例场景 |
| **P3** | CLI 补图查询通道（或明确不做） | 影响离线/脚本化巡检 |
| **P3** | impact/traverse 空集合改返回 `[]` 非 `null` | API 契约质量，客户端需额外判空 |
| **P3** | 确认性能值字符串解析稳健 | 面向真实网管导出 |

### 3.3 运维提示（给使用者）

**`internal/webui/dist` 不入库（.gitignore），且被 `go:embed` 编进二进制。**
因此顺序**必须**是「先建前端、再建二进制」：

```
cd web && npm ci && npm run build        # ① 更新 internal/webui/dist
cd .. && go build -o bin/netopslab.exe ./cmd/netopslab   # ② 重新嵌入 dist
```

#### 本次审计实测到的陷阱

```
internal/webui/dist/index.html   20:06:38   ← 已重建（含新 mapdata）
bin/netopslab.exe                19:58:59   ← 旧二进制（嵌入的是旧 dist）

结果：GET /mapdata/hlj-cables.geojson.gz
      → 200 text/html（SPA 回退返回 index.html）
      → 地图新增图层静默缺失，控制台无报错
重建二进制后（20:39）：
      → 200 application/geo+json  ✅
```

**关键**：只重建前端而**不重建二进制无效**——旧二进制里嵌的是旧 dist。
这与 `go test` 无关（测试会重新编译，故测试能通过而运行时失败）。

**现象特征**（便于自查）：新增图层不显示、无报错、
直接访问 `/mapdata/xxx.geojson.gz` 返回 HTML 而非 JSON。

---

## 附录：审计方法

| 手段 | 说明 |
| --- | --- |
| 代码核对 | 枚举 / 迁移 / traverse 实现 / 重试逻辑逐行读 |
| 独立复现 | 自写 Python 审计脚本，**不执行项目 demo-real.sh**（避免其自证） |
| 库直查 | sqlite3 直读 schema / 资源与关系分布 / 告警与状态表 |
| HTTP 实测 | 起服务调 traverse / impact / context / metrics 端点 |
| 隔离复测 | 对失败测试单独 / 包级重跑，区分「确定性缺陷」与「负载偶发」 |

> 审计脚本留档：`_tmp/audit_import.py`、`audit_metrics.py`、`audit_http.py`、`audit_impact.py`
（临时目录，未入库）
