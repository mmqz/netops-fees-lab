# 主仓移除费用代码：范围、方式与红线

> 决策日期：2026-10-23
> 用户要求：主仓移除费用代码；**数据回滚记录保留**；**已修 bug 不得回滚**。

---

## 一、三条红线（先定，再动手）

### 红线 1：已修复的 bug 不得回滚

删除必须**只删费用专属代码**，不得 `git revert` 任何提交。

**严禁的操作**：
- ❌ `git revert <修复提交>`（会连带回滚该提交里的共用修复）
- ❌ 整体 checkout 到费用功能引入前的提交
- ❌ 反向 cherry-pick

**正确方式**：在当前最新代码上**精确删除费用文件 + 改造混合文件**，
并保证共用部分的修复（#75/#79/#80 路径契约、#124 上传并发等）**保持现状**。

### 红线 2：历史数据不删

费用表里可能有已导入的历史记录：

| 表 | 内容 |
| --- | --- |
| `fee_batches` | 费用导入批次（含 `rolled_back` 状态记录） |
| `fee_site_links` | 费用-资源关联（含已确认/已驳回决策） |
| 快照文件 | `data/fee-imports/` 不可变证据 |
| Parquet 大表 | `data/analysis/fees/` |

**处理原则**：
- **代码删除，数据保留**——表结构留在最新迁移链里不动；
- **不写破坏性迁移**（不 DROP TABLE、不 DELETE 数据）；
- 已有库升级后，费用表仍在、数据仍在，只是**没有代码去读写它们**；
- 快照与 Parquet 文件**原样保留**（它们是导入过的证据）。

### 红线 3：迁移链不得断

v29-v32 已存在于历史库中。**已应用的迁移不可删除或修改**——
否则老库升级会在版本校验处失败。

**做法**：v29-v32 迁移文件**保留在链上**，但删除费用业务代码。
表变成孤儿表——占空间但无害，且保住了历史记录。

---

## 二、删除范围

### A 类：整包/整文件删除（费用专属，零共用）

```
internal/fees/                    整包（26 文件）
internal/api/fees.go              HTTP 费用端点
internal/api/fees_test.go         其测试
internal/app/fees_cli.go          CLI 费用子命令
internal/app/e2e_fees_test.go     费用 e2e
internal/domain/fees.go           费用领域类型
internal/mcpserver/feetools.go    MCP 费用工具
internal/repo/feebatches.go       费用批次仓储
internal/repo/feesitelinks.go     费用关联仓储
web/src/pages/Fees.tsx            费用页面
web/src/pages/fees-format.ts      费用格式化
web/src/pages/fees-format.test.ts
```

### B 类：改造（混合内容，只删费用部分）

| 文件 | 费用提及 | 处理 |
| --- | --- | --- |
| `internal/api/routes.go` | 14 处 | 删 13 条 `/api/fees*` 路由 |
| `internal/app/cli.go` | 12 处 | 删 `fees` 子命令注册与用法 |
| `internal/api/openapi.go` | 1 处 | 删 fees tag 引用 |
| `internal/repo/repo.go` | 12 处 | 删 FeeBatches/FeeSiteLinks 仓储装配（字段与构造） |
| `internal/services/services.go` | 11 处 | 删 WithFees 选项与费用服务装配 |
| `internal/api/server.go` | 依赖 | 删费用 handler 字段 |
| `internal/app/app.go` | 依赖 | 删费用相关装配 |
| `internal/mcpserver/mcpserver_test.go` | 测试 | 删费用工具断言 |

### C 类：保留不动

| 项 | 理由 |
| --- | --- |
| `internal/repo/migrations_v29..v32.go` | **迁移链完整性**（红线 3）；表留数据留 |
| 对应的 `_test.go` | 同上——它们验证的是迁移，不是业务 |
| 数据库中的 fee_* 表与数据 | **历史记录保留**（红线 2） |
| `data/fee-imports/`、`data/analysis/fees/` | 已导入的证据文件 |
| `internal/metrics` | IPRAN 指标时序自己在用，非费用专属 |

---

## 三、执行顺序

1. **先建冻结分支/标签**（如 `v0.15.2-with-fees`），保留移除前的完整状态；
2. 删 A 类文件；
3. 改造 B 类文件；
4. `go build ./... && go test ./... ` 必须全绿；
5. 用**已有的真实库**（含费用历史数据）验证：能正常启动、能读写 IPRAN 数据、费用表数据仍在；
6. 提交，**不做任何 revert**。

---

## 四、验收标准

**功能**
1. `go build ./...` 通过；
2. `go test ./...` 全绿（删掉费用测试后）；
3. IPRAN 全部功能正常（导入/校验/拓扑/仿真/告警/报告）；
4. 前端可构建，页面导航里无费用入口。

**红线验证**
5. `git log` 中**不存在 revert 提交**；
6. 共用修复仍在：
   - `snapshot.go` 里的路径契约（虽随 fees 删除，但 `checkpoints.file_path` 的 v32 修复必须在）；
   - 上传并发原语的 Windows 行为（#124）不受影响；
7. 老库升级后：**fee_batches / fee_site_links 表与数据仍存在**，
   `SELECT count(*) FROM fee_batches WHERE status='rolled_back'` 仍能返回历史回滚记录；
8. 迁移链：空库从 0 跑到 32 版本无错；老库（含 v32）启动无版本冲突。

**残留检查**
9. `grep -r "internal/fees" --include=*.go .` 零命中；
10. 除迁移文件外，业务代码 `grep -ri "fee" --include=*.go internal/ web/src/` 仅剩迁移相关命中。

---

## 五、风险与建议

| 风险 | 缓解 |
| --- | --- |
| 删共用代码导致 IPRAN 功能退化 | 改造 B 类文件时只删费用行；用全量测试兜底 |
| 误删迁移导致老库无法升级 | v29-v32 一律保留（红线 3） |
| 数据丢失 | 不写破坏性迁移（红线 2）；先建标签备份 |
| 回滚修好的 bug | 禁止 revert（红线 1）；对照 #75/#79/#80/#124 逐条验收 |

**建议**：这是**一次性大改**，建议在独立分支完成后
（1）跑全量测试；（2）用真实库验证；（3）人工 diff 复核 B 类改造；
**最后**才合入主分。
