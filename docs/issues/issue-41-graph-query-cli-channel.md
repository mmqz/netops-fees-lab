# [P3] 图查询无 CLI 通道

来源：[docs/audit-v0.8.0.md](../audit-v0.8.0.md) §2.5

## 现象

```
HTTP:  GET /api/resources/{id}/traverse | impact | context    ✅ 已实现
CLI:   netopslab query traverse ...                           ❌ 不存在
       query 子命令仍为 resources / stats / relations / governance
       （实测报错：查询子命令必须是 resources、stats、relations 或 governance）
```

即：v0.7.0 新增的三大图查询能力**只有 HTTP 入口**。

## 与设计的一致性

**这符合设计**（`design/data-model-v1.md` §7.2）：

| 通道 | 职责 |
| --- | --- |
| MCP（5 工具冻结） | AI 受控操作 |
| **HTTP** | **全量数据访问（AI 读数据走这里）** |
| CLI | 脚本 / 批处理（导入、报表） |

故**不属缺陷**。但存在一个实际约束：

## 影响

**离线 / 无服务场景无法使用图查询**：
- 每日巡检若由脚本在导入后直接分析，必须先 `serve` 再调 HTTP
- 无法在纯 CLI 环境（如定时任务）做「导入 → 穿透分析 → 输出」的原子流程
- AI 智能体若只接 MCP（无 HTTP），则拿不到 traverse / impact 结果

## 建议

两种方向，**需明确选择其一**：

**方案 A：补 CLI 通道**
```
netopslab query traverse --db <path> --id <resourceId> [--direction down] [--max-depth 6]
netopslab query impact   --db <path> --id <resourceId> [--direction up]
netopslab query context  --db <path> --id <resourceId> [--depth 1]
```
与既有 `query resources|stats|relations` 同族，实现上复用同一 service 层。

**方案 B：明确不做，并在文档声明**
若确认「HTTP 是图查询的唯一预期入口」，应在
`docs/api.md` 与 README 中显式说明，避免使用者反复尝试 CLI。

## 验收

- [ ] CLI 通道已提供，**或**文档明确声明「仅 HTTP」
- [ ] 若提供：输出契约符合既有 §10（stdout 恰好一个 JSON 对象）
- [ ] 若提供：与 HTTP 端点结果一致（跨通道测试）