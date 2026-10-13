# [P1] Windows 测试门禁不可用：6 包 15 项失败

来源：[docs/audit-v0.8.0.md](../audit-v0.8.0.md) §2.1（2026-10-13 本地审计）

## 现象

CHANGELOG v0.8.0 称「427 → 429 顶层测试」「本地门禁全绿」，但该结论基于 linux/amd64。
在 **Windows** 实跑 `go test -count=1 ./...`：

```
ok   17 包
FAIL  6 包：api, app, mcpserver, repo, sim, webui
失败测试 15 项
```

## 根因一：TempDir 清理（10 项）——修复未覆盖新包

```
testing.go:1617: TempDir RemoveAll cleanup: unlinkat ...\netops.db:
  The process cannot access the file because it is being used by another process
```

SQLite 句柄在 `Close()` 后仍被 Windows 持有，`t.TempDir` 的 RemoveAll 失败。

**v0.6.2 修过此问题（issue #5），但做法是各包加局部辅助**：

```
internal/sim/helpers_test.go:30    func tempDirT(...)
internal/repo/repo_test.go:20      func tempDirT(...)
```

v0.7.0 新增测试（traverse/impact/context/detection/run-state）**重新用了裸 `t.TempDir()`**，
于是同一问题在新包复发：

```
api (9):  TestDetectionRulesCRUD / TestDetectionRunAndAlerts /
          TestDetectionRunStateIntegration / TestAPIRenameApplyTargetMissingDirectDelete /
          TestAPIErrorPaths / TestRunStateEndpoints /
          TestTraverseEndpointContract / TestImpactEndpointContract / TestContextEndpoint
mcpserver (1): TestMCPLabDiffUndo
```

## 根因二：POSIX 路径断言（1 项）

```go
// internal/api/pagination_issue7_test.go:157（issue #8 的验证测试）
if health.DBPath == "" || !strings.HasPrefix(health.DBPath, "/") {
```

Windows 绝对路径为 `E:\temp\...\netops.db`，不以 `/` 开头 → 必然失败。
**产品行为正确**（health 确实返回绝对路径），是**测试不可移植**。

## 根因三：GC 权限语义（1 项，既有）

```
gc result = {Scanned:1 Deleted:1 Referenced:0 Errors:0}, want deleted=0 errors=1
```

测试用 chmod 制造「不可删除」；Windows 下删除仍成功，期望的 error 路径不可达。
**v0.6.0 审计已发现的既有项，非本次回归。**

## 影响

- 用户实际环境是 Windows → **本地无法用 `go test` 验证改动**
- 每次改代码都会看到 15 项假失败，掩盖真实回归
- 新测试文件会持续复发（缺乏共享辅助）

## 建议修复方向

1. **共享 TempDir 辅助**：抽到 `internal/testsupport`（或各包统一 TestMain 包装），
   MkdirTemp + 清理重试；api/mcpserver 改用它
2. `TestHealthCarriesDBPath` 改用 `filepath.IsAbs(health.DBPath)`
3. GC 测试按平台分支（Windows 跳过 error 路径断言，或改用其他失败注入方式）
4. 考虑在 CI 增加 `windows-latest` job（至少覆盖 `go test ./...`）

## 验收

- [ ] Windows 下 `go test -count=1 ./...` 全绿（或仅剩有据可依的平台跳过）
- [ ] TempDir 辅助为共享实现，新测试无需重复踩坑
- [ ] Linux 行为不变