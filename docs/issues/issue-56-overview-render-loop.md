# [P0] 概览页无限渲染循环：组件体内裸调 getAlertStats，单用户打开首页即产生 52 QPS 持续压测

来源：对 `e258543`（v0.12.0）的 UI 全面扫描，2026-10-17
方法：headless Chrome + CDP Network 域逐请求捕获 + 进程 CPU 采样

---

## 一、现象

打开概览页 `/` 后，浏览器**无限循环**请求 `GET /api/alerts/stats`，永不停止。

CDP 实测（20 秒窗口）：

```
20秒内 /api/alerts/stats 请求次数: 1050
平均间隔: 19 ms          ← 约 52 次/秒
```

CPU 实测（首页保持打开 25 秒后采样）：

```
后端 netopslab 进程：单核占用 11.6%（12 核机器上 1% 全机）
Chrome 渲染进程：     单核占用 13%
```

**只要页面开着就不停。10 个用户同时在线 = 后端单核被吃满。**

## 二、根因（已定位到行）

`web/src/pages/Overview.tsx:379-383`

```tsx
// v0.11-R11：告警分布（stats 一次拿到三态计数 + 待处置级别矩阵，
// 取代 v0.9 的 listAlerts pageSize=1 探针——更少请求、更全信息）。
getAlertStats()                                  // ← 裸写在组件函数体里
  .then((stats) => setAlertStats(stats))
  .catch(() => setAlertStats(null));
```

**这段代码不在任何 `useEffect` 内，直接位于 `OverviewBody` 组件函数体中。**

循环链路：

```
渲染 → 组件体执行 → getAlertStats() 发请求
     → 响应回来 → setAlertStats(...) → 触发重渲染
     → 组件体再次执行 → getAlertStats() 再发请求 → 无限循环
```

同文件其它取数点（`load`、`loadGov`）都正确地包在 `useCallback` + `useEffect` 中
（第 328-356、358-377 行），**只有这一处漏了**。

全站扫描确认这是孤例——扫描 `web/src/pages/*.tsx` 中"组件体内裸调用 API 函数"的模式，
**仅命中 `Overview.tsx:381` 一处**，其余页面无此问题。

## 三、影响

1. **后端持续负载**：单用户 52 QPS，10 用户即打满单核；
2. **数据准确性**：每次响应都会写 `setAlertStats`，即使值不变 React 也会因新对象引用重渲染——
   这意味着页面**永远处于重渲染状态**，任何输入交互都可能被拖慢；
3. **功耗**：笔记本用户开着页面即持续耗电；
4. **日志噪音**：后端 access log 被同一路径刷屏（本次实测服务端日志被完全淹没，
   排查其它问题时无法看到有效请求）；
5. **与产品定位冲突**：本项目目标是"每天导入表格做巡检"的长期驻留型控制台，
   一个永远在刷接口的首页是最不该出现的问题。

## 四、建议修复

最小改动——包进 `useEffect`：

```tsx
useEffect(() => {
  let cancelled = false;
  getAlertStats()
    .then((stats) => { if (!cancelled) setAlertStats(stats); })
    .catch(() => { if (!cancelled) setAlertStats(null); });
  return () => { cancelled = true; };
}, []);
```

若需要"重跑检测后刷新告警数"，应挂在明确的触发点上（如 `load()` 成功回调、
或用户点击刷新），而不是依赖组件渲染。

**建议顺带统一**：该文件已有 `loadGuard`/`govGuard`（`useSeq`）守卫纪律，
告警统计也应纳入同一套守卫或新增独立 `alertGuard`，保持单取数点纪律一致。

## 五、验收标准

1. 打开 `/` 停留 30 秒，CDP 统计 `/api/alerts/stats` 请求数 **≤ 2**（首次 + 可能的显式刷新）；
2. 页面静置时后端进程 CPU 占用 **≈ 0%**；
3. 告警数字仍然正确显示（firing/error/warning/acked 四项）；
4. 建议补一条前端测试：渲染 `OverviewBody` 并断言取数函数**仅被调用一次**
   （可用 mock 计数），防止同类回归。

---

## 附：本轮 UI 全面扫描的其它发现

### 扫描覆盖

12 个路由全部加载验证，逐个收集 console 错误 / 未捕获异常 / 网络失败：

```
/  /import  /resources  /issues  /alerts  /rules
/maintenance  /report  /dev  /lab  /lab/:id  /nope-404
```

**console 错误、未捕获异常、HTTP 4xx/5xx：全部为 0。** 前端运行时的健壮性很好。

### 代码卫生（优秀，无缺陷）

- `any` 类型滥用：**0 处**
- `console.log` / `console.debug` 残留：**0 处**
- `TODO`/`FIXME`/`HACK`：**0 处**
- 所有 `setInterval`/`setTimeout` 均有对应清理逻辑

### 注：`gofmt -l` 报 293 个文件"未格式化"是假阳性

`gofmt -l .` 在 Windows 检出下会列出大量文件，但这些是 **CRLF 行尾**导致
（`gofmt` 只认 LF）。抽查 `internal/api/auth.go` 确认全部为 `^M` 行尾差异，
无真实格式问题。**建议 CI 在 Linux 上跑 gofmt，或本地加 `.gitattributes` 归一化**，
避免开发者在 Windows 上被这个噪音误导。

### 告警状态机（质量很高，无缺陷）

六个边界全部正确：

```
缺 ackedBy      → 400 "ackedBy 必填（确认人，告警审计字段）"
正常确认        → 200 {state:"acked", ackedAt, ackedBy}
重复确认        → 409 not_firing
正常清除        → 200 {state:"cleared", clearedAt}
重复清除        → 409 already_cleared
清除后状态      → 正确保留审计字段
```

### 维护窗口表单（校验正常，但语义提示缺失 → 见 #55）

空表单提交正确提示「请填写事由」。表单字段齐全（事由/开始/结束/范围/备注）。

**但页面说明写着「判定基准是指标数据时间线，可重放」——设计意图是对的，
表单却用 `datetime-local` 让用户填墙钟时间，没有任何"当前数据时刻"提示。**
这正是 #55 那个缺陷的用户界面成因：语义在那里，但没传达给填表的人。
