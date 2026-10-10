# Changelog

本项目所有显著变更记录于此。格式参考 Keep a Changelog；版本遵循语义化版本（SemVer）。

## [0.3.2] — 2026-10-10

**工程审查整改版**（R1 架构 A- / R2 代码质量 / R3 计划偏移 / R4 主键身份模型——四维只读审查的整改落地；用户需求驱动：「继续做修复然后做分析告诉我下一步的开发」）。

### 修复 — 版本事实源（R1-D1 / R3-C1，全项目唯一 P1）
- **根因**：AppVersion 三处手抄（domain / web i18n / CHANGELOG），v0.3.1 已真实漂移——domain 停在 v0.3.0，`/api/health` 与 MCP 握手错报，README 承诺未兑现。
- **修复**：三处统一升版 v0.3.2；新增 `TestAppVersionMatchesChangelogHead`——domain 常量必须等于 CHANGELOG 头版本，再漂移即测试红；侧栏版本号改读 `/api/health`（前端不再手抄）。

### 修复 — 后端健壮性（R2-P2×2 + R1-D6 + R2-P3×3）
- `importer.Rollback` 的 in-use 检查与状态前置校验**移入 `db.Do` 事务内**（BEGIN IMMEDIATE 先取写锁，与其他写事务严格串行，TOCTOU 窗口归零）。
- workbook 层新增**解压上限**（单 sheet 物化行数 / 全簿单元格总量封顶，超限返回新增 422 错误码 `workbook_too_large`）——zip 炸弹暴露面收敛；解析改为逐 sheet 流式。
- serve 启动**自愈**：进程崩溃遗留的 `analyzing`/`importing` 瞬态批次复位为 `failed(interrupted)`（R1-D6 崩溃窗口关闭）。调用点在 cmd 层——`serve` 启动与 CLI `import` 入口（确定本进程无在途导入的入口，非 api.New）；MCP 进程不做以免误伤并发导入。
- `handleBatchDelete` 删除响应计数改 **COUNT 聚合**（不再全量加载行结果与 issue 仅为取 len）。
- MCP `resource_search` 的 type 描述与错误文案**补 port**（文案与封闭集对齐）。
- dataDir 为空时上传请求直接 500（消除半初始化相对路径拼接）。

### 修复 — 前端（R2-P2-3 / R1-D4 / R4 短期）
- **Overview 类型统计动态化**：此前硬编码 `device|ip_pool|circuit` 三类型，v0.3.1 新增的 port 在总览统计完全缺席——改为按后端 stats.byType 动态渲染全部类型。
- **Report 页批次切换加 useSeq 陈旧响应守卫**（快速切换下拉时慢响应后到覆盖新批次预览的串台修复；顺带为 Overview 挂载/重试加载加同款守卫——全站异步加载点全部收口）。
- **导入映射确认页主键稳定性告警**（R4 短期落地）：IP 类列被选入自然键时告警「IP 属可变属性，重划后同键资源将分裂」；名称类自然键提示 (地市, 名称) 作用域。

### 文档 — 诚实对齐（R3-P0×2 + P1×2 + P3）
- README 合规横幅**加 samples/ 真实样例例外条款**；新增 `samples/README.md`（来源/授权/范围说明——红线 8「no real network data」的已文档化偏差）。
- 新增 `docs/natural-keys.md`：**各资源类型自然键规范**（R4 裁决落地——UUID 代理键 PK 不变；编号类自然键优先；名称键须 (地市,名称) 作用域；IP 一律降级可变属性；port 键含 IP 定性为 P1 隐患并排入 v0.4 迁移计划）。
- `docs/api.md` 补 v0.3.x 契约（拓扑节点 geoLat/geoLng、`type=port`、port 复合键导入示例、422 `workbook_too_large`，版本戳 v0.3.2）；`docs/import-and-validation.md` 补 port / 重复表头 / 解压上限 / 崩溃自愈行为。
- `docs/verified-capabilities.md` 新增 v0.3.2 段（测试计数口径注明）；ADR-0002 / ADR-0009 补遗（DSN `_txlock=immediate`；maplibre-gl 懒加载独立 chunk）；`architecture.md` §4 undo 标记原子化更正。
- backup-local-impl 本地分支处置决策已执行：本地 tag `archive/backup-local-impl` 归档（价值已迁移主线的旧基线实验；该分支与 tag 均永不推送远程），决策记入 worklog。

