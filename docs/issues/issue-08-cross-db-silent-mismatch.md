## 现象
用 CLI 在库 A 创建工作区后，Web 界面显示「工作台不存在或已被删除」，
而 CLI 查询该工作区一切正常。

## 复现
```
# 服务连的是 data/netops.db
$ netopslab serve --db ./data/netops.db

# 但 CLI 用了另一个库
$ netopslab lab workspace create --db ./data/geo-test.db --name demo --all-batches
{"workspaceId":"ff3038b3-...", ...}      # 创建成功

# 浏览器打开 /lab/ff3038b3-...
# -> 工作台不存在或已被删除

$ curl /api/workspaces/ff3038b3-...
{"error":{"code":"not_found",...}}        # 404，因为不在服务的库里
```

## 影响
CLI 与服务可自由指定 `--db`，两者不一致时**无任何警告**。
用户看到「工作台不存在」会误以为数据丢失或程序 bug，
实际只是连错库——排查成本高。

## 建议修复方向（讨论）
- `serve` 启动时在日志中显著打印实际使用的 DB 绝对路径（当前已打印相对路径）
- 或提供 `netopslab doctor` 之类的自检命令，打印实际生效的 DB 及其内容摘要
- 前端错误文案可更精确（「该工作台不在当前数据库 `<path>` 中」）

## 验收标准
- [ ] 用户能快速辨别「连错库」与「数据丢失」两种情况