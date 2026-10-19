# NetOps Lab — 云网资源治理与 AI 仿真工作台（Stage 0 + Phase 1 + Phase 2）

```
                    ┌──────────────────────────────────────────────┐
   Excel 工作簿      │                netopslab 单二进制              │
 (devices/ippools/  │                                              │
  circuits .xlsx)   │  importer ──► SQLite（modernc.org/sqlite，    │
        │           │  列映射确认     纯 Go 无 CGO，事务 + 行级记账）   │
        ▼           │      │                                       │
   analyze/导入      │      ▼                                       │
                    │  validation（规则 E/W/I，确定性、可重算）        │
                    │      │                                       │
                    │      ▼                                       │
                    │  sim 引擎（类型化操作·故障注入·校验 CHK·undo·     │
                    │  物化      检查点——独占仿真表写路径）             │
                    │      │                                       │
                    │      ▼                                       │
                    │  ┌── API（net/http stdlib）── 嵌入式 React UI    │
                    │  │   （/lab Cytoscape 拓扑画布）                │
                    │  ├── CLI（结构化 JSON 输出；lab 子命令树）       │
                    │  └── MCP（stdio，5 工具：1 只读 + 4 仿真）       │
                    │      │                                       │
                    │      ▼                                       │
                    │  query/topology/report（检索·二跳关系·质量报告）  │
                    └──────────────────────────────────────────────┘
```

> ⚠️ **合规声明**：本平台**不具备生产设备写能力**。AI 仅可操作本地实验室工作区（仿真工作台）；生产操作必须由人员通过公司批准的外部工具执行。仓库内所有数据均为**合成测试数据**，仅用于工程方法验证，不代表真实网络运行情况——**唯一例外**：`samples/` 目录含项目所有者在需求会话中提供的真实电信运营导出样例（含真实设备 IP 与运营商标头，未脱敏），仅限本私有仓库内做导入工程验证、不得外发或再分发（授权与范围详见 [samples/README.md](samples/README.md)）。此为红线「no real network data」的**已文档化偏差**（经项目所有者授权入库并记录于案）。此外 v0.8.0 起仓库含**互联网公开许可数据及其派生物**（OSM © OpenStreetMap contributors，ODbL 1.0；OpenCelliD，CC BY-SA 4.0）——非运营商标注数据，不触碰该红线；来源、口径与许可逐项披露于 [docs/data-sources.md](docs/data-sources.md)。
>
> ⚠️ **Compliance**: This platform has **NO production-device write capability**. AI may only operate the local lab workspace (simulation workbench); production operations must be performed by humans through company-approved external tooling. All data in this repository is **synthetic** and used solely for engineering-method validation — with **one documented exception**: the `samples/` directory holds real telecom-operator export samples provided by the project owner (containing real device IPs and operator headers, not anonymized). They are licensed for import-engineering validation **inside this private repository only** and must not be redistributed; see [samples/README.md](samples/README.md). This is a **documented, owner-authorized deviation** from the "no real network data" red line.

## 1. 项目定位

NetOps Lab 是一个本地优先（local-first）的云网资源治理与 AI 仿真工作台：把多份 Excel 资源台账与 PTP 配置脚本（设备 / 地址池 / 电路 / 端口 / 基站 / 机房 / 局站 / PTP 端口）经**用户确认的列映射**导入 SQLite，完成行级记账的标准化与确定性质量校验；再从导入资源**一键物化**虚拟拓扑工作区，让用户与 AI（经 MCP）通过**封闭的类型化操作集**（含故障注入）在仿真态上做真实状态迁移，引擎给出确定性校验结果、前后对比与三层撤销（事务回滚 / 变更集逆向 / 检查点）。出口为 HTTP API、嵌入式 React 界面（含 Cytoscape 拓扑画布）、CLI 与 **MCP 十工具**（1 检索 + 4 仿真 + 5 只读分析）。本仓库交付 **Stage 0（技术验证）+ Phase 1（资源治理）+ Phase 2（仿真工作台）**。

## 2. 当前阶段能力清单（Stage 0 + Phase 1 + Phase 2 已交付）

### 告警生命周期通知（v0.13 第一轮 R19，开发中）

- **ack/clear 处置回流**：人工确认/清除落库后，`alert.ack` / `alert.clear` 单事件载荷异步投递到已配置的 webhook（与检测运行通知同一通道、同一条 wire 纪律——有界重试/URL 脱敏/WithoutCancel 审计）；`ts` 取操作时间戳（确定性载荷），尽力而为旁路——投递失败绝不影响处置主流程。
- **投递审计表 `alert_event_deliveries`（migration v28）**：每次处置通知的重试终局一行（event CHECK 闭集 ack|clear——只回流人工边，引擎恢复清除归运行通知）；`alert_id` 刻意非 FK（v27 run_id 先例）；读面 `GET /api/alerts` 每行 `lifecycleDelivery` 三值诚实块（delivered / failed / `''` 从未通知——字段永不缺省），/alerts 行展开「处置通知」状态行。
- 三边状态机零变化、无任何超时/自动动作（ADR-0011 D3）——通知只是把已发生的人工决策回流给值班渠道。

### 告警质量治理（v0.12.0 告警质量治理版）

- **维护窗口（计划内抑制）**：`/maintenance` 页面 + API——割接/升级窗口内的新告警燃点被抑制（不落行、不写运行状态，计数入回执），窗口结束持续越限以同一「应燃 id」落燃（可关联）；在案告警生命周期照常、恢复清除永不抑制。
- **告警关联（根因/次生分诊）**：引擎按图层阶梯计算——设备告警解释电路告警（疑似次生链接最近根因）、上层依赖有告警即根因嫌疑；严格层差反对称（同层永不互标）；`?role=` 过滤 + 表格 chip + 行展开因果行 + CSV/MCP 同字段——AI 分诊直接读裁定。
- **抖动治理（重燃抑制门）**：阈值徘徊序列三个完整 fire→clear 周期（尾随 14 天证据时间、仅引擎清除计数）后，第 4 次重燃被拦（`flapHeld` 计数 + 应燃 id）；周期出窗自然老化释放、持续越限落被承诺的同一身份——churn 有界、永不永久。
- **webhook 投递硬化**：有界重试（1+2 次，退避可注入，调用方 ctx 界定）吸收接收方瞬时故障 + `webhook_deliveries` 投递审计（CHECK 闭集、URL 脱敏——查询串 token 永不落库、审计写入脱离取消）；运行历史「通知」列（已送达/投递失败/—）。
- **迁移 v24-v27 全纯增量**；三边告警状态机四轮零改动（抑制/分诊全部是机器之外的门）；505 顶层测试全绿 + 四份浏览器级回归（r15 19/19 · r16 11/11 · r17 17/17 · r18 7/7）。

### 自主运营（v0.11.0 自主运营版）

