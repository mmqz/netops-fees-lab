# 费用仓拆分决策与裁剪清单

> 决策日期：2026-10-23
> 结论：**fork**（不开分支），**接受底座分裂**（不抽公共库），新仓**砍掉费用不需要的部分**。

---

## 一、为什么 fork 而不是分支

| | 分支 | fork |
| --- | --- | --- |
| 前提 | 最终要合回主仓 | 长期各自演进 |
| 本项目 | ❌ | ✅ |

费用与 IPRAN 是**两个业务域**：数据源不同（财务表格 vs 网管导出）、
判断逻辑不同（金额异常 vs 拓扑影响）、用户不同（财务/综维 vs 网络运维）、
时间粒度差两个数量级（月度结算 vs 秒级告警）。

**开分支会让两边都背着「迟早要合并」的心理负担**，改起来都缩手缩脚。

---

## 二、为什么不抽公共库

两边确实共享「导入 + 校验 + 审计」这层底座，但：

1. 抽公共库本身要 2-4 周（拆接口、双仓引用、版本管理、CI 改造）；
2. 费用仓连数据入口都还没打通（#125），等不起；
3. 两个项目规模都不大，各自留一份底座比维护公共库便宜。

**结论：接受分裂。** 底座 bug 两边各修一次，几分钟的事；
业务逻辑本来就不该共享。

---

## 三、新仓保留 / 删除清单

### 必须保留（费用线的全部依赖）

| 包 | 用途 | 说明 |
| --- | --- | --- |
| `internal/fees` | 费用核心 | 快照/Parquet/8 风险场景/关联 |
| `internal/metrics` | 指标时序 | 双账本交叉的另一半（有费无量/有量无费） |
| `internal/domain` | 领域类型 | 地基 |
| `internal/repo` | 存储层 | 地基（含迁移链） |
| `internal/mapping` | 列映射注册表 | 导入必需 |
| `internal/workbook` | Excel/CSV 读取 | 导入必需 |
| `internal/api` | HTTP 面 | 裁剪后保留费用相关路由 |
| `internal/app` | CLI 骨架 | 裁剪后保留 fees/metrics 子命令 |
| `internal/services` | 服务装配 | 依赖注入 |
| `internal/validation` | 校验 | 导入质量必用 |
| `internal/query` | 查询 | 视费用查询是否依赖 |
| `internal/report` | 报告 | 费用报告需要 |
| `internal/webui` | 前端嵌入 | 保留壳 |
| `internal/testsupport` | 测试辅助 | 几十行，留着省事 |
| `internal/testfixtures` | 测试夹具 | 同上 |

### 建议删除（IPRAN 专属，费用用不到）

| 包 | 行数量级 | 理由 |
| --- | --- | --- |
| `internal/sim` | 5,330 | 仿真工作台（故障注入/撤销/检查点）——费用无此概念 |
| `internal/topology` | 1,005 | 拓扑图遍历——费用无拓扑关系 |
| `internal/detect` | 1,272 | 告警检测引擎——**告警与费用在物理上不能互相驱动** |
| `internal/notify` | — | webhook 通知（告警运行回执）——费用不需要 |
| `internal/renames` | — | 资源改名提案——IPRAN 数据治理专属 |
| `internal/changes` | — | 变更集——仿真工作台配套 |
| `internal/governance` | — | 治理总览——IPRAN 资源治理面板 |
| `internal/exporter` | — | 资源导出——IPRAN 专属 |
| `internal/mcpserver` | 1,415 | MCP 工具面——**可砍，后期按需重建费用版** |

### 需要改造后才能删

| 项 | 现状 | 处理 |
| --- | --- | --- |
| `internal/ptpconfig` + workbook.script.go` | workbook 支持 PTP 配置脚本解析（119 行） | 费用只处理 Excel，**删 script.go 即可断开 ptpconfig 依赖** |
| `internal/api/routes.go` | 84 个路由 | 裁剪到费用相关（约 15 个） |
| `internal/app/cli.go` | 18 个子命令 | 裁剪到 fees/metrics/serve/backup 等 |
| `internal/repo/migrations_*` | 32 版迁移 | **保留全部**（历史迁移不可拆），但删 IPRAN 专属表定义 |
| `web/src/pages/*` | 13 个页面 | 只留 fees + 概览 + 导入，删 lab/alerts/rules/maintenance/issues 等 |

---

## 四、拆分验证标准（关键）

**删完之后必须满足：**

1. `go build ./... && go test ./... ` 全绿；
2. 费用导入 + 8 个风险场景 + 双账本交叉**功能不变**；
3. **不残留 IPRAN 概念**：grep `device|topology|sim|alert|ring|ptp|igp` 在业务代码里零命中；
4. 数据库迁移能从头跑通（空库 → 最新版本）；
5. 前端可构建、可访问。

**验证方法**：先临时删包跑测试，不合再还原——**一两个小时就能确认拆分是否可行**。

---

## 五、主仓（IPRAN）冻结条款

拆分后主仓状态：

- **功能冻结**：不接新功能，只修 P0/P1 缺陷；
- **费用模块迁出**：从主仓删除 `internal/fees` 及费用相关路由/页面/迁移（或保留为只读历史）；
- **README 顶部明示**：当前定位 + 冻结声明 + 指向费用仓；
- **停止「版本驱动」节奏**：不再为发版而发版。

---

## 六、已知的遗留问题归属

| issue | 归属 |
| --- | --- |
| #125 多 sheet 导入（P0） | **费用仓**——这是费用线唯一的硬阻塞 |
| #124 Windows 测试失败 | 主仓（其中上传并发原语问题费用仓也要修） |
| #123 趋势预测未实现 | 主仓（IPRAN 指标趋势） |
| #76 CI 配额 | 两仓都要处理（各自 CI） |
