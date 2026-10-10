# Changelog

本项目所有显著变更记录于此。格式参考 Keep a Changelog；版本遵循语义化版本（SemVer）。

## [0.2.2] — 2026-10-10

**画布多模式 + 去 AI 味打磨版**（用户反馈驱动）：拓扑画布从「3 布局 + 单一交互」升级为 **6 布局 × 3 交互模式 + 链路标签开关 + 迷你导航图**；删除侧栏渐变 logo 方块（「AI 味」首要来源）、卡片扁平化。**修复 v0.2.1 遗留回归：布局切换按钮实际跑的是旧布局**（setState 闭包陷阱，VLM 复核截图证实「网格」按钮截图实为力导排布）。181 个 Go 测试全绿；tsc 0 错误。

### 修复 — 画布（TopologyCanvas）
- **布局切换闭包陷阱（P1）**：`runLayout(kind)` 先 `setLayout(kind)` 再调用 `useCallback([layout])` 闭包里的 `startLayout`——实际执行的是**旧布局**（点击「网格」时闭包里仍是 force）。v0.2.1 的「布局切换」截图复核（VLM）证实从未生效。修复：`startLayout(cy, kind, eles?)` 显式传布局类型，不再依赖状态闭包
- Cytoscape 3.32 无 `:hover` 样式伪类（控制台警告「selector invalid」）：hover 微交互改事件驱动 `.hovered` 类（节点描边提亮 2.5px / 边线宽 3px，120ms 过渡）

### 新增 — 画布多模式（对标 Kiali/Grafana 拓扑视图）
- **6 布局**：力导 cose / **层级 breadthfirst**（每个连通分量取最高度节点为根，自上而下树形——核心-汇聚-接入分层视角）/ **同心圆 concentric**（按连接度分环，枢纽居中）/ 网格 / 环形 / **锁定 freeze**（停用自动重排，新节点围绕质心螺旋落位，保留手动拖拽）
- **3 交互模式**：选择（原有表格联动）/ **路径追踪**（依次点击两台设备 → 核心 `aStar` 最短路径青绿高亮 + 其余压暗 0.14，顶部横幅显示跳数与途经——超 6 台折叠「等 N 台」；不可达红色横幅；再点起点取消；Esc/点击空白/× 清除）/ **邻域聚焦**（点击设备，一跳闭邻域之外压暗；Esc/空白清除）
- **链路标签开关**：聚簇降噪（`edge.nolabel` 类规则置于属性选择器之后，同特异度源顺序取胜）
- **迷你导航图**（右下角 160×104，DPR 自适应）：节点状态缩略点 + 视口矩形（`cy.extent()` 映射），**点击/拖动直接平移主视图**（模型坐标 → panBy 推导，防大图迷航）；pan/zoom/position/layoutstop 事件 rAF 合并重绘
- **工具带重设计**：Grafana 式图标分段控件（布局 6 + 模式 3 + 标签开关），完整中文 tooltip 说明、`aria-pressed`、窄屏收纳提示文案
- **持久化 v2**：布局 + 交互模式 + 标签开关按工作台存 sessionStorage（JSON），兼容读取 v1 纯布局字符串

### 升级 — 去「AI 模板味」（VLM 视觉审计驱动）
- **删除侧栏渐变 logo 方块**（左上角「N」135° 渐变圆角块——AI 生成控制台的头号特征，真实 B2B 工业软件用文字字标）；折叠态改纯字符标记（无底色块）
- **卡片扁平化**：去除全部卡片投影，1px 边框承载边界（AntD 5/Linear 式平面；浮层对话框保留 `--shadow-lg`）
- favicon 去渐变（扁平深蓝底 + 白色拓扑 glyph，与侧栏色系一致）

### 验证
- 真浏览器（agent-browser）全模式走查 19 张截图：层级/同心圆/网格/环形 VLM 分类全部正确（布局切换修复生效）；路径追踪（20 跳可达 + 不可达红横幅）、邻域聚焦（47 元素中 5 保留）、标签开关（20/20 边生效）、锁定（手动位置保持，切回力导重排）、缩略图（角点点击视口 -230→-1059 平移）、v2 持久化（reload 后标签开关保持）、1280 折叠侧栏（logo 无回归）
- 控制台零警告零错误（v0.2.2 前：2 条 invalid selector 警告）；gofmt 空 / vet 干净 / 181 测试 PASS / tsc 0 错误 / vite build / `make build` 嵌入

