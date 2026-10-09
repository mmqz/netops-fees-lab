# ADR 0005 — 前端构建产物嵌入（dist stub + vite outDir + go:embed）

- 状态：已采纳（2026-10-09，Phase 1 前端交付 T8 + 构建 T7）
- 背景：交付形态是**单二进制**（`bin/netopslab`），前端必须打进 Go 可执行文件；同时仓库不能提交构建产物（体积、噪声、锁文件地狱）。
- 决策（三个部件咬合）：
  1. **vite outDir 指向仓库内部**：`web/vite.config.ts` 的 `build.outDir: '../internal/webui/dist'`，`emptyOutDir: true`（产物目录在 web/ 之外，vite 要求显式 empty）。`npm run build` = `tsc -b && vite build`（TS strict，零错误才出产物）。
  2. **提交占位、不提交产物**：`.gitignore` 写 `/internal/webui/dist/*` + `!/internal/webui/dist/.gitkeep`；`web/package.json` 的 `postbuild` 脚本在每次构建后**重建空的 `.gitkeep`**（`emptyOutDir` 会先清掉它）——保证任何克隆/检出后 `go:embed all:dist` 都有目标目录，`go build` 不报错。
  3. **`go:embed all:dist`**（`internal/webui/webui.go`）：`all:` 前缀包含点/下划线开头的文件（.gitkeep 也在内）。Handler 语义：存在文件按文件服务；未知路径回退 index.html（SPA 客户端路由）；**未构建前端时**（dist 里只有 .gitkeep，没有 index.html）非根路径安全 404，绝不回环。
- 构建顺序约束：**先 `make web` 再 `make build`**——`go build` 嵌入的是 dist 当时的内容。`make all` = `web + build` 固化此顺序。若跳过 `make web`，二进制仍能构建与跑 API，但 SPA 是占位态（curl / 得 404 而非 index.html）。
- 后果：
  - 正面：单文件分发/部署（~27MB 含前端）；前端构建产物永不入库；CI 可以把 web 与 go 拆成两个 job（见 `.github/workflows/ci.yml`）；
  - 负面/权衡：前端缓存策略受限于 embed FS（index.html 设 `Cache-Control: no-cache`，哈希资源走默认）；dev 模式仍用 vite dev server + `/api` 代理（不碰 dist）。
- 已验证：`make web`（npm ci + build，tsc strict 零错误）→ `make build` → `curl /` 返回真实 SPA（标题 `NetOps Lab · 云网资源治理`，引用 `/assets/index-*.js` 哈希包）；`make clean` 后重走全链路可复现。`.gitkeep` 在构建后保持存在（postbuild 重建）。