- **定时巡检调度器**：`serve --detect-interval 15m`（最小 1s，空 = 显式触发）——每个间隔自动执行一次全库检测、落库回执并触发 webhook；单飞（上一轮未完成则跳过不重叠）、优雅停机、health 暴露 enabled/rounds/nextRunAt。
- **检测运行历史**：迁移 v21 `detection_runs`——每次扫描（api/cli/scheduler，CHECK 闭集）落一条持久回执（来源/时钟区间/耗时/窗口/四计数/失败原因，失败轮也留痕）；`GET /api/detection/runs` 分页回读 + /alerts「运行历史」对话框。
- **告警分布统计**：`GET /api/alerts/stats` 闭集零填充（byState 三态 + activeBySeverity 待处置矩阵）——概览告警卡与巡检状态条统一改由该端点驱动。
- **巡检状态条**：/alerts 顶部一行——上次回执（来源徽章 · 相对时间 · 耗时 · 新增/清除，失败红字悬停原因）+ 下次自动巡检时刻 + 运行历史入口。
- **资源视角告警页签**：资源抽屉「告警」tab（本资源告警史全状态 50 条 + 页脚深链告警中心）——告警页链出到资源、资源抽屉链回告警，故障定位双向闭环；指标页签对称 CSV 导出（5 列点平面化，BOM + 30s 延迟撤销）。
- **写操作令牌网关**：`serve --auth-token T`——变更类 /api 请求需 `Authorization: Bearer`（常数时间比较），读面/health/OpenAPI/SPA 刻意开放；前端请求自动附着 + 侧栏令牌设置对话框；空 = 不启用零差异。
- **整库备份**：`netopslab backup --db --out snapshot.db`——SQLite 在线备份 API（检查点同机制）暴露为运维命令，源库读写不中断，stdout 单 JSON 对象。
- **读路径索引（迁移 v22）**：`idx_alerts_state_fired`（告警状态筛选 + fired_at 排序此前无索引可用）；metrics 序列读经审计确认已走 WITHOUT ROWID 主键——审计结论连同否决理由一并钉死在测试里。

### 运营闭环补全（v0.10.0 运营闭环补全与 API 可发现性版）

- **检测规则管理页 `/rules`**：阈值规则的配置侧入口（v0.7 起 API 完整而 UI 空白）——全量表格（启停徽章/级别/规则标签/指标/触发条件/窗口）+ 行内启停/编辑/删除；新建走封闭指标目录下拉（含中文标签与合法区间提示），编辑态指标只读（身份字段）；被告警引用的规则删除被 409 拒绝（审计保护），前端引导改用停用。
- **立即检测入口**：/alerts 与 /rules 页头「立即检测」按钮 → 确认 → 回执对话框（发现/新建/清除/写入四格统计 + 数据截止 + 告警变更明细表），完成自动刷新告警队列——检测运行从 CLI/MCP 专属变为浏览器可达。
- **告警 CSV 导出**：当前筛选的全量告警（分页拉全）→ 20 列 CSV（UTF-8 BOM、RFC4180 转义、日期文件名）——运营交接与离线分析的出站文件。
- **Webhook 通知通道**：`serve --webhook-url <URL>`（`detect --webhook-url` 同）——每次检测运行完成后 POST JSON 摘要（计数 + 封顶 50 行变更明细）；尽力而为投递（5s 界、失败仅记日志、绝不影响检测结果）。
- **OpenAPI 3 文档**：`GET /api/openapi.json`（65 端点、每操作中文摘要、version 与 /api/health 同源）——**由后端路由表生成，与实现结构性零漂移**；`/dev` 参考页实时渲染（方法徽章 + 分组目录）。AI 集成方读一个端点即可发现全部接口。

### 运营可见性（v0.9.0 运营可见性与 AI 分析闭环版）

- **告警中心页 `/alerts`**：检测告警的运营第一入口——状态筛选（触发中（默认）/已确认/已清除）+ 级别筛选 + 深链；行级最新值 vs 阈值（比较器中文）、资源跳转（→ 抽屉）；**确认需署名**（专用对话框收集必填确认人——审计字段）、清除走危险确认；告警记录只归档不删除；行展开详情含确认/清除审计线。
- **指标时序可视化**：资源抽屉「性能指标」页签——零依赖确定性 SVG 折线（y 三线网格 + x 首末日期、suspect 橙点/missing 空心、**劣化序列红色**），中文指标标签来自封闭目录；7 日光功率 -8→-24 dBm 劣化故事在 UI 直接可见。
- **影响分析页签**：资源抽屉四子模式——根因分析（impact down：根因候选 + 中文判定理由 + 受影响面）/ 影响面（up）/ 向下穿透 / 向上汇总；节点行带层族色 chip 与运行态 chip，点击穿透到对应资源抽屉。
- **运行状态视图**：抽屉属性页的运行状态行（运行/劣化/停机/维护/未知 + 最近 8 条状态历史小时间线）——台账状态（登记事实）与运行状态（观测事实）双态模型的 UI 面；概览页「触发中告警」统计卡红色高亮。
- **MCP 十工具**（见 §8）：五个只读分析工具构成 AI 根因分析闭环——`alerts_query → graph_context → graph_impact(根因) → metrics_query`。

### 仿真工作台（Phase 2，v0.6.0 治理三部曲终章与数据双向闭环版）

