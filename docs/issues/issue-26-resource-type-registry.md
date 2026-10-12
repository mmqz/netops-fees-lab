# [设计缺失] 资源类型注册表：单一事实源未定义

关联：`docs/design/data-model-v1.md` §3（45 类）、§10.2（层次表）

## 问题

45 类资源的元数据目前**散落多处**，扩到 45 类后必然不一致：

| 元数据 | 现在在哪 |
| --- | --- |
| 类型枚举 | `domain/enums.go`（`IsValid` switch） |
| 字段规格（自然键/必填/格式） | `mapping/schema.go`（`Specs`） |
| 中文标签 | `mapping/schema.go`（`Label`） |
| 层次归属 | **本设计文档 §10.2 的表格**（非代码） |
| SQL CHECK 约束 | `repo/migrations*.go` |
| API 合法值 | `api/*.go`（硬编码错误消息） |
| 前端类型列表 | `web/src/schema.ts` |

**7 个位置需要手工同步**。既有 8 类已勉强维护，45 类时会失控。

## 影响

- 新增类型时漏改某处 → 运行时不一致（如 API 接受但 DB 拒绝）
- §10.2 的层次表是**文档**不是**代码**，查询时无法程序化使用
  （而 §10.3 的 `layers` 参数需要层次信息）
- 新增类型的迁移成本被低估（实际要改 6-7 处）

## 建议方向

**建立单一事实源（registry）**，其余位置从中派生：

```go
// 单一事实源：每类资源一处定义
type ResourceTypeSpec struct {
    Type        domain.ResourceType
    Label       string          // 中文名
    Layer       int             // 层次（§10.2）
    Domain      string          // 专业域：wireless|transport|ip|access|space
    NaturalKey  []string        // 自然键字段
    Fields      []FieldSpec     // 字段规格（沿用既有）
    // ...
}
```

派生点：
- SQL CHECK：由生成器产出迁移脚本（或运行时校验 + 定期一致性测试）
- 前端类型列表：`GET /api/schema` 已投影 `Specs`（既有能力），扩展即可
- API 合法值：从 registry 动态生成错误消息

## 评判标准

1. **同步一致性测试**：一个测试断言 registry 与 SQL CHECK / API / 前端列表一致
   （这类测试是「防漏改」的关键）
2. **新增类型的改动点数量**：目标从 6-7 处降到 1 处 + 生成
3. **层次信息可编程访问**：§10.3 的 `layers` 参数能从 registry 取

## 默认方案（不能决定时采用）

**扩展既有 `mapping.Specs` 为 registry**（不新建体系）：
- 给 `TypeSpec` 增加 `Layer` / `Domain` 字段
- 保留既有 `mapping.Specs` 作为唯一事实源
- 加一致性测试（registry ↔ SQL CHECK ↔ API）

```go
// TODO(design-v2): 类型注册表见 issue #26；
// 当前以 mapping.Specs 为事实源，新增 Layer/Domain 字段。
```

## 处置指令

本项为**设计缺失**（非「两难选择」）：实施前必须补齐设计。

1. **先补设计**：把结论回写 `docs/design/data-model-v1.md`
2. **可测的项要测**：给出量化数据
3. **不能决定时**：采用「默认方案」+ `TODO(design-v2): ...` 标记 + 本 issue 记录原因


## 验收

- [ ] 单一事实源确定
- [ ] 一致性测试存在（registry ↔ SQL ↔ API）
- [ ] 层次信息可程序化访问
- [ ] 结论已回写设计文档 §3