### 测试
- Go **207 个顶层测试**全绿（+8 修复回归：rollback 事务化 / 解压上限 / 崩溃自愈 / 动态统计等修复项各自的新增测试，+1 版本一致性 `TestAppVersionMatchesChangelogHead`；基线为 v0.3.1 实测 198——彼时宣称 200 属口径未对齐，本版已校正为 `go test -list '.*'` 顶层函数实测计数）。门禁六项全绿：gofmt 空 · vet 0 告警 · 14 包全 ok · `-race` 0（importer/workbook/api/repo）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——待推送后记录——见 verified-capabilities.md v0.3.2 段）。

## [0.3.1] — 2026-10-10

**真实表格导入 + 3D 比例修复版**（用户需求驱动：「这个是可导入的多个表格示例，还有这个 3D 地图太过于大了，应该根据所有的点调整默认展示比例」）。

### 修复 — 3D 地图默认视野（用户点名的核心 bug）
- **根因**（三层，逐层排查收敛）：① 首批数据 fitBounds 与视图切换 easeTo 并发互相打断——相机停在 DEFAULT_CENTER 而非数据区；② MapLibre 的 pitch 感知 fitBounds「恰好容纳」解在大倾角下过于保守（黑龙江数据实测 zoom 2.84 ≈ 全球视野，塔群挤成一撮占屏宽 ~3%）；③ `fitGeoBounds` 读瞬时 pitch 在「挂载即 3D」时误判为 2D。
- **修复**：`fitGeoBounds(map, data, view)` 按**目标视图**适配；3D 分支弃用黑盒解，改用**墨卡托几何直解** `zoomForBBox3D`（bbox 弧度宽/高 × 容器像素 × 斜视压缩系数 → 塔群占屏宽 ~55%、等效高 ~30%，clamp [3, 7.2]），center 取质心 + 18% 纬度北移（pitch 58 的地面投影中心偏下补偿）；挂载初始 pitch/bearing 按目标视图设置（消除切换动画与首批 fit 的竞态）；2D↔3D 切换保留 zoom ±1.3 补偿（等效覆盖守恒）。
- **验证**：相机实测 (zoom 7.2, center 124.59/50.65, pitch 58) 与手算公式解**逐项吻合**；VLM 终评四项全过（塔群居中长条分布、塔/标签/链路可辨、构图专业无渲染错误）；F 键「适配视图」同路径生效。

### 新增 — port 资源类型（真实电信端口工作簿导入）
- 真实样例入库 `samples/telecom-port-multi-header.xlsx`（一个 sheet 内两种表头形态 + 逐字重复表头行的多段导出拼接）；导入端到端：2 imported（含带宽 0）/ 1 skipped（重复表头）/ 2 failed（表头 B 行走正常 E004 证据链）。
- migration **v7**（`port_resource_type`，首个 `ForeignKeysOff` 迁移）：resources 的 CHECK 重建扩展 `'port'`（专用连接事务外关 FK → 事务内全列复制重建 → `foreign_key_check` 空结果门禁 → 恢复 FK）；virtual_devices 不动（v6 已含 geo 列）。
- `portSpec` 16 字段：**复合自然键** `device_ip + port_name`（`|` 连接落库，如 `12.1.53.24|bvi2.1008`；单键类型 join 恒等原值，零行为扰动）；`bandwidth_mbps` 无 IntMin（真实表带宽可为 0）；`device_ip` 保持 ipv4 值列而非 device ref（端口工作簿自含导出，设备行可未入库，避免 E003 拒整表）；含 geo_lat/geo_lng/geo 同设备可选坐标列。
- 导入器**复合自然键**改造（E002/E001/幂等重导/`lookupExisting` 全走拼接键）；**重复表头行**检测跳过（≥3 非空格且逐格等于列头 → skipped 专属消息、零 issue）。
- 前端：`ResourceType + 'port'`；`TYPE_SPECS` port 镜像 + `naturalKeyFields()` 数组化（keyColumns 双键全量）；device 补 geo 三列前端镜像（v0.3.0 的 UI 缺口——此前界面上无法映射「装机经纬度」列）；i18n 全量标签 + `chip--port` 徽章。

