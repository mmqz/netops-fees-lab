# v0.10.0 / v0.11.0 本地验证报告

> 拉取方式：`gh api` 下载 tarball（git 443 不通，见 §4）
> 验证对象：`5ca2962`（云端 main，含 v0.9.0 + v0.10.0 + v0.11.0 R11-R14）
> 环境：Windows / amd64
> 日期：2026-10-15

---

## 0. 结论

**v0.9 + v0.10 两轮迭代质量高，我此前提的 4 个 issue 全部按建议方案修复。**

| 我提的 issue | 修复状态 | 是否按我的建议 |
| --- | --- | --- |
| #46 测试把 elapsedMs 纳入深比较 | ✅ 修复 | ✅ 按方案 B（`stripClockFields` 精确剔除） |
| #47 工具带按钮顺序 | ✅ 修复 | ✅ actions 移到末组 |
| #47 工具带换行 | ✅ 修复 | ✅ 图标化 + nowrap |
| #47 页头卡片冗余 | ✅ 修复 | ✅ 改为 `.lab-pagebar` 无边框单行 |
| #47 缺多画布视图 | ✅ 实现 | ✅ /lab 画布概览（SVG 缩略图 + 懒加载） |

**新发现 1 项确定性缺陷**：`TestSchedulerFiresAndRecords`（见 #48）。

---

## 1. 版本与规模

```
云端 HEAD   5486d754  (2026-10-15 03:49)
版本        AppVersion = v0.10.0（含 v0.11 R11/R12 的调度器与告警页签）
标签        v0.9.0  v0.10.0
包数        25（新增 internal/notify）
API 路由    67
页面        10（新增 Alerts / Rules / ApiReference）
```

### 1.1 v0.9.0「运营可见性与 AI 分析闭环」

- **4 个运营 UI 入口**：告警中心 `/alerts`、检测规则 `/rules`、资源抽屉的指标时序图、影响分析页签
- **5 个只读 MCP 分析工具**（`internal/mcpserver/graphtools.go`）：

```
graphTraverseName    图穿透
graphImpactName      影响分析（根因候选）
graphContextName     资源上下文
alertsQueryName      告警查询
metricsQueryName     指标查询
```

**这正好落在我设计文档 §7.3 提的「AI 分析接口」**——AI 现在能经 MCP 直接做根因分析，
而不必自己起 HTTP 会话。

### 1.2 v0.10.0「运营闭环补全与 API 可发现性」

- **R6 检测规则管理页**：规则 CRUD 从「API 有而 UI 无」补上配置入口
- **R7 立即检测 UI + 告警 CSV 导出**（顺带修了 `revokeObjectURL` 竞态）
- **R8 Webhook 通知通道**（`internal/notify`，尽力而为、失败不致命）
- **R9 OpenAPI 3 文档**：`GET /api/openapi.json` + `/dev` 参考页，
  **路由表单源化**（`internal/api/routes.go`）→ 规格与实现结构性零漂移

---

## 2. 独立复测（实测，非采信声明）

### 2.1 测试

```
go test -count=1 -timeout 30m ./...
  → 23 包 ok
  → 1 包 FAIL: internal/detect (TestSchedulerFiresAndRecords)
```

**失败详情**：

```
scheduler_test.go:106: live status Running=true between rounds, want false
连跑 5 次：5/5 失败（确定性，非偶发）
```

**根因**：测试用亚秒间隔驱动巡逻，却断言「两轮之间 `Running == false`」——
间隔趋近 0 时「两轮之间」的窗口几乎不存在，`Running` 几乎恒为 true。
实现（`beginRound`/`endRound` 的互斥锁 + single-flight）本身正确。

详见 issue #48。

### 2.2 构建与服务

```
npm ci && npm run build   → ok
go build -o bin/netopslab.exe  → ok
run.ps1（我写的一键脚本）→ 在新版本上正常工作
```

### 2.3 数据

```
资源        1,991（32 类）
关系        3,193
工作台      1（设备 30 / 接口 64 / 链路 32）
拓扑 API    nodes=30  edges=32   ← 正常
```

注：`/api/workspaces/{id}/topology` 的边字段名是 **`edges`**（不是 `links`），
初次核对时我用错了字段名，一度误判为「链路丢失」——记录以免后人重复。

### 2.4 UI（headless 截图验证）