- **一键物化**：从导入批次生成虚拟拓扑（设备 → 虚拟设备、电路 → 链路、确定性 `eth0/eth1…` 端口分配）；演示规模 27 设备 / 40 接口 / 20 链路，物化 3–4 ms。
- **类型化操作集（含故障注入）**：10 个前向操作（增删设备/接口/链路、启停、配地址）经同一引擎路径（API/MCP/CLI 无旁路）；单事务变更集，失败零污染且留档（422 `op_failed`）。
- **确定性校验**：CHK-001..005（重复主机地址/网段重叠/孤岛设备/部分分区/未配置接口），同状态同结果；评分 100−5×错误−1×警告；运行自带拓扑快照。
- **连通性与前后对比**：up-only BFS 路径查询；两次校验运行的 diff（新增/消失问题按业务键匹配、连通性丢失/恢复对、评分差）。
- **三层撤销**：事务回滚（失败变更集）/ 变更集逆向 LIFO undo（撤销本身也是变更集，可撤销的撤销）/ 检查点（SQLite Online Backup API 快照，恢复 = 前向操作变更集，精确到状态哈希相等，no-op 检测）。
- **拓扑画布（v0.2.3 全屏工作台）**：`/lab/:id` Cytoscape 交互图——**6 种布局**（力导 / 层级树 / 同心圆 / 网格 / 环形 / 锁定手动排布）× **3 种交互模式**（选择联动表格 / 路径追踪：点两台设备高亮最短路径与跳数、未选起点时有引导横幅 / 邻域聚焦：一跳之外压暗）+ 链路标签开关 + 右下角迷你导航图（点击/拖动定位视图）+ 悬停信息条；up/down 分色、实线/虚线故障可视；布局与模式按工作台持久化。
- **画布全屏工作模式（v0.2.3 zen）**：一键让画布接管整个视口（优先浏览器原生全屏，拒绝时降级应用内全屏；Esc 逐级退出：右键菜单 → 路径/聚焦 → 全屏）；全屏中左上浮动**选中详情卡**（设备：状态/接口 up-down/IPv4；链路：两端与端口），工具带深色化；Toast 浮层不被遮盖。
- **画布工业级交互（v0.2.3）**：**右键上下文菜单**（设备：邻域聚焦/设为路径起点/表格定位/复制 ID；链路：表格定位/复制 ID；空白：适配/重置；a11y role=menu + 焦点管理 + 越界翻转）；**PNG 快照导出**（整图 2 倍分辨率不透明深色底）；**缩放百分比实时指示**；**手动排布位置持久化**（localStorage，重载不丢，切布局自动重排）；键盘快捷键（F 适配 / L 标签开关 / Esc 链级）。
- **画布工作区布局（v0.2.3）**：页头紧凑化（画布高度 1080p 达 760px）；**分栏拖拽**（30–85% 自适应 + 键盘 ±2% + a11y separator + 持久化）；**右栏折叠**（画布占满工作区宽度）；画布非全屏 948×760 → 全屏 1920×1080（约 2.9 倍面积）。
- **地理视图（v0.3.0 三视图切换；v0.6.1 地图修复轮重制底图与图层）**：画布「拓扑图 / 2D 地图 / 3D 地图」分段切换（按工作台持久化）；导入设备表时自动识别**经度/纬度/经纬度列**（组合列按中国坐标边界启发式拆分），物化后设备携带 geoLat/geoLng；2D 俯视——设备按真实经纬度落位（状态色圆点 + 编码牌）、链路 up 蓝实线 / down 红虚线 marching-ants；3D 斜视 58°——**离线 DEM 真实地形起伏**（AWS Terrain Tiles/Mapzen 高程数据 97 瓦片随二进制内嵌，图层面板「地形高程」开关，2D 亦呈山体阴影）+ 设备按连接数分高的示意柱体（柱高物理钳制 [60,4000] 米）、右键拖拽旋转环视；2D 同城密集设备自动**聚簇**为计数气泡（点击放大展开，标签不再堆叠）；设备点击/选中与右侧表格双向联动。**底图四模式**（图层面板切换，偏好本地持久化）：**离线简洁**（v0.6.4 默认：浅色类标准地图——森林绿、河网蓝、地名 + 交通区划，**无商铺 POI 标注**，内网开箱即用）、高德街道（在线，含 POI 标注）、高德卫星（影像 + 路网标注叠加）、ESRI 深色极简；高德模式下业务坐标自动 WGS84→GCJ-02 投影。**离线叠加图层**（© OpenStreetMap contributors，黑龙江本地数据随二进制内嵌）：**自然要素**（森林/水系/草地农田/地名，29,449 条 + 17,615 地名，双坐标变体任意底图可用，默认开）、行政区划（默认开）、公路（默认开）、铁路（默认关——不干扰主观看，可一键开）；瓦片持续失败自动降级离线底图并保留叠加。**3D 自然呈现**：地表覆盖贴在离线 DEM 真实地形上——河流随山谷走势，森林给山脉上绿色。地形高程数据 © Mapzen / AWS Open Data（SRTM·GMTED，terrarium 编码，z4–z8 内嵌）。maplibre-gl 懒加载独立 chunk。设计决策详见 [docs/adr/0010-map-basemap-modes.md](docs/adr/0010-map-basemap-modes.md) 与 [docs/adr/0011-ops-capabilities.md](docs/adr/0011-ops-capabilities.md)。演示数据：`testdata/geodevices.xlsx`（黑龙江哈尔滨→漠河北南干线 + 地震局 10M 长专线，坐标为演示近似值），一键脚本 `scripts/demo-geo-lab.sh`。
- **真实公开数据演示（v0.8.0）**：图层面板新增「真实网络数据」分组——**光缆干线**（7 条走廊：哈大齐/哈牡/哈佳/佳牡/齐加/加漠/哈伊，© OpenStreetMap 真实公路/铁路几何按线路名选取——绥满高速 G10·滨洲线·嫩林线·京漠线 G111 等，标注锚点链里程，铁路走廊细虚线分层，默认开）与**无线基站**（OpenCelliD 真实小区 1,198 站/1,851 小区，运营商配色移动蓝·联通红·电信绿·广电紫，点径随小区数，默认关一键开；数据 © OpenCelliD CC BY-SA 4.0）。配套**真实数据集** `testdata/realdata/`（32 本工作簿 1,991 行全 33 类型：真实局站 83（OSM node id 身份）/真实基站 1,198（主小区 CGI 身份）/真实光缆 A-Z 端与锚点链测地里程，模拟台账细节逐字段披露于 `docs/data-sources.md` + `manifest.json`）与一键脚本 **`scripts/demo-real.sh`**（导入 → PTP 脚本 → 7 日指标摄入 → 哈大齐劣化告警 → 物化 → 光缆置 down → 影响面 100 节点跨层波及 circuit/protection_group/customer/service/leased_line）。数据许可与嵌入边界裁决见 [docs/adr/0015-real-public-data.md](docs/adr/0015-real-public-data.md)。

- **字段厚度与图连接（v0.8.1，issues #40/#42/#45）**：真实数据集重制为 32 本工作簿 1,991 行——基站表补齐真实表 17 列（环/链/归属A/电联共享/坐标…）且 1,198 站全部有 `uplink_to`（坐标最近邻派生）与 `serves` 直连边；电路 32 条三族（工作台 32 链路/64 接口）；保护组/PTP 端口补齐（33 类仅 in_vpn 无数据——低优先级主动不做）；各台账状态列合法枚举全覆盖（无状态资源 98%→8.3%，**问题清单全清零、质量评分 100**）；`resources` 表 geo 坐标 REAL 列化（migration v20）+ CLI `query nearest` 空间查询。**地图↔台账联动**（W31）：2D/3D 地图上点击任一真实基站 → 跳转其资源详情抽屉（属性/关系/穿透分析——图层要素携带站编码=自然键）。
- **port 资源类型（v0.3.1 真实表格导入；migration v8 换稳定键）**：真实电信端口工作簿（端口流量监控表 / 端口资源表两种表头形态）可直接导入——`device_name + port_name` 复合自然键（`|` 拼接落库，键的设备侧自 migration v8 起锚定设备名称而非 IP：设备重划 IP 不再分裂资源，存量库升级语义见 [docs/data-model.md](docs/data-model.md) §11）、逐字重复表头行自动跳过多段导出拼接、带宽 0 合法入库；样例 `samples/telecom-port-multi-header.xlsx`（项目所有者在需求会话中提供的真实电信运营导出，已授权入库、仅限本私有仓库使用——来源与授权见 [samples/README.md](samples/README.md)；自然键稳定性分析见 [docs/natural-keys.md](docs/natural-keys.md)）；3D 地图默认视野按全部数据点的包围盒几何直解自适应（修「3D 地图过大」）。
- **base_station 资源类型与条件自然键（v0.4，migration v9）**：真实无线基站工作簿可直接导入（14 真实业务列，无地市列；样例 `samples/telecom-base-station.xlsx`，来源与授权见 [samples/README.md](samples/README.md)）——主自然键为**基站编码**（非必填，真实表编码列常为空），编码缺失时键退化为 `(地市, 无线基站名称)` 兜底拼接、跳过空值，行照常导入并记 **W004**（身份键退化，warning 级治理 issue）；E001 判重与幂等 skip 都在解析后的最终键上（编码行与名称兜底行天然异键）；基站IP 值带 /30 掩码按 string 值列入库（IP 属可变属性，永不作身份）；键策略裁决见 [docs/natural-keys.md](docs/natural-keys.md) §4、行为细节见 [docs/import-and-validation.md](docs/import-and-validation.md) §12、DDL v9 见 [docs/data-model.md](docs/data-model.md) §12。
- **机房 / 局站资源类型与 belongs_to 跨表关联（v0.4，migration v10）**：真实机房局站表（8 真实业务列：地市/区县/设备名称/IP地址/机房ID/机房名称/局站ID/局站名称；样例 `samples/telecom-site-machine-room.xlsx`，来源与授权见 [samples/README.md](samples/README.md)）**同一张 sheet 承载两类实体**——先按局站类型导入（键 = 局站ID，12 位数字编码），再按机房类型导入同一 sheet（键 = 机房ID，17 位数字编码，机房 spec 的 局站ID 列映射为可选 ref 字段）：局站先入库即自动派生 `machine_room --belongs_to--> site` 关系，导入顺序由 E003 语义钉死；两类 ID 均为 KindString（前导零即身份，绝不数值化）；机房名称/局站名称/设备名称为昵称（展示名/属性），IP 地址为可变属性；键策略裁决见 [docs/natural-keys.md](docs/natural-keys.md) §4、行为细节见 [docs/import-and-validation.md](docs/import-and-validation.md) §13、DDL v10 见 [docs/data-model.md](docs/data-model.md) §13。
- **MCP 五工具**：`resource_search`（只读）+ `lab_apply_ops` / `lab_run_checks` / `lab_diff_undo` / `lab_checkpoint`——AI 改仿真态的唯一通道；`restore_*` 内部操作码三边界拒绝；**无建删工作区工具**。
- **CLI `lab` 子命令树**：workspace/topology/path/apply/checks/diff/undo/checkpoint，全部单一 JSON 输出。
- **仿真报告**：`GET /api/lab/report`（HTML/JSON）：引擎版本、评分、发现、变更集史、检查点、固定免责声明。