## [0.2.1] — 2026-10-10

**全局打磨版**：四个独立审计（后端 bug / 前端 bug / 降级核查 / 浏览器 UI 双对标）驱动的修复与升级。引擎修复 4 项 P1 + 10 项 P2；前端修复 4 项 P1 + 15 项 P2 + 1 项 P0；UI 功能对标工业控制台、视觉对标顶尖大厂（VLM 评审 7.2 → 8.5 → 9.0）。**181 个 Go 测试全绿**（+12）；零实现降级（审计确认）。演示：`./scripts/demo-lab.sh`（不变）。

### 修复 — 仿真引擎（internal/sim · repo · api · mcp）
- **空操作集拒绝**：`{"ops":[]}` / `{"ops":null}` 在引擎边界即被拒绝（400 `invalid_request`）——此前会落一条 0 操作的已应用变更集，使变更集级 undo 被 409 `not_latest` 永久卡死（审计 AUD1-3）
- **并行链路闭环**：`create_link` 对同接口对的第二条链路返回 `op_failed`（「link already exists between interfaces…」）；检查点恢复的链路匹配键加入 label 并按多重集对比——旧数据中的重复对链路也能哈希精确恢复（审计 AUD1-1）
- **前后对比 = 完整谓词**：连通性差异计算从「仅链路状态」升级为 §5 完整谓词（设备 up × 接口 up × 链路 up）——`set_device_status down` / `set_interface_status down` 注入的故障现在会产生正确的 `connectivityLost` 对（运行快照扩展 4 个可选状态字段，旧格式视为 up，状态哈希不变；审计 AUD1-2）
- **并发安全**：DSN 追加 `_txlock=immediate` + 事务开启 3 次有界重试，消除 SQLITE_BUSY（读→写升级竞争导致的 500 与运行丢失，审计 AUD1-4）；DSN 路径百分号编码（含空格/中文路径可用）
- **撤销原子性**：逆向应用与 `MarkReverted` 同事务；状态守卫拒绝撤销非已应用变更集
- **检查点完整性**：快照内嵌状态哈希（marker）+ 恢复前校验（被篡改/漂移 ⇒ 409 硬失败）+ 恢复后哈希复核
- **错误消息补全**：`simErrorMessages` 补 `invalid_request` / `op_failed` 条目——操作校验 400 不再返回空 message（此前前端只能显示「请求失败」）
- 其余：物化关系读失败传播（不再静默丢电路）、回滚失败保留原始错误、MCP `lab_checkpoint list` 空列表渲染 `items:[]`+`total:0`（§8 对齐）、`Path` 不可达 `hops:[]`（非 null）、链路仓储数值排序、`statusRecorder.Unwrap` 落实

### 修复 — 前端（web/src）
- **P0 拓扑标签不可见**：`min-zoomed-font-size:6` + 默认 fit 缩放 <0.6 导致全部标签消失（演示核心画面变成「匿名方块」）——标签始终渲染 + 节点内编码徽章化 + `minZoom:0.55` 钳制（审计 AUD4-P0）
- **新建接口 IPv4 校验缺失**：`create_interface` 分支静默丢弃地址 / `prefix:"abc"` 产生 NaN→400——补齐与配置分支一致的校验与中文行内错误（审计 AUD2-1）
- **`isValidIpv4` 前导零**：`010.0.0.1` 通过前端校验后被 Go 拒绝——正则收紧（审计 AUD2-2）
- **实验室取流竞态**：全部 /lab 取流挂载 `useSeq` 顺序守卫（工作台路由切换陈旧覆盖、重叠刷新留旧评分、运行详情下拉抖动）（审计 AUD2-8）
- 15 项 P2：表头吸顶修复、`fit` 等 `layoutstop`、增量布局不再全图重排、全局最新变更集判定（跨页撤销不再 409）、删除后页码钳制、`lastWorkspace` 失效清理、模态滚动锁 + 焦点陷阱（`useModalA11y` 统一五处）、错误块二级详情 + 重试、下拉截断提示、检查点标签确认后清空、错误 Toast 8s + 关闭按钮、`types.ts` 线格式对齐、`§4` 文档引用清出用户文案

