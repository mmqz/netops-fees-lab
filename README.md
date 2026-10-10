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

> ⚠️ **合规声明**：本平台**不具备生产设备写能力**。AI 仅可操作本地实验室工作区（仿真工作台）；生产操作必须由人员通过公司批准的外部工具执行。仓库内所有数据均为**合成测试数据**，仅用于工程方法验证，不代表真实网络运行情况——**唯一例外**：`samples/` 目录含项目所有者在需求会话中提供的真实电信运营导出样例（含真实设备 IP 与运营商标头，未脱敏），仅限本私有仓库内做导入工程验证、不得外发或再分发（授权与范围详见 [samples/README.md](samples/README.md)）。此为红线「no real network data」的**已文档化偏差**（经项目所有者授权入库并记录于案）。
>
> ⚠️ **Compliance**: This platform has **NO production-device write capability**. AI may only operate the local lab workspace (simulation workbench); production operations must be performed by humans through company-approved external tooling. All data in this repository is **synthetic** and used solely for engineering-method validation — with **one documented exception**: the `samples/` directory holds real telecom-operator export samples provided by the project owner (containing real device IPs and operator headers, not anonymized). They are licensed for import-engineering validation **inside this private repository only** and must not be redistributed; see [samples/README.md](samples/README.md). This is a **documented, owner-authorized deviation** from the "no real network data" red line.

## 1. 项目定位

NetOps Lab 是一个本地优先（local-first）的云网资源治理与 AI 仿真工作台：把多份 Excel 资源台账（设备 / 地址池 / 电路）经**用户确认的列映射**导入 SQLite，完成行级记账的标准化与确定性质量校验；再从导入资源**一键物化**虚拟拓扑工作区，让用户与 AI（经 MCP）通过**封闭的类型化操作集**（含故障注入）在仿真态上做真实状态迁移，引擎给出确定性校验结果、前后对比与三层撤销（事务回滚 / 变更集逆向 / 检查点）。出口为 HTTP API、嵌入式 React 界面（含 Cytoscape 拓扑画布）、CLI 与 **MCP 五工具**。本仓库交付 **Stage 0（技术验证）+ Phase 1（资源治理）+ Phase 2（仿真工作台）**。

## 2. 当前阶段能力清单（Stage 0 + Phase 1 + Phase 2 已交付）

### 仿真工作台（Phase 2，v0.3.2 工程审查整改版）

