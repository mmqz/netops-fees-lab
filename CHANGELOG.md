# Changelog

本项目所有显著变更记录于此。格式参考 Keep a Changelog；版本遵循语义化版本（SemVer）。

## [0.6.3] — 2026-10-12

**用户反馈地图轮**（issue #12/#13/#14 一次收口 + 发布断档修复）。核心：针对用户对 v0.6.2 现场反馈的三条缺陷——2D/3D 相机「锁在一起」、初始视野比例不合适、地理高程支撑不可见——给出根因级修复；并修复发布流水线断档（v0.4.0–v0.6.2 只有 tag 没有 Release，用户实际在跑 v0.3.0，之前多轮地图修复从未送达）。附带全仓 `gofmt` 空白规范化（6 个历史空格缩进文件）。

### 修复 — issue #12：2D/3D 相机锁联 + 初始视野比例不合适
- **相机锁联**：GeoCanvas 单实例承载 2D/3D，center/zoom 全程共享（仅 ±1.3 zoom 补偿 hack），在 2D 平移后切 3D 即继承，反之亦然——「两个图锁在一起」。修复：**每视图独立相机快照**——离开视图时存（center/zoom/bearing/pitch），进入时恢复；首次进入按该视图 fitBounds；经拓扑图中转 = 组件卸载重挂、快照丢弃 → 重新适配（行为可预期）。±1.3 补偿 hack 移除；用户在 2D 的自定义 bearing 不再被切回时强制归零（回归 G2 断言钉住）。
- **默认比例**：2D fitBounds `maxZoom` 9 → **12.5**（城市尺度数据不再被压远、同城设备初始视野即可分开）；留白 80px 定值 → **容器短边 8% 自适应（夹 [24,80]px）**（小画布上固定 80px 吞掉 1/3 视野）；3D 上限 7.2 → 8.6（城市尺度 3D 近观塔体与地形）；聚簇展开 `maxZoom` 从「当前 +2.2」升级为 **max(当前 +2.2, 成员 bbox 的 cameraForBounds 解)**——哈尔滨 3 台一次点击即从 z4.8 拆簇到 z11.5（旧版需点两次）。

### 新增 — issue #13：地理高程支撑可见化（等高线 + 自适应夸张 + 海拔读数）
- **根因**：v0.6.2 的 `setTerrain` 已绑定 dem 源，但整省远观（z≈5.5，视域 ~400km）1500m 起伏 ×1.2 夸张占屏 <1%——**视觉全平**（VLM 复核截图确认）；2D 只有 hillshade，无等高线与海拔读数——「地理高度地图」缺位。回归只断言 terrain 绑定，未断言视觉有效性。
- **等高线图层**：`scripts/gen_contours.py` 从同一批 97 块 z8 terrarium 瓦片 marching squares 生成——**200m 首曲线 / 500m 计曲线 + 计曲线高程标注**（18,604 条折线、29 万点、RDP 1.5e-3° 简化），gzip 1.4MB 随二进制内嵌；**GCJ-02 预投影变体**（逐顶点 wgs84ToGcj02，高德底图上不偏移）+ WGS84 变体双分发——等高线**任意底图可用**（不随 GCJ 底图禁用，区别于行政区划/铁路/公路叠加）。图层面板独立「等高线」开关（默认开）。
- **自适应地形夸张**：`terrainExaggerationFor(zoom)`——z<6.5 → 4（整省远观起伏可见）、6.5–8 → 2.5、≥8 → 1.3（近观回真实比例）；zoomend 后按当前档位重挂 setTerrain。浏览器回归 C3/C4 断言远观 ≥2.5 / 近观 ≤1.5，VLM 复核 3D 截图确认「山脉立体轮廓、山谷、凹凸感」可见。
- **光标海拔读数**：`geoElevation.ts` 客户端解码离线 terrarium 瓦片（fetch → ImageBitmap → OffscreenCanvas 像素 → `R·256+G+B/256−32768`）双线性采样，左下角实时 chip 显示「海拔 N m」；**GCJ 底图下单步近似逆偏移采样**（误差 <2m）；覆盖外显示「—（超出高程覆盖）」；瓦片 LRU 缓存 80 块、卸载清空。2D/3D、任意底图可用。回归 H1/H2：大兴安岭光标 772m、覆盖外降级「—」。

### 修复 — issue #14：发布断档——v0.4.0–v0.6.2 从未送达用户
- 事实核查：远端 tag 已至 v0.6.0、main 已至 v0.6.2，但 GitHub Releases 最新只有 **v0.3.0**——用户拿到的一直是旧二进制，其对「聚簇/地形/比例」的抱怨在 v0.3.0 上**全部字面成立**（v0.3.0 无聚簇、无 DEM、fit 更粗）。修复：v0.6.3 一站式发布（linux-amd64 二进制 + source tar.gz，含 DEM/等高线全量内嵌），发布后核对 `/api/health` 实报 v0.6.3 与资产清单；流程约定固化为**截屏仅存 repo `tmp/` 作回归证据，永不进入 download/ 与 release 资产**（用户明确要求）。

### 门禁与事实源
- Go 21 包全绿（347 顶层测试）；`gofmt`（含 6 文件空白规范化）/`go vet` 干净；`tsc -b` 零错；`vite build` 过（dist 14MB，含 97 DEM 瓦片 + 等高线双变体）；浏览器回归扩展至 **21/21 PASS**（新增：等高线默认可见/源加载、远观/近观夸张分档、等高线开关、相机独立性 G1/G2、海拔读数 H1/H2）。版本矩阵四处对齐 v0.6.3（domain.AppVersion / i18n.APP_VERSION / README MCP version / api.md 基线），`TestAppVersionMatchesChangelogHead` 钉死 CHANGELOG 头。

## [0.6.2] — 2026-10-12

**运维加固轮**（W17 单轮：剩余 6 个 open issue 全部处置 + 用户反馈的两条地图体验问题）。核心：把 v0.6.1 之后剩余的跨平台/契约类缺陷（#3–#8）一次收口——Windows 测试可跑、无名 sheet 不再静默零行导入、report --out 不再静默无输出、负数分页不再静默忽略、跨库错配一步可辨；地图侧响应现场反馈完成两件事——2D 设备**聚簇**（修「锁在一起/默认比例不合适」）与 3D 视图**真实离线 DEM 地形**（修「2.5D 无地理高度支撑」）。issue #10（能力差距）以 ADR-0011 设计决策记录处置。

