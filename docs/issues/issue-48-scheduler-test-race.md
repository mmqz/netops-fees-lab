# [P1] TestSchedulerFiresAndRecords 确定性失败：亚秒间隔下断言「两轮之间 Running=false」不成立

来源：v0.10.0/v0.11 本地验证（Windows，2026-10-15）

## 现象

```
go test ./...  →  23 包 ok, 1 包 FAIL
--- FAIL: TestSchedulerFiresAndRecords (12.10s)
    scheduler_test.go:106: live status Running=true between rounds, want false
```

## 复现率：100%（连跑 5 次全失败）

```
第1次: --- FAIL: TestSchedulerFiresAndRecords (1.39s)
第2次: --- FAIL: TestSchedulerFiresAndRecords (2.33s)
第3次: --- FAIL: TestSchedulerFiresAndRecords (2.75s)
第4次: --- FAIL: TestSchedulerFiresAndRecords (2.24s)
第5次: --- FAIL: TestSchedulerFiresAndRecords (1.68s)
```

**确定性缺陷，非偶发**（也非平台差异——逻辑与 OS 无关）。

## 根因分析

### 测试的意图（`scheduler_test.go:97-107`）

```go
// Status snapshot while live: enabled, rounds >= 3, nextRunAt set.
st := sched.Status()
if !st.Enabled || st.IntervalSeconds != 0 || st.Rounds < 3 { … }
if st.NextRunAt == "" { … }
if st.Running {
    t.Error("live status Running=true between rounds, want false")   // ← L105-107
}
```

测试用**亚秒间隔**（`IntervalSeconds: 0`）驱动巡逻，连跑 ≥3 轮，
然后在 `Status()` 采样点断言 `Running == false`——即假设采样发生在「两轮之间」。

### 实现（`scheduler.go:128-145`）本身是正确的

```go
func (s *Scheduler) beginRound() bool {
    s.mu.Lock(); defer s.mu.Unlock()
    if s.running { return false }   // single-flight
    s.running = true
    return true
}
func (s *Scheduler) endRound() {
    s.mu.Lock(); defer s.mu.Unlock()
    s.running = false
    s.rounds++
}
```

互斥锁保护、single-flight 语义都正确。

### 矛盾点

**间隔为 0（亚秒）时，「两轮之间」的窗口极小甚至不存在**：

```
t0  轮次 N 结束 (running=false)
t0+ε 立即触发轮次 N+1 (running=true)
    ↑ Status() 采样极大概率落在这里
```

即：**巡逻几乎无间断 → Running 几乎恒为 true → 断言必然失败**。

## 影响

- **测试套件常红**：Windows/Linux 均失败，CI 门禁失效
- v0.9/v0.10 的 release commit 称「本地门禁全绿」，
  但实测该测试确定性失败——说明验收未真正跑通全量套件
- 掩盖真实回归（与其他失败混在一起时难以分辨）

## 修复建议

**方案 A（推荐）：断言改为「调度器处于活跃状态」而非「不在运行」**

```go
// 亚秒间隔下 Running 反映的是「当前是否有轮次在执行」，
// 它是合法状态而非缺陷；应断言的是「状态可被观测且一致」。
if st.Rounds < 3 { … }              // 已有
if st.NextRunAt == "" { … }         // 已有
// 删除 Running==false 断言，或改为：
if !st.Running && st.Rounds == 0 {
    t.Error("scheduler neither running nor has run")
}
```

**方案 B：在测试里给一个「可观测的空窗」**

把间隔调大到足以保证采样落在轮次之间（如 500ms + 轮次耗时 << 间隔），
或在断言前 `cancel()` 停止调度、等待当前轮结束，再断言 `Running == false`。

```go
cancel()                      // 停止后续 tick
// 等待在跑的轮次结束（轮询 Status 直至 Running==false，带超时）
deadline := time.Now().Add(2*time.Second)
for time.Now().Before(deadline) {
    if !sched.Status().Running { break }
    time.Sleep(10*time.Millisecond)
}
if sched.Status().Running {
    t.Error("Running still true after cancel + drain")
}
```

**方案 C：明确语义**——若产品定义为「轮次间 Running 必须为 false」，
则需在 `endRound` 后保证一定的 idle 窗口（与 single-flight 语义一致），
但那会人为降低巡逻频率，不建议。

**推荐 A + B 组合**：删除时序敏感的断言，改在「取消后」验证 `Running` 能回到 false
（这才是真正值得钉死的契约：停止后不残留 Running）。

## 验收

- [ ] `TestSchedulerFiresAndRecords` 稳定通过（连跑 10 次全绿）
- [ ] 仍能捕获真实缺陷：如 `running` 泄漏（轮次结束后不复位）应导致测试失败
- [ ] 全量 `go test ./...` 在 Windows + Linux 全绿

## 附：v0.10.0 本地验证其余结果（均通过）

| 项 | 结果 |
| --- | --- |
| 包数 | 23 ok / 1 FAIL（本项） |
| 版本 | v0.10.0（超集含 v0.11 的调度器） |
| #46 修复 | ✅ `stripClockFields` 精确剔除 `stats.elapsedMs` |
| #47 诉求 1 | ✅ actions 移到工具带末组 |
| #47 诉求 2 | ✅ 图标化 + nowrap，1600 视口单行 |
| #47 诉求 3 | ✅ 页头卡片移除（`.lab-pagebar` 无边框单行） |
| #47 诉求 4 | ✅ /lab 画布概览（SVG 缩略图 + 懒加载） |
| MCP | ✅ 新增 5 个只读分析工具（traverse/impact/context/alerts/metrics） |
| API | ✅ 67 路由 + OpenAPI 3 (/api/openapi.json) + /dev 参考页 |
| 数据 | ✅ 1,991 资源 / 3,193 关系 / 32 链路 / 64 接口，拓扑正常渲染 |