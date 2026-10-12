# NetOps Lab 数据模型设计（v1.0 目标态）

> **文档性质**：实施规格（可据此直接开发）
> **读者**：开发实施方（人 / AI 编码代理）
> **状态**：设计稿，未实现
> **关联**：issue #16（数据模型缺层）、ADR-0011（时序/检测/告警/预测）
> **日期**：2026-10-12（v2：补 §10 图查询语义与防环设计）

---

## 0. 一句话目标

**每天导入网管导出的表格 → 数据入库 → AI 查询分析 → 发现设备劣化趋势 → 提示保养**。

系统不做分析本身（分析由外部智能体经 MCP/HTTP 完成），系统的职责是：
**把网络资源与时序数据存成 AI 可直接消费的结构**。

### 设计的第一原则

> **存储与调用对 AI 友好优先于界面对人友好。**

推论：
- 结构规整（同构表、稳定字段名）、可预测（同参数同结果）
- 单次查询即可拿到「一台设备及其全部上下文」（避免 AI 做 N 次往返拼装）
- 时序数据可做范围查询与聚合（AI 判断趋势不需要拉全量点）
- 一切关系显式建模，**AI 不需要从命名/前缀推断**（沿用项目既有红线）

---

## 1. 范围

### 1.1 覆盖的专业域（全量）

| 专业域 | 资源 | 优先级 |
| --- | --- | --- |
| 物理层 | 局站、机房、设备、机架、槽位、板卡、端口 | P0 |
| 物理层（光） | 光缆、光缆段、纤芯、ODF、接头盒、人井 | P0 |
| 传输层 | 电路、波道（OTN）、时隙（SDH） | P0 |
| 逻辑层 | 环网、链、保护组、IGP 域、VPN 实例 | P1 |
| 业务层 | 客户、业务实例、专线、云专线 | P1 |
| 接入层 | OLT、ONU、PON 口、基站、BBU、RRU | P1 |
| 支撑层 | 管道、杆路、电源（可选） | P2 |

### 1.2 明确不做

- **能耗优化**：属动环/暖通范畴，网管软件不承载该数据（用户已确认）
- **AI 分析算法**：本系统只提供数据与接口，分析由外部智能体完成
- **采集对接**：本期只做表格导入通道（网管在内网，不开放接口）

### 1.3 数据来源策略

| 阶段 | 数据 | 说明 |
| --- | --- | --- |
| 开发期 | **占位数据**（合成 + 公开数据） | 用于功能验证与演示；**不进入宣传对外环节** |
| 生产期 | 网管导出表格 | 用户申请后替换；导入通道不变，只换数据文件 |

**占位数据的授权说明**（重要）：
- 优先 **CC0 / 公有领域**（无约束）
- 其次 **CC BY-SA 等开放许可**——**仅作开发期占位**，
  不用于对外宣传分发，故不触发衍生作品许可传染；
  若未来要对外展示，须替换为自有数据或公有领域数据。
- 合成数据（本项目生成器产出）**无任何第三方许可约束**，可自由使用。

---

## 2. 核心架构：一张图 + 多投影

### 2.1 为什么是一张图

故障分析必须**跨层穿透**（业务故障 → 传输层 → 物理根因）。
若数据按专业域分成孤立表，AI 无法做跨域推理，
因此**底层只有一张统一图**：所有资源是节点，所有关系是边。

```
        ┌──────────────────────────────────────┐
        │   统一图模型                          │
        │   nodes: 全部资源（各专业域、各层次）   │
        │   edges: 全部关系（承载/保护/归属/...） │
        └────────────────┬─────────────────────┘
                         │ 投影（查询时计算，非存储冗余）
     ┌───────────┬───────┴───────┬────────────┐
     ▼           ▼               ▼            ▼
  拓扑投影     地理投影        层次投影      专业域投影
 （连接关系） （经纬度落点）  （纵向穿透）  （只看传输/无线）
```

### 2.2 多专业域的表示

**不为每个专业域建独立表族**，而是：

- 所有资源共用 `resources` 表（既有），以 `resource_type` 区分
- 类型专属字段放 `attributes` JSON（既有机制），
  但**高频查询字段提升为生成列**（见 §4.3）
- 类型目录 `resource_type_catalog` 封闭注册（与既有 `Specs` 同哲学）

### 2.3 与既有实现的关系

**保留**：`resources` / `relations` / `source_refs` / `import_batches` /
`import_row_results` / `issues` 六张核心表的结构与语义（v1–v13 迁移全部保留）。

**扩展**：
- `resources.resource_type` 的 CHECK 约束扩容（新增类型）
- `relations.relation_type` 的 CHECK 约束扩容（新增关系）
- 新增时序表族（§5）
- 新增生成列与索引（§4.3）

**不变式**（必须保持）：
- 自然键唯一：`UNIQUE (resource_type, natural_key)`
- 关系显式：不从名称/前缀推断
- 溯源：每个资源有 `source_refs` 指向导入批次
- 幂等：同键重导不产生重复资源

---

## 3. 资源类型全量清单

### 3.1 命名规范

- `resource_type` 值：小写蛇形（`optical_cable`）
- 自然键字段：`<type>_code` / `<type>_id` / `<type>_name`（按类型语义）
- **所有类型必须有稳定自然键**——无自然键不得导入（沿用宁跳勿错原则）

### 3.2 类型目录

标注：**[既有]** = v0.6.4 已存在；**[新增]** = 本设计新增。

#### 空间层

| type | 中文 | 自然键 | 关键字段 | 状态 |
| --- | --- | --- | --- | --- |
| `site` | 局站 | `site_id` | 局站名称、地市、区县 | [既有] |
| `machine_room` | 机房 | `machine_room_id` | 机房名称、所属局站 | [既有] |
| `rack` | 机架 | `rack_code` | 所属机房、列号、行号 | [新增] |
| `duct` | 管道 | `duct_code` | 起止局站、长度、孔数 | [新增] P2 |
| `manhole` | 人井 | `manhole_code` | 所属管道、经纬度 | [新增] P2 |
| `pole` | 杆路 | `pole_code` | 起止、杆号 | [新增] P2 |

#### 物理层（设备）

| type | 中文 | 自然键 | 关键字段 | 状态 |
| --- | --- | --- | --- | --- |
| `device` | 网元设备 | `device_code` | 设备名称、设备IP、厂家、型号、状态、经纬度 | [既有] |
| `board` | 板卡 | `board_id` | 所属设备、槽位号、板卡型号、类型 | [新增] |
| `port` | 设备端口 | `device_name\|port_name` | 端口名、类型、带宽、描述 | [既有] |
| `ptp_port` | PTP端口 | 复合键 | PTP 时钟参数 | [既有] |

#### 物理层（光缆网）