### 修复 — issue #3：Windows e2e 测试构建的临时二进制缺 `.exe` 后缀（11 个测试失败）
- `internal/app/e2e_test.go` 增加 `exeSuffix()`（`runtime.GOOS=="windows"` → `.exe`），TestMain 构建产物在 Windows 可被 `exec.Command` 执行；`Makefile` 的 `build`/`build-testdatagen` 同步追加 `BIN_EXT`（`go env GOOS` 探测），`make build` 在 Windows 产出可运行的 `bin/netopslab.exe`。Linux 行为零变化。

### 修复 — issue #4：类型识别仅依赖 sheet 名 → 标准导出（Sheet1）整表静默跳过
- 新增 `mapping.SuggestTypeFromHeaders`：sheet 名无 SheetHint 时按**表头行**推断类型——逐 spec 统计字段命中数（exact/normalized/synonym 同 bestMatch 阶梯），**完整自然键在场 + 必填字段过半**才合格（无身份键不导入，宁跳勿错），分最高者胜、同分取 Specs 序（确定论）。`Suggest` 与 `SuggestMapping` 均接入：issue 复现的 `Sheet1` + 设备名称/端口名称/设备IP/带宽表头 → 建议 port import（此前 action=skip →「导入成功但 0 行」的静默失败）。新增 `suggest_headers_test.go` 六测（含无自然键守卫、port 优先于 device 的判别、Sheet1 端到端不再跳过、列建议限定候选集）；`importer` 钉住旧行为的测试改钉新契约。

### 修复 — issue #5：Windows TempDir 清理失败（3 测试）与非法文件名（1 测试）
- modernc.org/sqlite 在 Windows 上释放文件句柄滞后于 `Close()` 返回，t.TempDir 的 RemoveAll 报「directory is not empty」→ 测试被记失败。修复：repo/sim 两包各置 `tempDirT`（MkdirTemp + 清理重试 ~1s、残余失败记日志不判败；POSIX 首试即成、永不 sleep）。`TestOpenPathWithSpecialCharacters` 的 `?` 路径在 Windows 非法 → 平台分支改用合法但同样考验 DSN 编码的字符（空格/`#`/`%`）。

### 修复 — issue #6：`report --format json --out` 退出码 0 但 stdout 空
- §10 契约（每命令 stdout 恰好一个 JSON 对象）此前对 `--out` 路径失守：文件写完、stdout 沉默，脚本无法区分成功与失败。修复：`--out` 时 stdout 输出 `reportResponse` 摘要（file/format/scope/totalResources/qualityScore/issuesBySeverity——对齐 export 命令的模式），json/html 两格式一致；无 `--out` 行为不变。新增 `TestCLIReportOutStdoutSummary` 双格式断言。

### 修复 — issue #7：API 分页负数参数静默忽略
- `limit`/`offset` 此前是**未知名**，任何值都被忽略（负值与「已生效」不可区分）。修复：`parsePagination` 支持 REST 风格双接口——`limit`（1–100，>100 钳制 100）+ `offset`（0 起，页号 = offset/limit+1）；非数值/负数/零 → 400 `bad_request`（消息说明取值范围，与枚举参数的严格性对齐）；两接口混用（page+offset、pageSize+limit）→ 400（显式冲突优于隐式优先级）。全部列表端点（resources/issues/batches/workspaces 三处）经共享 helper 一体获得。新增 `pagination_issue7_test.go`（校验 8 例 + 语义 3 例 + 既有 page/pageSize 行为回归）。

### 修复 — issue #8：CLI 与 serve 使用不同 DB 时无提示（易误判数据丢失）
- 三处可见性：① `GET /api/health` 新增 `dbPath`（服务端绑定库的**绝对**路径）；② 工作区类 404 信封追加「this server is bound to database <abs>——cross-database mismatch (serve --db vs CLI --db) is the usual cause」；③ `lab workspace create` 输出嵌入 `MaterializeResult` 并新增顶层 `db` 字段（绝对路径），与 serve 启动日志的绝对路径一行比对即分辨「连错库」与「数据丢失」。serve 启动日志同步升级为绝对路径（相对路径按工作目录解析会误导）。新增 health/404/create 三组回归测试。

### 新增 — 2D 设备聚簇（用户反馈：「2D 图锁在一起，默认没有按合适的比例缩放」）
- 根因：初始 fitBounds 是全省视野（数据跨 7° 纬度），同城设备屏幕间距仅 1–2px，编码牌堆叠成黑块。修复：`geoCluster.ts` 贪心 leader 聚簇（纯函数）——任意两个可见项（簇心/单例）屏幕距离 ≥56px，成员吸附最近 leader；`GeoCanvas` 在 move/zoom（rAF 节流）与数据/选中变化时重算，簇渲染为计数气泡（32px 圆徽 + 故障红边 + 故障角标 + 选中蓝圈，键盘可聚焦），被吞并设备的独立地标隐藏；**点击簇 fitBounds 成员 bbox 放大展开**（maxZoom +2.2），簇随缩放拆分。演示数据下：10 设备 → 2 簇（哈尔滨 3 + 大兴安岭 2）+ 4 个可读单例。GCJ 底图下投影与 Marker 同源，坐标系无关。

### 新增 — 3D 真实离线 DEM 地形（用户反馈：「2.4D 地图没有地理高度地图支撑」）
- **数据**：AWS Terrain Tiles（Mapzen 数据管线，SRTM/GMTED 融合，terrarium 编码，公开免密钥）——黑龙江 bbox（121.6–128.2E / 45–53.6N）z4–z8 共 **97 瓦片 ≈6.4MB** 随二进制内嵌（`web/public/mapdata/dem/`，`scripts/download_dem_tiles.py` 可重跑扩展）；z8 原生 ~300m/px，更高 zoom 由 MapLibre 父级上采样。
- **渲染**：`geoBasemap` 注册 `dem` raster-dem 源（encoding=terrarium，minzoom 4/maxzoom 8）+ hillshade 图层（底图之上、业务图层之下，深色主题柔和参数）；`GeoCanvas` 在 3D 视图按 `map.setTerrain({source:'dem', exaggeration:1.2})` 挂起伏、回 2D/关偏好显式卸载（2D 无视觉贡献但占渲染开销）；**图层面板新增「地形高程」开关**（默认开，localStorage 持久化；任何底图可用——DEM 是本地栅格，不受 GCJ/WGS84 底图限制）。setStyle 重建后 terrain 自动重挂（reapply 路径）。webui 对 `/mapdata/dem/**` 统一 1h 缓存（新增 PNG MIME + 磁头测试）。
- 浏览器级回归 `scripts/w17_browser_regression.sh`：**11/11 PASS**（初始聚簇 ≥2、点击展开 zoom 4.60→6.80、3D terrain=dem、hillshade 可见、页面实际拉取 76+ 张 DEM 瓦片、开关卸载/隐藏、零页面错误）。