### 资源治理（Stage 0 + Phase 1）

- **Excel 导入管线**：多工作表解析、列映射建议（确定性建议引擎，置信度 1.0/0.9/0.75）、用户确认后执行；导入全程单事务，任何内部错误整体回滚（零半写）。
- **行级记账**：每一数据行必有 `imported / skipped / failed` 之一；跳过即幂等（重复导入 0 新增），失败行携带规则码与消息。
- **数据质量校验**：13 条版本化规则（E001–E006 / W001–W006 / I001），行级规则随导入触发，集合级规则（网段重叠、地址重复、孤岛设备）导入后自动重算且幂等；每条问题携带来源证据（工作簿/Sheet/行号）或显式 `applicationGenerated` 标记。
- **资源检索与统计**：类型/状态/关键词过滤、分页、资源详情（原始行回溯）、全库统计。
- **关系查询**：assigned_to / a_end / z_end / belongs_to 四类关系，1 跳与 2 跳 BFS 子图（节点 + 边）。
- **拓扑可达性检查**（Stage 0）：按设备编码的两端连通性判定 + 路径（如 DEV-001→DEV-003 路径长 2）。
- **质量报告**：JSON 与独立 HTML 两版（评分 = 100 − 5×错误 − 1×警告，下限 0），支持全局与单批次两个口径。
- **批次管理**：批次列表/详情/行结果查询；已完成批次可回滚（被其他批次引用时报 409 `batch_in_use`）。
- **site / machine_room 资源类型与 belongs_to 关联（v0.4，migration v10）**：真实机房局站工作簿（样例 `samples/telecom-site-machine-room.xlsx`）同一张 sheet 双读法——先局站后机房，机房ID/局站ID 数字编码作键（前导零即身份），机房可选 局站ID ref 派生 `belongs_to` 关系（E003 钉死导入顺序）；见 [docs/import-and-validation.md](docs/import-and-validation.md) §13、[docs/data-model.md](docs/data-model.md) §13。
- **属性变更历史与人工确认（v0.4 W4-D，migration v11）**：同键再导不再静默丢弃属性差异——diff 逐字段记为**待确认变更**（`resource_attribute_changes`，W005 issue，行仍 skip 幂等不变），人工经 HTTP API / CLI（`changes` 子命令）apply 后属性才真正生效（旧值校验 + 同事务写入），dismiss 可忽略；v0.6.0 W13-D 起 ref 类字段（机房 site_id、地址池归属设备、电路 a/z 端）的 apply 在同一事务内把对应派生关系改挂到新目标（目标解析不到 → 409 `ref_target_missing`；无边的引用字段只改属性、绝不新建无谱系关系，建边仍需重导）；回滚只删本批 pending，人工决策审计线保留；见 [docs/import-and-validation.md](docs/import-and-validation.md) §14、[docs/data-model.md](docs/data-model.md) §14。
- **费用-资源分级关联（#63，#58 阶段 3，migration v30）**：费用编码 ↔ 站点类资源（site/machine_room/base_station）三级匹配——**exact** 逐字节自动确认 / **rule** 命名规则（fold 全角折叠、zero_pad 前导零）自动确认 + 规则留痕 / **fuzzy** 相似度 ≥0.8 出 pending 提案，人工 confirm/dismiss（W005/W006 教义——身份关联绝不自动合并）；**人类确认优先于自动扫描**（scan 永不覆写 human 行；数据面消失的编码其自动链接被 dropout 清扫，人工决策寿命超越数据）；编码全集来自活跃 Parquet 的**纯 Go 列投影读取**（默认构建可关联）；HTTP `POST /api/fees/links/scan` + `GET /api/fees/links` + `confirm|dismiss`，CLI `fees link-scan` / `fees links list|confirm|dismiss`；DDL v30 见 [docs/data-model.md](docs/data-model.md) §19。
- **费用分析层（#58/#60/#61 阶段 1/2，migration v29）**：电费/铁塔租费/修理费/综维数据的两层存储落地——**状态层 SQLite**（fee_batches 状态机：幂等键 = 哈希×类型×账期×映射的部分唯一索引、failed/rolled_back 不占键、重试永远干净）+ **快照层**（内容寻址不可变，行号级证据链）+ **分析层 Parquet 分区大表**（`fee_type/year/month` hive 分区，同键多文件**批次共存不合并**——重复结算检测查它们而不是丢它们）。写入纯 Go（全平台可构建）；查询引擎 DuckDB（go-duckdb，`-tags duckdb` CGO 构建，#59 实测 ×2.5–×53）；CLI `netopslab fees import|list|rollback|query`（query 预设 totals/duplicates，固定参数化——调用方传不了 SQL）；设计全貌见 [docs/fees-layer.md](docs/fees-layer.md)、DDL v29 见 [docs/data-model.md](docs/data-model.md) §18。
- **疑似改名检测与身份合并人工流（v0.5 W6-D，migration v12）**：名称类键类型（ip_pool / base_station / port）的新键导入行与「旧键已从源消失」的库内资源属性全等时，行照常按新资源导入并发 **W006** + pending 改名提案（`rename_proposals`；检测五条件保守判定，绝不自动合并）；人工经 HTTP API / CLI（`renames` 子命令）apply 合并身份——关系改挂旧资源、键分量属性同步、旧资源换键而代理键 `res_x` 与 SourceRef 不动——或 reject 维持独立；回滚不对称语义与 v11 一致（删本批 pending、留终态审计线）；见 [docs/import-and-validation.md](docs/import-and-validation.md) §16、[docs/data-model.md](docs/data-model.md) §15。
- **来源引用版本化（v0.5 W7-D，零迁移）**：skip 即谱系版本——同键再导入（纯 skip 与 W005 diff-skip）对每批每实体追加一条 `source_refs`（行语义零变化；E001 批内重复不追加），资源详情 API 的 `sourceRefs[]` 按 rowid 版本序展示全史（创建批次在前、`raw` 记录该批工作簿原话），回滚随批消失（`batch_id ON DELETE CASCADE`）；见 [docs/import-and-validation.md](docs/import-and-validation.md) §17。
- **PTP 配置脚本导入通道（v0.5 W8-D，migration v13）**：第 4 类真实电信材料——中兴 VRP 风格 PTP 配置脚本（`.txt`/`.cfg`）经确定性解析器产出「PTP配置」虚拟 sheet，**完整复用既有 analyze → 映射确认 → 导入执行管线**（全规则/幂等/回滚/来源版本化/改名检测天然适用，无旁路）；新资源类型 `ptp_port`（第 8 类，复合自然键 `设备型号|接口名称`，23 字段，模型级模板身份诚实声明）；解析失败 422 带行号；样例 `samples/ptp-config-script.txt`；见 [docs/import-and-validation.md](docs/import-and-validation.md) §18、[docs/data-model.md](docs/data-model.md) §16。
- **资源导出通道（v0.6.0 W12-D）**：`GET /api/exports/resources?types=` / `netopslab export --db --out [--types]` / 资源页「导出资源」按钮——把当前资源清单导出为与导入工作簿同构的 xlsx（每类型一 sheet、表头 = 字段 Label、行 = 属性快照，含 W005/W006 apply 后状态），**字节级确定性**（同库同参数两次导出逐字节一致）；**回源幂等**：导出文件直接走既有 analyze → suggest → import 管线导入全新库，资源集合与关系集合（ref 重新派生）等价；MCP 零新增；见 [docs/api.md](docs/api.md) 导出节 + [docs/import-and-validation.md](docs/import-and-validation.md) §19。
- **治理总览看板（v0.6.0 W14-D）**：`GET /api/governance/summary` / `netopslab query --db <path> governance` / 概览页「治理总览」区——把待确认队列（属性变更 / 疑似改名）、决策审计计数、问题分布（按严重级 + 按规则 Top 10）与资源/关系/批次形态汇成一屏的只读聚合；全部计数走 SQL COUNT（无全表拉取），除 generatedAt 外全字段确定性（同库两次调用逐字段相等），空库恒全零；MCP 零新增；见 [docs/api.md](docs/api.md) 治理总览节。
- **schema 动态发现与 uploads 垃圾回收（v0.4 W5-D 发布轮）**：`GET /api/schema` 返回 `mapping.Specs` 的忠实投影（v0.7.0 W21 起 33 类资源的字段/键位/枚举/ref/同义词/层次/专业域 + 13 条规则目录，机械生成禁止手抄，仅 HTTP 不进 MCP）
；`serve` 启动与 CLI `netopslab gc` 清理未被任何批次引用的孤儿上传文件（任何状态的批次都算引用）；前端构建 manualChunks 分包（主 chunk 795 → 187 kB）。
- **v0.7.0 里程碑收口（W25）**：`leased_line.customer_code` 补齐（真实专线表「客户名称」列 → subscribed_by 直连客户——「某客户有多少条专线」可答）；浏览器级 UI 验收（通用资源页 33 类型筛选/chip/抽屉 + 向导 33 选项，`scripts/w25_acceptance.sh` 24 断言全过）；四层模型端到端实测（层次穿透 L6→L2 / 业务影响含客户与专线 / 保护结构 / 客户聚合）。
- **无人值守导入（v0.7.0 W24，migration v19）**：映射模板持久化（§12-B7 方案 A）——表头指纹（sheet 名 + 规范化表头序列 sha256，**列序敏感**）命中即以已确认映射自动执行（`POST /api/imports?auto=1` / `netopslab import --auto`），任一表头变更 → 指纹变化 → 拒绝执行并点名 sheet（**表头变更感知，从不静默**），批次回落 `awaiting_mapping` 人工兜底；模板捕获一键保存（`POST /api/imports/{id}/template` / `netopslab templates save`）、回滚不对称（模板是确认产物，批次回滚不删模板）；七日无人值守测试钉死全旅程。
- **异常检测与告警（v0.7.0 W23，ADR-0011 D2/D3）**：检测规则配置化（`detection_rules` 表 + 5 条种子规则，不改代码新增阈值规则）；纯函数判定引擎（末尾连续 window 点越限去抖、ts 全来自数据、同数据同规则同结果）；告警状态机三边（firing→acked 人工 / firing→cleared 恢复 / acked→cleared——无自动升级无超时关闭）；firing 告警联动 run_state（error→down / warning→degraded，source=alert）；HTTP 六端点 + CLI `netopslab detect`；MCP 冻结不动（AI 经 HTTP 消费告警）。
- **数据模型 v1.0 类型扩容（v0.7.0 W21）**：资源类型 8→33（新增光缆网/传输承载/逻辑/业务/接入层 25 类——光缆/纤芯/ODF/波道/时隙/环网/客户/业务/专线/OLT 等，migration v15+v16）；关系类型 4→29（含 `carried_by`/`uses_fiber`/`provided_by`/`serves`/`uplink_to`/`fiber_of` 等，方向语义注册表可执行门禁）；四点一致性测试（domain 枚举 ↔ SQL CHECK ↔ `/api/schema` ↔ web 镜像）防漂移；B8 键规范化（trim+全角半角+大写，前导零保留）；既有数据保守迁移零改动（B4 守卫测试）。
- **HTTP API**：`/api/...` 全套 REST 风格接口（错误统一 `{"error":{"code","message"}}` 信封）+ 17 条 §7 仿真路由。
- **嵌入式 React 前端**：概览 / 数据导入（三步向导）/ 资源浏览（抽屉 + 二跳）/ 问题清单 / 质量报告 / 工作台列表 / 拓扑工作台 中文界面，`go:embed` 打进单二进制。
- **结构化 CLI**：`analyze / import / export / validate / report / query / changes / renames / serve / mcp / gc / testdata / lab`，成功输出单一 JSON 对象，退出码 0/1/2（TeleAgent 的 CLI 回退路径）。`query` 子命令含图查询四通道（`traverse / impact / context / nearest`，v0.8.1）——与 HTTP 端点同一引擎同一装配、输出逐字节一致（跨通道 e2e 钉死），脚本化巡检无需先 `serve`（issue #41）。
- **工程化**：**331 个 Go 顶层测试**（20 个含测试的包；`go test -list '.*'` 顶层函数口径）全绿；gofmt/vet 干净；确定性导入/校验/仿真；可重复的一键演示脚本（demo.sh + demo-lab.sh）；**组合根单一装配**（v0.5 W9-D：`internal/services` 是服务装配唯一事实源，app/HTTP API/MCP 共享同一实例，指针级测试钉死）。