| type | 中文 | 自然键 | 关键字段 | 状态 |
| --- | --- | --- | --- | --- |
| `optical_cable` | 光缆 | `cable_code` | 光缆名称、A端局站、Z端局站、芯数、长度 | [新增] |
| `cable_segment` | 光缆段 | `segment_code` | 所属光缆、段序、A端接头盒、Z端接头盒 | [新增] |
| `fiber` | 纤芯 | `cable_code\|fiber_no` | 纤芯号、纤芯总数、使用情况、使用局向、收发关系 | [新增] |
| `odf` | 光配线架 | `odf_code` | 所属机房、名称、容量 | [新增] |
| `odf_terminal` | ODF端子 | `odf_code\|terminal_no` | 端子号、对应纤芯 | [新增] |
| `splice_box` | 接头盒 | `splice_box_code` | 经纬度、容量、已用芯数 | [新增] |

> `fiber` 的字段依据：真实电信资源表中光缆纤芯的列结构为
> 「端口号 | 光缆名称 | 纤芯号/纤芯总数 | 对端落地点 | 使用情况 | 使用局向 | 收发关系」
> （来源：电信设备资源机房标识规范）。上表已按此对齐。

#### 传输层

| type | 中文 | 自然键 | 关键字段 | 状态 |
| --- | --- | --- | --- | --- |
| `circuit` | 电路 | `circuit_code` | 电路编号、A/Z端、带宽、业务类型 | [既有，需扩展] |
| `otn_och` | OTN波道 | `och_code` | 波道号、波长、速率、承载光缆 | [新增] |
| `sdh_timeslot` | SDH时隙 | `timeslot_code` | VC 级别、时隙号、容量 | [新增] |
| `protection_group` | 保护组 | `pg_code` | 保护类型（1+1/1:1/环网）、工作/保护路径 | [新增] |

#### 逻辑层

| type | 中文 | 自然键 | 关键字段 | 状态 |
| --- | --- | --- | --- | --- |
| `ring` | 环网 | `ring_code` | 环名、环类型（接入环/汇聚环）、节点数 | [新增] |
| `igp_domain` | IGP域 | `domain_code` | 进程号、区域号、协议 | [新增] P1 |
| `vpn_instance` | VPN实例 | `vpn_code` | VPN 名称、RD/RT、客户 | [新增] P1 |

#### 业务层

| type | 中文 | 自然键 | 关键字段 | 状态 |
| --- | --- | --- | --- | --- |
| `customer` | 客户 | `customer_code` | 客户名称、客户类型、联系人 | [新增] |
| `service` | 业务实例 | `service_code` | 业务类型、带宽、客户、状态 | [新增] |
| `leased_line` | 专线 | `line_code` | 专线编号、STN编号、A/Z端、协议带宽 | [新增] |

> `leased_line` 字段依据：用户提供的真实专线表 23 列
>（级别/业务类型/组网方式/专线名称/专线编号/集团STN编号/互联网专线编号/
> 客户IP掩码/客户名称/A端装机地址/Z端装机地址/A端IP/名称/端口/VLAN ID/
> 装机经纬度/Z端.../协议带宽/工单号）

#### 接入层

| type | 中文 | 自然键 | 关键字段 | 状态 |
| --- | --- | --- | --- | --- |
| `base_station` | 基站 | 条件键（编码 / 地市\|名称） | 基站类型、电联共享、环/链、归属A | [既有，需扩展] |
| `bbu` | BBU | `bbu_code` | 所属基站、设备型号 | [新增] P1 |
| `rru` | RRU | `rru_code` | 所属 BBU、覆盖小区 | [新增] P1 |
| `olt` | OLT | `olt_code` | 所属机房、型号、PON口数 | [新增] P1 |
| `onu` | ONU | `onu_code` | 所属 OLT/PON口、用户 | [新增] P1 |
| `pon_port` | PON口 | `olt_code\|port_no` | 端口号、分光比 | [新增] P1 |

#### 既有但不属网络资源的类型

| type | 中文 | 说明 |
| --- | --- | --- |
| `ip_pool` | IP地址池 | [既有] 地址空间管理 |

### 3.3 类型扩展的迁移成本

新增类型**不改表结构**（`resources` 表已用 `resource_type` 区分，
字段存 `attributes` JSON）。迁移只需两件事：

1. **扩容 CHECK 约束**：`resources.resource_type IN (...)` 与
   `relations.relation_type IN (...)`——SQLite 需重建表（v7/v9/v10 已有同模式先例）
2. **注册 mapping.Specs**：每个类型一份字段规格（含自然键、必填、格式、同义词）

```sql
-- 迁移骨架（SQLite 无法 ALTER CHECK，需重建）
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN (
    'device','ip_pool','circuit','port','base_station','site','machine_room','ptp_port',
    -- 新增：空间
    'rack','duct','manhole','pole',
    -- 新增：物理（设备）
    'board',
    -- 新增：物理（光缆）
    'optical_cable','cable_segment','fiber','odf','odf_terminal','splice_box',
    -- 新增：传输
    'otn_och','sdh_timeslot','protection_group',
    -- 新增：逻辑
    'ring','igp_domain','vpn_instance',
    -- 新增：业务
    'customer','service','leased_line',
    -- 新增：接入
    'bbu','rru','olt','onu','pon_port'
  )),
  natural_key TEXT NOT NULL,
  name TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',
  status TEXT,
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (resource_type, natural_key)
);
INSERT INTO resources_new SELECT * FROM resources;
DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
COMMIT;
PRAGMA foreign_keys = ON;
```

> **注意**：重建 `resources` 会级联影响引用它的外键
>（`relations.from_resource_id` / `to_resource_id` / `source_refs`）。
> 实施时必须先关闭外键、重建后校验完整性，
> 并可参考 v7 的「FK 安全表重建」既有实现。


---

## 4. 关系类型全量清单

### 4.1 设计原则

**关系显式、方向明确、语义封闭。**

- 每条边只有一种语义，不重载
- 方向固定（`from → to`），反向查询由索引支持
- **禁止从命名/前缀/IP 段推断关系**（沿用项目既有红线）

### 4.2 关系目录

标注：**[既有]** = v0.6.4 已存在。

#### 空间归属

| relation | 方向 | 语义 | 状态 |
| --- | --- | --- | --- |
| `belongs_to` | machine_room → site | 机房属局站 | [既有] |
| `located_in` | rack → machine_room | 机架在机房 | [新增] |
| `housed_in` | device → machine_room | 设备在机房 | [新增] |
| `installed_in` | board → device | 板卡在设备 | [新增] |
| `mounted_on` | port → board | 端口在板卡 | [新增] |

> 说明：`housed_in` 与既有 `belongs_to` 语义不同——
> `belongs_to` 是「资源层级容器」，`housed_in` 是「物理位置」。
> 若认为二者可合并，实施时需显式裁决并记录 ADR。

#### 物理连接

