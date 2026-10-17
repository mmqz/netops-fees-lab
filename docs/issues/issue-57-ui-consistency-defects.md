# [P1] 三个 UI/文档一致性问题：告警资源列丢设备名、质量报告硬编码"合成数据"声明、v0.12 API 文档缺失

来源：对 `e258543`（v0.12.0）的 UI 全面扫描与文档核对，2026-10-17

---

## 一、（P1）告警列表"资源"列只显示端口名，无法区分所属设备

### 现象

告警页 `/alerts` 表格有 8 列（状态/级别/关联/规则/资源/触发时间/最新值/处置），
资源列渲染结果为：

```
触发中 | 警告 | — | 接收丢包率持续超限     | xgei-1/1/1 | 5 天前 | 4.82 | 确认 清除
触发中 | 警告 | — | 接收光功率持续偏低     | xgei-1/1/1 | 5 天前 | -24  | 确认 清除
触发中 | 警告 | — | 接收光功率持续偏低     | xgei-1/1/1 | 5 天前 | -24  | 确认 清除
触发中 | 警告 | — | 接收丢包率持续超限     | xgei-1/1/1 | 5 天前 | 4.82 | 确认 清除
```

**四条告警的资源列全是 `xgei-1/1/1`**，但实际分属两台不同设备：

| 告警 | 资源 naturalKey |
| --- | --- |
| dr_loss_rx | `大庆市传输设备01\|xgei-1/1/1` |
| dr_opt_rx_low | `大庆市传输设备01\|xgei-1/1/1` |
| dr_opt_rx_low | `齐齐哈尔市传输设备01\|xgei-1/1/1` |
| dr_loss_rx | `齐齐哈尔市传输设备01\|xgei-1/1/1` |

运维人员看到 4 行一模一样的 `xgei-1/1/1`，**无法知道是哪台设备出的问题**。

### 根因

`web/src/pages/Alerts.tsx:706`

```tsx
{row.resource.name !== '' ? row.resource.name : row.resource.naturalKey}
```

优先显示 `name`（端口名 `xgei-1/1/1`），而 `naturalKey`（含设备前缀）
只在 name 为空时兜底。对端口/板卡这类"名称本身无区分度"的资源类型，
这个优先级导致信息丢失。

后端 API 已正确返回两个字段（实测）：

```json
{"resource": {"id":"res_cd8c0729-...", "type":"port",
  "naturalKey":"大庆市传输设备01|xgei-1/1/1", "name":"xgei-1/1/1"}}
```

**是纯前端展示策略问题，不涉及后端。**

### 建议修复

端口类资源改为 `naturalKey` 优先，或两者都显示：

```tsx
// 方案 A：端口/板卡等子资源用 naturalKey 优先
const preferKey = ['port', 'board', 'odf_terminal', 'pon_port'].includes(row.resource.type);
{preferKey || row.resource.name === '' ? row.resource.naturalKey : row.resource.name}

// 方案 B（推荐）：主行显示 name，副行（muted-note）显示 naturalKey
<span className="entity-link">{row.resource.name}</span>
<span className="muted-note">{row.resource.naturalKey}</span>
```

方案 B 与资源浏览页的展示风格一致，信息最完整。注意列宽需相应调整。

### 验收标准

1. 告警列表能直接看出告警所属设备（不必点进去）；
2. 两台设备上的同名端口，在列表里显示为不同文本；
3. CSV 导出同步修正（`Alerts.tsx:196-199` 已同时导出两个字段，确认 CSV 可读即可）。

---

## 二、（P1）质量报告硬编码"合成测试数据"声明，与真实数据矛盾

### 现象

`/report` 页面顶部固定显示：

> 本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。

但当前系统装的是 **3,464 条真实公开数据**（OSM 局站/道路 + OpenCelliD 基站），
用户导入的也是自己的真实台账。**这个声明与实际数据性质完全相反。**

### 根因

`web/src/i18n.ts:986-987`

