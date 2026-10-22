# [P1] 费用分析层实测：三处需要决策的缺陷——金额用 float64、模糊匹配 O(n×m) 不可用、Windows 无 duckdb 构建路径

来源：对 `12a46ec`（v0.13.0，费用分析层阶段 1/2/3）的独立实测，2026-10-19
方法：读实现 + 写最小复现程序量化 + 端到端跑 CLI 导入

---

## 〇、先说实测通过的部分（质量很高）

费用模块的整体设计扎实，以下都实测正常：

| 能力 | 实测结果 |
| --- | --- |
| CSV 导入 + 自动识别表头 | ✅ `站址编码/电费(元)/账期` 识别成功 |
| 金额规范化 | ✅ `"1,234.56"` / `¥999.00` / `￥888.88` 全部正确解析 |
| **行级账期拆分** | ✅ 4 行按 `账期` 拆成 `month=05`(`3 行`) + `month=06`(`1 行`) 两个 Parquet |
| 内容寻址快照 | ✅ `fee-imports/electricity/2024/05/<sha256>_electricity2.csv` |
| **幂等重导** | ✅ 同文件重导 → `skippedDuplicate: true`、`exit 0` |
| **批次共存** | ✅ 第二份文件同站同账期 → 同目录下**两个 batch 文件共存，绝不合并** |
| 默认构建诚实降级 | ✅ 查询答 `501 engine_not_built`，错误信息说明原因 |

幂等 + 共存这两点做得尤其好——后者正是"重复结算检测"的数据基础，
`HAVING count(DISTINCT import_batch) > 1` 能天然工作。

**以下是三处需要决策的缺陷。**

---

## 一、（P1）金额用 `float64` 存储，财务场景应改整数分

### 现象

`internal/fees/parquet.go:37`

```go
Amount      float64 `parquet:"amount"`
```

`internal/fees/domain.go:128`, `normalize.go:50` 同样是 `float64` + `strconv.ParseFloat(s, 64)`。

### 实测误差（最小复现）

```
累加 100 万次 0.01 = 10000.0000001719   （应为 10000.00）
误差: 0.00000017 元

不同求和顺序（顺序/逆序/8 分块）：
  顺序 28498150.0000000037
  逆序 28498150.0000000000
  分块 28498149.9999999963
  差异(顺-块) 0.0000000075 元

0.1 + 0.2 = 0.30000000000000004441  ≠ 0.3
```

**单笔误差极小，但三个场景会暴露：**

1. **对账取整到分**：`ROUND(SUM(amount), 2)` 在多批次聚合时可能差 1 分钱，
   财务对账场景下"差 1 分"就是异常；
2. **重复结算比对**：判据是"两个批次金额是否相同"——
   若两侧求和顺序不同，可能得出"金额有 0.00000001 差异"的假线索；
3. **#59 已发现的双引擎计数差 0.07%**，本质就是同一问题的表现。

### 值得注意的反差

同一文件的 `NormalizeSiteCode`（`normalize.go:57-63`）注释写得极其严格：

> Leading zeros are PRESERVED (the code is an identity string, never a number
> — the same doctrine as the machine_room/site 12/17-digit IDs);
> **a numeric re-parse is deliberately absent.**

**身份字段刻意不数值化，金额字段却直接 ParseFloat——两种精度纪律不一致。**

### 建议

金额改为 **整数分（`int64`）** 存储与计算：

```go
AmountCents int64 `parquet:"amount_cents"`   // 1234.56 → 123456
```

- 解析时用字符串/定点数逐位解析，避免经过 `float64`；
- 展示/输出时才除以 100（`json:"amountCents"` 保持整数契约，前端格式化）；
- 若担心 Parquet schema 变更，可新增列并保留 `amount` 一列过渡期双写。

**替代方案**（改动更小）：保留 `float64` 但在**写入与聚合处强制 `math.Round(x*100)/100`**，
并在文档中明确"金额精度 = 分"。这治标不治本，但能消除对账差异。

---

## 二、（P1）模糊匹配复杂度 O(codes × candidates × len²)，万级站址即超时

### 现象

`internal/fees/link.go:174-261` 的 `ScanLinks`：

```go
for _, code := range codes {          // 费用侧全部站址编码
    // exact / rule 都是 O(1) 级查找
    proposals := topFuzzy(candidates, code)   // ← 对每个 code 遍历全部 candidates
}
```

`topFuzzy`（`link.go:346`）内部对每个 candidate 算 **Levenshtein 编辑距离**。
`LinkableResourceTypes` 包含 `base_station`，当前库**已有 1,198 个基站**。

### 实测（最小复现，模拟真实规模与编码长度）

```
规模: 10000 codes × 1364 candidates = 13,640,000 次比较（编码长度 18）
实测 100 codes: 155 ms
外推 10000 codes 全量: 15.5 秒
外推 50000 codes 全量: 78 秒（1.3 分钟）
```

**而这只是候选侧 1,364 个对象。**费用场景的真实规模是
"1 万–10 万站址 × 5 年 × 月度费用"（#58 架构描述），若资源库扩到 1 万对象：

```
10000 codes × 10000 candidates = 1 亿次 Levenshtein → 数分钟至十分钟
```

**HTTP 端点 `POST /api/fees/links/scan` 必然超时**，且期间占用 CPU。

### 加重因素

`sweepAutoDropouts`（`link.go:277-306`）每次扫描都执行：

```go
links, err := s.db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{})  // ← 全表加载
```