### 测试
- Go 194 → **200**（+6：port 建议/复合键校验 ×4、port 导入端到端（含表头跳过/复合 E001/E002/幂等/单键回归）、迁移 v7 升级含 port 落库断言）；既有断言零删除零放宽，仅 `TestMigrationV6UpgradesV5Database` 的版本期望 6 → 7（v7 自动级联，契约演进）。
- `gofmt -l .` 空 · `go vet` 干净 · 14 包全 ok · `tsc -b` 0 错误 · `npm run build` ✓。

## [0.3.0] — 2026-10-10

**地理视图版**（用户需求驱动：「能把现实地理 2D 地图和 3D 地图也纳入设计吗？可以视角切换」）：设备地理坐标全链路管道（导入列 → 资源属性 → 虚拟设备 → 拓扑 API）+ 画布三视图切换（**拓扑图 | 2D 地图 | 3D 地图**，MapLibre GL 懒加载）——真实深色地理底图上按经纬度落设备、链路状态分色、3D 倾斜视角塔体、视角切换与表格双向联动。**红线：geo 绝不进入规范状态哈希**——坐标是展示元数据，含/不含坐标的同业务状态哈希逐字节一致（回归测试双层证明）。194 个 Go 测试全绿；tsc 0 错误；浏览器控制台零警告零错误。

### 新增 — 映射与导入（internal/mapping · internal/importer）
- 设备 schema 三个**可选**字段（仅设备）：`geo_lat`（纬度，float [-90,90]）、`geo_lng`（经度，float [-180,180]）、`geo`（经纬度组合列，两个数字、顺序不限）；同义词梯子覆盖 经度/纬度/lat/lng/lon/GPS纬度/GPS经度/北纬/东经/经纬度/装机经纬度/坐标/GPS坐标/A端装机经纬度/位置
- 三个新 FieldKind（`lat`/`lng`/`geo`）的 E004 校验，中文原因：`不是合法的纬度（-90 到 90 的数字）`、`不是合法的经度（-180 到 180 的数字）`、`经纬度应为两个数字（如 126.63,45.75）`；浮点范围检查在 Kind 分支内实现（整数区间机制不受影响）
- **组合列拆分（中国边界启发式）**：`f1∈[73,136] ∧ f2∈[3,54]` → (经度,纬度) 高德惯例；`f1∈[3,54] ∧ f2∈[73,136]` → (纬度,经度)；境外/歧义坐标不硬失败（取合法解读，默认 `lng=f1, lat=f2`）；分隔符支持半角/全角逗号、分号、空白；拆分值先落属性再走与分列相同的校验；**分列优先**（纬度+经度两列同时提供时组合列不拆分，原始值保留溯源）
- 拆分结果以十进制原码串（`FormatFloat 'f' -1`）写入资源属性 `geo_lat`/`geo_lng`，同时保留原始 `geo` 属性

