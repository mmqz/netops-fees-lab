## 现象
```
$ netopslab report --db ./data/netops.db --format json --out report.json
$ echo $?
0
```
**退出码 0，但 stdout 为空**。（对比：`--format html` 正常输出 JSON 摘要。）

## 复现
```
### report-json
EXITCODE=0
（无任何输出）
```

## 影响
- 无法通过 stdout 获取 JSON 报告的执行结果
- 与 §10「每个命令 stdout 恰好一个 JSON 对象」的 CLI 契约不一致
  （HTML 格式有此契约，JSON 格式缺失）

## 待确认
`--out` 文件是否已正确生成？若是「文件已生成但 stdout 静默」，
则属契约不一致；若文件也未生成，则为功能缺陷。
需要维护者确认预期行为后决定修复方向。

## 验收标准
- [ ] `report --format json` 行为符合 CLI §10 契约（stdout 输出一个 JSON 对象），或
- [ ] 文档明确说明 json 格式的静默语义