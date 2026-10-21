# ADR 0008 — 检查点策略（Online Backup API + 工作区级恢复即前向变更集）

- 状态：已采纳（2026-10-09，Phase 2 / P2-T3）
- 背景：三层撤销的第 ③ 层需要一个"跨多个变更集、跨失败历史"的恢复点。约束：**禁止对活库做裸文件拷贝**（WAL 模式下拷贝 `-wal` 之外的 `.db` 会得到不一致快照）；必须用 SQLite 官方在线备份机制；恢复必须可审计、可再撤销。
- 关联：[ADR-0002](0002-purego-sqlite.md)（modernc.org/sqlite 选型），[ADR-0007](0007-lab-op-engine.md)（变更集机制），[docs/simulation.md](../simulation.md) §4。

## 决策 1 — 快照：Online Backup API（经匿名接口断言）

`repo.DB.Backup(ctx, dstPath)`（`internal/repo/backup.go`）：

```go
conn, _ := d.sql.Conn(ctx)               // 从 database/sql 连接池借一条连接
defer conn.Close()
conn.Raw(func(dc any) error {            // 拿到底层驱动连接（*sqlite.conn，未导出）
    driverConn, ok := dc.(interface {    // 匿名接口断言 —— 不 import 未导出类型
        NewBackup(dstURI string) (*sqlite.Backup, error)
    })
    …
    bck, _ := driverConn.NewBackup(dstPath)
    for { more, err := bck.Step(-1); …; if !more { break } }  // 全页拷贝
    return bck.Finish()
})
```

- `Step(-1)` 循环 + `Finish()`：一次拷完全部剩余页并正确收尾；源连接在备份期间继续可读写（这正是"online"的含义）。
- **为什么匿名接口断言**：modernc.org/sqlite 的 `NewBackup`/`NewRestore` 挂在**未导出**的 `*conn` 上，官方没有从 `driver.Conn` 暴露的路径；断言匿名接口是 Go 惯用的"结构化鸭子类型"，不引用未导出名、不 fork 驱动（P2-0 已 spike 验证 v1.60.1 暴露该面）。
- **为什么不是其他方案**：
  - *裸拷贝 .db 文件*：WAL 下数据可能还在 `-wal` 里，快照陈旧/撕裂——红线禁止；
  - *`VACUUM INTO`*：同样产出一致快照，但它是"复制优化后的整库"且语义面向压缩；Online Backup 是官方为备份设计的路径，页级增量、可分步（未来大库可 `Step(n)` 分批），且本 ADR 的断言方式对两者通用，选更正统的 API；
  - *SQL 层导出（dump 重建）*：文本转储无事务一致性保证，且要自己重新执行全部 DDL/DML。
- 落点：`<dbdir>/checkpoints/<workspaceId>/<cpId>.db`；`file_path` 存**相对路径**（库整体搬迁后检查点仍可用；#80 修复后该值恒为**逻辑形态（`/`）**——跨平台搬库（如 Windows 建库 → Linux 复原）也成立，存量行由 migration v32 归一，磁盘访问走 `filepath.FromSlash` 边界）；目录惰性创建；标记行（checkpoints 表）在**备份成功之后**插入——崩溃窗口只会留下"无引用的孤儿快照文件"（无害）而不会留下"指向不存在文件的行"。

## 决策 2 — 恢复：工作区级、前向操作变更集

`sim.RestoreCheckpoint` **不做**整表替换，而是：

1. 以只读 DSN（`?mode=ro`，无 journal pragma、**不跑迁移**——快照本身就是已迁移库）打开快照，用与状态哈希**同一个**类型化加载器读出该工作区的（设备、接口、链路）；
2. 与当前状态做差异，产出**纯前向操作**计划（依赖安全序）：
   1. 删多余链路 → 2. 删多余接口 → 3. 删多余设备 → 4. 建缺失设备 → 5. 建缺失接口（带快照状态/地址）→ 6. 建缺失链路（带快照状态/标签）→ 7. 对齐状态/地址差异；
   匹配键：设备按 `code`、接口按 `(device code, name)`、链路按 `(aDevice, aInterface, zDevice, zInterface)`——**业务键，不吃 id 漂移**（恢复重建的实体 id 是全新的）。
3. 作为一个普通变更集（trigger `checkpoint_restore`）经**同一引擎路径**应用——plan 预测引擎事务将分配的 id（序列确定性：第 k 个 create = 第 k 次 Allocate），create_link 因此能引用"尚不存在但即将存在"的接口。

**理由**：

- **统一审计**：恢复出现在变更集列表里，与用户操作、撤销并列——`GET /changesets` 是完整的操作史，没有"某天 03:00 表被换了"的暗门；
- **可再撤销**：恢复变更集自带逆操作，LIFO 撤销直接可用；
- **no-op 检测免费**：当前状态哈希 == 快照哈希（捕获时记录）⇒ 什么都不做（`changesetId:null, note:"already at checkpoint state"`）——sha256 比对代替全表 diff 试探；
- **失败语义复用**：任何不可表达的差异（如名称被改而操作目录无重命名）显式报 conflict，绝不静默"假装成功"。

**备选与否决**：

- *整表 DELETE+INSERT（表替换）*：绕过变更集机制（审计空档）、跨表外键顺序复杂、且工作区只是全库的一角——全库快照里还有其他工作区与 P1 资源表，替换会误伤；
- *只备份该工作区三张表的 SQL 行集*：看似省空间，实则自制了一整套备份格式（一致性、schema 版本、恢复校验全要自己做），Online Backup 把这些全部白送。

## 崩溃安全（诚实清单）

- id 分配 / 快照写 / 标记行是三步：崩溃至多烧一个 cp id 或留一个孤儿快照文件（不出现在列表中）；
- 标记行记录的状态哈希在备份成功**之后从快照重算**（v0.2.1 起，审计项 AUD1-9 落地；与本 ADR 初稿「备份前读活库」相反——初稿叙事已废弃，以代码为准：`sim.CreateCheckpoint` 在 `Backup` 后用与状态哈希同一类型化加载器只读打开快照重算）。理由：marker 恒描述恢复路径将要打开的**确切字节**——活库读取与备份完成之间落地的并发写不能使 marker 与快照脱钩；磁盘上被篡改的快照在恢复时过 marker 校验（409）而不是恢复到一个 marker 从未描述过的状态。受支持的单写者模式下该值等于活库哈希。恢复路径信任**快照内状态**（而非记录的哈希值——记录值只作篡改校验锚与 no-op 判据）。

## 后果与验证

- 正面：`TestCheckpointRestore`（ACC-P2-7：配置地址 → 检查点 → 破坏（状态翻转 + 删除）→ 恢复 ⇒ 哈希等于检查点哈希、发现与破坏前一致）；no-op 往返；快照文件被删 ⇒ 409 `checkpoint_missing_file`；`scripts/demo-lab.sh` 第 9 步现场演示（删除设备 → 恢复 3 个前向操作 → 哈希精确还原 → 重复恢复 no-op）。
- 权衡：全库快照意味着检查点体积 = 库体积（Phase 2 演示库 <1MB，无压力；万级实体再评估按工作区裁剪）；恢复的 create 依赖 id 预测，**仅在受支持的单写者模式下成立**（多工作区并发写是显式 out-of-scope）。