### 新增 — 存储与仿真（internal/domain · internal/repo · internal/sim）
- `domain.VirtualDevice.GeoLat/GeoLng *float64`（nil = 无位置）；迁移 **v6**（`device_geo_columns`）：`ALTER TABLE virtual_devices ADD COLUMN geo_lat REAL / geo_lng REAL`，可空无默认，v5 库打开自动升级、旧行 NULL；仓储层 `sql.NullFloat64 ↔ *float64`、Insert 对 nil 写 NULL
- 物化解析资源属性 geo_lat/geo_lng（合法且在界内才采纳，非法忽略属防御）；拓扑节点 `GeoLat/GeoLng`（JSON `geoLat`/`geoLng`，`omitempty`——无位置节点不带键）
- **撤销路径保位置**：`delete_device` 的逆操作快照 `DeviceSnap` 携带 `geoLat`/`geoLng`（camelCase；旧 inverse_json 缺键解码为 nil，兼容）
- **哈希红线**：`hashDevice` 不含 geo（`internal/sim/state.go` 未改动）；`TestWorkspaceHashIgnoresGeo` 双层证明——两个全新库分别导入含/不含 经纬度 列的同数据工作簿，物化哈希逐字节相同；单元级直接清空已加载状态的 GeoLat/GeoLng，hashValue 不变

### 新增 — 测试数据与演示
- `cmd/testdatagen -geo`：新增 `testdata/geodevices.xlsx`（设备 sheet + 经度/纬度 分列，10 行）与 `testdata/geocircuits.xlsx`（电路 sheet，10 行）——黑龙江真实网络孪生（设备编码/名称/型号/IP/电路编号取自运营商真实导出，**坐标为演示近似值**）；哈尔滨→漠河北南干线 + 地震局 10M 长专线 + 基站支路；确定性生成（固定文档属性，重跑 sha256 一致）；**既有 4 个合成工件零改动**（sha256 前后一致）
- `scripts/demo-geo-lab.sh`：导入两本地理工作簿 → 物化「黑龙江-地理孪生演示」（描述注明坐标为演示近似值）→ GD-202366 两端配置 10.61.0.1/30 ↔ 10.61.0.2/30 → 关断 CKT-QQH-JGDC-10G（干线中段红色故障）→ 起 serve 随机端口 curl 拓扑 API 展示 geoLat/geoLng
- CLI `lab workspace create` 新增可选 `--desc`（透传 workspaces.description，此前仅 HTTP 可设）

### 已知边界（诚实清单）
- 检查点恢复重建被删设备走 `create_device`（封闭操作面无 geo 字段）→ 重建设备无坐标（状态哈希不受影响；撤销路径不受影响）
- 3D 塔为示意级数字孪生（塔高 = 设备连接数），非物理仿真渲染（无材质光影/流量驱动高度）；VLM 终评 2D 7.5 / 3D 6.5 / 综合 7.0
- 地理底图依赖公网瓦片服务（ESRI anonymous），离线时自动降级为无栅格深色网格模式（设备/链路本地 GeoJSON 恒可渲染）

### 新增 — 前端地理画布（web/src/components/lab/GeoCanvas.tsx，GEO-2）
- **三视图切换**：画布工具带新增分段控件「拓扑图 / 2D 地图（俯视 · 设备按真实经纬度落位）/ 3D 地图（斜视 58° · 塔高示意设备连接数 · 右键拖拽旋转）」，按工作台持久化（画布 v2 存档新增 view 字段，兼容旧档）；全部设备无坐标时显示引导空态 +「返回拓扑图」按钮；部分缺坐标时右下角 chip 提示「N 台设备未显示（无坐标）」
- **maplibre-gl 6.13.0（精确锁定）懒加载**：`await import('maplibre-gl')` 独立异步 chunk（主包不增重；worker 经 vite `?url` 哈希资源发射）
- **设备 HTML Marker**：状态色圆点 + 编码牌（`HLB-A4`），键盘可聚焦（tabindex + Enter/Space 触发选择），点击/选中与拓扑图 tap **同一套选中语义**（右侧表格同步定位、选中态强调环）；悬停复用画布左上信息条；L 键标签开关（隐藏编码文字保留圆点）
- **链路 GeoJSON 图层**：up #5b8fd9 2px 实线 / down #ff5252 3px 红虚线（rAF marching-ants 动画，prefers-reduced-motion 关闭）/ 选中 #2f9dff 4px；端点缺坐标的链路跳过并计数
- **3D 塔（fill-extrusion）**：仅 3D 视图可见，像素高 18 + 连接数 × 10；2D↔3D easeTo pitch 0|58（3D 同时 bearing -30 斜视角，700ms，reduced-motion 时 0）；拖拽旋转/俯仰为 MapLibre 原生右键/Ctrl 拖拽
- **地理控制**：NavigationControl（visualizePitch 指北针 + 缩放）+ ScaleControl + 适配视图（fitBounds，F 键）/ 指北复位；首批数据自动 fitBounds（maxZoom 9）
- **zen 全屏兼容**：ResizeObserver → map.resize()，1920×1080 下地图 1888×999 满幅、pitch/塔体保持；Esc 链与拓扑视图一致；图例（地理变体）含塔高说明

