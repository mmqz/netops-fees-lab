# [P1] Windows 上持久化/API 的相对路径用反斜杠：`TestAPIFeesImportJourney` 稳定失败，跨平台契约被破坏

> GitHub issue 编号：**[#75](https://github.com/mmqz/netops-lab/issues/75)**
> （仓库内文件名 issue-66-*.md 是本地序号，GitHub 上 #66–#74 已被其他 issue 占用）

来源：v0.15.1（`ddd9c3e`）全量测试，2026-10-20
环境：Windows / Go 1.27 / `go test -timeout 25m ./...`

---

## 一、现象

`go test ./...` 结果：**24 包 ok / 1 包 FAIL / 1 个断言失败**，无超时。

```
--- FAIL: TestAPIFeesImportJourney (15.03s)
    fees_test.go:136: snapshotPath = "fee-imports\\electricity\\2024\\05\\c84b6f703ca61d10dfa5fbffdfe6bc74d7fa5e4592b33062f03ab6378e5c8086_may.csv",
                      want the content-addressed tree under the declared period
FAIL	github.com/mmqz/netops-lab/internal/api	684.418s
```

**高确定性复现**：单独跑该测试 2.90 秒即稳定失败，非偶发、非资源竞争。

## 二、根因

`internal/fees/snapshot.go:63-67`

```go
func SnapshotPath(feeType FeeType, year, month int, sha256, originalName string) string {
	return filepath.Join("fee-imports", string(feeType),
		fmt.Sprintf("%d", year), fmt.Sprintf("%02d", month),
		sha256+"_"+originalName)
}
```

`filepath.Join` **用平台分隔符**：Windows 产出 `\`，Linux/macOS 产出 `/`。

测试断言（`fees_test.go:135`）用的是 POSIX 约定：

```go
if snapRel == "" || !strings.HasPrefix(snapRel, "fee-imports/electricity/2024/05/") {
```

## 三、为什么这是产品缺陷而非测试缺陷

`SnapshotPath` 的返回值**不是**一个仅供内部 `os.Open` 用的本地路径——它是**跨平台数据契约**：

| 去向 | 证据 |
| --- | --- |
| 持久化到 SQLite `fee_batches.snapshot_path` | `service.go:157` → `repo/feebatches.go:79` |
| HTTP API 返回 `snapshotPath` | `api/fees.go:206/226/254` |
| CLI JSON 输出 | `app/fees_cli.go:229/246` |
| 前端展示 | `/fees` 页面批次表 |

`domain/fees.go:99` 的字段注释本身就是契约声明：

```go
SnapshotPath string // relative to the data dir (content-addressed)
```

**"relative to the data dir" 是一个逻辑路径，不是 OS 路径。** 用 `filepath.Join` 生成它，
意味着同一份数据在 Windows 导入、在 Linux 读取时，数据库里的路径字符串无法直接对应磁盘。

### 实际后果

1. **跨平台迁移失效**：Windows 上导入费用的数据库 + 数据目录拷到 Linux，
   库里的 `fee-imports\\electricity\\...` 在 Linux 上拼不出正确路径（反之亦然）；
2. **前端/API 契约不一致**：同一套前端代码在不同后端平台上看到不同分隔符；
3. **测试在 Windows 上必然红**：这不是"Windows 上的偶发环境问题"，是确定性失败——
   即本项目的 CI（其实测在 linux）**永远看不到这个失败**，Windows 开发者每次都撞。

### 同类第二处

`internal/fees/parquet.go:99`

```go
rel := filepath.Join("fees",
    fmt.Sprintf("fee_type=%s", string(feeType)),
    fmt.Sprintf("year=%d", year),
    fmt.Sprintf("month=%02d", month),
    "batch-"+batchID+".parquet")
```

`relPath` 同样进入 API/CLI 的 `files[]` 数组。实测 v0.13.0 时 CLI 输出的就是：

```json
"files": ["fees\\fee_type=electricity\\year=2024\\month=05\\batch-fee_xxx.parquet", ...]
```

（当时没有测试断言它，所以没被发现。）

## 四、建议修复

**`path.Join`（逻辑路径，永远 `/`）生成相对路径，`filepath.FromSlash` 在使用前转回 OS 路径。**

```go
import "path"

func SnapshotPath(...) string {
	return path.Join("fee-imports", string(feeType), ...)   // 逻辑路径：永远 /
}
```

拼磁盘路径时：

```go
// snapshot.go:77
dst := filepath.Join(dataDir, filepath.FromSlash(rel))
// parquet.go:112
dst := filepath.Join(analysisDir, filepath.FromSlash(f.relPath))
```

同样处理 `parquet.go:99` 的 `rel`。

**兼容性说明**：已存在的 Windows 数据库里存的是反斜杠形式。建议在读取侧做一次
`path.Clean(filepath.ToSlash(stored))` 归一化（或加一个 v31 迁移统一改写），
避免升级后旧批次路径解析失败。

## 五、验收标准

1. `TestAPIFeesImportJourney` 在 Windows 与 Linux 上**都通过**；
2. `SnapshotPath` / Parquet `relPath` 返回值**不含反斜杠**（可加断言 `!strings.Contains(rel, "\\")`）；
3. 跨平台验证：Windows 生成的数据库在 Linux 上能正确解析快照路径（可用 CI 交叉 job 做）；
4. 旧数据兼容：已有反斜杠记录的批次仍能正常回滚/读取。

---

## 附：本次全量测试结果（24 包）

```
ok    internal/app         658.117s
ok    internal/repo        683.351s
ok    internal/sim         602.935s
ok    internal/importer    501.863s
ok    internal/detect      425.929s
ok    internal/fees        306.936s
ok    internal/topology    242.091s
... 共 24 包 ok
FAIL  internal/api         684.418s   ← 仅此一处断言失败
```

**无超时**（`-timeout 25m` 生效；本机 fsync 仍慢但已能跑完）。
除本 issue 外，v0.15.1 未发现其他产品缺陷。