## 3. 快速开始

前置要求：**Go 1.27+**（编译后端；无 CGO，纯 Go）、**Node 18+**（构建前端；本仓库验证于 Node 24 / npm 11）、python3（演示脚本用）。

```bash
# 1) 测试
make test            # go test -count=1 -timeout 15m ./...（434 个顶层测试 / 23 包 ok，v0.8.1）

# 2) 构建前端（产物嵌入 internal/webui/dist，供 go:embed 使用）
make web             # cd web && npm ci && npm run build

# 3) 构建单二进制（约 28MB，含嵌入式前端）
make build           # bin/netopslab

# 4) 一键演示：资源治理（全新临时库：导入×3 → 校验 → 报告 → 统计，< 1 秒）
./scripts/demo.sh    # 或 make demo

# 5) 一键演示：仿真工作台（故障注入故事，见下）
./scripts/demo-lab.sh    # 或 make demo-lab

# 6) 启动服务并打开界面
./bin/netopslab serve --addr :8080 --db ./data/netops.db
#    浏览器打开 http://localhost:8080（仿真工作台入口：/lab）
#    可选：检测运行通知——加 --webhook-url http://<接收端>，
#    每次 POST /api/detection/run 或 `netopslab detect` 完成后向该 URL
#    POST 一份 JSON 摘要（event=detection.run + 计数 + 告警变更明细，
#    最多 50 行）。投递尽力而为：失败只记 warn 日志，绝不影响检测结果。
```

说明：`make web` 会把 `web/` 构建到 `internal/webui/dist`（该目录不提交，仅保留 `.gitkeep` 占位）；`make build` 嵌入的是**当时 dist 里的内容**——先 `make web` 再 `make build` 才会得到带真实前端的二进制。`make all` = `web + build`。

