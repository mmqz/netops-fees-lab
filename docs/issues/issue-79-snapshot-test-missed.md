# [P1] #75 修复遗留：`parquet_test.go:148` 漏改，`TestSnapshotLayer` 在 Windows 上稳定失败

来源：v0.15.2（`ab9c71b`）Windows 全量测试，2026-10-20
关联：#75（同一跨平台路径契约问题）

---

## 一、现象

`go test -timeout 25m ./...`：**24 包 ok / 1 包 FAIL / 1 个断言失败**。

```
--- FAIL: TestSnapshotLayer (0.00s)
    parquet_test.go:150: snapshot path = "fee-imports/electricity/2024/05/db5406..._bill.csv",
                         want "fee-imports\\electricity\\2024\\05\\db5406..._bill.csv"
FAIL	github.com/mmqz/netops-lab/internal/fees	4.108s
```

**稳定复现**，单跑 0.7 秒必现。

## 二、根因：一处孤立的漏改

v0.15.2 的 #75 修复把产品侧的路径改成逻辑路径（`path.Join`，永远 `/`），
并把大量测试的期望值改为 `filepath.FromSlash` 或字符串常量。

`internal/fees/parquet_test.go:148` **漏了**：

```go
want := filepath.Join("fee-imports", "electricity", "2024", "05", sha+"_bill.csv")
if rel != want {
    t.Fatalf("snapshot path = %q, want %q", rel, want)
}
```

- 产品 `WriteSnapshot` 返回 `fee-imports/electricity/...`（正确，正斜杠）
- 测试期望 `filepath.Join` 在 Windows 上产出 `fee-imports\\electricity\\...`（反斜杠）
- **两边不一致 → Windows 必失败**

### 同文件其他测试都改对了

```go
// parquet_test.go:30  —— 已改为字符串常量并加注释说明
// constants, NOT filepath.Join — the API/CLI echo them verbatim, so

// parquet_test.go:48  —— 读磁盘时正确转换
if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.relPath))); err != nil {

// service_test.go:86 / snapshot_test.go:52 —— 同样正确
```

**只有 148 行这一处遗漏**，全仓库扫描确认（`internal/**/*_test.go` 中用于**断言期望值**的
`filepath.Join` 仅此一处；其余都是合法的磁盘路径拼接）。

## 三、为什么 CI 看不见

CI 跑在 **linux**，那里 `filepath.Join` 与 `path.Join` 产出相同（都是 `/`），
所以测试全绿。**Windows 开发者每次必撞。**

这与 #75 本体的成因完全一致：`filepath.Join` 泄漏平台分隔符，而验证只在 linux 跑。

## 四、建议修复

```go
// 与同文件 30 行的既有约定保持一致
want := "fee-imports/electricity/2024/05/" + sha + "_bill.csv"
```

注意第 152 行的读盘调用应同步改为：

```go
got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(want)))
```

## 五、验收标准

1. `TestSnapshotLayer` 在 **Windows 与 Linux** 上都通过；
2. 建议加一条守卫测试：断言 `SnapshotPath` / `WriteSnapshot` 返回值**不含反斜杠**
   （`TestSnapshotPathIsLogicalContract` 已覆盖 SnapshotPath，但 **WriteSnapshot 的返回值未覆盖**——
   本 issue 恰好是 WriteSnapshot 这条路径）；
3. 建议 CI 增加 Windows job 跑 `./internal/fees`（#76 配额恢复后可考虑）。

---

## 附：本轮独立核实的云端声明

云端 #77 记录称「25 包 Go 测试 ok / 三脚本 68 断言全绿 / 无可做」。我的核实结果：

| 声明 | 核实结果 |
| --- | --- |
| 25 包 Go 测试 ok | ❌ **Windows 上 24 ok / 1 FAIL**（本 issue；该结论应来自 linux） |
| CI 配额未恢复 | ✅ **属实**，GitHub 原文：`The job was not started because recent account payments have failed or your spending limit needs to be increased`（runner_id=0，4-12 秒失败） |
| 仅剩 #76/#78 两个 open issue | ✅ 属实，且都是外部阻塞，代码侧确实无解 |
| 浏览器回归脚本校准 | 未独立复跑（需要 agent-browser 环境），但脚本 diff 与记录一致 |

**结论**：云端"无可做"的判断在**大方向上是准确的**（剩下的确实卡在账户计费），
但**"测试全绿"这个前提在 Windows 上不成立**——所以还有一件小小的能做的事。
