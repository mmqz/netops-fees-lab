# [环境] 本机 fsync 被 Kaspersky 拖慢 200–2000 倍，导致 `go test ./...` 大面积超时（非代码缺陷）

来源：v0.12.1 本地验证过程，2026-10-19
性质：**本机环境问题，不是仓库代码缺陷**。记录在此以免被误判为 v0.12.1 回归。

---

## 一、现象

`go test ./...` 在 v0.12.1 上出现 6 个包 FAIL：

```
FAIL  github.com/mmqz/netops-lab/internal/api       611.583s
FAIL  github.com/mmqz/netops-lab/internal/app       634.229s
FAIL  github.com/mmqz/netops-lab/internal/detect    607.511s
FAIL  github.com/mmqz/netops-lab/internal/importer  606.603s
FAIL  github.com/mmqz/netops-lab/internal/repo      606.733s
FAIL  github.com/mmqz/netops-lab/internal/sim       603.182s
ok    ...（其余 18 个包）
```

**全部是 `panic: test timed out after 10m0s`，零个 `--- FAIL:` 断言失败。**

## 二、根因：fsync 被实时杀毒拦截

### 2.1 堆栈指向同一处系统调用

所有超时 goroutine 的堆栈末端完全一致：

```
syscall.SyscallN
  → golang.org/x/sys/windows.FlushFileBuffers
    → modernc.org/libc.XFlushFileBuffers
      → modernc.org/sqlite/lib._winSync
        → _sqlite3OsSync
          → _sqlite3PagerCommitPhaseOne
            → _sqlite3BtreeCommitPhaseOne
              → _vdbeCommit ... (SQLite 事务提交)
```

**不是算法卡死，是 SQLite WAL 提交时等 `FlushFileBuffers` 返回。**

### 2.2 实测 fsync 单次耗时 230 ms

最小复现程序（纯 Go，不涉及仓库代码）：

```go
f, _ := os.CreateTemp(os.TempDir(), "fsynctest")
for i := 0; i < 50; i++ {
    f.Write([]byte("hello world data 0123456789\n"))
    f.Sync()          // ← fsync
}
```

```
50 次 write+fsync 耗时: 11.53s（每次约 230.6 ms）
```

**正常 NVMe SSD 上 fsync 应为 0.1–1 ms，这里慢了 200–2000 倍。**

### 2.3 关键旁证：进程在等 I/O 而非计算

```
repo.test  PID 45048  运行 363 秒  CPU 仅 4.9 秒  → 单核利用率 1.35%
repo.test  PID  xxxx  运行 251 秒  CPU 仅 2.0 秒  → 单核利用率 0.80%
```

CPU 几乎空转 = 阻塞在 I/O 等待。

### 2.4 简单测试耗时也异常

```
=== RUN TestOpenReadOnlyRefusesWrites      --- PASS (13.47s)
=== RUN TestOpenPathWithSpecialCharacters  --- PASS ( 8.02s)
=== RUN TestOpenAppliesMigrationsIdempotently --- PASS (5.35s)
ok  internal/repo  27.925s
```

三个 `repo.Open()` 级测试正常应在几百毫秒内完成。每个 `Open` 要应用 27 个迁移，
每次迁移提交都是一次 fsync 风暴。

### 2.5 环境确认

```
Kaspersky 进程：avp.exe ×4 + avpui.exe
磁盘：E: NTFS Fixed Healthy，可用 212 GB（空间充足）
TEMP = E:\temp（与工作区同盘）
```

**Kaspersky 的实时文件扫描拦截了每次 fsync。**

## 三、为什么之前几轮能通过

- v0.12.0 时代 `internal/repo` 跑 517s（已很慢但未触顶 10 分钟）
- v0.12.1 新增 `suppression_wallclock_test.go`（264 行）等，写盘量增加
- **刚好跨过 10 分钟默认超时阈值**

即：不是 v0.12.1 引入回归，而是既有环境劣化 + 测试量增长叠加越线。

## 四、建议

### 对本机（立即可做）

1. **把工作区与 TEMP 加入 Kaspersky 排除项**（递归排除 `E:\Code\ai\netops-lab` 与 `E:\temp`）；
2. 测试时临时挂起实时防护；
3. 若长期开发，考虑把 `TEMP` 移到不扫描的盘。

### 对仓库（建议优化，非阻塞）

1. `Makefile` / CI 里给 `go test` 显式设 `-timeout 20m`——10 分钟默认值在慢盘/杀毒环境下不够；
2. 测试用 SQLite 可考虑 `_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)` 降低 fsync 次数
   （**注意**：这是测试专用配置，不能改生产 DSN 的持久性语义）；
3. 关键包 e2e 测试可用 `t.TempDir()` 落在 RAM disk（若 CI 支持）。

## 五、结论

**v0.12.1 的代码没有问题证据**：
- 18 个包全部 ok，含 `topology`(510s)、`metrics`(324s) 等重负载包；
- 6 个 FAIL 全部是超时，零断言失败；
- 单独跑 `-run TestOpen` 三个测试全部 PASS；
- `go vet` 全通过。

待环境优化（加入杀毒排除）后重跑 `go test ./...` 以取得完整绿灯证据。