| relation | 方向 | 语义 | 状态 |
| --- | --- | --- | --- |
| `a_end` | circuit → device | 电路 A 端设备 | [既有] |
| `z_end` | circuit → device | 电路 Z 端设备 | [既有] |
| `cable_a_end` | optical_cable → site | 光缆 A 端局站 | [新增] |
| `cable_z_end` | optical_cable → site | 光缆 Z 端局站 | [新增] |
| `carries_fiber` | optical_cable → fiber | 光缆含纤芯 | [新增] |
| `fiber_terminates` | fiber → odf_terminal | 纤芯成端于ODF端子 | [新增] |
| `segment_of` | cable_segment → optical_cable | 光缆段属光缆 | [新增] |
| `connects_device` | optical_cable → device | 光缆连接设备（配线光缆） | [新增] |

#### 传输承载（层次穿透的关键）

| relation | 方向 | 语义 | 状态 |
| --- | --- | --- | --- |
| `carried_by` | circuit → otn_och \| sdh_timeslot | 电路由波道/时隙承载 | [新增] **P0** |
| `routed_over` | otn_och → optical_cable | 波道路由经过光缆 | [新增] **P0** |
| `uses_fiber` | otn_och → fiber | 波道占用纤芯 | [新增] **P0** |

#### 逻辑归属

| relation | 方向 | 语义 | 状态 |
| --- | --- | --- | --- |
| `member_of_ring` | device → ring | 设备属某环 | [新增] **P0** |
| `protects` | protection_group → circuit | 保护组保护电路 | [新增] |
| `in_domain` | device → igp_domain | 设备属IGP域 | [新增] P1 |
| `in_vpn` | port → vpn_instance | 端口属VPN实例 | [新增] P1 |

#### 业务服务（业务影响分析）

| relation | 方向 | 语义 | 状态 |
| --- | --- | --- | --- |
| `serves` | device \| base_station → customer | 设备/基站服务客户 | [新增] **P0** |
| `subscribed_by` | service → customer | 业务属客户 | [新增] |
| `provided_by` | service → circuit | 业务由电路提供 | [新增] **P0** |
| `depends_on` | service → device \| circuit | 业务依赖资源 | [新增] **P0** |

#### 接入关联

| relation | 方向 | 语义 | 状态 |
| --- | --- | --- | --- |
| `assigned_to` | ip_pool → device | 地址池分配设备 | [既有] |
| `uplink_to` | base_station → device | 基站上联设备（**替代「归属A」文本匹配**） | [新增] **P0** |
| `home_bbu` | rru → bbu | RRU 归属 BBU | [新增] |
| `covers` | rru → base_station | RRU 覆盖基站 | [新增] |
| `connects_onu` | onu → pon_port | ONU 接 PON 口 | [新增] |

### 4.3 关系类型的迁移

与资源类型同理，`relations.relation_type` 的 CHECK 需扩容：

```sql
relation_type TEXT NOT NULL CHECK (relation_type IN (
  'assigned_to','a_end','z_end','belongs_to',
  -- 空间
  'located_in','housed_in','installed_in','mounted_on',
  -- 物理连接
  'cable_a_end','cable_z_end','carries_fiber','fiber_terminates',
  'segment_of','connects_device',
  -- 传输承载
  'carried_by','routed_over','uses_fiber',
  -- 逻辑
  'member_of_ring','protects','in_domain','in_vpn',
  -- 业务
  'serves','subscribed_by','provided_by','depends_on',
  -- 接入
  'uplink_to','home_bbu','covers','connects_onu'
))
```

### 4.4 纵向穿透路径（设计验证）

**这是本设计要解决的核心问题**——从业务故障定位到物理根因。

**场景**：某客户专线中断，需要找出根因。

```
customer（客户）
  ↑ subscribed_by
service（业务实例）
  ↑ provided_by
circuit（电路）
  ↑ carried_by
otn_och（波道）
  ↑ uses_fiber
fiber（纤芯）
  ↑ carries_fiber
optical_cable（光缆）
  ↑ cable_a_end / cable_z_end
site（局站）→ machine_room（机房）→ device（设备）→ port（端口）
```

**反向查询**（设备故障 → 影响哪些客户）：

```
device（故障设备）
  → port（端口）
  → fiber（纤芯）
  → otn_och（波道）
  → circuit（电路）
  → service（业务）
  → customer（客户）
```

**这个查询现有的 `/api/resources/{id}/relations?depth=N` 已支持 BFS**，
但需要**关系类型到位**才能穿透。当前只有 4 种关系，穿两层就断了。


---

## 5. 时序指标存储（P0，本设计的另一半）

### 5.1 为什么需要

用户目标原文：**「根据设备多日的动态变化发现他的问题及时保养」**。

这要求存储**同一指标的多日取值**，才能判断：
- 光功率是否在持续劣化（光衰）
- 丢包率是否在上升
- 带宽利用率是否逼近阈值

而现有 `resources.attributes` 是**静态台账**（一个字段一个当前值），
**无法表达「同一资源的同一指标在不同时刻的多个值」**。

### 5.2 数据来源确认（用户已提供真实表头）

端口表 **13–72 列**是性能数据，当前**全部丢弃**：

```
总流量 / 总带宽利用率 / 接收字节数 / 接收速率(ipv4/ipv6) / 发送字节数
发送速率 / 接收峰值速率 / 发送峰值速率 / 总峰值速率 / 包发送速率
发送带宽 / 发送带宽利用率(峰值) / 发送单播/组播/广播/非单播包数
发送丢包数 / 发送丢包率 / 发送错误包数 / 发送错误包率
接收带宽 / 接收带宽利用率(峰值) / 接收单播/组播/广播/非单播包数
接收丢包数 / 接收丢包率 / 接收错误包数 / 接收错误包率 / 接收未知协议包数
接收光功率均值/峰值/谷值 / 发送光功率均值/峰值/谷值
偏置电流 / 供电电压 / 光模块温度均值/峰值/谷值
ipv4流量占比 / ipv6流量占比 / 发送光功率 / 接收光功率
```

**这些正是判断设备劣化的核心数据**，必须有存储位置。

### 5.3 表结构（三表设计）

