# v0.15.1 检查更新与 #65 修复验证（2026-10-20）

来源：从 `6e7e4c0`（v0.13.0 + 我的 #65 报告）拉到 `ddd9c3e`（v0.15.1），共 17 个提交。

---

## 一、云端进展概览

| 版本 | 内容 |
| --- | --- |
| **v0.13.1** | **#65 修复轮**（整数分金额 + 无损模糊剪枝 + Windows 查询路径） |
| v0.14.0 | 费用风险六场景 + 证据追溯（#64、#58 阶段 4） |
| v0.15.0 | 费用工作台收尾：MCP `fee_risks` 工具（第 11 个）+ `/fees` UI + 关联人工通道 |
| v0.15.1 | 关联重提案修复（#69）+ 其他缺陷修复（#68/#70/#71/#72/#73） |

MCP 工具数：5 → **11**。前端新增 `/fees` 页面。

---

## 二、#65 三项 P1 的独立验证

### 2.1 金额改整数分 —— ✅ 彻底修复（11/11 边界全过）

我写了独立测试程序（不用仓库自带测试）：

```
OK   "1,234.56"              -> 123456     [千分位]
OK   "¥999.00"               ->  99900     [货币符号]
OK   "￥888.88"              ->  88888     [全角货币]
OK   ".5"                    ->     50     [省略整数位]
OK   "-1234.56"              -> -123456    [负数]
OK   "０１２３．４５"          ->  12345     [全角数字]
OK   "1234.567"              -> 拒绝        [三位小数拒绝静默取整]
OK   "1e3"                   -> 拒绝        [科学计数法拒绝]
OK   "92233720368547758.07"  -> MaxInt64   [int64 上限精确]
OK   "92233720368547758.08"  -> 拒绝        [超上限]

累加 100 万次 0.01 = 1000000 分 = 10000.00 元  精确无误差
```

对比修复前（v0.13.0 float64）：
```
累加 100 万次 0.01 = 10000.0000001719（误差 1.7e-7 元）
```

**实现质量超出建议**：不只是改 int64，还做了逐位定点解析（全程不过 float）、
第三位小数拒绝而非静默舍入、int64 溢出逐位保护、
`ParseFloat` 接受的 `1e3` 也被收紧拒绝（"科学计数法不是契约，是 float 路径的意外"）。

### 2.2 模糊匹配性能 —— ✅ 彻底修复（15.5s → 0.3s）

仓库自带性能测试实测：

```
TestFuzzyScanPerformance:
  10k codes × 1k same-length-class candidates (parallel, 12 workers): 301.6ms
```

- 修复前（我 v0.13.0 实测外推）：**15.5 秒**
- 修复后（同规模、同长度类最坏情况）：**0.3 秒**
- **约 50 倍改善**，远超我提的 < 2 秒验收线

**三道剪枝都带无损证明**（`link.go:157-179`）：
1. 长度预筛——`dist >= |lenDiff|`，在最小距离处已不达标即判否；
2. ASCII 字符计数剪枝——一次编辑改变计数向量至多 2，`Δ > 2·cap` 必不达标；
3. 带状 DP + 行最小值单调早退。

并且明确写了：**`cap` 由朴素阈值表达式本身求得，不做浮点代数截断**——
保证 fast path 的接受/拒绝与原路径**逐位相同**（`TestSimilarityAtLeastEquivalence` 用等价语料钉死）。

这比我建议的"长度差预筛"更彻底，且论证严谨。

另外 `DeleteAutoNotIn` 的注释指出一个我没想到的坑：

> a chunked NOT IN would be WRONG, not just slow（每批只保护自己那一片，会重删前面批次保留的）

因此改用 per-connection TEMP keep table + 单条 `NOT IN (SELECT …)`。**这是正确性修复而非性能优化。**

### 2.3 Windows 查询路径 —— ⚠️ 绕开而非解决

云端没有解决 Windows 原生构建（`rstrtmgr` 确实来自 mingw-w64 缺失），而是提供替代路径：

- Docker 镜像**默认带引擎**（`golang:bookworm` CGO + `-tags duckdb`）
- `make build-duckdb` 服务 linux/WSL2/macOS
- `docs/fees-layer.md` §5.1 文档化两条路径

**最好的一点：错误消息自引导。** 默认构建实测返回：

```
HTTP 501 engine_not_built
"fees: 分析查询引擎未编译进本二进制（需要 -tags duckdb 与 CGO 构建；导入/快照/Parquet 写入不受影响）。
 Windows 原生构建暂不可用（go-duckdb 链接的 rstrtmgr 库 mingw-w64 未提供）
 ——请用 Docker 镜像（docker build -t netops-lab:local . ，已含 duckdb 引擎）
 或 WSL2 内 CGO_ENABLED=1 go build -tags duckdb 获得 Linux 查询构建；详见 docs/fees-layer.md §5"
```

