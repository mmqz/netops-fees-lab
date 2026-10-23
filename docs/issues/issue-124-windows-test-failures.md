# [P1] Windows 上 7 个测试失败：上传并发原语在 Windows 非原子覆盖 + 文档门禁被 CRLF 骗过 + TempDir 清理

> 关联：#112（storeUpload 共享原语）、#116（api.md prose 契约门禁）、#79/#80（跨平台路径契约）

来源：v0.15.2 + 48 个未发版提交（`264d8e1`）的 Windows 全量测试，2026-10-23

| 项目 | 结果 |
| --- | --- |
| 包 | **23 ok / 2 FAIL** |
| 断言 | **7 个失败** |

三类根因，性质完全不同：

| # | 失败 | 性质 |
| --- | --- | --- |
| 1 | APIs 三个并发上传测试 | **产品缺陷**（Windows 文件语义） |
| 2 | TestAPIMDocHeadingsMatchRouteTable | **测试缺陷**（CRLF 骗过正则） |
| 3 | TestSnapshotLayer / TestLinkScanFailsLoud | **环境问题**（TempDir 清理） |

---

## 一、（产品缺陷）并发同名上传在 Windows 上 100% 失败，返回 500

### 现象

三个通道全部失败，**连跑 5 次，5/5 必现**：

```
--- FAIL: TestAPIFeesConcurrentSameUpload
    fees_test.go:906: round 4: concurrent import status = 500 (want 201/200)

--- FAIL: TestAPIImportConcurrentSameUpload
    uploads_race_test.go:217: round 0: concurrent upload 2 status = 500 (want 202)

--- FAIL: TestAPIMetricsConcurrentSameUpload
    uploads_race_test.go:334: round 0: concurrent metric import 0 status = 500 (want 202)
```

服务端日志：

```
store upload failed: 保存上传文件: rename C:\temp\...\uploads\.tmp-2393708100
  C:\temp\...\uploads\8cb427a0..._race-devices.xlsx: Access is denied.
```

### 根因：`os.Rename` 在 Windows 上不是原子覆盖

`internal/api/uploads.go:31-53` 的设计注释明确假设（关键三行）：

> Identical content replaces itself safely: concurrent same-content submissions
> each write their own temp and **RENAME over the target — rename is atomic per file**,
> so the racers replace each other with whole identical files and **no reader ever observes a partial one**.

**这个假设只在 POSIX 成立。** Windows 上 `MoveFileEx`/`ReplaceFile` 在目标文件
正被其他句柄打开时返回 `ERROR_ACCESS_DENIED`（`Access is denied`），
**不是原子覆盖，而是失败**。

三个通道（resource workbook / metrics / fee staging）都骑这一个原语，所以一起挂。

### 影响

1. **Windows 上并发重复上传同一文件会返回 500** —— 内容寻址设计的本意是
   「同内容 → 同路径 → 幂等」，用户看到的却是内部错误；
2. 错误分类也不对：这是**并发竞争**，不是 `internal` 服务器故障，
   前端无法据此决定「重试」还是「放弃」；
3. 与 #104「fee import errors get classified — operator refusals vs server faults」
   的方向相反：这里把可重试的竞争归成了服务器故障。

### 建议修复（择一）

**方案 A（最小）**：rename 失败时检查目标文件是否已存在且内容一致——
存在即视为成功（内容寻址语义本来就是这样）：

```go
if err := os.Rename(tmp.Name(), path); err != nil {
    _ = os.Remove(tmp.Name())
    // 并发同内容竞争：目标已由另一请求原子落盘，内容必然一致（同一 sha256）
    if fi, statErr := os.Stat(path); statErr == nil && fi.Size() == int64(len(data)) {
        return path, nil
    }
    return "", fmt.Errorf("保存上传文件: %w", err)
}
```

**方案 B**：进程内按目标路径加互斥锁（同一进程内并发已足够，无需跨进程）。

方案 A 更贴合内容寻址语义，且顺带处理了残留文件的自愈。

### 验收

1. 三个并发测试在 **Windows 与 Linux** 上都通过；
2. 并发同内容上传返回 201/200（幂等），不同内容返回各自 201；
3. 改名失败但目标已存在时应视为成功，不得退化为 500。

---

## 二、（测试缺陷）文档门禁被 Windows CRLF 骗过，误报 api/health 漂移

### 现象

```
api.md is missing sections for 1 live route(s): [GET /api/health]
api.md documents 1 route(s) that no longer exist: [GET /api/health
]
```

**同一个路由既被说成「缺失」又被说成「多余」** —— 自相矛盾，本身就该怀疑门禁。

`docs/api.md:14` 明明有 `## GET /api/health`，且是干净 ASCII（逐字节核对过：
`35,35,32,71,69,84,32,47,97,112,105,47,104,101,97,108,116,104`）。

### 根因：正则字符集没排除 CR

`internal/api/facts_gate_test.go` 里：