### 设计 — issue #10：能力差距 ADR（数据异常检测/设备告警/趋势预测）
- `docs/adr/0011-ops-capabilities.md`：四个能力的优先级与数据模型设计（时序指标表、检测规则引擎与 E/W/I 规则目录的关系、告警状态机、预测算法选型），以及 MCP 工具面的红线决策（保持「恰好 5 个只读工具」冻结，分析类能力走 HTTP + Web 面板）。按 issue 建议以设计记录形式处置（不实现）。

### 门禁与事实源
- Go 21 包全绿（含新增：suggest_headers ×6、pagination/health/404 ×4、e2e report-summary + lab-create-db ×2、webui DEM ×1）；`gofmt`/`go vet` 干净；`tsc -b` 零错；`vite build` 过（dist 含 97 张 DEM 瓦片）；浏览器回归 11/11。版本矩阵四处对齐 v0.6.2（domain.AppVersion / i18n.APP_VERSION / README MCP version / api.md 基线），`TestAppVersionMatchesChangelogHead` 钉死 CHANGELOG 头。

## [0.6.1] — 2026-10-12

**地图修复轮**（W16-D 单轮开发 + W16-Q 独立核验；需求驱动为 v0.6.0 发布后的四条地图缺陷 issue——#1 画布高度塌陷、#2 Windows `.mjs` MIME、#9 3D 巨型光柱、#11 底图单一不可切换）。核心：把「能看地图」修成「能用于运维的地图」——画布塌陷与 Windows Worker 加载两处功能级失效归零；底图从硬编码 ESRI 极简变为四种可切换模式（高德街道/高德卫星/ESRI 极简/离线极简），黑龙江本地 OSM 行政区划/铁路/公路叠加数据随二进制内嵌分发（内网可用）；3D 柱体高度获得物理钳制与正确像素换算；新增图层面板让画布自己决定显示什么（铁路默认关闭，不干扰主观看）。

### 修复 — issue #1：2D/3D 地图画布高度塌陷（所有平台）
- `.geo-map` 与 MapLibre 追加的 `.maplibregl-map`（`position: relative`）同为 (0,1,0) 特异性、vendor CSS 后加载胜出 → `absolute/inset:0` 失效 → 容器高度塌 0、canvas 回退 300px、地图黑屏。修复：`global.css` 提升为 `.geo-canvas .geo-map`（(0,2,0)，结构上恒为父子、稳定）。浏览器级回归实测画布 756×544（修复前 756×0）。

### 修复 — issue #2：Windows 下 `.mjs` 静态资源 MIME 错误（MapLibre Worker 加载失败）
- Windows 注册表无 `.mjs` 条目 → `mime.TypeByExtension` 回退 `text/plain` → 浏览器拒绝将其加载为 ES Module Worker。修复：`internal/webui/webui.go` 显式钉死现代资源 MIME 表（.mjs/.js/.geojson/.wasm/.webp/.avif），不依赖平台注册表；Linux 行为不变（同值）。附带修复：目录路径（/assets/、/mapdata/）此前会走 FileServerFS 目录列表，现统一 SPA 回落 index.html。新增 `webui_test.go`（MapFS 隔离测试：MIME 钉死 / geojson.gz 头 / SPA 回落 + 真实 dist 冒烟）。

### 修复 — issue #9：3D 节点巨型光柱（宽度随缩放漂移 11 倍 / 高度 62km / 无地形说明）
- **metersPerPixel 基准修正**：v0.6.0 用 256px 瓦片基准（156543·cos/2^z），MapLibre 世界坐标实为 512px（worldSize=512·2^z）→ 系统性放大约 2×，叠加重建时机漂移实测底面宽 19→217px。修正为 78271.517·cos(lat)/2^z。
- **footprint 语义修正**：`TOWER_FOOTPRINT_PX=7` 为直径语义，构建取半宽（此前当半径用 → 实际 14px）。
- **高度物理钳制**：塔高（米）= 像素高 × mpp，数据驱动 `heightM` 钳制 [60, 4000]——不再出现数十公里光柱、柱体不再冲出画布遮挡底图。zoom 动画期间 rAF 节流同帧重建 footprint 与高度（恒定 7px 屏宽跟手，不再「动画结束才跳变」）。
- **3D 定位诚实声明**：UI tooltip 与 README 明示「3D = 倾斜相机示意（柱高示意连接数），非地形高程」（无 DEM 数据源，issue 建议的两个方向中取文档化方向）。

### 新增 — issue #11：底图四模式 + 离线叠加 + 图层面板
- **底图注册表**（`web/src/components/lab/geoBasemap.ts`）：高德街道（webrd style=8 栅格，中文标注/行政区划/交通）、高德卫星（webst style=6 + style=8 路网标注透明叠加）、ESRI 极简（原 v0.6.0 底图保留）、离线极简（无外网瓦片：画布底色 + 本地叠加）。高德 key 按维护者要求嵌入代码（栅格端点免鉴权，key 预留 Web 服务 REST 扩展）。attribution 随源切换（高德 / Esri / © OpenStreetMap contributors）。
- **GCJ-02 坐标契约**：业务数据按 WGS84 存储，高德（火星坐标）底图渲染前逐点 WGS84→GCJ-02（国界外原样返回）；设备/链路/塔/fitBounds 全走同一投影入口。离线叠加仅在 WGS84 底图（极简/离线）下可用——高德底图上叠加会偏移 ~500m 且信息重复，面板中禁用并提示。
- **离线地图数据**（Release `mapdata-hlj-2026-10-12` 资产，© OpenStreetMap contributors）：黑龙江全省 147 行政区划（省/市/县三级边界+中文名）、17257 铁路线（滨洲线等，含名称）、27170 干线公路（含国道编号 ref）——RDP 简化 + 属性精简后共 1.71MB（.geojson.gz），放 `web/public/mapdata/` 随 vite 构建进 `internal/webui/dist`、`go:embed` 进二进制（内网单二进制即可用）。服务端以 `Content-Encoding: gzip` 透明分发（浏览器自动解压，MapLibre 直接 fetch JSON）。
- **图层面板**（「画布决定显示什么」）：右上角常驻折叠钮，展开三分区——底图（radio×4）/ 叠加图层（行政区划、铁路、公路）/ 业务图层（设备、链路）。偏好 localStorage 持久化（隐私模式静默降级）。默认：底图高德街道、行政区划开（运维需知道设备属于哪个区县）、**铁路关**（用户要求不得干扰主观看，可一键开）、公路关、设备链路开。
- **瓦片失败降级看门狗**：连续 ≥3 次底图瓦片错误 且 距最近成功 ≥4s → 自动切离线底图（叠加图层保留、chip 提示）；看门狗周期复查覆盖「错误波停在宽限期内不再重发」的停滞态。双保险防假阳性：相机飞动取消的在途瓦片，MapLibre 同样发 `AJAXError: Failed to fetch (0)`（与真实断网无法从消息区分）——成功数据事件即清零计数。降级后面板 radio 反映 effective 状态（离线选中），点回原底图是真实变更（重置降级重试），绝不卡死降级态。

