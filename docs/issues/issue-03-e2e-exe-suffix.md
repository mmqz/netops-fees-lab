## 环境
Windows / amd64，Go 1.27.0，v0.6.0 @ 2033de8

## 现象
`go test -count=1 ./internal/app/...` → **11 个测试失败**

```
exec: "E:\\temp\\netopslab-e2e-3203553024\\netopslab": executable file not found in %PATH%
```

## 根因
`internal/app/e2e_test.go:46`：
```go
bin := filepath.Join(tmp, "netopslab")
build := exec.Command("go", "build", "-o", bin, "./cmd/netopslab")
```
构建产物无 `.exe` 后缀，Windows 下 `exec.Command` 无法执行。

同类问题也影响 `make build`：产出 `bin/netopslab` 在 Windows 无法直接运行。

## 受影响测试（11 个）
TestCLIEndToEnd、TestCLIChangesFlow、TestCLIChangesRefRetargetFlow、
TestCLIExportRoundTrip、TestCLIExportUsage、TestCLIGovernanceSummary、
TestCLIGovernanceEmpty、TestCLIGovernanceUsage、TestCLILabFlow、
TestCLIRenamesFlow、TestCLIRenamesReject

## 建议修复方向
```go
bin := filepath.Join(tmp, "netopslab"+exeSuffix())
// exeSuffix() { if runtime.GOOS == "windows" { return ".exe" }; return "" }
```
Makefile 同理可用 `$(shell go env GOEXE)`。

## 验收标准
- [ ] Windows 下 `go test ./internal/app/...` 全绿
- [ ] `make build` 产物在当前平台可直接执行
- [ ] Linux 行为不变