### 升级 — UI 功能性（对标 Grafana / Datadog 工业控制台）
- **全出血工作台**：`/lab/:id` 突破 1200px 容器（1920 下画布 648→948+px）；分栏断点 1280→1080（笔记本保留双栏）；1024 堆叠无横向滚动
- **画布工具带**：放大 / 缩小 / 适配 / 重置 1:1 浮动控件 + 完整图例（运行/停机/接口故障设备 · 正常/中断链路）
- **列排序**：工作台列表、实体三表（设备/接口/链路）、检查结果、变更集——三态表头（升→降→还原）+ `aria-sort` + 数值后缀排序（vd-9 < vd-10）
- **中文检查目录**：CHK-001..005 中文标题/释义为第一行、英文引擎消息降为次行；实体 id 等宽码片化
- **对比对话框**：默认双运行自动生成（免手动点击）；连通性对列表折叠为「共 N 对，点击展开」芯片
- **演示级 Toast**：成功携带「查看变更集」动作链接（跳转变更集页并闪烁定位行）+ 变体图标；错误 8s
- 实体表筛选框、相对时间（「刚刚/5 分钟前」+ title 全时）、布局与标签页 sessionStorage 持久化、批量 uuid 短码 + 点击复制、`prefers-reduced-motion`

### 升级 — 视觉（对标 Ant Design Pro / Linear / Stripe）
- **暗色工程画布**：分层深蓝底（非纯黑）、节点=浅底编码徽章（内部 `DEV-001` 等宽 12px，9:1 对比度）、悬停信息栏（编码·名称·状态）、贝塞尔连线（up 加亮 `#7387ab`、down 3px 红虚线、选中加粗）
- **AA 对比度**：全部状态芯片文字 ≥4.5:1（success 3.05→4.71、error 3.73→5.40、warning 2.90→5.20、info 3.64→5.30、pool/circuit/banner 同步达标）；暗底图形元素 ≥3:1；警告色 token 统一 `#C77D0A`
- Favicon（SVG 网络节点标记）、`tabular-nums` 数字对齐、原生 select/滚动条 token 化、图例留白

### 文档
- `docs/simulation.md`：对比谓词升级说明（示例 38→24 对）、create_link 同对唯一性、检查点完整性校验
- `docs/api.md`：§7 错误消息示例更新（非空 invalid_request/op_failed）、空操作集 400、`hops:[]`
- `docs/verified-capabilities.md`：v0.2.1 验证矩阵（181 测试 + 审计 + 双对标 + VLM 评审记录）

## [0.2.0] — 2026-10-09

**Phase 2（仿真工作台）全量交付**：在 Phase 1 资源治理之上，把导入的合成台账一键物化为虚拟拓扑，让人与 AI 在**封闭的类型化操作集**上做可审计、可撤销的仿真变更（含故障注入）。169 个 Go 测试全绿；演示：`./scripts/demo-lab.sh`。

### 新增 — 仿真工作台
- **一键物化**：从导入批次生成虚拟拓扑（设备→虚拟设备、电路→链路、确定性 `eth0/eth1…` 端口分配、自环电路跳过）；演示规模 27 设备 / 40 接口 / 20 链路，状态哈希 `aea27501…`；同批次二次物化结构一致（id 全新）
- **类型化操作引擎**（`internal/sim`）：10 个前向操作（增删设备/接口/链路、启停、配置/清除 IPv4）+ 3 个引擎内部 `restore_*` 逆操作；严格字段校验（未知码/多余字段/坏类型 ⇒ 400）；单事务变更集（op_json + inverse_json 逐操作留档）；**失败零污染**：语义失败整体回滚 + 第二事务记录失败变更集（`ops_failed=0`），HTTP 422 `op_failed` 消息含失败变更集 id
- **确定性校验目录**（rule_version 1.0.0）：CHK-001 重复主机地址 / CHK-002 网段重叠（identical·contains·partial）/ CHK-003 孤岛设备 / CHK-004 部分分区 / CHK-005 未配置接口；发现行 + 恒定基线行（运行自描述）；评分 100−5×错误−1×警告（下限 0）；运行携带规范拓扑快照
- **连通性与前后对比**：up-only BFS 路径查询（邻居编码升序，路径确定）；两次运行的 diff——问题按业务键匹配（id 漂移免疫）、连通性丢失/恢复对、评分差