### Windows 一键运行（PowerShell）

上面的 `make` 与 `scripts/*.sh` 需要 bash。**Windows 用户直接用 `run.ps1`**：

```powershell
.\run.ps1              # 全流程 + 自动开浏览器（http://127.0.0.1:8080）
.\run.ps1 -Rebuild     # 强制重建数据库
.\run.ps1 -Port 9000   # 换端口
.\run.ps1 -SkipWeb -SkipBuild   # 跳过构建（都最新时）
```

脚本一步完成：**构建前端 → 构建二进制 → 按依赖序导入 32 工作簿 → 7 日性能
→ 检测告警 → 物化工作台 → 启动服务 → 开浏览器**。
数据库已存在时自动跳过导入（幂等）。

> ⚠️ 顺序不能颠倒：`internal/webui/dist` 被 `go:embed` 在**构建时**嵌入二进制。
> 只重建前端而不重建二进制 → 新增地图图层**静默缺失**
> （`GET /mapdata/*.geojson.gz` 返回 `200 text/html`，控制台无报错）。
> `run.ps1` 按正确顺序执行，并在启动后**自检图层 Content-Type**。

完整参数与排错见 [docs/run-windows.md](docs/run-windows.md)；`make run-win` 等价于 `.\run.ps1`。

### 仿真工作台快速开始（CLI 路径）

无浏览器环境时，故障注入故事可用 CLI 完整走通（即 `scripts/demo-lab.sh` 的内核）：

```bash
DB=./data/netops.db
# 导入三本工作簿（同上 demo.sh 流程）后：
netopslab lab workspace create --db $DB --name 我的实验台 --all-batches
#   → {27 设备/40 接口/20 链路，workspaceHash aea27501…}，记下 workspaceId
netopslab lab checks  --db $DB --workspace <wsId> --run          # 基线：评分 95（0/5/36）
netopslab lab path    --db $DB --workspace <wsId> --from DEV-001 --to DEV-003
#   → 可达 2 跳（DEV-001→DEV-002→DEV-003）
# 写 ops.json（同地址配到 if-1/if-3 + ln-1 down）后：
netopslab lab apply   --db $DB --workspace <wsId> --ops ops.json   # cs-1 + 自动校验 89
netopslab lab diff    --db $DB --workspace <wsId> --from run-1 --to run-2
#   → 评分差 −6，连通性丢失 20 对
netopslab lab undo    --db $DB --workspace <wsId>               # 哈希回到基线
netopslab lab checkpoint --db $DB --workspace <wsId> --create --label 基线
netopslab lab checkpoint --db $DB --workspace <wsId> --restore --id cp-1
```

浏览器路径：`serve` 后打开 `http://localhost:8080` → 侧栏「仿真工作台 → 工作台」→ 新建工作台（默认勾选全部已完成批次）→ 进入拓扑工作台：左侧 Cytoscape 画布，右侧六个标签页，页头六个动作（运行校验 / 执行操作 / 撤销最近变更 / 创建检查点 / 前后对比 / 导出报告）。

## 4. 10 分钟演示路径（浏览器操作）

前提：`make web && make build`，然后 `./bin/netopslab serve`（默认 `:8080`，数据库 `./data/netops.db`），浏览器打开 http://localhost:8080。

1. **数据导入（/import）** — 依次上传 `testdata/devices.xlsx`、`testdata/ippools.xlsx`、`testdata/circuits.xlsx`（顺序即依赖顺序：设备 → 地址池 → 电路）。
   - 第 ① 步上传工作簿（.xlsx，≤10MB）；
   - 第 ② 步确认列映射：系统已按表头建议预填（含置信度徽章、必填字段标记），直接“确认导入”；
   - 第 ③ 步查看执行结果：27/0/3、16/0/4、21/0/4（总/成功/跳过/失败），失败行可按状态过滤并查看规则码、消息与来源行号。
2. **问题清单（/issues）** — 按严重级别/规则码/批次过滤；每条问题显示 severity 徽章、证据来源（工作簿/Sheet/行号）或“系统生成”标记。集合级规则示例：E005 网段重叠（POOL-15/POOL-16）、E006 地址重复（10.10.10.5）、I001 孤岛设备（DEV-030）。
3. **质量报告（/report）** — 选择批次或全局口径，页面内嵌 HTML 报告预览，可下载 HTML / JSON；顶部有显著的合成数据免责声明横幅；全局口径评分 32（E13/W3/I1），设备单批次口径评分 74。
4. **资源浏览（/resources）** — 类型/状态过滤 + 关键词搜索（如 `DEV-001`）；点击行打开右侧抽屉：属性 JSON、来源引用（工作簿/Sheet/行号 + 原始行）、关系页签（1 跳列表，“查看二跳”展开 2 跳子图）。
5. **概览（/）** — 资源/问题/评分统计卡片、最近批次表。
6. **回滚演示（可选）** — 重复上传同一工作簿可见“跳过”幂等行为；在批次管理中回滚 circuits 批次后统计中电路清零，尝试回滚 devices 批次将得到 409（被地址池/电路引用）。
7. **仿真工作台（/lab）** — 侧栏「仿真工作台 → 工作台」→ 新建（默认全部批次）→ 拓扑工作台：画布上关断一条链路（表格行内[停用]）或配一个重复地址（[配置地址]）→ 右侧「检查结果」看 CHK 发现与评分变化 → 「前后对比」看连通性丢失对 → 「变更集」里撤销（哈希回到基线）→ 「检查点」创建/恢复。与 `./scripts/demo-lab.sh` 同一故事。

无浏览器环境时，等价演示：`./scripts/demo.sh`（资源治理）与 `./scripts/demo-lab.sh`（仿真工作台）。

## 5. 技术栈

| 层 | 技术 | 说明 |
| --- | --- | --- |
| 后端 | Go 1.27（无 CGO） | 仅 `net/http` stdlib 路由；无 Web 框架 |
| 存储 | SQLite（modernc.org/sqlite v1.60.1） | 纯 Go 驱动；WAL + busy_timeout + foreign_keys；Online Backup API（检查点） |
| Excel | xuri/excelize/v2 v2.11.0 | 多 Sheet 解析、类型化单元格读取 |
| MCP | modelcontextprotocol/go-sdk v1.8.0 | stdio JSON-RPC；5 工具（1 只读 + 4 仿真） |
| 前端 | React 18 + TypeScript 5.6（strict）+ Vite 6 | 手写设计系统 CSS，无 UI 框架 |
| 拓扑图 | cytoscape 3.32.0（精确版本，无插件） | /lab 画布；单 chunk 嵌入（ADR-0009） |
| 嵌入 | go:embed all:dist | 前端产物打进二进制，单文件分发 |
| 测试 | go test（table-driven） | 434 个顶层测试 / 23 包（`go test -list '.*'` 口径，v0.8.1）；含 CLI 子进程 E2E、MCP stdio 回环、确定性双库比对、跨通道图查询一致 |

## 6. 仓库结构

