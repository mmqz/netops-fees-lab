# NetOps Lab — 云网资源治理与 AI 仿真工作台

面向云网运营场景的本地优先（local-first）工程应用：多 Excel 工作簿资源导入 → 数据标准化 → 质量校验 → 关系查询，并为第二阶段的虚拟仿真与受控 MCP 操作奠定工程基础。

> ⚠️ **合规声明**：本平台不具备生产设备写能力。AI 仅可操作本地实验室工作区；生产操作必须由人员通过公司批准的外部工具执行。仓库内所有数据均为合成测试数据，仅用于工程方法验证，不代表真实网络运行情况。
>
> ⚠️ **Compliance**: This platform has NO production-device write capability. AI may only operate the local lab workspace. All data in this repository is synthetic and used solely for engineering-method validation.

## 当前阶段（Roadmap）

- **Stage 0 — 技术验证**（2026-10-09 ~ 10-23）：XLSX 读取、SQLite 存取、MCP 单工具、事务原子性、打包冒烟。
- **Phase 1 — 资源治理**（2026-10-24 ~ 11-15）：导入工作簿、列映射、标准化、质量校验、关系查询、质量报告。
- **Phase 2 — 竞赛 MVP**（2026-11-16 ~ 12-31）：虚拟设备/链路仿真、故障注入、变更比较与撤销、MCP 工具全集。（不在本仓库当前交付范围）

## 快速开始（随交付完善）

```bash
make test      # 运行全部 Go 测试
make web       # 构建前端（需 Node 18+）
make build     # 构建单二进制 netopslab
./bin/netopslab serve --addr :8080 --db ./data/netops.db
```

## 技术栈

Go 1.27（纯 Go，无 CGO）· SQLite（modernc.org/sqlite 驱动）· React 18 + TypeScript + Vite · MCP（modelcontextprotocol/go-sdk，STDIO）

## 文档索引（随交付完善）

- `docs/architecture.md` — 架构与模块边界
- `docs/data-model.md` — 数据模型与 DDL
- `docs/api.md` — HTTP API 契约
- `docs/verified-capabilities.md` — 已验证 / 未验证能力清单（诚实性文档）
- `docs/adr/` — 架构决策记录