### 新增 — 故障注入与三层撤销
- **故障注入**：`set_link_status down` / `set_device_status down` / 删除类操作即可注入故障；连通性即时反映（演示：关断 ln-1 ⇒ DEV-001→DEV-003 不可达、连通性丢失 20 对、CHK-003/CHK-004 发现）
- **三层撤销**：
  1. **事务回滚**——失败变更集零状态污染（留档可审计）；
  2. **变更集逆向 LIFO undo**——逆操作按序倒序作为新变更集（trigger `undo`），撤销可再撤销；仅最新已应用前向变更集可撤销（409 `not_latest`）；
  3. **检查点**——SQLite **Online Backup API** 全库快照（`Step(-1)`+`Finish`，经 `(*sql.Conn).Raw` 匿名接口断言，**非裸拷贝**）；**工作区级恢复 = 前向操作变更集**（删除-重建计划、业务键匹配、id 预测），统一审计、可再撤销；状态哈希相等 ⇒ no-op（`changesetId:null`）

### 新增 — 出口层
- **HTTP API §7**（17 条路由）：workspaces CRUD/列表/详情、topology、path、ops、checks run/list/detail、diff、changesets、undo、checkpoints create/list/restore、lab report（HTML/JSON）；英文错误信封（`invalid_request`/`not_found`/`conflict`/`not_latest`/`checkpoint_missing_file`/`no_source_resources`/`op_failed`）
- **MCP 五工具**（SEC-P2）：`resource_search`（P1 不变）+ `lab_apply_ops` / `lab_run_checks` / `lab_diff_undo`（mode 判别）/ `lab_checkpoint`（action 判别）；AI 改仿真态的唯一通道，**无建删工作区工具**；`restore_*` 三边界拒绝（schema + 引擎双重）
- **CLI `lab` 子命令树**：`workspace create / topology / path / apply / checks / diff / undo / checkpoint`；单一 JSON 输出、退出码 0/1/2
- **仿真工作台报告**：`GET /api/lab/report`（HTML 同 P1 视觉风格 + JSON：引擎版本/评分/发现/变更集史/检查点/免责声明）

### 新增 — 前端（/lab）
- 新导航组「仿真工作台」：工作台列表页（新建对话框：批次多选，默认全部已完成）+ 拓扑工作台页（`/lab/:id`）
- **Cytoscape 3.32.0 拓扑画布**（精确版本、无插件）：节点 up/down 分色、链路实线/虚线、力导/网格/环形布局切换、点击联动右侧表格；增量元素同步保留视角
- 右侧六标签页：设备 / 接口（配置地址/清除）/ 链路 / 检查结果（发现+基线+运行历史）/ 变更集（LIFO 撤销按钮）/ 检查点（创建/恢复确认）；页头六动作含前后对比对话框与导出报告
- 操作对话框：11 个中文操作表单（拓扑预填下拉 + 客户端校验）+ 原始 JSON 模式
- 构建产物：js 717.63 kB（gzip 226.73）/ css 22.22 kB（cytoscape 约 +500 kB，单 chunk 嵌入决策见 ADR-0009）

### 新增 — 工程化
- DDL v5（workspaces / 全局 sequences / virtual_devices / interfaces / links / changesets+ops / check_runs+results / checkpoints）+ 8 个仿真仓储 + Online Backup/只读打开（`repo.Backup`/`OpenReadOnly`）
- 169 个 Go 测试（16 包 ok，14 含测试；P1 96 + 73 新增）：ACC-P2-1..8 / SEC-P2 / DET-P2 / E2E-P2 / API-P2 / PERF-P2 全覆盖；物化/操作/校验/undo/检查点均含双库确定性比对
- `scripts/demo-lab.sh`（可重复故障注入演示，双跑输出一致）+ Makefile `demo-lab` 目标
- 文档：`docs/simulation.md`、ADR 0007–0009；更新 README / api（真实捕获示例）/ data-model（DDL v5）/ architecture / teleagent-integration（5 工具 + jsonschema 教训附录）/ verified-capabilities

### 已知限制（诚实声明）
- **单工作区活跃模式**：schema 支持多工作区共存，但多工作区**并发**写不受支持（检查点恢复的 id 预测依赖单写者）；
- 部署态 TeleAgent MCP 调用仍未验证（进程内回环 + 真进程 stdio 冒烟已验证）；真实浏览器 /lab 冒烟待终审；Windows/macOS 未构建（linux/amd64）；详见 docs/verified-capabilities.md。