### 修复 — 底图与 3D 塔两处 P0/P1（编排者终验发现并动手）
- **P0 底图水印砖（取证与替换）**：CARTO `basemaps.cartocdn.com/dark_all` 对匿名 IP 统一返回 2513B 的「API KEY REQUIRED」水印砖（浏览器内 fetch z3/z4/z10 字节级相同的错误砖；截图左区亮像素 18.3% = 文字墙；HTTP 200 故瓦片 error 降级不触发）→ 替换为 **ESRI Canvas/World_Dark_Gray_Base + Reference 双层栅格底图**（免密钥、深色画布风格匹配、Reference 层提供地名/边界注记，注意 z/y/x 路径顺序）；修复后 VLM 确认真实地理内容（满洲里/海拉尔/根河等地名可见）
- **P1 3D 塔不渲染（两处叠加根因）**：① 塔高表达式对 `['zoom']` 直接做除法运算违反 MapLibre 规范（paint 属性里 zoom 只能作顶层 interpolate 输入）→ 样式校验失败、`getStyle()` 返回 undefined；修复：顶层 `interpolate exponential-2` 双停止点（z0→9e4 × towerPx、z16→9e4/2^16 × towerPx，几何插值恒等于 9e4×2^zoom）② 塔 footprint 固定 0.0016°（≈120 米）在省级缩放下仅 0.04px，亚像素多边形光栅化不出面；修复：footprint 改为恒定 7px 像素宽（`buildTowerCollection(topology, zoom)` 纯函数按当前 zoom 逐设备换算，zoomend 防抖 150ms 重建，`applyDataToMap` 不再触碰塔源防清空）；修复后 `queryRenderedFeatures(towers)`=10/10、VLM 确认「明显 3D 立体柱状物、高度不一、数字孪生感较强」
- **离线降级匹配**：瓦片错误源判定覆盖 arcgisonline 主机名；降级样式重建时塔数据与可见性一并回填

### 验证 — GEO-2 前端（agent-browser 1920×1080 真浏览器）
- 2D：ESRI 底图真实渲染（纹理 425 色 + VLM 确认地理内容与地名）；pitch=0；标记点击 → 表格行选中（HLB-MH-BS1 已停机同步）；红虚线故障链路可见
- 3D：pitch=58.0 / bearing=-30 / zoom=3.2；塔 10/10 渲染；zen 3D 全屏 1888×999 满幅
- 快捷键：F fitBounds（黑龙江 bbox）/ L 标签往返（10 codes ↔ 10 nolabel ↔ 0 恢复）
- 空态与持久化：无坐标工作台引导 + 返回拓扑图可用；重载后持久化视图自动恢复
- 稳定性：15 次快速视图切换后控制台 0 error 0 warning；拓扑视图回归（布局/路径模式横幅/节点渲染/minimap）抽查通过；截图归档 `download/ui-screenshots-v0.3.0-geo2/`（22 张）