### 已知限制（诚实声明）
- **3D 非地形高程**：倾斜相机示意，无 DEM 地形数据（见上方声明与 ADR-0010）。
- **离线标注字形**：叠加图层中文名依赖 OpenFreeMap 字形 CDN（免密钥，CJK 覆盖）；完全离线时线/面几何照常渲染、文字标注静默降级为无（后续可自建本地字形库）。
- **塔高钳制的取舍**：省级缩放下柱体屏高 ~5px（物理高度上限优先于「恒定可见」）——设备可见性由 HTML 地标承担，柱体为辅助示意；放大到城市级恢复 40-58px 屏高与连接数差异。
- 开发沙箱的 Playwright 浏览器对页面世界的 autonavi 请求存在封禁（curl/worker/隔离世界可达、ESRI 同路径正常），瓦片加载在用户真实浏览器不受影响——回归脚本已改为证据型断言（详见 W16 worklog 调试记录）。

## [0.6.0] — 2026-10-11

**治理三部曲终章与数据双向闭环版**（五轮迭代：W11 治理债务收口轮 + W12-W14 三轮功能开发（回源导出 / 关系重挂 / 治理总览）+ W15 发布轮收口；需求驱动为 v0.5.0 各轮质量审查与审后处置留下的治理债务清单（target_missing 失实口径、W006 深链任一侧可达性、冒烟验证资产丢失、api.md 契约基线并存 P3）与「导入数据经治理后可回源」的双向闭环、治理工作量一屏入口的演进需求）。核心：治理三部曲终章（W005 属性 / W006 身份 / W13 关系——ref 字段确认时同事务改挂派生关系），数据从单向导入走向双向闭环（确定性 xlsx 导出、回源幂等），治理工作获得一屏入口视角（SQL COUNT 聚合总览），并清偿 v0.5.0 的口径与验证资产债务。

### 修复 — 治理债务收口（W11-D，零迁移）
- **target_missing 诚实口径**：`importer.Rollback` 删除链先删本批 pending 提案再删资源——标准回滚后改名提案已消失，apply 实得 **404**（W6 时代「回滚后 apply 得 409 target_missing」的文档/注释口径失实）。409 `target_missing` 是防御性守卫，唯一典型可达触发 = 新资源被其它路径直接删除而提案幸存；错误文案、api.md、import-and-validation、web 行内注释等全仓触点统一为诚实口径，`TestAPIRenamesRejectAndRollback` 更名并钉住「回滚 → deleted.renameProposals=1 → apply 404」旅程。
- **renames `resource_id` 任一侧过滤**：W006 issue 的 EntityID 落在新资源侧，深链打开新资源抽屉时 `GET /api/renames?resource_id=<新id>` 此前查不到（待确认面板不渲染）——List 条件改 `(resource_id = ? OR new_resource_id = ?)`（COUNT/SELECT 共享同一 where 与参数序），repo/服务/HTTP/CLI 四层测试钉死，双侧抽屉均渲染（W11-Q 真浏览器补测关闭缺口）。
- **w7/w8 冒烟脚本重建**：环境重置丢失的两份验证资产按原规格重建并实测 SMOKE-OK（SourceRef 版本化三轮 + 回滚随批消失 deleted 记数；PTP 脚本 4/0/0 + W006 协同 apply 键迁移 + 零警重导）。

### 新增 — 资源导出通道与回源幂等（W12-D，零迁移）
- **确定性导出**：`internal/exporter` 纯函数核心（Build/BuildBytes/WriteFile）——每个有资源的类型一张 sheet（8 张固定名字表，`SuggestType` 回投不变量钉死）、表头 = spec 全部字段 Label、行 = 当前属性快照（含 W005/W006 apply 后状态）、行序 = natural_key 字典序。excelize v2.11.0 输出全链无 wall-clock（zip 条目时间戳恒 1980-01、文档属性时间为库内常量），钉**字节级确定性**：同库同参数两次导出逐字节一致、输入顺序无关、CLI 与 HTTP 跨通道字节相同；文档属性时间为常量（真实导出时刻不落盘）的代价在 api.md 与 §19 两处诚实声明。
- **键分量三规则回填**（朴素 `split('|')` 取首段必破坏幂等——base_station 退化键「哈尔滨|基站名称」会写错编码列，负对照测试红）：①单键类型键列 = 键整体不拆（键含 `|` 亦无损）；②复合键（port/ptp_port）末分量 = Resource.Name，`TrimSuffix(key, "|"+Name)` 精确还原前缀（分量自身含 `|` 也无损）；③条件键（base_station）退化行按 KeyFallbackFields 非空属性值拼接恰等于 natural_key 判定——主键列置空、兜底列走属性值（回源重新派生同一退化键并复记 W004，身份语义保真而非把站名洗进编码列）。W005 致兜底键漂移的行（city 事后补）落主键路径——键仍精确还原，编码列内容非真实系统编码（已知精度限制，文档诚实声明，测试钉死该例）。
- **回源幂等（验收核心）**：导出 xlsx 直接走既有 analyze → suggest → import 管线导入全新库——建议映射全字段完整回填（实测置信 0.75×65 + 0.9×1——Label 走同义词档而非 exact 档，非 1.0，docs 两处如实钉）、八类型资源集合与关系集合（ref 重新派生）双库等价（CLI e2e 含 W005/W006 apply 后状态 + 退化键三形态；w12 冒烟 80 资源/60 关系 sqlite 级等价）；同库回导纯 skip 且零行级 issue。
- **三通道**：HTTP `GET /api/exports/resources?types=`（200 + xlsx 双头；types 逗号分隔、归一 Specs 序、去重、空段跳过，坏值 400 点名合法值清单）、CLI `netopslab export --db --out [--types]`（stdout 单 JSON `{file,types,totalRows}`；缺 --out/坏 types exit 2）、资源页「导出资源」按钮（filter bar 末尾常态可见，当前类型筛选进 types 参数、搜索关键词与状态筛选不参与——三处同口径）。空库导出成功但仅含中性占位空 sheet「资源导出」（重导入 422 `empty_workbook` 诚实拒绝）。
- **E003 部分导出诚实声明**：`?types=ip_pool` 部分导出的文件独立回源会因引用缺失 E003 失败（行照实 failed + 证据链），api.md 与 §19 两处声明「部分导出文件不适合独立回源」（W12-Q 实测钉死）。

