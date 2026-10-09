# ADR 0003 — MCP stdio 服务器（go-sdk v1.8.0，单工具面）

- 状态：已采纳（2026-10-09，Stage 0）。**部署态（TeleAgent 宿主）调用未验证**——见下"未验证声明"。
- 背景：NetOps Lab 要作为 TeleAgent 的工具提供方。协议选 MCP（Model Context Protocol）over stdio JSON-RPC 2.0。
- 决策：
  1. 使用官方 **`github.com/modelcontextprotocol/go-sdk` v1.8.0**（`mcp.NewServer` + `mcp.AddTool` + `IOTransport`），不自研 JSON-RPC；
  2. **单工具面**：只注册 `resource_search`（只读检索：`q/type/limit/offset` → `total + items[]`，每项带 workbook/sheet/rowNumber 来源）。**不提供** SQL、Shell、SSH、文件、任何写工具——这是安全不可协商项，测试 `TestMCPToolRegistry` 断言 `tools/list` 恰好等于 `["resource_search"]`；
  3. 服务器身份：name `netops-lab`，version `v0.1.0`；日志走 stderr（stdout 是协议通道）；
  4. DB 路径解析顺序：`--db` 参数 > `NETOPS_DB` 环境变量 > `./data/netops.db`。
- **教训（jsonschema tag）**：go-sdk 通过 `google/jsonschema-go` 从结构体 tag 生成工具输入 schema；tag 内容是**纯文本属性描述**，**不能**写成 `jsonschema:"description=…"`——`WORD=` 前缀是保留语法，启动即 panic（"tag must not begin with 'WORD='"）。本项目初版即踩中，被 MCP 回环测试当场捕获（若上线，`netopslab mcp` 对每个用户都会崩溃）。结论：tag 写纯中文描述即可。
- **CLI fallback 决策**：同时提供结构化 JSON CLI（`analyze/import/validate/report/query`，退出码 0/1/2）作为 TeleAgent 的第二集成路径——当 MCP 宿主不可用/不适用时，Skills 可直接解析 CLI 的单一 JSON 对象输出（见 `docs/teleagent-integration.md`）。两条路径共用同一服务层，零逻辑分叉。
- **未验证声明**：本开发环境没有部署态 TeleAgent 实例，**"TeleAgent 通过 MCP 调用 netopslab"未经端到端验证**。已验证的是：go-sdk 客户端经双 `os.Pipe` 对 `mcpserver.Run` 的完整 initialize/tools-list/tools-call 回环（`TestMCPServerResourceSearch`）。接入部署态时按 `docs/teleagent-integration.md` 操作并反馈。
- 备选与否决：手写最小 JSON-RPC（go-sdk 不可用时的 B 计划）——实际可用，弃用。
