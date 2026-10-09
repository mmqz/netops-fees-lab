# ADR 0002 — 纯 Go SQLite 驱动（modernc.org/sqlite）

- 状态：已采纳（2026-10-09，Stage 0；环境踩坑记录在案）
- 背景：需要嵌入式单文件存储，且团队约束"无 CGO"（见 ADR-0001），排除 `mattn/go-sqlite3`。
- 决策：使用 **`modernc.org/sqlite` v1.60.1**（CC0 转译的 SQLite，纯 Go），驱动名 `sqlite`。
- ⚠️ **坑：模块路径不是 github.com**。正确的 module path 是 `modernc.org/sqlite`（**没有** `github.com/` 前缀；源码仓库在 github.com/modernc/sqlite，但 Go module path ≠ 仓库 URL）。写成 `github.com/modernc.org/sqlite` 会导致 `go get` 404——本项目早期实际踩过该坑（T1 日志），并已回写到工程规范。
- DSN（`internal/repo/repo.go`）：

```
file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)
```

  - `foreign_keys` **必须逐连接显式开启**（SQLite 默认关，DSN pragma 是可靠位置）；
  - `journal_mode(WAL)` + `busy_timeout(5000)`：读写并发与 5 秒写锁等待，Phase 1 单写者场景足够。
- 备选与否决：
  - `mattn/go-sqlite3`：成熟但需 CGO → 违反约束；
  - 纯文件 JSON / bbolt：放弃 SQL 的 CHECK 约束、事务、迁移生态与未来迁移到 Postgres 的路径；
  - Postgres：Phase 2+ 再评估（本阶段要求零外部服务）。
- 后果：
  - 正面：交叉编译友好；无需系统 sqlite3；DSN pragma 保持 SQL 层行为一致；
  - 负面：纯 Go 驱动首次编译较慢（~数十秒，2 核；之后走构建缓存）；大规模写入吞吐低于 C 驱动——Phase 1 数据量（几十行/批）远未触界。
- 验证：repo 层 22 个测试（迁移幂等、各聚合 CRUD、并发语义靠 WAL/busy_timeout 保障）；导入原子性/回滚测试全部经真实 SQLite 事务路径。
