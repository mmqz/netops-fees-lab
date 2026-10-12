# [Windows] `.mjs` 静态资源返回 `text/plain`，MapLibre Worker 加载失败

> **平台限定：仅 Windows。**
> 这是叠加在地图主因（issue-01 CSS 高度塌陷）之上的第二个独立缺陷。
> Linux 下 Go 内置 MIME 表含 `.mjs` 映射，故 CI 全绿。

## 环境
- OS: Windows（amd64）
- 版本: v0.6.0 @ 2033de8

## 现象
浏览器控制台报错：
```
Error: Worker failed to load. Check that the worker URL is correct.
    at Worker.<anonymous> (/assets/vendor-maplibre-haL-SkfR.js:5:273149)
```

## 根因
```
Windows:
  GET /assets/maplibre-gl-worker-VaXr2Fck.mjs
  Content-Type: text/plain; charset=utf-8        ← 错误

Linux (Docker):
  同一个文件
  Content-Type: text/javascript; charset=utf-8   ← 正确
```

Go 标准库查询：
```
.mjs  ->  "text/plain; charset=utf-8"    ← 缺陷
.js   ->  "application/javascript"       ← 正常
```

Windows 上 `mime.TypeByExtension` 从注册表读取，`.mjs` 未注册即回退 `text/plain`；
浏览器拒绝将 `text/plain` 加载为 ES Module Worker。
`internal/webui/webui.go:52` 仅硬编码 HTML 的 Content-Type，其余依赖标准库推断。

## 复现对照
| 平台 | `.mjs` Content-Type | Worker |
| --- | --- | --- |
| Windows | `text/plain` | ❌ 加载失败 |
| Linux (Docker) | `text/javascript` | ✅ 正常 |

## 影响
- Windows 上 2D/3D 地图**双重失效**（本缺陷 + issue-01 CSS 缺陷）
- Linux 仅受 issue-01 影响
- **修好 issue-01 后，Windows 上地图仍不可用**，必须同时修本项

## 建议修复方向
在 `internal/webui/webui.go` 显式注册现代资源类型 MIME，不依赖平台注册表。

## 验收标准
- [ ] Windows 下 `/assets/*.mjs` 返回 `text/javascript` 或 `application/javascript`
- [ ] `window.__geomap.loaded()` 为 `true`（配合 issue-01 修复）
- [ ] Linux 行为不变