在内存里过滤。链接数 = 站址编码数，万级时也是可观的内存与时间开销。

### 建议

1. **预筛后再算编辑距离**（最直接）：
   ```go
   // 长度差 > (1-THRESHOLD)*maxLen 的直接跳过，不算 Levenshtein
   if abs(len(a)-len(b)) > int((1-FUZZY_THRESHOLD)*max(len(a),len(b))) { continue }
   ```
   典型站址编码长度相近，这一步能省掉大部分计算；
2. **前缀/桶索引**：先按编码前 2-4 位分桶，只在同桶内做 fuzzy
   （站址编码通常有区域前缀，语义上也更合理）；
3. **候选集裁剪**：`base_station` 是否该进 fuzzy 候选？1,198 个基站的自然键
   与费用站址编码的语义距离较远，可评估是否只在 exact/rule 级参与；
4. **扫描改异步任务**：若规模必然很大，`POST /links/scan` 应返回 task id 而非同步等待；
5. **dropout 清扫下推到 SQL**：`WHERE source='auto' AND status!='dismissed' AND fee_site_code NOT IN (...)`
   分批执行，避免全表加载。

---

## 三、（P1）Windows 上无法构建 duckdb 版本，费用查询在 Windows 永久 501

### 现象

按文档 `CGO_ENABLED=1 go build -tags duckdb` 在 Windows 上**链接失败**：

```
ld.exe: cannot find -lrstrtmgr
collect2.exe: error: ld returned 1 exit status
EXIT=1
```

（`rstrtmgr` 是 Windows Restart Manager 库，go-duckdb 的静态绑定需要它，
当前 mingw64 工具链未提供。）

### 影响链

1. 用户在 Windows 上跑 `run.ps1` → 得到默认构建（不含 duckdb tag）；
2. 费用查询 → `501 engine_not_built`（实测确认）；
3. `run.ps1` 与 `Makefile` **都没有 duckdb 构建路径**（实测 grep 为空）；
4. 用户**没有官方途径**在 Windows 上获得可查询的费用功能。

而本项目的主要使用环境就是 Windows（`run.ps1` 是为 Windows 单独写的）。

### 建议

1. **至少提供明确的安装说明**：文档写明"Windows 用户查询费用需 Docker/WSL2 运行 linux 构建"，
   并给出一行命令；
2. **提供 Docker 镜像**：发布 `-tags duckdb` 的 linux 镜像，Windows 用户
   `docker run` 即可查询；
3. **评估 pure-Go DuckDB 替代**：若 go-duckdb 的 CGO 在 Windows 不可行，
   可评估 `duckdb` CLI 子进程模式（Pipe 出 Parquet 查询结果），
   牺牲少量性能换取全平台可用；
4. **在 UI 上提前提示**：费用页应检测 `engine_not_built` 并显示
   "当前构建不含分析引擎，请使用 duckdb 构建"而不是让用户撞 501。

---

## 四、附带（P3）：`site_code` 英文表头不在同义词表

`recognize.go:153` 的 `siteCode` 同义词全是中文：
`{站址编码, 站址编号, 站址ID, 站点编码, 站点编号, 站点代码, 资源编码, 资源ID, 机房编码, 基站编码, 局站编码, 局站ID}`

实测用 `site_code` 作表头 → 报错"缺少必需列映射"。

`amount` 侧有 `电费` 等中文 + 单位后缀兜底（`电费(元)` 能命中），
但 `site_code`/`siteCode`/`site id`/`station_code` 这类常见英文表头都识别不了。

**建议**：同义词表补充英文常见写法（`site_code`、`siteCode`、`siteID`、`station_code`、
`cell_code` 等）。当前行为是"识别不到就报错要求人工映射"——**安全但不够友好**，
而费用表格来自不同系统导出，英文表头很常见。

---

## 五、验收建议（闭环记录，2026-10-22）

1. ~~金额精度：`SUM` 结果对账到分**零误差**~~ → **已闭环**：v0.13.0 起全链 int64 分币（`AmountCents`），`normalize_test.go` 断言 100 万行 0.01 累加 == 1000000 cents 零误差；
2. ~~模糊匹配：10,000 codes × 1,000 candidates 的 scan **< 2 秒**~~ → **已闭环**：`TestFuzzyScanPerformance`（#65 验收，#82 竞态校准 8s 门，实测 1.38s idle）+ `BenchmarkFuzzySweep10k1k`（`-bench` 基准，实测 1.39s/op）；
3. Windows：`run.ps1` 或文档给出可查询费用的明确路径——**外部依赖项**（本沙箱无 Windows CGO 工具链，见 verified-capabilities v0.13.0 未验证表）；Docker 路径见 Makefile `build-duckdb` 注释与 docs/fees-layer.md §5；
4. ~~识别器：英文常见表头能自动识别~~ → **已闭环**：`normalize_test.go` 断言 site_code / Site Code / SITE_CODE / siteCode 等拼写与大小写变体。

---

## 附：实测数据与环境

```
二进制：bin/netopslab.exe (65,175,552 bytes, v0.13.0 默认构建)
duckdb 构建：CGO_ENABLED=1 go build -tags duckdb → 链接失败（-lrstrtmgr）
导入结果：4 行 → 2 个分区文件（month=05: 3 行, month=06: 1 行）
幂等：同文件重导 → skippedDuplicate=true, exit 0
共存：第二文件 → 同目录 2 个 batch 文件
查询：GET /api/fees/query/totals → HTTP 501 engine_not_built
```