```sql
-- ① 指标目录：封闭注册的指标定义（与 mapping.Specs 同哲学）
CREATE TABLE metric_keys (
  metric_key   TEXT PRIMARY KEY,       -- optical_power_rx_dbm
  label        TEXT NOT NULL,          -- 接收光功率
  unit         TEXT NOT NULL,          -- dBm / % / Mbps / count / ℃
  value_kind   TEXT NOT NULL           -- gauge | counter | ratio
                 CHECK (value_kind IN ('gauge','counter','ratio')),
  min_value    REAL,                   -- 合理下界（异常值过滤，可空）
  max_value    REAL,                   -- 合理上界
  direction    TEXT                    -- higher_is_better | lower_is_better | neutral
                 CHECK (direction IN ('higher_is_better','lower_is_better','neutral')),
  created_at   TEXT NOT NULL
);

-- ② 序列元数据：某资源某指标 = 一条序列（避免每行重复存资源ID）
CREATE TABLE metric_series (
  series_id    TEXT PRIMARY KEY,       -- ser_<uuid>
  resource_id  TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  metric_key   TEXT NOT NULL REFERENCES metric_keys(metric_key),
  scope        TEXT,                   -- 细分维度：如端口名（NULL = 资源级）
  first_seen_at TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL,
  UNIQUE (resource_id, metric_key, scope)
);
CREATE INDEX idx_metric_series_resource ON metric_series(resource_id);
CREATE INDEX idx_metric_series_key      ON metric_series(metric_key);

-- ③ 测量点：窄表，(series_id, ts) 主键，天然幂等（同点重导覆盖）
CREATE TABLE metrics (
  series_id  TEXT NOT NULL REFERENCES metric_series(series_id) ON DELETE CASCADE,
  ts         TEXT NOT NULL,            -- RFC3339 UTC，来自数据（非服务器时钟）
  value      REAL NOT NULL,
  quality    TEXT DEFAULT 'ok'         -- ok | missing | suspect（数据质量）
                 CHECK (quality IN ('ok','missing','suspect')),
  batch_id   TEXT REFERENCES import_batches(id),
  PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;
CREATE INDEX idx_metrics_ts ON metrics(ts);
```

### 5.4 关键设计决策

**D1. 窄表而非宽表**

宽的「一行一天、72 列指标」表看似直观，但：
- 加一个指标 = 改表结构（不可接受的迁移负担）
- AI 想查「所有设备的接收光功率」需扫全表 72 列

窄表 `(series_id, ts, value)` 下：
- 新增指标 = 插一行 `metric_keys`（**零迁移**）
- AI 查询「某指标的全部序列」走 `metric_key` 索引

**D2. 时间戳来自数据，不用服务器时钟**

沿用项目确定性哲学：`ts` 必须来自表格中的「日期」列。
保证**同数据重导 → 同结果**（幂等可验证）。

**D3. 幂等由主键保证**

`PRIMARY KEY (series_id, ts)` + `INSERT OR REPLACE`：
**同一天重复导入同一数据 → 覆盖而非追加**（不会产生重复点）。

这与既有「同键重导 skip」哲学略有不同（那里是 skip，这里是 upsert），
原因：时序点是**值语义**（同时间同指标的最新值即真值），
而资源是**身份语义**（同键即同资源，属性变更需人工确认）。
**此差异需在实施时以 ADR 显式记录。**

**D4. 序列元数据分离**

`metric_series` 单独存 `(resource_id, metric_key, scope)`，
使 `metrics` 表每行只需 `series_id`（8 字节 UUID 前缀 vs 40 字节资源 ID），
在「万级设备 × 数十指标 × 每日一点」量级下节省显著。

**D5. scope 字段支持端口级指标**

端口表的数据是**端口级**的（设备IP + 端口名）。
两种建模方式：
- (a) 每个端口是独立 `port` 资源 → `resource_id` 指向 port
- (b) 指标挂在设备上，`scope='端口名'` 区分

**推荐 (a)**：端口已是既有资源类型（v7 落地），
`scope` 留给无法独立成资源的细分维度（如光模块的收发双向）。

### 5.5 AI 查询友好性

**AI 需要的典型查询**（这些必须在设计时就能高效执行）：

```sql
-- Q1: 某设备近 30 天的接收光功率趋势（判断劣化）
SELECT m.ts, m.value
FROM metrics m
JOIN metric_series s ON s.series_id = m.series_id
WHERE s.resource_id = ? AND s.metric_key = 'optical_power_rx_dbm'
  AND m.ts >= ?
ORDER BY m.ts;

-- Q2: 哪些设备的指标在持续下降（全库扫描，用于每日巡检）
SELECT s.resource_id, s.metric_key,
       COUNT(*) AS n,
       -- 线性回归斜率（SQLite 无内置，可用首末差分近似）
       (MAX(m.value) - MIN(m.value)) AS delta
FROM metrics m JOIN metric_series s USING (series_id)
WHERE s.metric_key = 'optical_power_rx_dbm' AND m.ts >= ?
GROUP BY s.resource_id, s.metric_key
HAVING delta < -1.0;   -- 下降超过阈值

-- Q3: 某资源一次拿全所有指标的最新值（AI 上下文组装）
SELECT k.label, k.unit, m.value, m.ts
FROM metric_series s
JOIN metric_keys k USING (metric_key)
JOIN metrics m ON m.series_id = s.series_id
WHERE s.resource_id = ?
  AND m.ts = (SELECT MAX(ts) FROM metrics WHERE series_id = s.series_id);
```

**结论**：Q1/Q2 走索引；Q3 是 N 次子查询，
建议实施时提供**物化视图或 API 端点**（§7.3）。


---

## 6. 纵向穿透视图设计

### 6.1 定位

**不是新增一张画布，而是现有图的一个投影。**

拓扑投影回答「谁连着谁」（横向），
穿透视图回答「这条业务经过哪些层」（纵向）。

### 6.2 视图形态

```
┌─────────────────────────────────────────────────────────┐
│  业务层   ┌──────────┐                                  │
│           │ 客户A专线 │                                  │
│           └────┬─────┘                                  │
│                │ provided_by                             │
│  传输层   ┌────▼─────┐                                  │
│           │ 电路 C-001│←── ⚠️ 此处中断                   │
│           └────┬─────┘                                  │
│                │ carried_by                              │
│  波道层   ┌────▼─────┐                                  │
│           │ OTN 波道 5│                                  │
│           └────┬─────┘                                  │
│                │ uses_fiber                              │
│  物理层   ┌────▼─────┐    ┌──────────┐                  │
│           │ 纤芯 3   │───→│ 光缆 G-12│                  │
│           └──────────┘    └────┬─────┘                  │
│                                │ cable_a_end             │
│  空间层                ┌───────▼───────┐                │
│                        │ 局站：加区枢纽楼│                │
│                        └───────────────┘                │
└─────────────────────────────────────────────────────────┘
```

### 6.3 数据来源

**完全由既有 BFS 查询驱动**，无需新存储：

```
GET /api/resources/{id}/relations?depth=N&direction=both
```

已有实现，但**受限于关系类型只有 4 种**——扩容后即可穿透。

### 6.4 AI 侧的使用

**AI 不需要看图**，它需要的是穿透结果的结构化数据：

```json
{
  "rootResourceId": "res_customer_001",
  "layers": [
    {"depth": 0, "type": "customer", "id": "...", "name": "XX局"},
    {"depth": 1, "type": "leased_line", "id": "...", "name": "MSTPHL1002660715"},
    {"depth": 2, "type": "circuit", "id": "...", "status": "中断"},
    {"depth": 3, "type": "otn_och", "id": "..."},
    {"depth": 4, "type": "fiber", "id": "..."},
    {"depth": 5, "type": "optical_cable", "id": "..."},
    {"depth": 6, "type": "site", "id": "...", "name": "加区枢纽楼"}
  ],
  "edges": [
    {"from": "...", "to": "...", "relation": "provided_by"},
    // ...
  ]
}
```

