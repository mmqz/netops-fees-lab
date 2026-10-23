# [P0] 真实台账形态直接不可用：一个 Excel 多 sheet 只读第一个且静默丢数据，同文件换 feeType 会污染数据

来源：用户真实台账结构确认 + 端到端实测，2026-10-23
用户原话：**「一个 Excel 是一个月，一个表是当月的多个事项」**

---

## 一、用户真实数据形态

```
2024年05月费用.xlsx          ← 一个文件 = 一个月
├── sheet「电费」      站址编码 | 站址名称 | 电费(元)   | 账期
├── sheet「铁塔租费」  站址编码 | 站址名称 | 塔租费(元) | 账期
└── sheet「修理费」    站址编码 | 站址名称 | 修理费(元) | 账期 | 工单号
```

**一个文件包含多种费用类型，每种一个 sheet。** 这是业务自然形态——月度账单一本。

---

## 二、实测结果：三处硬阻塞

我按上述结构生成了真实文件（3 sheet / 5 行数据）实测。

### 阻塞 1：只读第一个 sheet，其余静默丢弃（P0）

```
POST /api/fees/imports  feeType=electricity
→ status: completed
  rowsTotal: 2, rowsImported: 2, rowsFailed: 0
```

**文件有 3 个 sheet、5 行；只导入 2 行；报告完全成功、零失败、零警告。**

代码依据 `internal/fees/recognize.go:37`：

```go
// CSV: UTF-8 with BOM tolerated; xlsx: the first sheet only.
```

以及 `recognize.go:185-190` 显式取 `sheets[0]`：

```go
sheets := f.GetSheetList()
sheet := sheets[0]
it, err := f.Rows(sheet)
```

**这是最危险的失败模式**：用户看到「导入成功」，实际三分之二的数据从未进系统。
后续所有风险分析都建立在不完整数据上，而系统不会有任何提示。

### 阻塞 2：同一文件换 feeType 会污染数据（P0）

因为「一个文件只能指定一个 feeType」，我尝试把同一文件按 `tower_lease` 再导一次：

```
POST /api/fees/imports  feeType=tower_lease  （同一个 xlsx）
→ status: completed, rowsImported: 2

GET /api/fees/query/totals?feeType=tower_lease
→ SITE-0001: 12345.67 元    ← 这是【电费】sheet 的数据！
   预期应为:   5000.00 元     ← 【铁塔租费】sheet 的数据
```

**系统把「电费」sheet 的内容当成了「铁塔租费」。**

原因：它仍然只读第一个 sheet（电费），却按调用方声明的 `feeType` 落库。
于是同一份电费数据被重复计入两个费用类型。

**后果**：
- 费用总额虚高（同一笔钱算两次）；
- 六场景 + 双账本交叉全部基于错误分类，结论无意义；
- 且因为批次 sha256 不同、feeType 不同，**幂等键无法拦截**（幂等键是
  `(sha256, fee_type, year, month, mapping)`——feeType 变了，就是一条新键）。

### 阻塞 3：sheet 名与 feeType 完全无关联

系统能自动识别**列**（`核心字段同义词`），但**完全不看 sheet 名**。

而用户的 sheet 名「电费 / 铁塔租费 / 修理费」**恰好就是准确且完备的类型声明**——
信息就在文件里，系统没有利用。

---

## 三、建议方案

### 方案 A（推荐）：多 sheet 导入 + sheet→feeType 映射

导入接口增加 sheet 处理策略，一次调用吃掉整个文件：

```json
POST /api/fees/imports  (file=2024年05月费用.xlsx, year=2024, month=5)
{
  "sheets": "auto"            // auto | all | first(default, 保持兼容)
}
```

**`auto` 模式**：逐 sheet 用现有 `Recognize()` 识别表头——
「电费(元)」列映射到 amount、「塔租费」映射到 amount，再由 **sheet 名 + 表头关键字**判定 feeType
（`feeTypeMarkers` 已有该机制，只是目前只用于「提示」未用于「判定」）。

每个 sheet 产出一个独立批次（各自 feeType + 独立证据链），返回数组：

```json
{ "batches": [
  {"sheet":"电费","feeType":"electricity","rowsImported":2,"batchId":"fee_a..."},
  {"sheet":"铁塔租费","feeType":"tower_lease","rowsImported":2,"batchId":"fee_b..."},
  {"sheet":"修理费","feeType":"repair","rowsImported":1,"batchId":"fee_c..."}
] }
```

**关键：无法判定 feeType 的 sheet 必须显式报错或列出待人工确认，绝不能静默跳过。**

### 方案 B（最小止血）：读全部 sheet，但只接受单类型文件

若暂不做 sheet→feeType 映射，至少要做到：
1. **扫描全部 sheet**，把非首 sheet 的数据行计入 `rowsSkipped` 并在响应中返回；
2. 存在多 sheet 且有数据时，**默认拒绝导入**并说明「本文件含 N 个 sheet，请用 --sheet 指定或用多 sheet 模式」；

宁可拒绝，不可静默丢弃。

### 方案 C（防御）：feeType 与表头交叉校验

即使保留单 sheet 模式，也应校验：**声明 feeType= tower_lease，但表头是「电费(元)」→ 拒绝**。

这能直接堵住阻塞 2 的数据污染。

---

## 四、验收标准

1. 导入一个含「电费/铁塔租费/修理费」三 sheet 的 xlsx：
   - **三个批次全部产生**，各自 feeType 正确；
   - `tower_lease` 汇总等于塔租 sheet 的金额（5000.00），**不是电费的 12345.67**；
2. 任何被跳过的 sheet / 行必须出现在响应中（`rowsSkipped` 或 `sheets` 数组），
   **`status: completed` 而数据有遗漏的情形必须不可能发生**；
3. 声明 feeType 与表头矛盾时拒绝导入（错误信息指出矛盾点）；
4. 幂等性：同一文件重复导入，三个 sheet 各自幂等跳过，不产生重复批次；
5. 回归：现有单 sheet / CSV 行为不变。

---

## 五、为什么这是 P0 而非 P1

用户的核心诉求是**「看看 AI 能不能从费用数据里发现异常和风险」**。

而现在：
- 用户的**唯一数据形态**（月度多 sheet 账本）拿到系统里，**三分之二数据静默消失**；
- 想补导另外两个 sheet，只能换 feeType 重传同一文件 → **数据污染**；
- 两条路都会让后续风险结论**建立在错误数据上**，而且**系统不会报警**。

风险分析模块本身是好的（实测 spike 场景精准命中 337% 涨幅并给出行级证据），
**但它拿不到完整正确的输入，就一点用都没有。**

---

## 附：本轮其他实测结论（供参考）

### 风险场景能力已验证可用

构造 SITE-A 六个月 1000→1020→1010→1030→**4500**→4600，spike 场景精准命中：

```
metricValue: 3.3689（环比涨 337%）
thresholdLabel: 环比涨幅 ≥ 0.30（上期 103000 分 → 本期 450000 分）
evidence: 文件 X 第 6 行 · 批次 fee_7968... · 450000 分
```

对照站 SITE-B 未误报；样本不足时 outlier 诚实返回空（不瞎报）。**模块质量是好的。**

### 另一处会影响真实台账的问题

CSV 中未加引号的千分位会被判为该行失败：

```
SITE-0004,佳木斯基站,2,234.56,2024-05,...
→ 行 5 字段 period 值 "234.56" 无法规范化
```

系统正确报错未静默写错（值得肯定），但真实台账这种写法很常见，
建议导入前提供**格式体检**：检测到某行字段数异常时提示「可能是未引号千分位」。
