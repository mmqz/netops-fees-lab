# [P2] #75 契约遗留（产品侧）：checkpoints.file_path 以平台原生分隔符持久化——跨平台搬库后 restore 必断

> GitHub issue：[#80](https://github.com/mmqz/netops-lab/issues/80)

来源：#79 修复时的全仓复扫（2026-10-20）。
关联：#75（跨平台路径契约裁决）、#79（测试侧镜像修复，ef0262a）。

---

## 一、机理

**写侧**（`sim.CreateCheckpoint`，checkpoint.go:73）：

```go
relPath := filepath.Join(checkpointDir, workspaceID, cpID+".db")  // 平台原生分隔符
...
cp := domain.Checkpoint{ ..., FilePath: relPath }                  // 原样入库
```

**读侧**（`RestoreCheckpoint`，checkpoint.go:218）：

```go
snapshotPath := filepath.Join(filepath.Dir(s.db.Path()), cp.FilePath)
if _, err := os.Stat(snapshotPath); err != nil { → checkpoint_missing_file }
```

- Windows 建库：`file_path = "checkpoints\<ws>\cp-1.db"`。同平台自洽（Windows 把 `\` 当分隔符），**纯 Windows 使用无恙**——潜伏缺陷，非现行故障。
- 该库拷到 Linux restore：`\` 在 Linux 不是分隔符 → 整串变成一个字面文件名 → stat 必败 → **restore 必断**（响亮失败：409 checkpoint_missing_file，非静默）。
- 反向（Linux 建库 → Windows 用）：Windows 接受 `/`，恰好无恙。

与 #75（fee snapshot_path）是**同一契约的最后一个漏网点**：持久化在 DB 里的相对路径是跨平台数据契约，必须恒为逻辑形态（`/`），磁盘访问走 `filepath.FromSlash` 边界。3377762 + v31 已把 fee 侧闭环，sim 侧漏网。

## 二、为什么测试没红

`sim/checkpoint_test.go:72` 的期望值用同样的 `filepath.Join` 构造——两边同形，任何平台都自洽通过。#79 是「测试期望值坏、产品对」；本 issue 是「产品坏、测试跟着坏」——需要连着改，测试改完即契约守卫。

## 三、全仓普查（持久化路径列，2026-10-20）

| 列 | 写侧形态 | 判定 |
| --- | --- | --- |
| `fee_batches.snapshot_path` | #75 前原生 → 3377762 改 `path.Join` + v31 归一化 | ✅ 已闭环 |
| `import_batches.workbook_path` | 调用方传入路径**原样记录**（本地绝对/相对路径） | ➖ 不同类：记录的是「导入时本地文件在哪」，文件本身不跨平台存在；GC 三形态匹配正为此设计。非契约违规 |
| `checkpoints.file_path` | `filepath.Join` 生成 → **本 issue** | ❌ 最后一个违规点 |

API 面核查：`checkpointItem` DTO 只回 `checkpointId/label/workspaceHash/createdAt`，**不暴露** file_path——波及面收窄到 DB 存储 + restore 磁盘访问两处。

## 四、归一化安全性（v31 论证平移）

`file_path` 三个组件全部系统生成：`checkpointDir`（常量 `"checkpoints"`）、`workspaceID`（`uuid.NewString()`）、`cpID`（`FormatSimID(SeqCheckpoint, n)` = `cp-<n>`）——**任何 `\` 都只能是分隔符**。v31 的「Windows 文件名禁 `\` → 行内每个 `\` 必为分隔符」论证逐字平移成立。

## 五、修复方案

1. **写侧**：`relPath := path.Join(...)`（逻辑形态）+ `dst := filepath.Join(dir, filepath.FromSlash(relPath))`（磁盘边界）；
2. **读侧**：`filepath.Join(dir, filepath.FromSlash(cp.FilePath))`；
3. **迁移 v32**（循 v31 形制）：`UPDATE checkpoints SET file_path = replace(file_path, '\', '/') WHERE file_path LIKE '%\%'` —— 纯 UPDATE、幂等、无 schema 变更、foreign_keys 保持 ON；
4. **测试**：checkpoint_test.go 期望值改字符串常量 + FromSlash 读盘 + 入库形态反斜杠守卫；repo 侧仿 migrations_v31_test.go 钉死 v32 重写行为。

## 六、验收标准

1. 写侧入库值恒为 `checkpoints/<ws>/cp-<n>.db`（测试钉死）；
2. 迁移测试：含反斜杠行被重写、无反斜杠行不动、二跑幂等；
3. 既有库升级路径正常，Open 幂等；
4. 全门禁绿（gofmt/vet/25 包/tsc/vitest/web build）；
5. （配额恢复后）windows CI job 绿 run 复核——挂 #76。

## 七、定级：P2（非 P1）的理由

同平台使用完全无恙（Windows↔Windows、Linux↔Linux 都自洽）；破坏仅在「跨平台搬库」这一显式工作流下发生，且失败响亮（409 带清晰错误）非静默；但契约裁决已在 #75 确立（持久化相对路径 = 跨平台契约），此为该契约的漏网终点站，且修复成本一期可收（~4 文件）。

---

## 修复记录（2026-10-20，推送 06ce6cd）

**修复方案 = 第五节原样落地**：

| 触点 | 修复内容 |
| --- | --- |
| `sim/checkpoint.go` 写侧 | `relPath := path.Join(...)` + `dst` 走 `filepath.FromSlash` 边界 |
| `sim/checkpoint.go` 读侧 | restore 的 `snapshotPath` 走同一 `FromSlash` 边界（新旧行同解析） |
| `repo/migrations_v32.go` | v31 孪生：`replace('\','/')` + `LIKE '%\%'` 守卫，纯 UPDATE 幂等；磁盘快照文件不动，只改标记行 |
| `repo/migrations_v32_test.go` | v31 测试同形：raw SQL 种 Windows 形态行 + POSIX 形态行 → 前者归一后者逐字节不动 → MAX(version)=32 → 二次 reopen 幂等 |
| `sim/checkpoint_test.go` | 期望值字符串常量 + 反斜杠守卫 + FromSlash 读盘 |
| 14 个迁移测试 | registry-head 钉随约定 31→32（v31 自己的隔离测试保持 31——withoutV31 模式下 v32 不在注册表） |

**验收对照**：1-4 全 ✅（25 包 ok / 0 FAIL / -race sim+repo 0 race / tsc 0 / vitest 40/40 / demo.sh + demo-lab.sh 回归绿——checkpoint 快照→破坏→恢复→no-op 旅程与状态哈希基线不变）；5 ⏳ 挂 #76 配额（run 39420112339：4 job 壳 + runner_id=0 + 3 秒死，配额模式逐字一致）。

**契约闭环**：至此 #75「持久化相对路径 = 跨平台契约」的两个产品侧落点（fee v31 / checkpoint v32）全部闭环；第三节普查表确认无其他违规点。