```go
var apiDocHeading = regexp.MustCompile("^#{2,4} (GET|POST|PUT|DELETE|PATCH) ([^ ?\\x60—]+)")

for _, line := range strings.Split(string(raw), "\n") {   // CRLF 文件每行带 \r
    m := apiDocHeading.FindStringSubmatch(line)
    doc[m[1]+" "+m[2]] = true
}
```

`[^ ?\x60—]+` **没有排除 `\r`**，所以 Windows 上提取到的是 `"/api/health\r"`，
与路由表的 `"/api/health"` 不等。

### 为什么只有这 1 条中招

我用 Python 复现了 Go 的提取逻辑（同样只按 `\n` 切分）：

```
文件含 CRLF: True   CR 数量: 1759 / LF 数量: 1759
提取数: 84
含 health 的 key: 'GET /api/health\r'
带 \r 的 key 数: 1 / 84      ← 只有 1 个！
```

因为其他 83 个标题都是 `## GET /api/xxx — 中文标题` 形式，
字符集在 `—` 处就停了，自然不含 CR。**唯独 `## GET /api/health` 这行
路径后直接换行**，才把 CR 吃进去。

这也解释了报错里 `[GET /api/health\n]` 的怪异换行——那就是 CR 的终端显示。

### 建议修复

```go
for _, line := range strings.Split(string(raw), "\n") {
    line = strings.TrimRight(line, "\r")   // 或 strings.TrimSpace
    if m := apiDocHeading.FindStringSubmatch(line); m != nil {
        doc[m[1]+" "+m[2]] = true
    }
}
```

更稳的做法是给字符集也加 CR：加 `\r\n` 到排除类。建议两者都做。

### 验收

1. 门禁在 Windows 与 Linux 上结论一致；
2. 建议加一条自检：构造含 CR 的行，断言提取结果不含 CR。

---

## 三、（环境）TempDir 清理失败——Windows 已知问题，非产品缺陷

```
TestSnapshotLayer: testing.go:1617: TempDir RemoveAll cleanup: unlinkat
  .../fee-imports/electricity/2024/05: The directory is not empty.

TestLinkScanFailsLoudOnUniverseReadErrors/corrupt_parquet_aborts_scan,_links_survive:
  ...batch-fee_0746650c....parquet: The process cannot access the file
  because it is being used by another process.
```

**断言本身是通过的**，失败发生在 `t.TempDir()` 的自动清理阶段——
Windows 上 SQLite 连接/文件句柄释放慢于清理，这是本仓库历史上反复出现的同一现象。

**不算产品缺陷**，但建议：
1. 对持有 DB/文件句柄的测试显式在 `t.Cleanup` 里先 Close 再让 TempDir 清理；
2. 或用 `testsupport.TempDir`（仓库已有该封装）统一处理。

---

## 附一：本轮审核的其他确认

### #79 已按建议修复

```diff
- want := filepath.Join("fee-imports", "electricity", "2024", "05", sha+"_bill.csv")
+ want := "fee-imports/electricity/2024/05/" + sha + "_bill.csv"
- got, err := os.ReadFile(filepath.Join(dir, want))
+ got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(want)))
```

实测 `TestSnapshotLayer` 断言通过（剩余失败仅是上面的 TempDir 清理）。

### #80 是我漏判的同类问题

我当时判断 `checkpoints.file_path`「是内部路径、不算缺陷」——**判断错了**。
云端不仅修了（`path.Join` + `FromSlash` + v32 迁移），还补了迁移测试。
实测 `TestMigrationV32` 与 `TestCheckpoint` 均通过。

### #123 的「零命中」声明属实

`grep predict/forecast/外推` 在时序预测语义上确实零命中；
命中的 `sim/checkpoint.go` 是 ID 分配预测、`repo/metrics.go` 是内存预测量，语义无关。

### 顺带一提：CRLF 是本仓库的反复伤

`docs/api.md` 当前是 **CRLF**（1759 CR / 1759 LF）。
本 issue 第二节、以及更早的 #118（`docs/issues/README.md keeps its CRLF line endings`）
都栽在同一件事上。建议加一条换行一致性检查，或 `.gitattributes` 统一。

---

## 附二：完整失败清单

```
--- FAIL: TestAPIMDocHeadingsMatchRouteTable (0.54s)
--- FAIL: TestAPIFeesImportErrorClassification (0.19s)
--- FAIL: TestAPIFeesConcurrentSameUpload (0.24s)
--- FAIL: TestAPIImportConcurrentSameUpload (1.76s)
--- FAIL: TestAPIMetricsConcurrentSameUpload (0.87s)
--- FAIL: TestLinkScanFailsLoudOnUniverseReadErrors (2.63s)
--- FAIL: TestSnapshotLayer (0.04s)

FAIL github.com/mmqz/netops-lab/internal/api  68.290s
FAIL github.com/mmqz/netops-lab/internal/fees 19.569s
```

其中 `TestAPIFeesImportErrorClassification` 首轮失败但单独复跑未复现，
可能与其依赖的并发路径同源，建议一并回归。