### 新增 — ref 属性变更的关系重挂（W13-D，零迁移，治理三部曲终章）
- **三分支语义**：W005 确认流对 ref 类字段（机房 `site_id`、地址池 `assigned_device`、电路 a_end/z_end）的 apply——①非 ref 字段只改属性（关系零触碰）；②ref 字段且资源已有派生边 → 新值按被引用类型自然键解析成功后在**同一事务内**把边改挂到新目标（`relationRetargeted: true`；谱系 `first_batch_id`/`source_refs`/`attributes` 原样不动——relations 表无 updated_at 列，改挂的审计线 = 变更记录本身）；③ref 字段无边（空旧值 W001 孤儿路径）→ 只改属性**绝不新建无谱系关系**（`relationRetargeted: false`，建边仍需重导——每条关系行锚定其工作簿证据，人工 apply 没有工作簿行可作谱系）。目标解析守卫对**所有** ref 字段 apply 生效（含无边分支——否则 apply 可把导入路径必拒的悬空引用写进 attributes 且永不自愈）。
- **409 `ref_target_missing`**：新值解析不到 → 409 拒绝（message 含字段/目标键/被引用类型），提案保持 pending、资源与关系**零改动**（目标解析在任何写入之前）。可达触发 = 提案记录后目标资源被其它路径删除（回滚/直删）——E003 解析先于 diff 检测，「新值悬空的提案」不可能由导入通道产生；四层（服务/HTTP/CLI/冒烟）测试用自然旅程构造：独立批次导入目标 → 提案 → 回滚 → 409 → 重导目标 → 同一提案 apply 200。
- **同事务原子性**：属性写、NameField 同步、改挂、提案置 applied 全在一个 `DB.Do` 里——注入测试钉死（改挂撞 UNIQUE（改名合并残留形态）→ 整个 apply 回滚，属性/提案/双边零残留）；UNIQUE(from,to,type) 安全性论证：from 与 type 不动仅 to 移动，撞边需要同 (from,type) 已指向新目标的第二条边——导入派生世界每个单值 ref 字段至多一条边且此时 W005 旧值守卫在上游否决提案，唯一理论来源改名合并在改挂时原子失败而非脏边集。a_end/z_end 多 ref 字段独立改挂互不干扰（双向不干扰测试钉死）。
- **`relationRetargeted` 全链透传**：列表项上为实时谓词重算（引用字段且当前有派生边 → true——pending 项作确认预览，web 渲染「确认后将同步改挂关系」提示；applied 与 dismiss 后同为重算值，仅语义预览）；apply/dismiss 响应上为操作事实（apply 重挂成功 true / dismiss 恒 false）。HTTP/CLI/web 三通道同字段名。
- **`mapping.RelationTypeFor` 单一映射**：ref 字段 → 派生关系类型的映射从 importer 私有函数迁至 `internal/mapping`（纯数据包、仅依赖 domain+stdlib）——导入建边与确认改挂共用**同一张表**，绝不第二映射（语义逐字节零漂移，表测试随迁）。

### 新增 — 治理总览（W14-D，零迁移）
- **只读聚合端点** `GET /api/governance/summary`（无参数恒 200 + no-store）：待确认双队列（属性变更/疑似改名 pending 计数）、决策审计（applied/dismissed/rejected）、问题分布（按严重级三固定键 + 按规则 Top 10——`ORDER BY COUNT(*) DESC, rule_code ASC` 钉死在 repo SQL，TopRuleLimit=10 平局按码序裁决）、资源/关系/批次形态小表（byType/byRelationType 动态键）。**全部 6 条 SQL COUNT GROUP BY 下推，零全表拉取零 len()**（包注释声明该纪律）。
- **确定性**：除 generatedAt（time.Now.UTC()，契约即墙钟、测试显式豁免）外全字段是库状态的纯函数——同库两次调用逐字段相等 + 六数据块 JSON 字节相等；空库恒全零（固定键 0 / 动态 map {} / byRule []）。
- **CLI 等价物与组合根**：`netopslab query --db <path> governance`（单 JSON 输出）与 HTTP 端点逐字段相等（generatedAt 豁免，冒烟钉跨通道相等）；组合根第 8 组装配（`internal/services` Governance 字段，wiring_test 指针恒等 8 组在表）；新增 4 个最小 COUNT repo 方法（AttributeChange/RenameProposal/Batch 各 CountByStatus + Issue.CountByRule）。
- **web 概览页「治理总览」区**：待确认双卡（N>0 时 `stat-value--error` 高亮 + 「去确认」跳 /resources）、问题分布（severity 三行 + 规则 Top 5 中文规则名）、形态小表、批次状态行；独立取数点 + 面板内降级（ErrorBlock compact 不阻塞页面其余区，真浏览器拦截实证；重试可恢复）。

