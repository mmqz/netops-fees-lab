# [P1] TestCLIGraphQueryChannels 恒失败：断言把运行耗时 elapsedMs 纳入深比较

来源：v0.8.1 本地验证（2026-10-14）

## 现象

v0.8.1 修复了 #38（Windows 门禁），全量测试从 **6 包 15 项失败 → 1 包 1 项失败**。
但唯一剩下的这项是 **v0.8.1 新引入的测试缺陷**，导致套件不绿：

```
--- FAIL: TestCLIGraphQueryChannels (6.22s)
    e2e_graphquery_test.go:69: traverse: CLI and HTTP documents differ
FAIL	github.com/mmqz/netops-lab/internal/app	267.550s
```

## 复现与定位（实测）

我构造同一库，分别用 CLI 与 HTTP 查同一电路、同参数：

```
CLI : {"visitedEdges": 2, "elapsedMs": 0}
HTTP: {"visitedEdges": 2, "elapsedMs": 1}
```

**逐顶层键比较结果**：

| 键 | 结果 |
| --- | --- |
| `nodes` | ✅ 完全相同 |
| `edges` | ✅ 完全相同 |
| `root` | ✅ 完全相同 |
| `truncated` | ✅ 完全相同 |
| **`stats`** | ❌ **仅 `elapsedMs` 不同（0 vs 1）** |

**即：契约本身是对的，唯一差异是查询耗时（时钟值，两次调用必然不同）。**

## 根因

`internal/app/e2e_graphquery_test.go:213` `assertJSONEqual` 做**全文档逐字节比较**：

```go
g, _ := json.Marshal(got)      // CLI 文档
w, _ := json.Marshal(want)     // HTTP 文档
if string(g) != string(w) { t.Errorf(...) }   // ← 含 stats.elapsedMs
```

而 `stats` 由设计定义就是**运行时钟**：

```go
// internal/topology/traverse.go
ElapsedMs int `json:"elapsedMs"`   // the ONE wall-clock field
```

**用含时钟值的文档做相等断言，必然失败**——与平台无关，Linux 上同样会红。

## 影响

- **测试套件常红**：CI 门禁失效（真回归会被这条噪音掩盖）
- v0.8.1 的 release 提交称「本地门禁全绿」，但实测该测试失败——
  说明该轮的本地验证**未真正跑通全量套件**（或跑了但未处理）

## 修复建议

在比较前剔除时钟字段（保留契约校验的意图，去掉不可避免的差异）：

```go
// 方案 A：比较前剔除 stats（或仅剔除 stats.elapsedMs）
delete(gotDoc, "stats"); delete(wantDoc, "stats")
// 方案 B：比较 stats 的非时钟子键
//   visitedEdges/truncated 应当相等，elapsedMs 不应参与
// 方案 C：给 elapsedMs 加 `json:"-"`（若它不该进契约）
```

**推荐方案 B**：`stats.visitedEdges` 是确定性契约值（应相等），
只有 `elapsedMs` 是时钟——精确剔除它既保留契约校验又消除噪音。

## 验收

- [ ] `TestCLIGraphQueryChannels` 稳定通过（Windows + Linux）
- [ ] 仍能捕获真实的 CLI/HTTP 契约分叉（如 visitedEdges 不一致时应失败）
- [ ] 全量 `go test ./...` 在 Windows 下全绿

## 附：v0.8.1 验证结论（其余全部通过）

| 项 | v0.8.0 | v0.8.1 | 判定 |
| --- | --- | --- | --- |
| 孤立资源 | 1,486 (75.6%) | **197 (9.9%)** | ✅ #42 修复 |
| `uplink_to` | 0 | **1,198** | ✅ |
| `serves` | 0 | **1,228** | ✅ |
| 有 status | 40 (2.0%) | **1,826 (91.7%)** | ✅ #45 修复 |
| geo 列 | 无 | **1,288 有值** | ✅ #45 修复 |
| 工作台接口/链路 | 20 / 10 | **64 / 32** | ✅ #42 修复 |
| 工作簿/行数 | 31 / 1,965 | **32 / 1,991** | ✅ |
| 导入失败 | 0 | **0** | ✅ |
| 告警 | 4 firing | **4 firing** | ✅ |
| CLI 图查询 | 无 | **traverse/impact/context 均 exit 0** | ✅ #41 修复 |
| 空集合契约 | `null` | **`[]`** | ✅ audit §2.6 修复 |
| Windows 测试 | 6 包 15 项失败 | **1 包 1 项** | ✅ #38 修复（余本项） |