### 验证 — GEO-1 后端
- 门禁：gofmt 空 / `go vet` 干净 / 全部测试通过（**+13 个新测试**，覆盖：三个 geo Kind 的 E004、拆分启发式（双序/中文逗号/空格/分号/境外/非法）、导入器端到端（组合列/分列/分列优先/非法拒绝）、物化→拓扑 geo 流转、哈希红线回归、delete→undo 位置保留、v6 迁移升级（v5 库活数据 + 幂等重开）、geo fixtures 不变量）
- 冒烟（真实二进制 + curl）：导入 geodevices/geocircuits → 物化 10 设备/10 链路 → 拓扑 API 10/10 节点携带 geoLat/geoLng（哈尔滨枢纽 45.773/126.636、漠河站 down）→ 故障注入后 CKT-QQH-JGDC-10G down；ops/undo/checkpoint 往返不受影响（留档 `/home/z/my-project/tmp/geo1/`）

## [0.2.3] — 2026-10-10

**画布全屏工作台版**（用户反馈驱动：「画布的组件太简陋了，画布的展示区域太小了，不能全屏工作」）：画布从「被四层壳挤压的 948×760 面板」升级为**可全屏的一等工作区**——zen 全屏模式（1080p 下 1920×1080，约 2.9 倍面积）+ 工业级画布交互（右键菜单 / PNG 导出 / 缩放指示 / 手动排布持久化 / 快捷键）+ 分栏拖拽与右栏折叠。全部改动仅在 web 前端 5 个文件（后端零改动，红线保持）。181 个 Go 测试全绿；tsc 0 错误；控制台零警告零错误。

### 新增 — zen 全屏工作模式（TopologyCanvas + LabWorkspace + global.css）
- **一键全屏**：工具带「全屏」按钮 → 画布卡片 `position:fixed inset:0 z-60` 接管整个视口（盖住侧栏/顶栏/页头/右面板）；优先 `requestFullscreen()` 原生全屏（目标 `document.documentElement`，Toast z-100 仍浮于其上），无权限/不支持时静默降级为应用内 CSS 全屏；`fullscreenchange` 监听同步（浏览器 Esc 退原生全屏时 CSS 全屏一并退，并顺带收起右键菜单——避免「盖着但非全屏」与菜单残留）
- **Esc 逐级链路**（不一键全清）：右键菜单 → 路径追踪/邻域聚焦痕迹 → 退出全屏；对话框（焦点陷阱）打开时让路
- **左上浮动选中详情卡**（全屏时右侧表格不可见）：设备显示编码/名称/状态/接口 up-down/IPv4 列表（超 5 个折叠「等 N 个」）、链路显示两端与端口（`DEV-010 · eth1 ↔ DEV-011 · eth0`）、接口显示所属与地址；无选中不显示
- **全屏工具带深色化**（与画布底色一致）+ 退出按钮；zen 状态 sessionStorage 持久化（刷新保持）
- 容器尺寸变化 `cy.resize()` 补偿（rAF 一次补齐，视口 zoom/pan 保持）

### 新增 — 画布工业级交互（FX-6）
- **右键上下文菜单**（无 cxtmenu 插件，`cy.on('cxttap')` + 自渲染菜单）：设备四项（邻域聚焦 / 设为路径追踪起点 / 在表格中定位 / 复制设备 ID）、链路两项（表格定位 / 复制链路 ID）、空白两项（适配视图 / 重置视图）；a11y `role=menu/menuitem` + 打开后焦点入首项 + Esc 还原打开前焦点 + ↑↓/Home/End 导航；落位鼠标右下、越界向对侧翻转；菜单外 pointerdown 捕获阶段关闭
- **PNG 快照导出**：`cy.png({full:true, scale:2, bg:'#0e1626'})` 保证不透明深色底，文件名 `netops-topology-<workspaceId>.png`
- **缩放百分比实时指示**：`cy.on('zoom')` rAF 节流，缩放控件旁深色 chip（如「62%」）
- **手动排布位置持久化**：拖动结束（160ms 合并窗口）全量 {id,x,y} 存 localStorage 按工作台隔离；重载恢复位置（等价锁定语义）；切布局/重置视图自动清除并重排；隐私模式 try/catch 降级
- **键盘快捷键**：F 适配视图 / L 链路标签开关 / Esc 链级；输入框聚焦、对话框打开、修饰键（Ctrl/Cmd/Alt）、长按重复均不响应

