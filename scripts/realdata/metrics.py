#!/usr/bin/env python3
"""metrics.py — W27b：真实设备端口性能工作簿（7 日，含劣化场景）。

输入：testdata/realdata/realdata-core.json + devices.xlsx（workbooks.py 产出）
输出：testdata/realdata/perf-day-01..07.xlsx + ingest-mapping.json

口径（诚实披露，manifest 同步）：
- 指标数值全部为模拟（示范形态：正常日稳定 + 哈大齐干线劣化日趋势）；
- 设备名称/端口名 = 演示工作簿真实布点设备（摄入后绑定真实 port 资源）；
- 数值遵循 internal/metrics/keys.go 目录 advisory 界（不越界，摄入零告警噪音）；
- 劣化设计：大庆/齐齐哈尔传输设备01 的 xgei-1/1/1 端口接收光功率 7 日内
  -8.0 → -24.0 dBm 线性劣化 + 接收丢包率 0 → 4.8%，触发种子检测规则
  （丢包率连续 3 点越限 → firing 告警 → run_state 联动）。
- 全部确定性：无随机，同输入字节级同输出。
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

from openpyxl import Workbook, load_workbook

sys.path.insert(0, str(Path(__file__).resolve().parent))
from common import CORE_JSON, WORKBOOK_DIR  # noqa: E402

DAYS = 7
START_DATE = "2026-10-06"
HEADERS = ["日期", "设备名称", "端口名",
           "接收光功率均值", "发送光功率均值", "接收丢包率", "发送丢包率",
           "接收带宽利用率", "发送带宽利用率", "接收速率", "发送速率",
           "接收错误包数", "发送错误包数", "光模块温度均值", "供电电压",
           "偏置电流", "接收光功率谷值"]

MAPPING = {
    "tsField": "日期",
    "resourceType": "port",
    "resolveBy": ["device_name", "port_name"],
    "metrics": {
        "optical_power_rx_avg_dbm": "接收光功率均值",
        "optical_power_tx_avg_dbm": "发送光功率均值",
        "packet_loss_rx_pct": "接收丢包率",
        "packet_loss_tx_pct": "发送丢包率",
        "bandwidth_util_rx_pct": "接收带宽利用率",
        "bandwidth_util_tx_pct": "发送带宽利用率",
        "rx_rate_mbps": "接收速率",
        "tx_rate_mbps": "发送速率",
        "errors_rx_count": "接收错误包数",
        "errors_tx_count": "发送错误包数",
        "module_temp_avg_c": "光模块温度均值",
        "supply_voltage_v": "供电电压",
        "bias_current_ma": "偏置电流",
        "optical_power_rx_min_dbm": "接收光功率谷值",
    },
}

# 劣化端口（哈大齐干线两站 01 号设备的第 1 端口）——按设备名后缀匹配
DEGRADE_DEVICE_HINTS = ("大庆市传输设备01", "齐齐哈尔市传输设备01")
DEGRADE_PORT = "xgei-1/1/1"


def date_plus(day: int) -> str:
    from datetime import date, timedelta
    y, m, d = map(int, START_DATE.split("-"))
    return (date(y, m, d) + timedelta(days=day)).isoformat()


def row_for(dev_name: str, ip: str, port: str, day: int, dev_idx: int, port_idx: int) -> list:
    degrade = dev_name in DEGRADE_DEVICE_HINTS and port == DEGRADE_PORT
    # 正常基线（确定性伪随机形态：小数位三角函数周期，无 RNG）
    ph = (dev_idx * 7 + port_idx * 3 + day) % 12
    rx_p = -8.0 - 0.4 * ((ph % 5) / 5.0)
    tx_p = -2.0 - 0.2 * ((ph % 4) / 4.0)
    loss = 0.01 * (ph % 3)
    if degrade:
        t = day / (DAYS - 1)
        rx_p = -8.0 - 16.0 * t          # -8 → -24 dBm
        loss = 0.02 + 4.8 * t           # → ~4.8%
    rx_min = rx_p - 1.2
    util_rx = 30.0 + 8.0 * ((ph % 7) / 7.0)
    util_tx = 25.0 + 6.0 * ((ph % 5) / 5.0)
    rx_rate = 800.0 + 60.0 * ((ph % 6) / 6.0)
    tx_rate = 370.0 + 40.0 * ((ph % 4) / 4.0)
    errs = (dev_idx + port_idx + day) % 3
    temp = 40.0 + 2.0 * ((ph % 5) / 5.0) + (6.0 * (day / (DAYS - 1)) if degrade else 0.0)
    volt = 3.30
    bias = 35.0 + 2.0 * ((ph % 4) / 4.0)

    def fmt(v: float, nd: int = 2) -> str:
        return f"{v:.{nd}f}"

    return [date_plus(day), dev_name, port,
            fmt(rx_p), fmt(tx_p), fmt(loss, 2), fmt(loss * 0.5, 2),
            fmt(util_rx, 1), fmt(util_tx, 1), fmt(rx_rate, 1), fmt(tx_rate, 1),
            str(errs), str((port_idx + day) % 2), fmt(temp, 1), fmt(volt, 2),
            fmt(bias, 1), fmt(rx_min)]


def main() -> None:
    _core = json.loads(CORE_JSON.read_text(encoding="utf-8"))  # 存在性校验
    wb = load_workbook(WORKBOOK_DIR / "devices.xlsx", read_only=True)
    ws = wb.worksheets[0]
    it = ws.iter_rows(min_row=2, values_only=True)
    devices = []
    for r in it:
        if r and r[0]:
            devices.append({"name": str(r[1]), "ip": str(r[5]), "ports": ["xgei-1/1/1", "xgei-1/1/2", "xgei-1/1/3"]})
    wb.close()
    if not devices:
        raise SystemExit("devices.xlsx 为空：先运行 workbooks.py")

    for day in range(DAYS):
        out = WORKBOOK_DIR / f"perf-day-{day + 1:02d}.xlsx"
        w = Workbook()
        s = w.active
        s.title = "端口性能"
        s.append(HEADERS)
        for di, d in enumerate(devices):
            for pi, p in enumerate(d["ports"]):
                s.append(row_for(d["name"], d["ip"], p, day, di, pi))
        w.save(out)
    (WORKBOOK_DIR / "ingest-mapping.json").write_text(
        json.dumps(MAPPING, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"perf-day 1..{DAYS}: {len(devices)} 设备 × 3 端口 = {len(devices) * 3} 行/日")
    print(f"劣化端口：{'、'.join(DEGRADE_DEVICE_HINTS)} 的 {DEGRADE_PORT}")


if __name__ == "__main__":
    main()