- **一键物化**：从导入批次生成虚拟拓扑（设备 → 虚拟设备、电路 → 链路、确定性 `eth0/eth1…` 端口分配）；演示规模 27 设备 / 40 接口 / 20 链路，物化 3–4 ms。
- **类型化操作集（含故障注入）**：10 个前向操作（增删设备/接口/链路、启停、配地址）经同一引擎路径（API/MCP/CLI 无旁路）；单事务变更集，失败零污染且留档（422 `op_failed`）。
- **确定性校验**：CHK-001..005（重复主机地址/网段重叠/孤岛设备/部分分区/未配置接口），同状态同结果；评分 100−5×错误−1×警告；运行自带拓扑快照。
- **连通性与前后对比**：up-only BFS 路径查询；两次校验运行的 diff（新增/消失问题按业务键匹配、连通性丢失/恢复对、评分差）。
- **三层撤销**：事务回滚（失败变更集）/ 变更集逆向 LIFO undo（撤销本身也是变更集，可撤销的撤销）/ 检查点（SQLite Online Backup API 快照，恢复 = 前向操作变更集，精确到状态哈希相等，no-op 检测）。
- **拓扑画布（v0.2.3 全屏工作台）**：`/lab/:id` Cytoscape 交互图——**6 种布局**（力导 / 层级树 / 同心圆 / 网格 / 环形 / 锁定手动排布）× **3 种交互模式**（选择联动表格 / 路径追踪：点两台设备高亮最短路径与跳数、未选起点时有引导横幅 / 邻域聚焦：一跳之外压暗）+ 链路标签开关 + 右下角迷你导航图（点击/拖动定位视图）+ 悬停信息条；up/down 分色、实线/虚线故障可视；布局与模式按工作台持久化。
- **画布全屏工作模式（v0.2.3 zen）**：一键让画布接管整个视口（优先浏览器原生全屏，拒绝时降级应用内全屏；Esc 逐级退出：右键菜单 → 路径/聚焦 → 全屏）；全屏中左上浮动**选中详情卡**（设备：状态/接口 up-down/IPv4；链路：两端与端口），工具带深色化；Toast 浮层不被遮盖。
- **画布工业级交互（v0.2.3）**：**右键上下文菜单**（设备：邻域聚焦/设为路径起点/表格定位/复制 ID；链路：表格定位/复制 ID；空白：适配/重置；a11y role=menu + 焦点管理 + 越界翻转）；**PNG 快照导出**（整图 2 倍分辨率不透明深色底）；**缩放百分比实时指示**；**手动排布位置持久化**（localStorage，重载不丢，切布局自动重排）；键盘快捷键（F 适配 / L 标签开关 / Esc 链级）。
- **画布工作区布局（v0.2.3）**：页头紧凑化（画布高度 1080p 达 760px）；**分栏拖拽**（30–85% 自适应 + 键盘 ±2% + a11y separator + 持久化）；**右栏折叠**（画布占满工作区宽度）；画布非全屏 948×760 → 全屏 1920×1080（约 2.9 倍面积）。
- **地理视图（v0.3.0 三视图切换）**：画布新增「拓扑图 / 2D 地图 / 3D 地图」分段切换（按工作台持久化）；导入设备表时自动识别**经度/纬度/经纬度列**（组合列按中国坐标边界启发式拆分），物化后设备携带 geoLat/geoLng；2D 俯视——真实深色地理底图（ESRI World_Dark_Gray，免密钥）上按真实经纬度落设备（状态色圆点 + 编码牌）、链路 up 蓝实线 / down 红虚线 marching-ants；3D 斜视 58°——设备渲染为按连接数分高的塔体（数字孪生示意）、右键拖拽旋转环视；设备点击/选中与右侧表格双向联动；瓦片失败自动降级离线网格底图；maplibre-gl 懒加载独立 chunk。演示数据：`testdata/geodevices.xlsx`（黑龙江哈尔滨→漠河北南干线 + 地震局 10M 长专线，坐标为演示近似值），一键脚本 `scripts/demo-geo-lab.sh`。
- **port 资源类型（v0.3.1 真实表格导入；migration v8 换稳定键）**：真实电信端口工作簿（端口流量监控表 / 端口资源表两种表头形态）可直接导入——`device_name + port_name` 复合自然键（`|` 拼接落库，键的设备侧自 migration v8 起锚定设备名称而非 IP：设备重划 IP 不再分裂资源，存量库升级语义见 [docs/data-model.md](docs/data-model.md) §11）、逐字重复表头行自动跳过多段导出拼接、带宽 0 合法入库；样例 `samples/telecom-port-multi-header.xlsx`（项目所有者在需求会话中提供的真实电信运营导出，已授权入库、仅限本私有仓库使用——来源与授权见 [samples/README.md](samples/README.md)；自然键稳定性分析见 [docs/natural-keys.md](docs/natural-keys.md)）；3D 地图默认视野按全部数据点的包围盒几何直解自适应（修「3D 地图过大」）。
- **MCP 五工具**：`resource_search`（只读）+ `lab_apply_ops` / `lab_run_checks` / `lab_diff_undo` / `lab_checkpoint`——AI 改仿真态的唯一通道；`restore_*` 内部操作码三边界拒绝；**无建删工作区工具**。
- **CLI `lab` 子命令树**：workspace/topology/path/apply/checks/diff/undo/checkpoint，全部单一 JSON 输出。
- **仿真报告**：`GET /api/lab/report`（HTML/JSON）：引擎版本、评分、发现、变更集史、检查点、固定免责声明。

### 资源治理（Stage 0 + Phase 1）

