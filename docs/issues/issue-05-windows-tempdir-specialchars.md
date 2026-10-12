## 环境
Windows / amd64，Go 1.27.0，v0.6.0 @ 2033de8

## 现象
`go test ./...` 有 4 个测试因平台问题失败。

### A. TempDir 清理失败（3 个测试）
```
testing.go:1617: TempDir RemoveAll cleanup: unlinkat E:\\temp\\...\\001:
The directory is not empty.
```
受影响：TestIssueCountByBatchMatchesList、TestMarkRevertedStatusGuard、
TestApplyOpsSemanticFailures

Windows 不允许删除仍被占用的目录（SQLite 文件句柄未及时释放），
POSIX 语义下无此限制。

### B. 文件名含 Windows 非法字符（1 个测试）
`internal/repo/polish_test.go:137`：
```
MkdirAll: mkdir ...\\scratch dir?with#hash:
The filename, directory name, or volume label syntax is incorrect.
```
`?` 与 `#` 在 Windows 文件系统中非法（POSIX 允许）。

## 建议修复方向
- A：测试结束前显式关闭 DB 句柄；或对 `RemoveAll` 失败作宽容处理（记录而非 fail）
- B：将该测试标记为平台相关（`runtime.GOOS` 分支或 `testing.Short()` 跳过）

## 验收标准
- [ ] Windows 下 `go test ./...` 全绿
- [ ] Linux 下行为与覆盖率不变