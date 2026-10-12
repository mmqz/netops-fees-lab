# ADR-0011: 运维能力差距的设计决策（时序指标 / 异常检测 / 告警 / 预测）

- 状态：已接受（设计记录，未实现）
- 日期：2026-10-12（W17）
- 关联 issue：#10（能力差距：数据异常检测、设备告警、趋势预测均不存在）
- 关联决策：ADR-0003（MCP stdio 工具面）、ADR-0007（lab 操作引擎）、ADR-0010（底图模式与叠加）

## 背景

issue #10 以「把本项目用作本地云网运维系统」为目标，核查确认四个能力全部缺失：
数据异常检测、设备告警、趋势预测、业务层影响（基站/客户维度）。其根因是**数据模型
缺口**而非功能缺口——性能/流量类时点指标（光功率、丢包率、带宽利用率）当前没有
存储位置：`resources.attributes` 是静态台账（identity 与展示属性），不适合放时点
数据；仿真侧的 `sim_*` 表是工作台沙箱，同样不承载真实采集指标。

本 ADR 按 issue 的建议（「记录能力差距与设计约束，供后续架构决策参考」）给出
优先级、数据模型与工具面决策，作为后续实现轮次的输入。**本轮不实现任何一项。**

## 决策

### D1. 时序指标存储：独立 `metrics` 表族，不污染台账

```
metrics(series_id, ts, value REAL, quality TEXT)        -- 窄表，(series_id, ts) 主键
metric_series(series_id, resource_id, metric_key, unit) -- 序列元数据
metric_keys(metric_key, label, kind, unit, min, max)    -- 指标目录（静态注册）
```

- 指标目录先行封闭集：`optical_power_dbm` / `packet_loss_pct` / `bandwidth_util_pct`
  / `latency_ms` / `interface_errors`（与导入 schema 的封闭类型集同一治理哲学）。
- 写入通道与导入解耦：新 CLI 子命令 `netopslab ingest --file perf.xlsx --db …`
  或 HTTP `POST /api/metrics`（批量 upsert，同一 (series, ts) 幂等覆盖）——复用
  导入引擎的「batch + 行级记账 + 回滚」骨架，但独立批次类型，不与资源批次混表。
- 查询面走既有分页契约（page/pageSize 或 limit/offset）；时间范围参数
  `from/to`（RFC3339）。

### D2. 异常检测：规则引擎，规则目录与 E/W/I 并列而非复用

- 检测规则是**配置数据**不是代码：`detection_rules(rule_id, metric_key,
  comparator, threshold, window, severity, enabled)`——如「packet_loss_pct
  连续 5 点 > 1.0 → warning」。
- 与既有 E/W/I 规则目录的关系：**同构不同表**。E/W/I 描述「导入数据的质量
  缺陷」（rule_version 锚定，输出 issues 表）；检测规则描述「运行指标的时序
  越限」（输出告警，见 D3）。二者共享 severity 语义与前端渲染组件，但
  `rule_version` 不跨域——混用会让导入回归测试与检测规则演进互相锁死。
- 判定引擎：纯函数 `detect(series, rules) → findings[]`，与导入校验引擎同为
  离线可测组件（确定性、无时钟依赖——ts 全部来自数据）。

### D3. 告警状态机：触发 → 确认 → 恢复（三态两时刻，无自动升级）

```
alerts(alert_id, rule_id, series_id, fired_at, cleared_at,
       acked_at, acked_by, state)   -- firing | acked | cleared
```

- 状态迁移只有三条边：`firing → acked`（人工确认）、`firing → cleared`
  （检测引擎发现恢复条件）、`acked → cleared`（恢复）。**不引入升级/超时
  自动关闭**——本地单机系统，状态机每加一条自动边就多一个不可复现的
  「告警自己消失了」工单来源。
- 去抖：规则窗口（D2）天然提供；告警侧不再叠加去抖，避免双层语义。

### D4. 趋势预测：先线性基线 + 季节分解，明确不做学习模型

- v1 只做两件事：最小二乘线性趋势（容量类指标：光功率劣化速率、带宽
  利用率增长）与 24h/7d 周期均值（流量类）。输出「N 天后到达阈值」的
  预测点与置信区间，渲染为地图/图表上的预测虚线。
- **明确不做**：在线学习、Prophet/ARIMA 全家桶——数据量（单局站数月）
  与运维场景（要解释性）都不支持；预测面板标注「外推参考，非承诺」。

### D5. MCP 工具面：维持「恰好 5 个只读工具」冻结，分析能力走 HTTP + Web

- 红线不动：MCP 工具面不加第 6 个工具（含分析类）。理由：
  1. 工具面是 AI 通道的攻击面与审计面，冻结是治理资产不是包袱；
  2. issue #10 的四个能力全部是「给人看 + 给人决策」的面板能力，
     自然归属 Web（复用既有告警/检测 HTTP 契约 + 前端表格/地图）；
  3. AI 需要消费分析结论时，走既有 `resource_search` 检索资源 + HTTP
     API 读告警（HTTP 面向集成开放，MCP 面向受控 AI 操作——分工见
     ADR-0003）。
- 若未来真出现「AI 必须主动订阅告警」的场景，届时以新 ADR 显式破线，
  并同步扩 `TestMCPToolRegistry` 的钉死断言——绝不静默扩面。

### D6. 业务层影响（基站/客户维度）：relations 扩关系类型，不建平行表

- `relations` 增加 `serves`（基站→客户/站点）与 `depends_on`（逻辑拓扑）
  两个封闭关系类型，从导入的 port/base_station sheet 的既有列（上联
  IP/网关/客户编码）物化——与 a_end/z_end 同一派生管线，不建第二套
  影响图。故障影响分析 = 既有拓扑遍历（`/api/resources/{id}/relations`）
  的反向查询，地图渲染复用本次 W17 的聚簇 + DEM 底座。

## 实现优先级（建议）

| 顺序 | 能力 | 依赖 | 理由 |
| --- | --- | --- | --- |
| P1 | D1 时序存储 + ingest 通道 | 无 | 一切分析的地基；无数据其余皆为空转 |
| P2 | D2 检测规则 + D3 告警状态机 | D1 | 「当日数据异常」是 issue 的第一诉求 |
| P3 | D6 业务层关系 | 无（可与 P1 并行） | 低成本高杠杆：纯导入管线扩展 |
| P4 | D4 趋势预测 | D1 + 数据积累（≥2 周） | 数据不足时上预测是自欺 |

## 后果

- 正面：后续实现轮次有明确的表结构与边界决策可循；MCP 冻结红线免于被
  「顺便加个工具」侵蚀；检测/告警/预测全部离线可测（确定性门禁可延续）。
- 负面/代价：本轮零实现（issue #10 的验收标准为「明确优先级与数据模型」，
  以本 ADR 满足）；时序表上线后需评估 SQLite 单机写入吞吐（万级 series ×
  分钟级采样 ≈ 10^4 rows/day，量级无压力，但需批量 upsert 而非逐行）。
- 检索词锚点（后续实现时替换为实码引用）：SuggestTypeFromHeaders 的
  保守守卫哲学（宁跳勿错）同样适用于检测规则的默认阈值——首版规则全部
  显式确认后启用，不做「开箱即报警」。