- **Excel 导入管线**：多工作表解析、列映射建议（确定性建议引擎，置信度 1.0/0.9/0.75）、用户确认后执行；导入全程单事务，任何内部错误整体回滚（零半写）。
- **行级记账**：每一数据行必有 `imported / skipped / failed` 之一；跳过即幂等（重复导入 0 新增），失败行携带规则码与消息。
- **数据质量校验**：10 条版本化规则（E001–E006 / W001–W003 / I001），行级规则随导入触发，集合级规则（网段重叠、地址重复、孤岛设备）导入后自动重算且幂等；每条问题携带来源证据（工作簿/Sheet/行号）或显式 `applicationGenerated` 标记。
- **资源检索与统计**：类型/状态/关键词过滤、分页、资源详情（原始行回溯）、全库统计。
- **关系查询**：assigned_to / a_end / z_end 三类关系，1 跳与 2 跳 BFS 子图（节点 + 边）。
- **拓扑可达性检查**（Stage 0）：按设备编码的两端连通性判定 + 路径（如 DEV-001→DEV-003 路径长 2）。
- **质量报告**：JSON 与独立 HTML 两版（评分 = 100 − 5×错误 − 1×警告，下限 0），支持全局与单批次两个口径。
- **批次管理**：批次列表/详情/行结果查询；已完成批次可回滚（被其他批次引用时报 409 `batch_in_use`）。
- **HTTP API**：`/api/...` 全套 REST 风格接口（错误统一 `{"error":{"code","message"}}` 信封）+ 17 条 §7 仿真路由。
- **嵌入式 React 前端**：概览 / 数据导入（三步向导）/ 资源浏览（抽屉 + 二跳）/ 问题清单 / 质量报告 / 工作台列表 / 拓扑工作台 中文界面，`go:embed` 打进单二进制。
- **结构化 CLI**：`analyze / import / validate / report / query / serve / mcp / testdata / lab`，成功输出单一 JSON 对象，退出码 0/1/2（TeleAgent 的 CLI 回退路径）。
- **工程化**：**207 个 Go 顶层测试**（14 个含测试的包；`go test -list '.*'` 顶层函数口径）全绿；gofmt/vet 干净；确定性导入/校验/仿真；可重复的一键演示脚本（demo.sh + demo-lab.sh）。

## 3. 快速开始

前置要求：**Go 1.27+**（编译后端；无 CGO，纯 Go）、**Node 18+**（构建前端；本仓库验证于 Node 24 / npm 11）、python3（演示脚本用）。

```bash
# 1) 测试
make test            # go test -count=1 -timeout 15m ./...（207 个顶层测试 / 14 包 ok）

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
```

说明：`make web` 会把 `web/` 构建到 `internal/webui/dist`（该目录不提交，仅保留 `.gitkeep` 占位）；`make build` 嵌入的是**当时 dist 里的内容**——先 `make web` 再 `make build` 才会得到带真实前端的二进制。`make all` = `web + build`。

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
| 测试 | go test（table-driven） | 207 个顶层测试 / 14 包（`go test -list '.*'` 口径）；含 CLI 子进程 E2E、MCP stdio 回环、确定性双库比对 |

## 6. 仓库结构

```
netops-lab/
├── cmd/netopslab/        # 单二进制入口：serve|analyze|import|validate|report|query|lab|mcp|testdata
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
├── samples/              # 真实电信运营导出样例（唯一合规例外，见 samples/README.md）
├── scripts/demo.sh       # 一键演示（资源治理）
├── scripts/demo-lab.sh   # 一键演示（仿真工作台/故障注入）
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
netopslab validate --db ./data/netops.db [--batch <id>]
netopslab report   --db ./data/netops.db --format html --out report.html
netopslab query    --db ./data/netops.db resources|stats|relations …
netopslab lab      --db ./data/netops.db workspace create --name <n> [--all-batches]   # 仿真工作台
netopslab lab      … topology|path|apply|checks|diff|undo|checkpoint                   # 见 docs/teleagent-integration.md §4.2
netopslab serve    --addr :8080 --db ./data/netops.db
netopslab mcp      [--db ./data/netops.db]                  # 或环境变量 NETOPS_DB
netopslab testdata --out testdata
```

