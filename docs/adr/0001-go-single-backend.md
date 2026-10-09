# ADR 0001 — Go 单一后端、无 CGO、stdlib-first

- 状态：已采纳（2026-10-09，Stage 0）
- 背景：NetOps Lab 需要一个可嵌入 AI 宿主（TeleAgent）与运维本地的工具型后端：Excel 解析、SQLite 存取、HTTP API、MCP stdio、CLI、HTML 报告。
- 决策：
  1. 后端只用 **Go**（1.27）。不引入 Python/Java/Rust 等第二运行时；
  2. **无 CGO**（`CGO_ENABLED` 默认可关）：交叉编译零负担、无系统库依赖，SQLite 选纯 Go 驱动（见 ADR-0002）；
  3. **stdlib-first**：HTTP 路由用 `net/http` 标准库（Go 1.27 method patterns：`mux.HandleFunc("GET /api/resources/{id}", …)`）；JSON 用 `encoding/json`；模板用 `html/template`；日志用 `log/slog`。不引入 Web 框架、ORM、DI 容器；
  4. 第三方依赖收敛为四个：`xuri/excelize/v2`（Excel）、`modernc.org/sqlite`（驱动）、`modelcontextprotocol/go-sdk`（MCP）、`google/uuid`（ID）。新增依赖必须记录理由。
- 理由：单二进制分发（含前端约 27MB）是交付形态；工具链越薄越可审计；项目规模不需要框架抽象。
- 后果：
  - 正面：构建/CI 简单（`go build` 即全量）；无 CGO 使 linux/amd64 构建 ~33s（首次、2 核）；嵌 `go:embed` 天然一体；
  - 负面：无框架意味着中间件（日志/恢复/分页）手写（`internal/api/server.go` ~100 行）；换来的是零魔法。
- 验证：`go vet` 干净；`make build` 产物单文件可执行；96 个测试全部基于标准库 `testing`。