### 发布轮（W15-D）
- 版本四处事实源统一 **v0.6.0**（`domain.AppVersion` + CHANGELOG 头 + api.md 契约基线 + web i18n；`TestAppVersionP2` 断言随发布适配并在注释声明）；CHANGELOG 0.6.0 段、docs/verified-capabilities.md v0.6.0 段（CI 运行态按 v0.4.0/v0.5.0 先例推送后补记）、README 版本行/能力清单/英文摘要/MCP version 对齐；api.md 文内 v0.6 W12-D/W13-D/W14-D 能力标注统一为 v0.6.0 口径（收口 W12-Q/W13-Q/W14-Q 的「契约基线 v0.5.0 与 v0.6 标注并存」P3）；W13-Q P3-2 补电路双端 List 预览断言（既有测试函数内，计数不变）；全仓 `v0.5.0` 残留逐处判定（版本事实点已升，历史段/时点事实/里程碑引用保留）。
- 发布验收全量实测：六门禁全绿 + `make build` + live serve（`/api/health` 实报 v0.6.0、`GET /api/schema` 8 类 / 89 字段 / 13 规则、`/` 与 SPA 路由及全部 chunk/worker/favicon 200）+ `testdata/devices.xlsx` 完整导入基线（30 行 27/0/3 + issues 4E/1W/27I）+ `samples/ptp-config-script.txt` 脚本通道（4/0/0）+ 六个冒烟脚本（w6/w7/w8/w12/w13/w14）全 SMOKE-OK + demo.sh + demo-lab.sh 全过 + MCP 真进程 stdio 握手（serverInfo v0.6.0、恰 5 工具）+ 治理总览与导出端点 live 实测 + 全仓凭据扫描零命中。

### 测试
- Go **331 个顶层测试**全绿（v0.5.0 基线 296 + 本里程碑四轮新增：W11 +0 / W12 +13 / W13 +9 / W14 +13 = +35，`go test -list '.*'` 顶层函数实测口径——W12 分项 exporter 9 / api 2 / app 2，W13 分项 changes 6 / repo 1 / api 1 / app 1 / mapping +1 与 importer −1（`TestRelationTypeFor` 迁移净零），W14 分项 governance 4 / repo 4 / api 2 / app 3；W15 发布轮零新增测试函数（版本断言随发布适配 + W13-Q P3-2 断言补入既有函数）；键回填三规则 / 字节确定性 / 回源双库等价 / 同事务改挂 / 409 零改动 / 绝不建边 / COUNT 下推 / byRule 平局裁决等；既有断言零放宽，唯一例外为 `TestAppVersionP2` 版本断言随发布适配【注释声明】）。门禁六项全绿：gofmt 空 · vet 0 告警 · 20 含测试包全 ok · `-race` 0（changes/repo/api/governance 等重点包）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——**已补记（三绿两受阻）**——W11-W13 三提交 CI 全绿（38151970576 / 38154941722 / 38158064643），W14/W15 两 run 因账号 Actions 分钟配额耗尽在 runner 启动层失败（0 steps 非代码原因，本地六门禁全绿佐证；恢复路径与证据链见 docs/verified-capabilities.md v0.6.0 段））。
- 端到端冒烟：W12/W13/W14 各轮独立冒烟脚本（w12/w13/w14）+ W11 重建的 w7/w8 + 既有 w6 + demo.sh / demo-lab.sh 全脚本可重复。

## [0.5.0] — 2026-10-11

**身份治理闭环与来源谱系版**（四轮迭代：W6-W8 三轮功能开发 + W9 重构收敛轮 + W10 发布轮收口；需求驱动为 R0-W5 发布时留下的下轮候选清单：名称改名的人工身份变更流、SourceRef 版本化、PTP 配置脚本导入通道、D2/D3 组合根收敛与四个语义尾巴）。核心：身份确认流补上最后一块拼图（改名合并人工流），来源证据从「首源」进化为「全史版本」，真实材料从三类工作簿扩展到第四类（配置脚本），服务装配收敛到唯一组合根。

### 新增 — 疑似改名检测与身份合并人工流（W6-D，migration v12，W006）
- 名称类键类型（ip_pool / base_station / port）的新键导入行，在「旧键已从源消失」且属性全等时**照常按新资源导入**并发 **W006** + pending 改名提案（`rename_proposals` 表，纯 CREATE TABLE）。检测五条件保守判定（类型开启 RenameDetection / 旧键消失 / 属性全等 / 新旧行双射 / 匹配集非空洞），**绝不自动合并**。
- **人工确认通道**（资源域写仅限人类）：HTTP `GET /api/renames` + `POST …/{id}/apply|reject` 与 CLI `netopslab renames list|apply|reject`。apply 单事务合并身份——关系改挂旧资源、**键分量属性同步**（新侧空值保留库内值）、删除新资源、旧资源换自然键；代理键 `res_x` 与 SourceRef 不动（审计痕迹保留）；apply 时新资源已消失 → 409 `target_missing`（驳回建议文案）。reject 零改动。回滚不对称：删本批 pending、留终态审计线。
- 增量导入（部分快照）会误报假提案——保守方向，人工 reject 即可，四处文档诚实声明。
- 端到端：基站改名副本 1/1/2 + W006 → 提案 pending（matchedFields 11 字段）→ apply（旧 res id 保留持新键、终态 2 资源）→ 再导 0/2/2 零 warning（键分量同步后无假 W005，幂等闭环）→ 重复 apply 409 conflict。

### 新增 — 来源引用版本化（W7-D，零迁移）
- **skip 即谱系版本**：同键再导入（纯 skip 与 W005 diff-skip）对每批每实体追加一条 `source_refs`（行语义/计数/issues 零变化；E001 批内重复不追加）——`UNIQUE(entity,entity_id,batch_id)` 每批一行的 v1 既有约束即版本化，**零迁移**（无 v13 涉入，source_refs DDL 逐字节未动）。
- 读侧版本序：`ListByEntity` 改 `ORDER BY rowid,row_number`（rowid 单调分配=时间版本序，删除不重编号、新版本恒殿后）；资源详情 `sourceRefs[]` 展示全史（创建批次在前，`raw` 记录该批工作簿原话）；回滚随批消失（`batch_id ON DELETE CASCADE`，DELETE 批次响应的 `deleted.sourceRefs` 诚实计数）；MCP `resource_search` 仍匹配首源（创建批次引用）。
- 三轮导入 → `sourceRefs=3` 版本序（创建 / diff-skip / 纯 skip 各留 raw）；回滚后版本行随批消失、幸存序稳定。