```
netops-lab/
├── cmd/netopslab/        # 单二进制入口：serve|analyze|import|validate|report|query|changes|renames|gc|lab|mcp|testdata
├── cmd/testdatagen/      # 生成 3 本合成工作簿（确定性）
├── internal/
│   ├── domain/           # 实体、枚举、错误、规则码 + 操作码/检查码（零依赖）
│   ├── repo/             # SQLite：Open/迁移 v1–v5 + 各聚合仓储 + Online Backup
│   ├── workbook/         # excelize 解析：Sheet/表头/行/空行
│   ├── mapping/          # 目标 schema、映射模型、确定性建议引擎
│   ├── importer/         # 分析/执行/回滚；行级记账；幂等
│   ├── validation/       # 集合级规则引擎（E005/E006/I001）
│   ├── sim/              # 仿真引擎：类型化操作/逆操作、物化、CHK 校验、
│   │                     #   连通性、状态哈希、diff、undo、检查点（独占写路径）
│   ├── topology/         # 关系图 + BFS 可达性
│   ├── query/            # 检索/统计/关系子图/拓扑检查
│   ├── report/           # 质量报告 JSON+HTML；仿真工作台报告
│   ├── mcpserver/        # MCP stdio 服务器（resource_search + 4 个 lab 工具）
│   ├── api/              # HTTP 处理器、错误信封、中间件 + §7 仿真路由
│   ├── app/              # 组装（配置/日志/DB/服务）+ CLI 实现（含 lab 子命令）
│   └── webui/            # go:embed 前端产物（dist/.gitkeep 占位）
├── web/                  # React 源码（npm；含 components/lab 拓扑画布与实验室组件；
│                         #   构建产物出 internal/webui/dist）
├── testdata/             # 合成工作簿（devices/ippools/circuits + geodevices/geocircuits）+ expected-summary.json
│   └── realdata/         # v0.8.1 真实公开数据集（32 工作簿 1,991 行 + manifest.json，见 docs/data-sources.md）
├── samples/              # 真实电信运营导出样例（唯一合规例外，见 samples/README.md）
├── scripts/demo.sh       # 一键演示（资源治理）
├── scripts/demo-lab.sh   # 一键演示（仿真工作台/故障注入）
├── scripts/demo-real.sh  # 一键演示（v0.8.0 真实公开数据全链路：导入→指标→告警→影响分析）
│   └── realdata/         # 真实数据提取/工作簿/指标生成管线（extract / workbooks / metrics）
├── docs/                 # 文档（见下方索引）
└── Makefile
```

## 7. 数据质量与仿真校验规则目录（rule_version 1.0.0）

**资源导入规则**（E/W/I）：

| 规则码 | 名称（中文） | 严重级别 | 阶段 | 触发条件 |
| --- | --- | --- | --- | --- |
| E001 | 重复键 | 错误 | 行级 | 自然键与本批次其他行或库中既有资源重复（重复导入 → 行状态 `skipped`） |
| E002 | 必填字段缺失 | 错误 | 行级 | 必填字段为空或未映射 |
| E003 | 引用无效 | 错误 | 行级 | 引用字段非空但目标资源不存在（批内 + 全库检查） |
| E004 | 格式无效 | 错误 | 行级 | ipv4/cidr/int/enum 解析失败或超界（如 VLAN 1–4094） |
| E005 | 网段重叠 | 错误 | 集合级 | 两个地址池 CIDR 重叠（证据含重叠网段） |
| E006 | 地址重复使用 | 错误 | 集合级 | 同一 mgmt_ip / gateway 被 >1 个资源使用 |
| W001 | 孤儿记录 | 警告 | 行级 | 可选关系字段留空（assigned_device） |
| W002 | 状态值未知 | 警告 | 行级 | status 非空但不在枚举内（行仍导入，状态丢弃） |
| W003 | 电路自环 | 警告 | 行级 | a_end == z_end（两端相同） |
| W004 | 身份键退化 | 警告 | 行级 | 条件自然键类型的主键字段值空，键退化为兜底字段拼接（行仍导入；v0.4 W2-D） |
| W005 | 属性变更待确认 | 警告 | 行级 | 同键再导命中库内资源且属性值不同（行=skipped 不改库；diff 逐字段记 pending 提案，人工 apply 后生效；v0.4 W4-D） |
| W006 | 疑似改名待确认 | 警告 | 行级 | 名称类键类型新键行与旧键消失资源属性全等（行照常导入；记 pending 改名提案，人工 apply 合并身份 / reject 维持独立；v0.5 W6-D） |
| I001 | 孤岛设备 | 提示 | 集合级 | 设备零关系（未关联任何地址池/电路） |

**仿真工作台校验**（CHK，状态函数、同状态同结果）：

| 检查码 | 名称（中文） | 严重级别 | 触发条件 |
| --- | --- | --- | --- |
| CHK-001 | 重复主机地址 | 错误 | ≥2 个 up 接口（设备 up）配置同一 IPv4 地址 |
| CHK-002 | 网段重叠 | 错误 | 两个已配置 up 接口的网段重叠（identical/contains/partial；同址对旧 CHK-001） |
| CHK-003 | 孤岛设备 | 警告 | up 设备无任何 up 链路（或无接口） |
| CHK-004 | 部分分区 | 提示 | up 设备分成 ≥2 个连通分量（最大分量之外每个 ≥2 成员分量一条） |
| CHK-005 | 未配置接口 | 提示 | up 接口（设备 up）无 IPv4 地址 |

行级 E 规则 → 该行 `failed`（不落库）；W/I 规则 → 行仍导入并记录问题。两套评分公式一致：`100 − 5×错误数 − 1×警告数`（下限 0）。详见 [docs/import-and-validation.md](docs/import-and-validation.md) 与 [docs/simulation.md](docs/simulation.md)。

## 8. CLI 与 MCP

**CLI**（所有成功输出 = stdout 单一 JSON 对象；退出码 0 成功 / 1 失败 / 2 用法错误）：

```bash
netopslab analyze  --file devices.xlsx                      # 离线解析 + 映射建议（不写库）
netopslab import   --file devices.xlsx --map map.json --db ./data/netops.db
netopslab import   --file devices.xlsx --db ./data/netops.db --auto     # 无人值守：表头指纹命中模板即自动执行（v0.7 W24）
netopslab templates --db ./data/netops.db save --batch <id> [--name <n>]  # 一键保存已确认映射为模板（v0.7 W24；list/delete 同树）
netopslab ingest   --file perf.xlsx --map metric-map.json --db ./data/netops.db   # 时序指标导入（v0.7 W20，upsert 幂等）
netopslab ingest   --file perf.xlsx --suggest               # 只输出建议指标映射 JSON（不写库）
netopslab synthdata --out ./synth --devices 20 --days 30 --scenario degrade --seed 42   # 确定性合成数据（同 seed 字节级相同）
netopslab export   --db ./data/netops.db --out resources.xlsx [--types device,ip_pool]  # 导出当前资源清单（xlsx，可回源导入）
netopslab validate --db ./data/netops.db [--batch <id>]
netopslab report   --db ./data/netops.db --format html|json [--out 文件]  # --out 时 stdout 输出单 JSON 摘要（v0.6.2）
netopslab query    --db ./data/netops.db resources|stats|relations|governance …       # 治理总览聚合（governance 输出单 JSON）
netopslab lab      --db ./data/netops.db workspace create --name <n> [--all-batches]   # 仿真工作台（输出含 db 绝对路径字段，可与 serve 绑定比对）
netopslab lab      … topology|path|apply|checks|diff|undo|checkpoint                   # 见 docs/teleagent-integration.md §4.2
netopslab serve    --addr :8080 --db ./data/netops.db
netopslab mcp      [--db ./data/netops.db]                  # 或环境变量 NETOPS_DB
netopslab gc       --db ./data/netops.db --data ./data       # 手动清理孤儿上传（serve 启动时也会跑）
netopslab testdata --out testdata
```

