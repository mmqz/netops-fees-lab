# #117 outlier 场景口径谎言：标签称「单位成本离群」，实现对月度总额直接 z-score

> **状态**：已修复（2026-10-22，v0.15.2 后 main）。GitHub issue：https://github.com/mmqz/netops-lab/issues/117

## 1. 发现背景

#114 调研顺带发现的口径偏差（#110「散文冻结了谎言」缺陷类）：

- **标签主张**：场景 5 名为「同类站**单位成本**离群」（`RiskScenarioLabels` / fees-layer.md §8 / api.md / project-overview / MCP 工具描述 / 前端 i18n 常量全用此名）；
- **实现事实**（`engine_risks_duckdb.go` `RiskOutlierPreset`）：对同期同类**月度总额** `total_cents` 直接 z-score——**无任何规模归一化**。

大站月费用天然高于小站——总额口径会把「大站」误报为「离群」，而标签向运营者承诺的是站点间可比的单位成本口径。

## 2. 裁决：为什么不直接实现归一化

单位成本 = 费用 ÷ 规模基数，**当前数据面不存在公认的规模字段**：费用表只有（站址、账期、金额、批次谱系）；资源属性按类型各异且无统一「规模」语义（载频数只在无线基站表、端口数只在设备表、租户数无处可查）。任何默认基数选择都是发明数据（违反「无魔数/不发明事实」教义）；且归一化需费用面（Parquet）× 资源面（SQLite）跨层组合——场景 5 从纯 Parquet 预设到跨层组合是架构级重构。**诚实优先：标签对齐实现，增强显式化。**

## 3. 修复

| 落点 | 变更 |
| --- | --- |
| `internal/fees/risks.go` | 标签映射「同类站单位成本离群」→「同类站月度金额离群」；场景 note 增补告诫——「本场景对月度总额而非单位成本计算，规模大的站天然偏高，请结合站点规模人工判断（#117：规模归一化待基数字段落地后显式参数化）」 |
| `internal/mcpserver/feetools.go` | 工具描述同步（schema 标签的「同批离群」措辞本就未主张单位成本，不动） |
| `internal/fees/risks_test.go` | fake 引擎 outlier 预设改返回诚实空集；新增 `TestRiskOutlierLabelAndNote`：钉死新标签 + note 三要素（「月度总额而非单位成本」/「规模大的站天然偏高」/「统计线索，非认定结论」） |
| `docs/fees-layer.md` | §8 名称列 + 判定口径列改写（明确「**非单位成本口径**」+ 指向裁决） |
| `docs/api.md` / `docs/project-overview.md` | 场景名同步，api.md 附一句口径说明 |
| `web/src/i18n.ts` | `RISK_SCENARIO_OUTLIER` 常量同步（Fees.tsx 已接线，死常量探测器防漏） |
| 历史文档 | v0.15.1 验证记录 / CHANGELOG 按「历史文档不改写」教义保持原样 |

## 4. 未来增强（显式登记，不悬空）

单位成本归一化：`normBasis` 参数（资源属性字段名，如 `carrier_count`）——提供则以「月度总额 ÷ 该属性值」为 z-score 输入，缺省不归一化 = 当前行为（零魔法默认）。前置条件：规模基数字段在数据面落地 + 跨层组合重构（场景 1/6/7 先例）。届时本场景 note 的 #117 告诫随之移除。

## 5. 门禁（本地执行）

25 包 ok × 双构建（默认 + `-tags duckdb`）/ gofmt 空 / vet 0 × 双构建 / vitest + tsc（i18n 常量变更，前端门禁必跑）。