**MCP**：`netopslab mcp` 启动 stdio JSON-RPC 服务器（server name `netops-lab`，version `v0.3.2`），注册**恰好 5 个工具**：

| 工具 | 类别 | 职责 |
| --- | --- | --- |
| `resource_search` | 只读 | 检索导入资源（P1 不变：q/type/limit/offset → total + items[]，每项含来源） |
| `lab_apply_ops` | 仿真写 | 类型化操作（含故障注入）→ 变更集 + 自动校验 |
| `lab_run_checks` | 只读 | 立即执行五项校验，返回发现行 |
| `lab_diff_undo` | 读/写 | 两次校验运行的前后对比 / LIFO 撤销（mode 判别） |
| `lab_checkpoint` | 仿真写 | 检查点 create / list / restore |

AI 改仿真态只能经这 4 个 lab 工具（每个变更显式携带 workspaceId、走引擎校验）；MCP **无建删工作区工具**、无 SQL/Shell/SSH/文件工具；`restore_*` 引擎内部操作码在 HTTP/MCP/CLI 三边界均被拒。

TeleAgent 集成：MCP 注册方式与 CLI 回退路径（以结构化 JSON 供 Skills 解析）见 **[docs/teleagent-integration.md](docs/teleagent-integration.md)** —— 注意：**部署态 TeleAgent 的 MCP 调用未在本开发环境验证**，接入时请按该文档操作并反馈。

## 9. 免责声明（再次强调）

- 本项目**没有任何生产网络写能力**，MCP/HTTP API 均不暴露 SQL、Shell、SSH 或通用文件访问；
- 仓库内资源数据除 `samples/` 真实样例（见顶部合规声明的例外条款）外均为**合成测试数据**（`testdata/`，可由 `netopslab testdata` 确定性再生成），**不代表真实网络运行情况**；
- 质量报告页脚固定携带免责声明：“本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。”
- 能力边界与已验证/未验证事项的诚实清单见 [docs/verified-capabilities.md](docs/verified-capabilities.md)。

## 10. English Summary

NetOps Lab is a local-first cloud-network resource governance and AI simulation workbench delivering Stage 0 + Phase 1 + Phase 2. Phase 1 imports three synthetic Excel workbook types (devices, IP pools, circuits) through a user-confirmed column mapping into SQLite (pure Go, no CGO), with single-transaction atomicity, row-level accounting, idempotent re-import, batch rollback, and a deterministic, versioned validation engine (10 rules, E001–E006/W001–W003/I001) with source evidence. Phase 2 adds the simulation workbench: one-click materialization of a virtual topology (27 devices / 40 interfaces / 20 links in the demo), a closed set of 10 typed operations (including fault injection) that humans AND the AI (via MCP) apply through the same engine path as atomic changesets, five deterministic checks (CHK-001..005, score 100−5E−1W), up-only BFS connectivity, before/after run diff (business-key matching, connectivity lost/gained pairs, score delta), and three-tier undo: transaction rollback for failed changesets, LIFO changeset-inverse undo (undo-of-undo works), and checkpoints via the SQLite Online Backup API with workspace-scoped restore-as-forward-ops (hash-equal verification, no-op detection). Exits: stdlib HTTP API (17 new §7 routes), embedded React 18 UI with a Cytoscape topology canvas (/lab), structured-JSON CLI (lab subcommand tree), and an MCP stdio server exposing exactly five tools (one read-only search + four typed lab tools; no workspace create/delete for the AI). Verified: 207 top-level Go tests green (v0.3.2 fresh count, counting convention in docs/verified-capabilities.md), live-server API journey, CLI e2e, in-process MCP round-trip + real-process stdio smoke, determinism (two fresh DBs), atomicity, checkpoint round-trip, repeatable demo-lab.sh double-run. Unverified: deployed-TeleAgent MCP invocation, real-browser /lab smoke (pending final review), Windows/macOS builds. No production write capability anywhere; all data is synthetic, with the single documented `samples/` exception (real telecom export samples, owner-authorized, private-repository-only — see samples/README.md).

## 文档索引

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