### 新增 — 画布工作区布局（FX-5）
- **页头紧凑化**：页头卡片 180px → 97px（不删信息不删按钮，仅压缩 padding/间距），1080p 画布渲染高度 717 → **760px**（`calc(100vh - 237px)`）
- **分栏拖拽**：6px 把手（`role=separator` + `aria-valuenow` + 左右方向键 ±2% + pointer capture 拖动 30–85%），拖动中 `col-resize` 光标 + `user-select:none` + Cytoscape resize 挂起（拖完一次补齐），比例 localStorage 按工作台持久化
- **右栏折叠**：一键单列（画布占满工作区宽 1618px@1920），localStorage 持久化

### 修复 — 体验缺陷（编排者终验发现）
- **路径模式引导横幅从未出现（时序缺陷）**：切换到路径模式后横幅不显示「点击起点设备」——挂载时序上 mode effect 先设 pick-a、topology 异步到达后数据同步 effect 又 `clearModeArtifacts()` 覆盖，而 mode 值不变时 effect 不会重跑。修复：pick-a 态**不入状态机**，改为渲染层从 `mode==='trace' && trace===null` 直接派生（数据刷新/清除路径后自然回到引导态）；引导态 × 按钮语义改为「退出路径模式」（否则清完又立刻派生回来，横幅关不掉）
- **原生全屏退出菜单残留**：浏览器 Esc 退原生全屏时 keydown 被浏览器消费，页面链路收不到 → 右键菜单残留悬浮。修复：`fullscreenchange` 同步退 zen 时顺带 `closeMenu`

### 验证
- 门禁：gofmt 空 / vet 干净 / 181 测试 PASS（版本断言动态化后无硬编码）/ tsc 0 错误 / vite build / `make build` 29.8 MB 嵌入
- 真浏览器（agent-browser 1920×1080）：
  - zen：非全屏 948×760 → 全屏 **1920×1080（2.88 倍面积）**；原生全屏 + reload 后 CSS zen 降级保留；Toast z-100 浮于全屏之上
  - Esc 链级：菜单开 Esc 只关菜单 / 路径完成 Esc 只清路径回引导态 / 引导态 Esc 直通退全屏；原生全屏 Esc（浏览器消费）→ 同步退 zen + 收菜单
  - 分栏：拖 60→70（画布 948→1144，reload 持久化）/ 键盘 72→76（aria-valuenow）/ 折叠 1618px（reload 保持）/ 展开恢复
  - 画布交互：右键空白菜单（适配/重置）+ 越界翻转；缩放 chip 86%→55% 实时；拖动节点 reload 位置保持；PNG 导出落盘 1924×1164 全不透明
  - 引导横幅：reload（mode 持久化恢复）→ 显示「路径模式：点击起点设备」；点起点 →「已选起点 DEV-002」；点终点 →「DEV-002 → DEV-007 · 5 跳 · 经 DEV-003、DEV-004、DEV-005、DEV-006」；Esc 清除回引导
  - zen 详情卡：链路版「CKT-010 运行中 两端 DEV-010·eth1 ↔ DEV-011·eth0」
  - 控制台全程零 error 零 warning
- 截图留档：`download/ui-screenshots-v0.2.3-fx6/`（16 张）+ `download/ui-screenshots-v0.2.3-fx5/`（25 张）+ `download/ui-screenshots-v0.2.3-final/`（编排者终验 8 张）

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