### 新增 — PTP 配置脚本导入通道（W8-D，migration v13，ptp_port 第 8 类资源）
- 第 4 类真实电信材料：中兴 VRP 风格 PTP 配置脚本（`.txt`/`.cfg`）经确定性解析器（`internal/ptpconfig`：节上下文栈、行号错误、`IsScriptText` 嗅探）产出「PTP配置」虚拟 sheet，**完整复用既有 analyze → 映射确认 → 导入执行管线**（全规则 / 幂等 / 回滚 / 来源版本化 / 改名检测天然适用，无旁路）；解析失败 422 `script_parse_error` 带行号，非 PTP 文本 422 `unsupported_script`（xlsx 永远走 excelize）。
- 新资源类型 `ptp_port`（复合自然键 `设备型号|接口名称`，23 字段，RenameDetection=true 与 W6 协同）；migration v13：resources CHECK 八类型重建（v9 ForeignKeysOff 模板复刻，存量行逐字节保持、relations 零触碰）。**模型级模板身份**诚实声明（脚本只携带型号不携带设备实例身份 → 无 ref 无关系）。
- 样例 `samples/ptp-config-script.txt`（真实材料逐字节提取，无凭据）；端到端 4/0/0 零 issue、再导 0/4/0 纯 skip、改名协同 apply 键迁移。

### 重构 — 组合根收敛与读路径（W9-D，零行为变化）
- 新包 `internal/services` 成为服务装配唯一事实源：`Services{db, Importer, Validation, Query, Changes, Renames, Lab, Report, LabReport}` + `New(db, opts…Option)`（`WithClock` 统一注入确认服务时钟）；app / HTTP API / MCP 三消费方共享同一实例（指针级测试 `TestAppSharesOneServiceComposition` 钉死 7 组恒等 + API/MCP 贯穿）——「App 死装配 + MCP 自建引擎」两处 R1-P2 病灶消除。
- MCP `resource_search` 收敛 `query.Service.SearchResourcesWithSource` + repo `ListByEntityIDs` 单查批量化：**N+1（1+N 查）→ 恒 2 查**（与 N 无关），repo 层 query-count 回归卫兵防倒退；MCP 契约（5 工具 / 输出形状 / 校验文案）零漂移，工具描述补 PTP端口（W8 漏的触点）；batches/issues 读路径裁量留原地（薄 handler 已锚定组合根库）。
- 四个语义尾巴收口：`intMax` 投影进 GET /api/schema（vlan 1..4094）；HTTP 409 `target_missing` 直删新资源补测（W6-Q P3-5 盲区闭）；apply 后展示名随键迁移（renames + changes 双修，键类名称字段经 `newRes.Name` 回落——W8-Q P3-5 闭）；`sheetHint` 断言钉真实值 PTP（同步修正 W8 文档漂移）。

### 发布轮（W10-D）
- 版本四处事实源统一 **v0.5.0**（`domain.AppVersion` + CHANGELOG 头 + api.md 契约基线 + web i18n；`TestAppVersionP2` 断言随发布适配并在注释声明）；CHANGELOG 0.5.0 段、docs/verified-capabilities.md v0.5.0 段（CI 运行态按 v0.4.0 先例推送后补记）、README 版本行 / 英文摘要 / 中文定位句对齐；全仓 `v0.4.0` 残留逐处判定（版本事实点改 12 处、7 文件：domain 常量+注释 2 / sim_test 断言 2 / i18n 1 / api.md 2 / README 3 / teleagent 2；其余历史段 / 时点事实 / 里程碑引用保留）。
- 发布验收全量实测：六门禁全绿 + `make build` + live serve（`/api/health` 实报 v0.5.0、`GET /api/schema` 8 类 / 89 字段 / 13 规则、`/` 与 SPA 路由及全部 chunk 200）+ `testdata/devices.xlsx` 完整导入基线（30 行 27/0/3 + issues 4E/1W/27I）+ `samples/ptp-config-script.txt` 脚本通道（4/0/0）+ 五个冒烟脚本（w3-w8）全 SMOKE-OK + demo.sh + demo-lab.sh 全过 + MCP 真进程 stdio 握手（serverInfo v0.5.0、恰 5 工具）+ 全仓凭据扫描零命中。

### 测试
- Go **296 个顶层测试**全绿（v0.4.0 基线 244 + 本里程碑四轮新增：W6 +17 / W7 +6 / W8 +21 / W9 +8 = +52，`go test -list '.*'` 顶层函数实测口径——W6 分项 importer 5 / repo 3 / renames 5 / api 2 / app 2，W7 分项 importer 4 / repo 1 / api 1，W8 分项 ptpconfig 9 / workbook 4 / mapping 4 / importer 3 / repo 1，W9 分项 **services 2 / repo 2 / query 1 / renames 1 / changes 1 / api 1**；检测五条件 / 单事务合并 / 谱系版本序 / 解析器 golden / v13 迁移 / 指针级装配 / query-count 卫兵等；既有断言零放宽，唯一例外为 `TestAppVersionP2` 版本断言随发布适配【注释声明】）。门禁六项全绿：gofmt 空 · vet 0 告警 · 18 含测试包全 ok · `-race` 0（importer/repo/api/services 等重点包）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——**待推送后补记**——见 docs/verified-capabilities.md v0.5.0 段）。
- 真实材料端到端冒烟：W6-W8 各轮独立冒烟脚本（w6/w7/w8）+ 既有 w3/w4 + demo.sh / demo-lab.sh 全脚本可重复。

## [0.4.0] — 2026-10-10

**真实电信资源类型与身份治理版**（五轮迭代：W1-W4 四轮功能开发 + W5 发布轮收口；需求驱动为 v0.3.2 后提供的真实电信样例工作簿与 R4 身份模型审查裁决）。核心：从「三类合成台账」走到「七类资源、真实表格直导、身份键受治理、属性变更有审计」。

### 新增 — port 稳定自然键（W1-D，migration v8，闭环 R4 审查 P1）
- **根因**：v0.3.1 的 port 复合键 = `device_ip|port_name`——IP 是可变属性，设备重划 IP 即端口资源分裂。v8 换稳定键 **`device_name|port_name`**（spec 字段序拼接）；`device_ip` 降级为必填值列（进 attrs，不再入键）。
- migration v8：纯 SQL 快照三守卫（冻结快照 + 唯一认领 + 目标未被占用）就地 UPDATE；冲突行**保守保留原键**；存量 fallback 行（不可能携带 device_name 属性）逐字节保留；幂等由构造保证。`NameFieldOverride` 钉定 port 展示名 = port_name（防止 device_name 劫持「first *_name」启发式）。
- 真实样例回归：`samples/telecom-port-multi-header.xlsx` 2 imported / 1 skipped / 2 failed 不变，键 = `B2|bvi2.1008` 等；前端 `schema.ts` 镜像同步 + 向导正向提示。