**设计要点**：
- 按 `depth` 分层返回（AI 可直接定位异常层）
- 边携带 `relation` 语义（AI 知道为什么这两层相关）
- 单次调用返回完整链路（避免 AI 多次往返）

### 6.5 界面呈现（次要，但要有）

- 工作台新增「穿透视图」标签（与拓扑图/2D/3D 并列）
- 从任一资源出发，纵向展开到相关层
- **异常层高亮**（如某层资源状态为故障）

**注意**：界面是本设计的**次要目标**，
核心是让 AI 能拿到穿透数据。


---

## 7. AI 友好接口设计

### 7.1 设计目标

用户目标：**「每天导入表格 → AI 分析异常 → 发现劣化 → 提示保养」**。

AI 在这个流程中需要的不是「网页」，而是**稳定的、结构化的数据接口**。

### 7.2 三条通道的职责划分

| 通道 | 谁用 | 职责 | 现状 |
| --- | --- | --- | --- |
| **MCP** | AI 智能体 | 受控操作（读资源、操控仿真） | 5 个工具，冻结 |
| **HTTP API** | 集成/前端 | 全量数据访问 | 已有 40+ 端点 |
| **CLI** | 脚本/批处理 | 每日导入、报表 | 已有 13 子命令 |

**关键决策（沿用 ADR-0011 D5）**：

> **MCP 工具面维持「恰好 5 个」，不因新增数据而扩容。**

理由：MCP 是 AI 的**受控通道**（审计面、攻击面），
新增数据应经 **HTTP API** 暴露，AI 通过既有 `resource_search` + HTTP 读取。

**但 5.5 的趋势查询 HTTP 侧目前没有对应端点**——这是要补的。

### 7.3 需要新增的 HTTP 端点（AI 分析用）

```
GET /api/metrics/keys
  指标目录（AI 先查有哪些指标可分析）

GET /api/resources/{id}/metrics?keys=&from=&to=&agg=
  某资源的时序数据（agg = raw|daily_avg|daily_min|daily_max）
  返回: series 数组，每项含 metricKey 与 points（ts/value）

GET /api/metrics/anomalies?metricKey=&from=&to=&threshold=&direction=
  异常扫描（每日巡检的核心调用）
  返回: 按资源分组的异常列表（含变化趋势、首次异常时间）

GET /api/resources/{id}/context?depth=1&metrics=latest
  单次拿全「一台设备及其上下文」（AI 组装分析上下文用）
  包含: 资源详情 + 关系邻居 + 各指标最新值 + 近期异常

GET /api/resources/{id}/traverse?direction=up|down&maxDepth=N&types=
  纵向穿透（§6.4 的结构化返回）
```

### 7.4 AI 每日巡检的完整流程（设计验证）

这是本设计的**验收场景**——它必须能端到端跑通：

```
① 运维人员从网管导出表格，放到指定目录
   ↓
② 定时任务执行导入（CLI）
   netopslab import --file daily-2026-10-13.xlsx --map daily.json --db prod.db
   （表格含：端口性能「光功率/丢包率/带宽」+ 资源变更）
   ↓
③ AI 经 HTTP 扫描异常
   GET /api/metrics/anomalies?metricKey=optical_power_rx_dbm&from=...&threshold=-1.0
   （返回：DEV-015 的接收光功率 30 天内下降 3.2dB）
   ↓
④ AI 取上下文做判断
   GET /api/resources/{dev015}/context?metrics=latest
   GET /api/resources/{dev015}/traverse?direction=down&maxDepth=5
   （拿到：该设备承载哪些业务、影响哪些客户）
   ↓
⑤ AI 输出巡检结论
   「DEV-015 光模块接收光功率持续劣化，建议更换光模块；
     影响业务：3 条客户专线，建议安排窗口期。」
```

**这个流程对存储的要求**（已在本设计满足）：
- 时序可存（§5）
- 可按资源查时序（§5.5 Q1）
- 可全库扫描异常（§5.5 Q2）
- 可穿透到业务影响（§4.4）

### 7.5 对 AI 友好的具体设计约束

| 约束 | 做法 | 理由 |
| --- | --- | --- |
| **稳定字段名** | 字段名一旦发布不改 | AI 提示词会引用字段名 |
| **确定性输出** | 同参数同结果（排序稳定） | AI 可缓存、可对比 |
| **单次拿够** | `context` 端点一次给全 | 减少往返，省 token |
| **显式关系** | 不靠名称推断 | AI 无业务先验知识 |
| **单位明确** | 每个指标带 `unit` | 避免 dBm/百分比混淆 |
| **方向明确** | 指标带 `direction`（高好/低好） | AI 判断劣化的方向 |
| **时间显式** | 所有 `ts` 是数据时间，非服务器时间 | 可复现 |

### 7.6 与现有 MCP 工具的衔接

现有 5 个工具中，`resource_search` 是唯一的数据读取工具。
**它需要小幅扩展以支持新资源类型**（不改工具数量）：

```
resource_search(q, type, limit, offset)
  → type 参数的合法值列表扩容（新增 30+ 类型）
  → 返回结构不变（id/type/naturalKey/name/status/attributes/source）
```

**其余 4 个 lab 工具（仿真）不受本设计影响。**

---

## 8. 占位数据策略（开发期）

### 8.1 原则

**开发期用合成数据 + 公开数据，验证功能；生产期替换为真实表格。**

关键：**导入通道不变**——只换数据文件，不改代码。

### 8.2 三层数据来源

| 层 | 来源 | 许可 | 用途 |
| --- | --- | --- | --- |
| **合成** | 本项目生成器 | 自有，无约束 | 主力：形态可控、量可调、可进仓库 |
| **公开** | OpenStreetMap / OpenCelliD 等 | CC BY-SA 等 | 地理参考、基站分布（**仅开发占位**） |
| **真实** | 网管导出 | 涉密 | 生产期替换（不入仓库） |

### 8.3 合成数据生成器（建议实现）

新增 CLI 子命令，与既有 `testdata` 同模式：

```
netopslab synthdata --out ./testdata/synth \
  --devices 500            # 设备数
  --days 60                # 性能数据天数
  --scenario degrade       # 故障场景：normal | degrade | outage
  --seed 42                # 确定性种子（同种子同输出）
```

**必须支持的特性**：
- **确定性**：同 `--seed` 同输出（延续项目 DET 测试哲学）
- **场景注入**：`degrade` 让指定设备的光功率**逐日下降**（用于验证劣化检测）
- **形态真实**：字段名、取值量纲、空值分布贴合真实网管导出
- **规模可调**：从演示（10 设备）到压测（万级设备）

**生成的数据覆盖**：
- 全部资源类型（§3.2）
- 全部关系类型（§4.2）
- 时序指标（§5.3），含正常与劣化两类曲线