## [0.1.0] — 2026-10-09

首个交付版本：**Stage 0（技术验证）+ Phase 1（资源治理）** 全量功能。合成数据、无生产写能力、单二进制分发。

### 新增 — 导入管线
- Excel 工作簿解析（多 Sheet、表头行、空行跳过计数、类型化单元格）
- 列映射模型与确定性建议引擎（置信度阶梯 1.0/0.9/0.75/0、同义词表、按 Sheet 类型限定）
- 三类资源 schema（device / ip_pool / circuit，字段级校验含取值范围）
- 用户确认映射后执行导入：全部 Sheet 单事务、行级记账（imported/skipped/failed）、幂等重导（skip-duplicate）、批次回滚（409 in-use 保护）
- 来源引用（source_refs：工作簿/Sheet/1-based 行号 + 原始行 JSON）

### 新增 — 数据质量校验与报告
- 规则目录 v1.0.0：E001–E006 / W001–W003 / I001（行级 7 条 + 集合级 3 条），严重级别映射集中于 domain 层
- 集合级引擎（网段重叠/地址重复使用/孤岛设备）：单事务删旧插新、幂等重算、确定性排序
- 质量报告：JSON + 独立 HTML（内联 CSS、自动转义、页脚合成数据免责声明）；评分 100−5×错误−1×警告（下限 0）；全局与批次两种口径
- 问题来源解析：source_ref / 行号证据 / applicationGenerated 三态

### 新增 — 查询与拓扑
- 资源检索（关键词/类型/状态、分页、详情 + 关系计数 + 来源引用）
- 全库统计（资源/状态/关系/问题计数、最近批次）
- 关系子图（1/2 跳 BFS，节点 + 边，无向遍历，确定性排序）
- 拓扑可达性检查（设备级图、maxHops、路径 hop 列表）

### 新增 — 出口层
- HTTP API（Go 1.27 stdlib 路由、统一错误信封、panic 恢复、slog JSON 请求日志、10MB 上传限制、优雅停机）
- CLI：`serve / analyze / import / validate / report / query / mcp / testdata`；成功输出单一 JSON 对象；退出码 0/1/2
- MCP stdio 服务器（go-sdk v1.8.0）：仅注册一个只读工具 `resource_search`
- React 18 + TypeScript（strict）+ Vite 前端：概览/数据导入三步向导/资源浏览（抽屉+二跳）/问题清单/质量报告，中文 UI，手写设计系统；`go:embed` 嵌入单二进制

### 新增 — 工程化
- SQLite 迁移框架（v1–v4）+ 六个聚合仓储；DSN pragmas（WAL / busy_timeout / foreign_keys）
- 合成测试数据生成器（`cmd/testdatagen`，字节级确定）+ 预期计数固化（`testdata/expected-summary.json`）
- 96 个 Go 测试（13 包）全绿：行级记账、幂等、原子性、确定性、来源证据、拓扑、API 形状回归、CLI 子进程 E2E、MCP stdio 回环、工具面安全断言
- `scripts/demo.sh` 一键演示（全新库 → 导入×3 → 校验 → 报告 → 统计）
- 文档套件：README / architecture / data-model / api / import-and-validation / teleagent-integration / verified-capabilities / ADR 0001–0006
- GitHub Actions CI（go + web 双 job）

### 已知限制（诚实声明）
- 部署态 TeleAgent MCP 调用未验证（进程内回环已验证）；Windows/macOS 未构建（linux/amd64）；真实浏览器渲染待终审；详见 docs/verified-capabilities.md。

## 终审补充（2026-10-09）

- ci：GitHub Actions 三次迭代后全绿（go: fmt/vet/96 测试含前端构建；web: tsc/vite）——run#1 暴露 go job 缺前端构建、run#2 暴露 npm 缓存路径，均已修复

- fix(api)：所有 `/api/*` 响应注入 `Cache-Control: no-store`，杜绝浏览器启发式缓存导致的"幽灵统计"（ADR 0006，含回归测试）
- 终审完成真实浏览器 UI 全流程冒烟（Chrome 155 / agent-browser 隔离会话），截图证据随交付包提供