| 页面 | 结果 |
| --- | --- |
| `/lab` 工作台列表 | ✅ 画布概览（SVG 缩略图卡片 + 画布/列表双模式切换） |
| `/lab/:id` 拓扑工作台 | ✅ 单行页头（无卡片）+ 工具带单行 + 视图控件在左、动作按钮在右 |
| 侧栏 | ✅ 新增 告警中心 / 检测规则 / API 参考 |

> 截图注意：headless Chrome 需 `--virtual-time-budget=40000` 才能等到
> Cytoscape 完成渲染；默认 15-20s 会拍到空白画布（**并非缺陷**）。

---

## 3. 遗留

| # | 优先级 | 内容 |
| --- | --- | --- |
| [#48](../issues/issue-48-scheduler-test-race.md) | **P1** | TestSchedulerFiresAndRecords 确定性失败（亚秒间隔下的时序断言） |

---

## 4. 同步方式说明（git 443 不通时的替代路径）

本次 `git fetch/push` 全部失败：

```
fatal: unable to access 'https://github.com/mmqz/netops-lab.git/':
       Failed to connect to github.com port 443 after 21028 ms
```

但 `gh`（GitHub API）通路正常——两者走不同网络路径。替代方案：

```powershell
# 1) 取 token（gh 已登录）
$token = gh auth token

# 2) 用 Python 下载 tarball（PowerShell 的 > 重定向会破坏二进制：
#    实测得到 1F EF BF BD 而非 gzip magic 1F 8B）
#    见下方脚本

# 3) 解压 + robocopy 镜像（排除 .git/bin/data/node_modules）
robocopy $remote $local /MIR /XD .git bin data node_modules
```

**教训**：PowerShell 的 `>` 与 `gh api -o` 都不能安全落盘二进制；
必须用 Python/curl 的二进制写入。


---

## 5. v0.11.0「自主运营版」补充验证（2026-10-15 更新）

云端继续迭代到 v0.11.0（五轮 R11-R14）。补充验证：

### 5.1 版本与迁移

```
AppVersion  v0.11.0
迁移        v20 geo 列 / v21 detection_runs / v22 alerts 状态索引
表总数      29
OpenAPI     61 paths
```

### 5.2 新能力自检（实测 HTTP）

| 端点 | 结果 |
| --- | --- |
| `/api/health` | ✅ 含 **scheduler 块**（enabled/intervalSeconds/running/rounds/lastRunAt/nextRunAt） |
| `/api/openapi.json` | ✅ 61 paths |
| `/api/alerts/stats` | ✅ `{firing: 4, acked: 0, cleared: 0}`（闭集零填充） |
| `/api/detection/runs` | ✅ 运行历史表存在（迁移 v21） |

### 5.3 v0.11.0 新增能力（CHANGELOG）

```
R11  定时巡检调度器（serve --detect-interval，单飞、优雅停机）
     检测运行历史落库（迁移 v21 detection_runs，失败轮也留痕）
     告警分布统计 + /alerts 巡检状态条
R12  资源抽屉「告警」页签（双向定位闭环）+ 指标 CSV 导出
R13  写操作令牌网关（serve --auth-token，Bearer 常数时间比较）
R14  告警状态读索引 + netopslab backup 整库快照
```

### 5.4 测试状态（v0.11.0）

```
go test -count=1 ./...
  → 23 包 ok
  → 1 包 FAIL: internal/detect（TestSchedulerFiresAndRecords，见 #48）
```

**#48 在 v0.11.0 上仍复现**（预期——issue 刚提，尚未修复）。

### 5.5 同步过程记录（git 443 不通的完整处置）

本次同步遇到两个非显然的坑，记录备查：

```
坑 1  PowerShell 的 > 重定向破坏二进制
      gh api .../tarball > file.tar.gz
      → 首字节 1F EF BF BD（应为 1F 8B）
      修法：用 Python urllib 二进制写入，或 curl

坑 2  robocopy 写入 LF 文件 + core.autocrlf=true
      → 390 个文件被标记为「已修改」（实际只是换行符差异）
      → git rebase 报「cannot rebase: You have unstaged changes」
      修法：git checkout -f origin/main（不要手工 commit 这些假变更）
```

另注意：`git checkout -f origin/main` 会进入 **detached HEAD**——
提交后需 `git branch -f main <sha> && git checkout main` 才能正常 push。