### 8.4 公开数据的授权说明（明确写入设计）

> **关于 CC BY-SA 类数据（如 OpenCelliD）：**
>
> 本项目在**初始开发阶段**使用其作为**占位数据**，用途限于：
> - 功能测试
> - 界面演示
> - 数据形态验证
>
> **不用于对外宣传、不随产品分发、不进入交付物**。
> 待真实数据获取后**全部替换**。
>
> 因此不触发 CC BY-SA 的衍生作品许可传染。
> 若未来需要对外展示，须替换为自有数据或 CC0 数据。

**建议实施时的额外保障**：
- 占位数据文件标注 `PLACEHOLDER` 前缀或独立目录
- 生成器脚本入库（可重跑），生成的数据文件不入库

---

## 9. 实施路径

### 9.1 迁移分期

| 期 | 内容 | 迁移版本 | 依赖 |
| --- | --- | --- | --- |
| **M1** | 时序表族（§5.3） | v14 | 无 |
| **M2** | 资源类型扩容（§3.2 全部） | v15 | 无 |
| **M3** | 关系类型扩容（§4.2 全部） | v16 | M2 |
| **M4** | 生成列与索引优化（高频查询字段） | v17 | M2 |

**M1 与 M2/M3 无依赖，可并行。**

### 9.2 为什么先做 M1（时序）

1. **用户的核心目标**（每日导入 → 发现劣化）**直接依赖时序**
2. **零风险**：只新增表，不动既有表结构
3. **立即可验证**：导入两天数据就能测「趋势查询」
4. **AI 立刻可用**：有了时序，AI 才能做「分析异常」

### 9.3 M2/M3 的风险

**资源/关系类型扩容需重建 `resources` 与 `relations` 表**（SQLite 无法 ALTER CHECK），
这会级联影响外键。**实施时必须**：

1. 先写**数据完整性校验**（重建前后资源数、关系数一致）
2. 参考 v7 的「FK 安全表重建」既有实现
3. 在**全新库**上先验证迁移，再对生产库执行
4. 迁移测试必须覆盖：**空库、有数据、有仿真工作区**三种状态

### 9.4 建议的开发顺序

```
第 1 步  M1 时序表 + synthdata 生成器
         → 验收：能导入多日数据、能查趋势

第 2 步  HTTP 端点（§7.3 前三个）
         → 验收：AI 能经 HTTP 拿到趋势与异常

第 3 步  M2/M3 类型扩容
         → 验收：光缆/纤芯/业务可导入、可穿透

第 4 步  §7.3 后两个端点（context / traverse）
         → 验收：§7.4 的完整巡检流程可跑通

第 5 步  穿透视图界面（次要）
```

### 9.5 验收标准（对应 issue #16）

- [ ] 时序数据可存、可查、可聚合，同数据重导幂等
- [ ] 资源类型覆盖 §3.2 全部（或明确分期）
- [ ] 关系类型覆盖 §4.2 全部
- [ ] 纵向穿透可从业务层走到物理层（§4.4 两条路径）
- [ ] AI 可经 HTTP 完成 §7.4 的每日巡检流程
- [ ] 合成数据生成器确定性可复现
- [ ] 既有 v1–v13 迁移与数据**完整保留**（无破坏性变更）

---

## 10. 图查询语义与防环设计（关键补充）

### 10.1 为什么这一章是必需的

§3/§4 只定义了「有什么类型的关系」，**没有定义「怎么查」**。

实践中的失败模式（用户已指出）：**AI 拿到一张大图却反复试探、兜圈子，
始终定位不到真实故障根因**。

根因有三：

1. **查不到**——深度上限 2，跨层链路（业务→物理共 6 层）走不通
2. **分不清**——遍历无向，AI 不知上下游，只能来回试
3. **查得乱**——全图松弛 + 环网边全收，返回大量无关内容污染上下文

**本章定义查询语义：同层横向可达 + 跨层纵向可穿，且必须收敛。**

### 10.2 核心概念：两种边、两个方向

#### 边的两种性质

| 性质 | 含义 | 例子 |
| --- | --- | --- |
| **同层边（lateral）** | 两端资源属同一层次 | `a_end`（电路↔设备）、`connects_onu`（ONU↔PON口） |
| **跨层边（vertical）** | 两端资源属不同层次 | `carried_by`（电路↕波道）、`uses_fiber`（波道↕纤芯） |

**同层边用于「横向找同类」（还有哪些设备受影响）**，
**跨层边用于「纵向找根因」（这条业务经过哪段光缆）**。

#### 层次定义（用于判定同层/跨层）

```
L5 业务层    customer / service / leased_line
L4 逻辑层    ring / igp_domain / vpn_instance / protection_group
L3 传输层    circuit / otn_och / sdh_timeslot
L2 物理层（光）  optical_cable / cable_segment / fiber / odf / odf_terminal / splice_box
L1 物理层（设备） device / board / port / ptp_port
L0 空间层    site / machine_room / rack / duct / manhole / pole
```

> **注意**：L2 与 L1 都是「物理」，但光缆网与设备网是两套并行的物理资源，
> 二者之间的跳转（`fiber_terminates` 纤芯→ODF端子）**算跨层**。

#### 每个关系的方向语义（必须有向）

**规则**：`from` 是「依赖方 / 上层 / 被承载方」，`to` 是「被依赖方 / 下层 / 承载方」。

即：**沿 `from → to` 走 = 向下追溯（找根因）；反向走 = 向上汇总（找影响面）**。

| relation | from（上层） | to（下层） | 层差 | 方向语义 |
| --- | --- | --- | --- | --- |
| `subscribed_by` | service | customer | 同层 | 业务属于客户 |
| `provided_by` | service | circuit | 跨层 | 业务由电路提供 |
| `depends_on` | service | device | 跨层 | 业务依赖设备 |
| `carried_by` | circuit | otn_och | 跨层 | 电路由波道承载 |
| `routed_over` | otn_och | optical_cable | 跨层 | 波道路径经光缆 |
| `uses_fiber` | otn_och | fiber | 跨层 | 波道占用纤芯 |
| `carries_fiber` | optical_cable | fiber | 同层 | 光缆含纤芯 |
| `fiber_terminates` | fiber | odf_terminal | 跨层 | 纤芯成端于ODF |
| `cable_a_end` | optical_cable | site | 跨层 | 光缆 A 端落局站 |
| `housed_in` | device | machine_room | 跨层 | 设备在机房 |
| `installed_in` | board | device | 同层 | 板卡在设备 |
| `mounted_on` | port | board | 同层 | 端口在板卡 |
| `uplink_to` | base_station | device | 跨层 | 基站上联设备 |
| `home_bbu` | rru | bbu | 同层 | RRU 归属 BBU |
| `serves` | device | customer | 跨层 | 设备服务客户 |
| `member_of_ring` | device | ring | 跨层 | 设备属某环 |
| `a_end` / `z_end` | circuit | device | 跨层 | 电路端接设备 |
| `assigned_to` | ip_pool | device | 跨层 | 地址池分配设备 |
| `belongs_to` | machine_room | site | 跨层 | 机房属局站 |