**MCP**：`netopslab mcp` 启动 stdio JSON-RPC 服务器（server name `netops-lab`，version `v0.15.0`），注册**恰好 11 个工具**（5 资源/仿真域 + 5 图分析域 + 1 费用风险域，TestMCPToolRegistry 钉死）：

| 工具 | 类别 | 职责 |
| --- | --- | --- |
| `resource_search` | 只读 | 检索导入资源（P1 不变：q/type/limit/offset → total + items[]，每项含来源） |
| `lab_apply_ops` | 仿真写 | 类型化操作（含故障注入）→ 变更集 + 自动校验 |
| `lab_run_checks` | 只读 | 立即执行五项校验，返回发现行 |
| `lab_diff_undo` | 读/写 | 两次校验运行的前后对比 / LIFO 撤销（mode 判别） |
| `lab_checkpoint` | 仿真写 | 检查点 create / list / restore |
| `graph_traverse` | 只读 | 图遍历（BFS 可达/路径） |
| `graph_impact` | 只读 | 影响面分析（资源闭包） |
| `graph_context` | 只读 | 资源关系上下文 |
| `alerts_query` | 只读 | 质量告警查询（含根因/次生关联裁定，AI 分诊证据通道） |
| `metrics_query` | 只读 | 指标时序查询（ts/value/quality，劣化趋势证据通道） |
| `fee_risks` | 只读 | 费用风险六场景查询（场景+费用类型+账期 → 风险线索 + 行级证据锚点；阈值恒用治理默认值，AI 不可调参；账单存在 ≠ 认定结论） |

AI 改仿真态只能经这 4 个 lab 工具（每个变更显式携带 workspaceId、走引擎校验）；MCP **无建删工作区工具**、无 SQL/Shell/SSH/文件工具；`restore_*` 引擎内部操作码在 HTTP/MCP/CLI 三边界均被拒；图分析域与费用风险域（后 6 个）全部只读。

TeleAgent 集成：MCP 注册方式与 CLI 回退路径（以结构化 JSON 供 Skills 解析）见 **[docs/teleagent-integration.md](docs/teleagent-integration.md)** —— 注意：**部署态 TeleAgent 的 MCP 调用未在本开发环境验证**，接入时请按该文档操作并反馈。

## 9. 免责声明（再次强调）

- 本项目**没有任何生产网络写能力**，MCP/HTTP API 均不暴露 SQL、Shell、SSH 或通用文件访问；
- 仓库内资源数据除 `samples/` 真实样例（见顶部合规声明的例外条款）外均为**合成测试数据**（`testdata/`，可由 `netopslab testdata` 确定性再生成），**不代表真实网络运行情况**；
- 质量报告页脚固定携带免责声明：“本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。”
- 能力边界与已验证/未验证事项的诚实清单见 [docs/verified-capabilities.md](docs/verified-capabilities.md)。

## 10. English Summary

NetOps Lab is a local-first cloud-network resource governance and AI simulation workbench delivering Stage 0 + Phase 1 + Phase 2. Phase 1 imports eight resource types (devices, IP pools, circuits, ports, base stations, machine rooms, sites, PTP ports — base stations/machine rooms/sites are v0.4 real-telecom workbook types, ptp_port is the v0.5 real-telecom script channel) through a user-confirmed column mapping (scripts map through a deterministic parser to a virtual sheet) into SQLite (pure Go, no CGO), with single-transaction atomicity, row-level accounting, idempotent re-import (skip-path re-imports append per-batch source-ref provenance, W7), batch rollback, attribute-change and suspected-rename proposals with human-confirmed apply/reject flows (W005/W006), and a deterministic, versioned validation engine (13 rules, E001–E006/W001–W006/I001) with source evidence. The v0.6 governance arc completes the trilogy and closes the data loop: ref-field attribute applies (machine-room site_id, pool assigned_device, circuit a/z ends) retarget the derived relation to the new target inside the same apply transaction (409 ref_target_missing when the target is gone; an edgeless reference never creates an orphan relation — lineage invariant), and a deterministic xlsx export channel (byte-identical across runs and across the CLI/HTTP channels; key-component back-fill preserves composite and fallback natural keys) round-trips through the same analyze → suggest → import pipeline into an equivalent fresh database (resource and relation sets equal, W005/W006 applied states included). Phase 2 adds the simulation workbench: one-click materialization of a virtual topology (27 devices / 40 interfaces / 20 links in the demo), a closed set of 10 typed operations (including fault injection) that humans AND the AI (via MCP) apply through the same engine path as atomic changesets, five deterministic checks (CHK-001..005, score 100−5E−1W), up-only BFS connectivity, before/after run diff (business-key matching, connectivity lost/gained pairs, score delta), and three-tier undo: transaction rollback for failed changesets, LIFO changeset-inverse undo (undo-of-undo works), and checkpoints via the SQLite Online Backup API with workspace-scoped restore-as-forward-ops (hash-equal verification, no-op detection). Exits: stdlib HTTP API (17 new §7 routes), embedded React 18 UI with a Cytoscape topology canvas (/lab), structured-JSON CLI (lab subcommand tree), and an MCP stdio server exposing exactly ten tools (one read-only search, four typed lab tools, and five read-only analysis tools — graph traverse/impact/context, alerts and metrics queries, the v0.9 root-cause loop; no workspace create/delete for the AI, and alert lifecycle edges stay human-only), all wired through a single composition root (internal/services, W9 — N+1-free MCP reads). Verified: 331 top-level Go tests green (v0.6.0 release count, counting convention in docs/verified-capabilities.md), and a one-screen governance overview (pending queues, decision audit, issue distribution, inventory shapes — SQL-COUNT aggregate, deterministic except generatedAt), live-server API journey, CLI e2e, in-process MCP round-trip + real-process stdio smoke, determinism (two fresh DBs), atomicity, checkpoint round-trip, repeatable demo-lab.sh double-run. Unverified: deployed-TeleAgent MCP invocation, Windows/macOS builds (the /lab browser smoke passed in the 2026-10-10 final review and again at v0.4.0). No production write capability anywhere; all data is synthetic, with the single documented `samples/` exception (real telecom export samples, owner-authorized, private-repository-only — see samples/README.md).

## 文档索引

- [docs/run-windows.md](docs/run-windows.md) — **Windows 一键运行**（run.ps1 用法与排错）

| 文档 | 内容 |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | 架构分层、模块边界、事务与确定性策略 |
| [docs/data-model.md](docs/data-model.md) | DDL v1–v5、实体说明、ID/时间戳约定、迁移机制 |
| [docs/api.md](docs/api.md) | HTTP API 参考（含真实请求/响应示例；§7 仿真路由） |
| [docs/import-and-validation.md](docs/import-and-validation.md) | 导入管线、行级记账、规则目录、评分、幂等与回滚 |
| [docs/natural-keys.md](docs/natural-keys.md) | 各资源类型自然键规范（分层身份模型、稳定性排序、现状差距） |
| [docs/simulation.md](docs/simulation.md) | 仿真工作台：物化、操作目录、校验目录、三层撤销、diff、确定性、性能 |
| [docs/teleagent-integration.md](docs/teleagent-integration.md) | TeleAgent 的 MCP 注册（5 工具）与 CLI 回退 |
| [docs/verified-capabilities.md](docs/verified-capabilities.md) | 已验证 / 未验证能力清单（诚实性文档） |
| [docs/adr/](docs/adr/) | 架构决策记录（ADR 0001–0009） |
| [CHANGELOG.md](CHANGELOG.md) | 版本变更记录 |
