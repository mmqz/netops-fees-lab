# v0.8.1 本地验证报告

> 验证对象：`5980832` → `47cfc04`（tag v0.8.1）
> 变更规模：132 文件，+2,535 / −560 行
> 环境：Windows / amd64
> 日期：2026-10-14

---

## 0. 结论

**v0.8.1 是针对 v0.8.0 审计 8 个 issue（#38–#45）的修复轮，修复效果显著。**

我逐项独立复测，**7 项完全修复，1 项部分（仅剩一个测试断言缺陷）**：

| # | 问题 | v0.8.1 状态 |
| --- | --- | --- |
| #38 | Windows 测试门禁（15 项失败） | ✅ **修复**（15 → 1） |
| #39 | 并发重试预算 | ✅ 修复（重试预算提升） |
| #40 | 演示数据质量（serves/非法枚举） | ✅ 修复 |
| #41 | 图查询无 CLI | ✅ **修复**（traverse/impact/context 齐备） |
| #42 | 层级不足（75% 孤立） | ✅ **修复**（75.6% → 9.9%） |
| #43 | 地理视图卡顿 | ✅ 修复（selected 移出 useMemo 依赖） |
| #44 | 布局三行冗余 | ✅ 修复（面板默认折叠、按钮合并） |
| #45 | 字段厚度（98% 无状态） | ✅ **修复**（2% → 91.7%） |
| — | 新发现的测试缺陷 | ⚠️ **#46**（见下） |

---

## 1. 关键指标对比（独立复测，非采信声明）

| 指标 | v0.8.0 | v0.8.1 | 变化 |
| --- | --- | --- | --- |
| 资源总数 | 1,965 | 1,991 | +26 |
| 关系总数 | 491 | **3,015** | +2,524 |
| 孤立资源 | 1,486 (**75.6%**) | **197 (9.9%)** | ✅ **−1,289** |
| `uplink_to` | **0** | **1,198** | ✅ 恢复 |
| `serves` | **0** | **1,228** | ✅ 恢复 |
| `in_vpn` / `mounted_on` | 0 | 90 / 90 | ✅ |
| `covers` / `protects` | 0 | 5 / 4 | ✅ |
| 有 status 的资源 | 40 (**2.0%**) | **1,826 (91.7%)** | ✅ **+1,786** |
| geo 列有值 | 无 geo 列 | **1,288** | ✅ 新增列 |
| 工作台 接口/链路 | 20 / 10 | **64 / 32** | ✅ **3.2 倍** |
| 工作簿 / 行数 | 31 / 1,965 | **32 / 1,991** | +1 本 |
| 导入失败行 | 0 | **0** | ✅ 保持 |
| 告警 | 4 firing | **4 firing** | ✅ 保持 |

### 1.1 演示数据字段厚度（#42/#45）

`base_stations.xlsx` 从 **4 列 → 17 列**：

```
基站编码 | 无线基站名称 | 基站类型 | 地市 | 环 | 链 | 电联共享 |
归属A IP | 归属A名称 | 归属A端口 | 网关IP | 基站IP | 状态 |
上联设备 | 服务客户 | 经度 | 纬度
```

新增 17 列中，**环/链、归属A、电联共享** 正是设计 §3.2 记录的真实表字段。
`protection_groups.xlsx` 为新增工作簿（6 列）。

### 1.2 工作台物化（#42 核心）

```
v0.8.0: 设备 30 / 接口 20 / 链路 10   → 18/30 设备零接口（孤立点）
v0.8.1: 设备 30 / 接口 64 / 链路 32   → 拓扑连通成网
```

截图对比见报告末尾。

### 1.3 代码层修复（逐行核对）

**#43 卡顿**（`GeoCanvas.tsx:787`）：

```diff
- const geoData = useMemo(() => buildGeoData(topology, selected, gcj), [topology, selected, gcj]);
+ const geoData = useMemo(() => buildGeoData(topology, gcj), [topology, gcj]);
```

`selected` 移出依赖；簇标记改为**增量复用**（`delete`/`get` 复用已有 marker，
不再全量 `forEach(remove)` + 重建）。

**#38 测试门禁**：新增 `internal/testsupport.TempDir`，
被 **19 个包**采用（repo 29 处、app 22 处、metrics 22 处、api 8 处、mcpserver 2 处…），
`dbPath` 断言改用 `filepath.IsAbs`。

**#41 CLI**：`query` 新增 `traverse` / `impact` / `context` 三个子命令，
含 `--direction` 取值校验（impact 不支持 both）。

**#44 布局**：`readPanelCollapsed` 无记录时返回 **true**（默认折叠），
且注释明确「自动展开不落盘，避免把默认值反向固化成已展开」。

**#45 geo**：migration v20 新增 `geo_lat`/`geo_lng` REAL 列 + 索引，
并用 `json_extract` 回填历史数据（纯增量、可重复执行）。

---

## 2. 唯一遗留问题：测试断言缺陷（#46）

```
--- FAIL: TestCLIGraphQueryChannels (6.22s)
    traverse: CLI and HTTP documents differ
```

**实测差异定位**：

```
CLI : {"visitedEdges": 2, "elapsedMs": 0}
HTTP: {"visitedEdges": 2, "elapsedMs": 1}
```

`nodes` / `edges` / `root` / `truncated` **全部相同**，
**唯一差异是 `stats.elapsedMs`（运行时钟）**。

`assertJSONEqual`（`e2e_graphquery_test.go:213`）做全文档逐字节比较，
把时钟值纳入断言 → **必然失败**（与平台无关）。

**这是 v0.8.1 新引入的测试缺陷，非产品缺陷**，但导致套件常红、CI 门禁失效。

详见 issue #46。

---

## 3. 验证方法（可复现）

```
1. 拉取：git pull（5980832 → 47cfc04）
2. 建前端（必须先）：cd web && npm ci && npm run build
3. 建二进制（必须在后）：go build -o bin/netopslab.exe ./cmd/netopslab
   ← dist 被 go:embed 构建时嵌入，顺序颠倒会导致图层静默缺失
4. 导入 32 工作簿（须按引用依赖序）：
   customers rings igp_domains vpn_instances sites machine_rooms bbus olts
   devices boards racks ports pon_ports base_stations rrus onus
   optical_cables cable_segments ducts manholes poles odf odf_terminals
   splice_boxes fibers otn_ochs sdh_timeslots circuits protection_groups
   ippools services leased_lines
   → 32/32 成功，1,991 行，零失败
5. ingest 7 日 → detect → lab workspace create
6. go test -count=1 ./... → 1 包 1 项失败（#46）
```

### 3.1 导入顺序（重要）

v0.8.1 把「所属环」「上联设备」「服务客户」等**从文本升级为引用列**，
因此导入必须满足引用依赖（被引用者先导）。

**错误顺序的症状**：

```
E003 引用不存在：device.ring_code="RING-HDQ"（目标类型 ring）
E003 引用不存在：base_station.uplink_device="DEV-..."（目标类型 device）
```

且会**级联**（device 失败 → board/port/base_station 全失败）。

**建议**：项目应提供 `scripts/import-all.sh` 内置依赖序（当前 `demo-real.sh` 的
ORDER 数组已过时，仍是 v0.8.0 的顺序）——否则使用者会踩同样的坑。

---

## 4. 待办

| # | 优先级 | 内容 |
| --- | --- | --- |
| [#46](../issues/issue-46-cli-http-clock-assert.md) | **P1** | 测试断言把 elapsedMs 纳入比较，套件常红 |
| 建议 | P3 | 更新 `scripts/demo-real.sh` 的导入顺序（含新增引用依赖） |