**这张表是实施的关键交付物**——每个关系类型都必须在此登记方向语义，
未登记方向的关系不得使用（缺乏语义的边只会让查询兜圈子）。


### 10.3 查询 API 设计

#### 现有端点的问题

```
GET /api/resources/{id}/relations?depth=1|2
```

| 问题 | 说明 |
| --- | --- |
| 深度上限 2 | 跨层链路需 6 层，**穿不透** |
| 无方向参数 | 上下游混在一起，AI **分不清流向** |
| 无关系过滤 | 返回全部边，**上下文被无关内容污染** |
| 无类型过滤 | 无法「只看传输层」 |

**现有实现（`internal/topology/topology.go:224`）的算法缺陷**：

```go
// 0/1-weighted BFS (fixpoint relaxation)
cost := map[string]int{id: 0}
for changed := true; changed; {          // 固定点迭代，无早期终止
    for cur, curCost := range cost {     // 每轮遍历整个已访问集
        for _, nb := range g.adj[cur] {  // 无向邻接表，不区分方向
```

- **无早期终止**：`depth` 只做过滤，不做剪枝 → 最坏 O(V×E)
- **无向遍历**：上下游不可区分
- **边收集方式**：遍历**全图所有边**检查两端是否在访问集（268-274 行），
  环网场景下会收集大量与查询无关的边

#### 新端点设计（替代而非扩展）

```
GET /api/resources/{id}/traverse

参数：
  direction = down | up | both      默认 down（追溯根因）
  layers    = 1,2,3,4,5            只走指定层（可空 = 全部）
  relations = carried_by,uses_fiber 只走指定关系（可空 = 全部已登记方向的关系）
  maxDepth  = 1..8                  默认 6（覆盖业务→物理完整链）
  maxNodes  = 1..500                默认 200（防上下文爆炸）
  pathOnly  = true | false          默认 false；true 时只返回最短路径

返回：
  {
    "root": {id, type, name},
    "nodes": [{id, type, name, layer, depth, status}],   // 按 depth 升序，同层按类型+键
    "edges": [{from, to, relation, isCrossLayer}],        // 去重后
    "paths": [[id, id, ...]],        // pathOnly=true 时给最短路径
    "truncated": false,              // 超出 maxNodes 时为 true
    "stats": {visitedEdges, elapsedMs}
  }
```

### 10.4 防环与去重规则（收敛保证）

**这是「不兜圈子」的技术保证。**

#### 规则 1：节点按 ID 去重，首次访问即定型

```
visited: Map<resourceId, {depth, layer, parentEdge}>
// 同一节点只展开一次；再次遇到只作为边端点，不重新入队
```

#### 规则 2：边按 (from, to, relation) 三元组去重

```
emittedEdges: Set<"from|to|relation">
// 环网中同一条边可能被多次遇到 → 只发一次
```

**这修正了现实现的问题**：现在按「两端是否在访问集」收集边，
会把环网里所有边都收进来（包括与本次查询路径无关的）。

#### 规则 3：BFS 用队列，不用固定点迭代

```
// 正确做法：层序 BFS，depth 单调递增，天然终止
queue = [{id: root, depth: 0}]
while queue not empty:
    cur = queue.shift()
    if cur.depth >= maxDepth: continue        // ← 剪枝，而非过滤
    for edge in outgoingEdges(cur.id, direction, relations):
        if visited.has(edge.to): 
            emitEdgeOnce(edge)                 // 只记边，不展开
            continue
        visited.set(edge.to, {depth: cur.depth+1})
        queue.push({id: edge.to, depth: cur.depth+1})
        emitEdgeOnce(edge)
```

**复杂度**：O(V + E)，与深度无关的固定开销，
且 `maxDepth` 是**剪枝条件**（达到即不入队），不是事后过滤。

#### 规则 4：环网特判（`ring` 场景）

环网中 A→B→C→A 是物理存在，查询时：

- 节点不重复（规则 1 已保证）
- 边不重复（规则 2 已保证）
- **额外**：`member_of_ring` 边只在**首次进入该环**时展开，
  避免「环内每个设备都展开一次环」

#### 规则 5：无向遍历必须显式声明

`direction=both` 是**可选**行为，默认 `down`。
且即使在 `both` 模式下，**边仍携带 `relation` 与方向信息**，
AI 可据此判断流向（不会拿到一堆无方向的边）。


### 10.5 故障根因追溯（本设计的核心场景）

#### 场景定义

用户诉求原文：**「通过表象找到真实故障来源」**。

```
表象：客户投诉「XX 局专线不通」
根因：某段光缆的某根纤芯衰减过大
```

两者之间隔着 6 层，**必须靠关系链穿透**。

#### 追溯算法（自下而上定位）

```
输入：一个异常信号（如「客户 A 的专线中断」或「设备 D 上报光功率异常」）

步骤 1  确定起点层
        业务侧异常 → 从 service/leased_line 出发
        设备侧异常 → 从 device/port 出发

步骤 2  向上游做影响面收集（direction=up）
        GET /traverse?direction=up&maxDepth=8
        得到：受影响的所有客户、业务

步骤 3  向下游做根因追溯（direction=down）
        GET /traverse?direction=down&maxDepth=8
        得到：该业务经过的全部物理资源

步骤 4  与告警/性能数据交叉
        对步骤 3 的物理资源集合，查各自的状态与指标
        GET /api/resources/{id}/context?metrics=latest
        找出「状态异常」或「指标越限」的那些

步骤 5  收敛到根因
        在最深层（L2 光缆层 / L1 设备层）找异常点
        该点即根因；其上层资源都是「受影响」而非「根因」
```

#### 关键设计：区分「根因」与「受影响」

**这是 AI 不兜圈子的核心**——必须能区分两种节点：

| 类型 | 特征 | 判据 |
| --- | --- | --- |
| **根因节点** | 自身异常 | 资源 `status` 为故障 **且** 其下游无异常 |
| **受影响节点** | 自身正常，上游有异常 | 资源 `status` 正常 **且** 其上游链上有异常节点 |

**算法**：

```
对步骤 3 得到的物理资源集合，按 depth 从大到小（最深优先）扫描：
    若某节点自身异常，且它的下游（更深层）全无异常：
        → 标记为根因候选
    若某节点自身异常，但下游也有异常：
        → 它是「中间传导点」，继续往深处找
    若某节点自身正常，但下游有异常：
        → 它是「受影响点」
```

**没有这个区分，AI 会看到整条链上多个异常，无法判断谁是真凶** → 兜圈子。

#### API 支持：返回影响链而非平铺列表