```ts
export const RPT_DISCLAIMER =
  '本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。';
```

在 `web/src/pages/Report.tsx:151` 无条件渲染：

```tsx
{RPT_DISCLAIMER}
```

无任何数据来源判断。

### 为什么这是 P1

1. **合规风险**：用户把报告交给上级/客户时，页面上写着"合成测试数据"——
   要么让对方误以为数据是假的，要么在真正是演示数据时给出了错误的免责声明；
2. **与数据现状脱节**：项目已从"合成演示"演进到"真实公开数据 + 用户真实台账"，
   文案没跟上；
3. **反向误导**：真正该被免责的是"演示数据"场景，而不是真实数据场景。

### 建议修复

按批次来源动态生成声明，而非固定文案：

```tsx
const isSynthetic = batches.every(b => b.source === 'demo' /* 或 manifest 标记 */);
{isSynthetic
  ? '本报告基于演示数据生成，仅用于工程方法验证，不代表真实网络运行情况。'
  : '本报告基于已导入的资源台账生成。'}
```

若无法可靠区分数据来源，**至少改为中性表述**：

> 本报告基于当前已导入的数据生成，请以实际网络情况为准。

### 验收标准

1. 导入真实数据后，报告不出现"合成测试数据"字样；
2. 若保留了合成数据场景，该场景下声明仍准确；
3. 声明措辞与实际数据来源一致。

---

## 三、（P2）`docs/api.md` 缺失 v0.12 两个新功能

### 现状

`/dev` 页面（API 参考）明确宣称：

> 本页目录由 /api/openapi.json 实时渲染（后端路由表的投影，与实现零漂移）

该页面的确自动同步了 v0.12 路由（`/api/maintenance/windows`、
`/api/detection/run`、`/api/alerts/{id}/ack` 等都在）。

但人工维护的 `docs/api.md` 没有跟上：

| 关键词 | docs/api.md 出现次数 |
| --- | --- |
| `maintenance/windows` | **0** |
| `correlationRole` | **0** |
| `suppressionNote` | **0** |
| `flapHeld` | 3 |
| `suppressed` | 3 |

**整个维护窗口 API（v0.12-R15 的核心功能）在 API 文档中完全不存在。**
告警关联的 `correlationRole`/`correlatedAlertId`/`correlatedResourceId` 字段也没有。

### 为什么重要

这正是 #55 那个缺陷的文档层面成因：**维护窗口的时基语义（墙钟 vs 数据时间）
在全系统没有任何一处文档化**——不在 api.md，不在 CHANGELOG，不在 OpenAPI 描述，
只有 UI 上一句"判定基准是指标数据时间线"。

### 建议修复

1. `docs/api.md` 补齐 `POST/GET/DELETE /api/maintenance/windows`，
   **明确写出时间基准语义**（与 #55 的修复方案保持一致）；
2. 补齐告警关联三字段说明（根因/次生判定规则、层级方向）；
3. 考虑在 `routes.go` 的 `Summary` 字段里也带上时基说明——
   OpenAPI 会自动继承，这样 `/dev` 页面也能看到。

### 验收标准

1. `docs/api.md` 覆盖全部 v0.12 新增路由与字段；
2. 维护窗口的时间基准在 **文档 + OpenAPI + UI** 三处表述一致。

---

## 附：本轮扫描确认无问题的部分

- **12 个路由全部加载正常**，console 错误 / 未捕获异常 / HTTP 4xx-5xx **均为 0**；
- **前端代码卫生优秀**：`any` 用量 0、`console.log` 残留 0、TODO/FIXME 0，定时器均有清理；
- **告警状态机六个边界全部正确**（缺 ackedBy 拒绝、重复确认 409、重复清除 409 等）；
- **维护窗口表单校验正常**（空表单正确提示"请填写事由"）；
- **资源浏览页正常**：3,464 条 / 174 页，分页与 35 种类型筛选齐备；
- **前端路由 404 页正常**。