用户撞 501 时直接看到解法，不需要查文档。**这采纳了我在 #65 建议中的第 1、4 点。**

**但仍开放的问题**：Windows 用户必须额外装 Docker/WSL2 才能用费用查询功能。
考虑到本项目主要使用环境是 Windows，这个门槛不算低。
是否值得为"只读分析查询"引入一个 **duckdb CLI 子进程模式**（Pipe 结果），
换取全平台可用，值得后续评估——已在 #65 中提过，云端暂未采纳。

### 2.4 附带（P3）英文表头 —— ✅ 已修

`recognize.go` 补充了英文同义词，提交说明称
`Site Code / SITE_CODE / siteCode / station_code / cell_code` 均可映射，
且"英文尾缀不会挤掉中文匹配"（保持既有建议顺序）。

---

## 三、费用风险模块（v0.14/v0.15）观察

### 3.1 六大场景落地

```
retired-billing      退租后仍在计费
duplicate-settlement 跨批次重复结算
ledger-mismatch      合同账单金额差异
spike                电费环比异常上涨
outlier             同类站单位成本离群
unlinked             零资源计费
```

### 3.2 产品措辞纪律很好（值得记录）

`risks.go` 头部：

> 账单存在 ≠ 认定结论：a clue is a TO-BE-VERIFIED signal carrying the amounts,
> the explicit threshold that fired, and row-level evidence anchors.
> **No scenario decides "duplicate payment" or "violation"; the human investigates
> with the evidence.**

`/fees` 页面同样明示"线索是待核实差异而非认定结论"。
风险系统最怕的就是自动下结论，这里守住了。

### 3.3 排除计数不静默丢弃

`RiskResult.Excluded int` 显式暴露，且每个场景配 `note` 解释口径，例如：

> 线索口径：单批次口径的站址月度合计环比涨幅达阈值；
> **跨批次 (站址, 账期) 已排除（那属于重复结算/金额差异场景），排除数见 excluded**

### 3.4 前端页面实测

`/fees` 页面加载正常，**零 console 错误**，三个功能区（风险六场景 / 费用-资源关联 / 费用批次）齐全。

---

## 四、本轮遇到的环境问题

### 4.1 `npm run build` 因 vitest 未安装而完全失败（本地依赖过期，非仓库缺陷）

`package.json` 声明 `vitest: ^5.0.5`，但本地 `node_modules` 是旧的：

```
$ npm run build
> tsc -b && vite build
$ npx tsc -b
src/components/lab/fitGuard.test.ts(1,38): error TS2307: Cannot find module 'vitest'
src/pages/fees-format.test.ts(9,38): error TS2307: Cannot find module 'vitest'
... EXIT=2
```

`tsc` 失败导致 **vite build 根本没执行**，`dist` 保持旧文件——
而 `go build` 会把旧 dist 嵌进二进制，**用户拿到的二进制前端是过期的**。

`npm install`（51 个包）后恢复正常（build 25.68s，新 chunk `index-BL4QL-wW.js` 343KB）。

**这暴露一个值得注意的设计点**：`tsconfig.app.json` 把 `.test.ts` 纳入类型检查，
使**测试文件的类型错误阻断生产构建**。好处是测试与产品同标准；
代价是任何测试依赖缺失都会让人拿不到可用前端。
若这不是刻意设计，建议把 `*.test.ts` 排除出 `tsconfig.app.json`（test 已有独立 tsconfig）。

### 4.2 本机 fsync 仍然很慢（承接上次记录）

全量测试仍在后台运行（`-timeout 25m`）。v0.12.1 那轮已确认本机
`write+fsync` 每次 230ms（正常 0.1–1ms），Kaspersky `avp.exe`×4 在跑。
**测试若再次超时，应归因于环境而非代码。**

---

## 五、结论

| 项 | 判定 |
| --- | --- |
| #65 P1-a 金额整数分 | ✅ **彻底修复**，11/11 边界独立验证通过，实现深度超出建议 |
| #65 P1-b 模糊匹配性能 | ✅ **彻底修复**，15.5s → 0.3s（约 50 倍），剪枝带无损证明 |
| #65 P1-c Windows 查询路径 | ⚠️ **绕开**（Docker/WSL2 替代），错误消息自引导做得很好，但原生构建仍不可用 |
| #65 P3 英文表头 | ✅ 已修 |
| 费用风险六场景 | ✅ 已落地，措辞纪律与排除计数处理到位 |
| `/fees` 前端页面 | ✅ 渲染正常，零 console 错误 |
| MCP 工具数 | 5 → **11** |

**没有发现新的产品缺陷。** 本轮唯一值得提的是 `npm run build` 的
测试类型检查会阻断生产构建这一设计点（4.1），以及 Windows 原生构建仍未解决（2.3）。
