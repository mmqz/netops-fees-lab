# ledger-2003-format.xls — 转换夹具（issue #7）

真实的 Excel 2003 二进制工作簿（BIFF8/CFB，magic `D0 CF 11 E0`），
用于 `internal/convert` 与 `internal/app/e2e_convert_test.go` 的离线
确定性测试。**不要用编辑器改动它**——它必须保持真实的 .xls 容器。

## 结构（真实台账形态）

- sheet `直供电`：双行表头（横向合并 8 处：A1:A2 / B1:B2 / C1:D1 /
  E1:E2 / F1:F2 / G1:G2 / H1:H2 / I1:I2），数据行含日期列
  （购电起始日/截止日）、RK 压缩小数（单价 0.85/0.92/0.75——钉住
  上游 RK×100 解码 bug 的回归）、公式列（应付电费 =G*H，缓存值
  110.5/58.88/0——钉住上游公式单元格整行丢弃的回归）。
- sheet `转供电`：单行表头，列名与 fees 导入的自动识别表头一致
  （站址编码 / 电费(元) / 账期 / 备注）——e2e 用它验证
  「转换产物可直接被 fees import 消费」。

## 再生成方式

```bash
python3 scripts/../scripts/r4_fixture.py   # openpyxl 生成 xlsx 源
#   → soffice --headless --convert-to xls （LibreOffice 真实 BIFF8 写出）
```
源脚本：`/home/z/my-project/scripts/r4_fixture.py`（openpyxl 构造 →
LibreOffice 无头转换）。当前字节由 LibreOffice 7.x 产出；再生成时
请复跑两个测试包确认断言仍成立。
