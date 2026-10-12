## 现象
导入一份表头完整的工作簿，结果：
```json
{"summary":{"rowsTotal":0,"rowsImported":0,"rowsSkipped":0,"rowsFailed":0}}
```
**一行都没导入，且无任何错误或警告。**

## 根因
`internal/mapping/suggest.go:88` 的 `SuggestType` 仅凭 sheet 名判断类型：
```go
func SuggestType(sheetName string) (domain.ResourceType, bool) {
	for _, spec := range Specs {
		if strings.Contains(sheetName, spec.SheetHint) {
			return spec.Type, true
		}
	}
	return "", false
}
```
名称为 `Sheet1`、`导出结果`、`查询结果` 等（网管/系统导出的默认命名）
不含任何 SheetHint 关键词 → `SuggestMapping` 判 `action: skip`（suggest.go:114）。

## 复现对照（同一文件、同一运行）
| 映射方式 | 结果 |
| --- | --- |
| 自动建议（sheet 名 `Sheet1`） | `{rowsTotal:0, rowsImported:0}` |
| 手工指定 `resourceType: port` | `{rowsTotal:5, rowsImported:2, rowsSkipped:1, rowsFailed:2}` |

**唯一变量是映射方式**，证明堵点在类型识别。

## 影响
真实网管、网优、GIS 系统导出的 sheet 名通常无业务含义，
导致「导入成功但 0 行」的静默失败——用户不易察觉。

注意：分析器其实已从表头正确识别出 12 个字段
（`device_name`/`device_ip`/`port_name`/`bandwidth_mbps`/`vendor`/`model`…），
但该信息未被用于决定类型。

## 建议修复方向
sheet 名未命中时，增加**表头指纹回退**：
以各类型 spec 的字段/同义词匹配表头，覆盖率达标则建议该类型。
需保持既有 sheet 名优先级与平局规则不变（现有测试对此有严格约束）。

## 验收标准
- [ ] sheet 名为 `Sheet1` 但表头匹配某类型时，建议 `action: import` 且类型正确
- [ ] 表头无法匹配任何类型时，仍保持 `action: skip`
- [ ] 既有 sheet 名识别行为与平局规则不变（现有测试全绿）