### 新增 — base_station 资源类型与条件自然键（W2-D，migration v9，W004）
- 真实无线基站工作簿 15 字段直导（真实 14 列 + 预留 city）；**条件自然键机制**（spec 声明式 `KeyFallbackFields`，零类型硬编码）：主键 base_station_code 非空 → 编码即键；为空 → 键退化为 `(地市, 无线基站名称)` 兜底拼接（跳过空值，真实表无地市列 → 纯名称），行照常导入并记 **W004 身份键退化**（仅 imported 行、幂等重导不重记）；E001 判重在解析后的最终键上。
- migration v9：resources CHECK 封闭集扩 `'base_station'`（SQLite CHECK 不可 ALTER → v7 同构 ForeignKeysOff 12 步安全重建）；基站IP 为 string 值列（真实值带 /30 掩码，IP 属可变属性不作身份）；MCP 文案/前端类型枚举/双 chip/文档六处封闭集触点全同步。样例 `samples/telecom-base-station.xlsx`（4 imported / 4 failed / 3×W004）。

### 新增 — machine_room / site 资源类型与 belongs_to 跨表关联（W3-D，migration v10）
- 真实机房局站表（8 真实业务列）**同表双读**：先按局站类型导入（键 = 局站ID，12 位数字编码），再按机房类型导入同一 sheet（键 = 机房ID，17 位数字编码；机房 spec 的局站ID 列映射为可选 ref）。两类 ID 均 KindString——**前导零即身份，绝不数值化**；机房名称/局站名称/设备名称为昵称属性，IP 地址为可变属性。
- 局站先入库即自动派生 `machine_room --belongs_to--> site` 关系（E003 语义钉死导入顺序）；同表双命中建议 = 局站（注册序容器优先），映射确认页可切换。migration v10：resources + relations 双表 CHECK 扩容重建（ForeignKeysOff 安全形 + foreign_key_check 门禁）。样例 `samples/telecom-site-machine-room.xlsx`（site 2/0/4 → machine_room 4/0/2，belongs_to×3，幂等重导 0/4/2）。

### 新增 — 属性变更历史与人工确认（W4-D，migration v11，W005）
- 同键再导不再静默丢弃属性差异：diff 逐字段记为**待确认变更**（`resource_attribute_changes` 表 + **W005** issue，行仍 skip 幂等）；口径与 attributes 归集同源（非键非枚举非空、新值空不记、pending 同值去重），与 row_result/counts 同事务。
- **人工确认通道**（资源域写仅限人类）：HTTP `GET /api/attribute-changes` + `POST …/{id}/apply|dismiss` 与 CLI `netopslab changes list|apply|dismiss`；apply 携带旧值校验（漂移即 409 `attribute_conflict`）+ 同事务写入 + `updated_at` 刷新；**MCP 工具面 5 个冻结不变**（AI 不得 apply 属性变更）。
- 回滚不对称语义：回滚删本批 pending、**保留 applied/dismissed 审计线**（被审计资源若被回滚删除则记录随之级联消失，四处注释/文档一致声明）；前端 Resources 详情新增待确认变更面板。端到端：改基站IP 再导 → 0/4/4 + W005×1 → CLI apply 生效 → 同值三导零 warning（幂等闭环）。

### 新增 — 发布轮工程化（W5-D）
- **GET /api/schema（动态 schema 发现端点）**：`{"types":[…],"rules":[…]}`——types 为 mapping.Specs 的**忠实投影**（type/label/sheetHint/nameField(+override)/naturalKeyFields/keyFallbackFields/fields[name,label,required,naturalKey,kind,enum?,intMin?,refType?,synonyms[]]，由 `BuildSchemaDocument()` 机械生成，投影测试逐字段对照 Specs，禁止手抄漂移）；rules 为 domain 规则目录（12 码 + SeverityForCode + 一行英文释义）。**仅 HTTP 端点，不进 MCP**（工具面 5 冻结）；前端 `schema.ts` 镜像保留（注释注明可经 /api/schema 校验，稳定性优先不强制切换）。
- **uploads 垃圾回收**：删除 `dataDir/uploads/` 下未被任何 import_batches.workbook_path 引用的孤儿文件（**任何状态的批次都算引用**——含 failed/rolled_back：工作簿是 issue 证据的回读源）。落点 = `serve` 启动时（监听启动前串行执行，无并发上传窗口；RecoverInterrupted 同款 cmd 层先例，**MCP 进程零触发**）+ CLI `netopslab gc --db --data` 手动子命令（stdout 单 JSON 计数契约，n>0 时 slog Info 走 stderr）；删除失败记日志不致命；目录不存在容错为 no-op。
- **前端构建分包**（vite manualChunks 按依赖家族）：主 chunk **795.03 kB → 187.19 kB**（gzip 249.86 → 54.51 kB）；react 家族 166.24 kB / cytoscape 438.71 kB 独立 vendor chunk；maplibre-gl 维持 ADR-0009 懒加载异步 chunk（1,085.01 kB，本体不可再分的第三方单库——vite 唯一 >500 kB 警告即此，如实保留）；maplibre CSS 83.15 kB 随异步 chunk 懒加载（首屏 CSS 123.61 → 40.46 kB）。

### 测试
- Go **244 个顶层测试**全绿（v0.3.2 基线 207 + 本里程碑五轮新增：W1 +2 / W2 +7 / W3 +9 / W4 +12 / W5 +7 = +37，`go test -list '.*'` 顶层函数实测口径——迁移守卫、条件键、W004/W005 语义、belongs_to 语义链、schema 投影、GC 四态与 CLI 契约等；既有断言零放宽，仅 max-version 断言随迁移前进）。门禁六项全绿：gofmt 空 · vet 0 告警 · 15 含测试包全 ok · `-race` 0（importer/repo/api 等重点包）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——**待推送后补记**——见 docs/verified-capabilities.md v0.4.0 段）。
- 真实样例端到端冒烟（W1-W4 各轮均验）：port / 基站 / 机房局站样例的导入-幂等-回滚旅程、属性变更 apply/dismiss 旅程、demo.sh 与 demo-lab.sh 全脚本可重复。

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