```
GET /api/resources/{id}/impact?direction=up|down&maxDepth=8

返回：
{
  "root": {...},
  "impacted": [                        // 受影响（自身正常）
    {"node": {...}, "distance": 2, "viaPath": [id1, id2]}
  ],
  "candidates": [                      // 根因候选（自身异常且下游无异常）
    {"node": {...}, "distance": 4, "reason": "status=down, 下游无异常"}
  ],
  "truncated": false
}
```

**`candidates` 就是 AI 要找的答案**——它不需要自己再遍历推理。

### 10.6 每日巡检场景的查询（对应 §7.4）

```
① 扫描指标异常（不涉及图查询，纯时序）
   GET /api/metrics/anomalies?metricKey=optical_power_rx_dbm&threshold=-1.0
   → 返回：DEV-015 光功率 30 天降 3.2dB

② 对每个异常资源，取影响面（向上）
   GET /api/resources/{dev015}/impact?direction=up&maxDepth=8
   → 返回：受影响客户 3 个、业务 3 条

③ 对每个异常资源，取根因候选（向下）
   GET /api/resources/{dev015}/impact?direction=down&maxDepth=8
   → 返回：根因候选 = {DEV-015 自身}（因为它是设备层，下游是端口）

④ AI 输出
   「DEV-015 接收光功率持续劣化 3.2dB，
     影响 3 个客户的 3 条专线，
     根因为该设备光模块劣化，建议更换。」
```

**整个流程 3 次 HTTP 调用**，无兜圈子。


### 10.7 索引与存储优化（性能保证）

图查询性能取决于**边的检索效率**，现有 `relations` 表需要补索引：

```sql
-- 既有索引（UNIQUE 约束隐含）
-- UNIQUE (from_resource_id, to_resource_id, relation_type)

-- 新增：反向查询（direction=up 时用）
CREATE INDEX idx_relations_to ON relations(to_resource_id, relation_type);

-- 新增：按关系类型过滤（relations= 参数）
CREATE INDEX idx_relations_type ON relations(relation_type, from_resource_id);
```

**时序侧**（§5.3 已定义）补充：

```sql
-- 已有 PRIMARY KEY (series_id, ts)，覆盖「某序列的时间范围查询」
-- 补：跨序列按时间扫描（每日巡检）
CREATE INDEX idx_metrics_ts_key ON metrics(ts, series_id);
```

### 10.8 实施约束（给开发方的硬性要求）

**以下为不可协商的设计约束**：

| # | 约束 | 理由 |
| --- | --- | --- |
| 1 | 每条关系**必须**在 §10.2 表中登记方向语义 | 无方向的边导致查询兜圈子 |
| 2 | 遍历**必须**用层序 BFS + 剪枝，**禁止**固定点迭代 | 现实现 O(V×E)，大图会崩 |
| 3 | 边去重**必须**按三元组，**禁止**按「两端在访问集」 | 环网场景会收无关边 |
| 4 | `direction` **默认 down**（追溯根因），`both` 需显式指定 | 默认行为应服务于主用例 |
| 5 | 必须返回 `truncated` 标记 | AI 需知道结果是否完整 |
| 6 | 必须区分 `impacted` 与 `candidates` | 否则 AI 无法定位真凶 |
| 7 | 单次查询必须有 `maxNodes` 上限 | 防 AI 上下文爆炸 |

### 10.9 验收测试（必须可复现）

**测试 1：跨层穿透完整性**

```
给定：一条专线业务，其下依次有 circuit → otn_och → fiber → optical_cable
执行：GET /traverse?direction=down&maxDepth=8
断言：返回的 nodes 覆盖全部 5 层，且 edges 关系语义正确
```

**测试 2：防环收敛**

```
给定：一个环形拓扑（A→B→C→A，且都属同一 ring）
执行：GET /traverse?direction=down&maxDepth=8
断言：A/B/C 各出现一次；环内边各出现一次；查询在 100ms 内返回
```

**测试 3：方向语义正确**

```
给定：device D 承载 circuit C，C 服务 customer X
执行：GET /resources/{D}/traverse?direction=down
断言：返回 C 与 X（下游 = 被承载方）
执行：GET /resources/{X}/traverse?direction=down
断言：返回 C 与 D（下游 = 承担方）
```

**测试 4：根因候选识别**

```
给定：光缆段 A 故障，导致其上电路中断，进而影响客户 X
执行：GET /resources/{X}/impact?direction=down
断言：candidates 恰为 [光缆段 A]，impacted 包含电路与设备
```

**测试 5：确定性**

```
执行：同参数查询两次
断言：返回结果逐字段相等（节点序、边序稳定）
```

**测试 6：截断标记**

```
给定：大图（>200 节点可达）
执行：GET /traverse?maxNodes=50
断言：truncated=true，且返回节点数 <= 50
```

### 10.10 与既有实现的迁移

| 现有 | 处置 |
| --- | --- |
| `GET /resources/{id}/relations?depth=1\|2` | **保留**（向后兼容），标为 deprecated |
| `MaxRelationDepth = 2` | 新端点不受此限；旧端点保持不变 |
| `Graph.Neighbors()` | **重写**为层序 BFS；旧调用点走新实现 |
| 现有测试 | 保持不变（旧端点行为不变），新增 §10.9 六项测试 |

**迁移原则**：新端点并行提供，旧端点保留，
待所有调用点切换后再决定是否废弃旧端点——
**不做破坏性变更**（与项目既有迁移哲学一致）。

---

## 11. 开放问题（需裁决）

实施前必须明确的问题，**不预设答案**：

1. **`belongs_to` 与 `housed_in` 是否合并**（§4.2 空间归属）
2. **时序 upsert 与资源 skip 的语义差异**是否可接受（§5.4 D3）
3. **端口性能数据的归属层级**：端口级资源 vs 设备级 + scope（§5.4 D5）
4. **新增 30+ 资源类型是否需要 UI 支持**，还是先只做数据层
5. **占位数据的目录约定**（`testdata/` vs 新增 `placeholder/`）

---

## 附录 A：与既有文档的关系

| 文档 | 关系 |
| --- | --- |
| [docs/data-model.md](../data-model.md) | 既有 schema 权威说明（v1–v13），本文档是其**扩展设计** |
| [docs/adr/0011-ops-capabilities.md](../adr/0011-ops-capabilities.md) | 时序/检测/告警的设计决策，本文档 §5 是其 D1 的**落地方案** |
| [docs/import-and-validation.md](../import-and-validation.md) | 导入与校验规则，本文档**不改其语义** |
| issue #16 | 本文档对应的 issue |

## 附录 B：术语

| 术语 | 含义 |
| --- | --- |
| 自然键 | 资源的业务唯一标识（如 device_code） |
| 投影 | 同一图模型的不同呈现方式（拓扑/地理/层次） |
| 穿透 | 沿关系链跨层查询（业务 → 物理） |
| 占位数据 | 开发期替代真实数据的合成/公开数据 |
| 时序点 | 某序列在某一时刻的一次测量 |


---

