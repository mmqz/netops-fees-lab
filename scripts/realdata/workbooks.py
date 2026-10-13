#!/usr/bin/env python3
"""workbooks.py — W27：真实数据核心 → v0.8.0 全层演示工作簿（33 类型）。

输入：testdata/realdata/realdata-core.json（extract.py 产出）
输出：testdata/realdata/*.xlsx + manifest.json（数据溯源与真实/模拟披露）

诚实原则（与仓库既有文化一致）：
- 真实列（来源+口径见 manifest「real」字段）：局站名/坐标/地市、机房与设备
  所在真实位置、光缆 A/Z 端局站与锚点链里程、光缆段端点、基站全部属性、
  接头盒/人井坐标（真实锚点/走廊内插）。
- 模拟列（manifest「simulated」字段）：芯数、波长/速率（真实标准网格值，
  但占用关系为示范）、IP 地址、厂商型号（真实产品族名，示范值）、客户/业务/
  专线名称（虚构）、端口/板卡/机架/ODF/PON 编码细节。
- 全部取值确定性（无随机）：同一 core.json → 字节级同输出。

工作表名命中 SheetHint（表名含提示词），列头 = spec Label（建议置信度 1.0）。
导入顺序（ref 解析依赖，demo-real.sh 按此序）：
  局站→机房→基站→客户→VPN→环网→IGP→设备→端口→板卡→机架→ODF→端子→
  管道→人井→杆路→光缆→光缆段→接头盒→纤芯→波道→时隙→电路→地址池→业务→
  专线→BBU→RRU→OLT→PON口→ONU
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

from openpyxl import Workbook

sys.path.insert(0, str(Path(__file__).resolve().parent))
from common import CORE_JSON, MANIFEST, WORKBOOK_DIR  # noqa: E402

# ---------------------------------------------------------------------------
# 演示词汇表（全部确定性；真实产品族名/标准网格值为「示范值」非某网真实配置）
# ---------------------------------------------------------------------------

VENDORS = [("华为", "OptiX OSN 9800"), ("中兴", "ZXMP M920"), ("烽火", "CiTRANS 660")]
BOARD_MODELS = [("100GE 线路板", "线路板"), ("主控处理板", "主控板"), ("2M 支路板", "支路板")]
ITU_GRID = [(1, "1530.33"), (2, "1531.12"), (3, "1531.90"), (4, "1532.68")]  # 100GHz C 波段
BBU_MODELS = ["BBU5900", "BBU6900"]
OLT_MODELS = ["MA5800-X17", "ZXA10 C320"]

CUSTOMERS = [
    ("CUST-001", "哈尔滨松江云计算有限公司", "政企", "张工"),
    ("CUST-002", "哈尔滨北岸金融服务外包公司", "金融", "李经理"),
    ("CUST-003", "齐齐哈尔鹤城医院集团", "医疗", "王主任"),
    ("CUST-004", "大庆油田数字服务分公司（虚构演示名）", "能源", "刘工"),
    ("CUST-005", "牡丹江雪城商贸有限公司", "商贸", "陈经理"),
    ("CUST-006", "佳木斯三江农业科技公司", "农业", "赵工"),
    ("CUST-007", "绥化北林食品加工有限公司", "制造", "孙经理"),
    ("CUST-008", "伊春林都旅游文化公司", "文旅", "周主管"),
    ("CUST-009", "黑河界江口岸物流有限公司", "物流", "吴经理"),
    ("CUST-010", "加格达奇兴安岭山特产公司", "农业", "郑经理"),
    ("CUST-011", "漠河北极村文旅发展有限公司（虚构演示名）", "文旅", "冯经理"),
    ("CUST-012", "双鸭山煤电信息化中心（虚构演示名）", "能源", "何工"),
]

# 设备布点（北南干线 + 东西干线）：地市/锚点局站名 → 2 台/站
DEVICE_SITE_NAMES = [
    "哈尔滨市", "大庆市", "齐齐哈尔市", "讷河市", "嫩江市", "加格达奇区", "塔河县", "漠河市",
    "牡丹江市", "佳木斯市", "尚志市", "绥化市", "伊春市", "七台河市", "鸡西市",
]


def gen_ip(i: int, j: int) -> str:
    """管理 IP（模拟）：10.40+站序.0.10+设备序"""
    return f"10.{40 + i}.0.{10 + j}"


def radio_gen(st: dict) -> str:
    m = {"GSM": "2G", "UMTS": "3G", "LTE": "4G", "NR": "5G"}
    order = ["2G", "3G", "4G", "5G"]
    gens = sorted({m[r] for r in st["radios"]}, key=order.index)
    return "/".join(gens)


# ---------------------------------------------------------------------------
# xlsx 写出（表头=Label，行=纯文本）
# ---------------------------------------------------------------------------

def write_xlsx(name: str, sheet: str, headers: list[str], rows: list[list]) -> str:
    fn = WORKBOOK_DIR / f"{name}.xlsx"
    wb = Workbook()
    ws = wb.active
    ws.title = sheet
    ws.append(headers)
    for r in rows:
        ws.append([("" if v is None else str(v)) for v in r])
    wb.save(fn)
    return fn.name


def main() -> None:
    core = json.loads(CORE_JSON.read_text(encoding="utf-8"))
    sites = core["sites"]
    trunks = core["trunks"]
    stations = core["stations"]
    by_name = {s["site_name"]: s for s in sites}
    by_id = {s["site_id"]: s for s in sites}
    WORKBOOK_DIR.mkdir(parents=True, exist_ok=True)

    counts: dict[str, int] = {}
    def reg(name: str, n: int):
        counts[name] = n

    # ---- 1 局站（真实：OSM 地名 + OSM id 身份 + 地市归属）----
    reg("sites", len(sites))
    write_xlsx("sites", "局站表", ["局站ID", "局站名称", "地市", "区县"],
               [[s["site_id"], s["site_name"], s["city"], s["district"]] for s in sites])

    # ---- 2 机房（真实位置归属；编号/名称模拟）----
    mrs = [{"id": "MR" + s["site_id"][2:], "site": s} for s in sites]
    reg("machine_rooms", len(mrs))
    write_xlsx("machine_rooms", "机房表", ["机房ID", "机房名称", "局站ID", "地市", "区县"],
               [[m["id"], f"{m['site']['site_name']}传输机房", m["site"]["site_id"],
                 m["site"]["city"], m["site"]["district"]] for m in mrs])

    # ---- 3 基站（真实：OpenCelliD 位置/制式/运营商/地市；编码=主小区 CGI）----
    reg("base_stations", len(stations))
    write_xlsx("base_stations", "基站表",
               ["基站编码", "无线基站名称", "基站类型", "地市"],
               [[st["code"], st["name"], radio_gen(st), st["city"]] for st in stations])

    # ---- 4 客户（虚构名单，名称带「虚构演示名」或全虚构词）----
    reg("customers", len(CUSTOMERS))
    write_xlsx("customers", "客户表", ["客户编码", "客户名称", "客户类型", "联系人"],
               [list(c) for c in CUSTOMERS])

    # ---- 5 VPN 实例 ----
    vpns = [
        ("L3VPN-001", "政企专线VPN", "65001:100", "65001:100", "CUST-001"),
        ("L3VPN-002", "金融专线VPN", "65001:200", "65001:200", "CUST-002"),
        ("L3VPN-003", "医疗专线VPN", "65001:300", "65001:300", "CUST-003"),
    ]
    reg("vpn_instances", len(vpns))
    write_xlsx("vpn_instances", "VPN实例表", ["VPN编码", "VPN名称", "RD", "RT", "客户"],
               [list(v) for v in vpns])

    # ---- 6 环网（结构示意：节点=走廊锚点局）----
    hdq_wp = next(t for t in trunks if t["code"] == "TRK-HDQ")["waypoints"]
    jm_wp = next(t for t in trunks if t["code"] == "TRK-JM")["waypoints"]
    rings = [
        ("RING-HDQ", "哈大齐复用段环", "二纤双向复用段环", len(hdq_wp)),
        ("RING-JM", "佳牡通道保护环", "通道保护环", len(jm_wp)),
    ]
    reg("rings", len(rings))
    write_xlsx("rings", "环网表", ["环编码", "环名称", "环类型", "节点数"],
               [list(r) for r in rings])

    # ---- 7 IGP 域 ----
    igps = [
        ("IGP-OSPF-01", "干线OSPF域", "1", "0.0.0.0", "OSPF"),
        ("IGP-ISIS-02", "城域IS-IS域", "2", "49.0001", "IS-IS"),
    ]
    reg("igp_domains", len(igps))
    write_xlsx("igp_domains", "IGP域表", ["域编码", "域名称", "进程号", "区域号", "协议"],
               [list(i) for i in igps])

    # ---- 8 设备（真实坐标/局站/机房归属；编码/IP/型号为模拟）----
    dev_sites = []
    for n in DEVICE_SITE_NAMES:
        s = by_name.get(n) or next((x for x in sites if x["site_name"].startswith(n)), None)
        if s is not None:
            dev_sites.append(s)
    devices = []
    for i, s in enumerate(dev_sites):
        for j in (1, 2):
            vendor, model = VENDORS[(i + j) % len(VENDORS)]
            devices.append({
                "code": f"DEV-{s['site_id'][2:]}-{j:02d}",
                "name": f"{s['site_name']}传输设备{j:02d}",
                "site": s, "ip": gen_ip(i, j), "vendor": vendor, "model": model,
                "mr": "MR" + s["site_id"][2:],
                "ring": "RING-HDQ" if s["site_name"] in ("哈尔滨市", "大庆市", "齐齐哈尔市") else "",
                "igp": "IGP-OSPF-01" if i < 8 else "IGP-ISIS-02",
            })
    reg("devices", len(devices))
    write_xlsx("devices", "设备清单",
               ["设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态", "所属机房",
                "所属环", "所属IGP域", "经度", "纬度"],
               [[d["code"], d["name"], d["site"]["city"], d["vendor"], d["model"], d["ip"],
                 "在网", d["mr"], d["ring"], d["igp"], d["site"]["lng"], d["site"]["lat"]]
                for d in devices])

    # ---- 9 端口（每设备 3 端口；键=设备名称|端口名称）----
    ports = []
    for d in devices:
        for k in (1, 2, 3):
            ports.append([d["name"], f"xgei-1/1/{k}", d["ip"], f"{k}号10GE线路口",
                          "10GE", "10000", "up", "up", "传输设备", d["site"]["city"],
                          d["site"]["district"], d["vendor"], d["model"], "2026-10-06"])
    reg("ports", len(ports))
    write_xlsx("ports", "端口清单",
               ["设备名称", "端口名称", "设备IP", "端口描述", "端口类型", "带宽Mbps",
                "管理状态", "运行状态", "设备类型", "地市", "区县", "厂家", "型号", "日期"],
               ports)

    # ---- 10 板卡 ----
    boards = []
    for d in devices:
        for k in (1, 2):
            bm, bt = BOARD_MODELS[(k - 1) % len(BOARD_MODELS)]
            boards.append([f"BRD-{d['code']}-{k:02d}", d["code"], f"{k:02d}", bm, bt])
    reg("boards", len(boards))
    write_xlsx("boards", "板卡表", ["板卡ID", "所属设备", "槽位号", "板卡型号", "板卡类型"], boards)

    # ---- 11 机架 ----
    racks = [[f"RACK-{s['site_id'][2:]}-01", "MR" + s["site_id"][2:], "01", "02"] for s in dev_sites]
    reg("racks", len(racks))
    write_xlsx("racks", "机架表", ["机架编码", "所属机房", "列号", "行号"], racks)

    # ---- 12 ODF（每设备局站 1 架）----
    odfs = [[f"ODF-{s['site_id'][2:]}-01", f"{s['site_name']}01号ODF", "MR" + s["site_id"][2:], "144"]
            for s in dev_sites]
    reg("odf", len(odfs))
    write_xlsx("odf", "ODF表", ["ODF编码", "ODF名称", "所属机房", "容量"], odfs)

    # ---- 13 ODF 端子（每架 8 端子；A 端局补对应纤芯）----
    a_end_digits = set()
    for t in trunks:
        a_end_digits.add(t["waypoint_ids"][0][2:])
    fiber_by_digits: dict[str, list[str]] = {}
    terms = []
    for s in dev_sites:
        digits = s["site_id"][2:]
        fibs = fiber_by_digits.get(digits, [])
        for k in range(1, 9):
            terms.append([f"ODF-{digits}-01", f"{k:02d}",
                          fibs[k - 1] if k <= len(fibs) else ""])
    reg("odf_terminals", len(terms))
    # （fiber_by_digits 在纤芯段填充，此处先写占位；两段互相独立——端子的对应纤芯列
    # 是纯文本列（KindString 非 ref），值在纤芯生成后回填。为保持单遍确定性，
    # 端子表延后到纤芯之后统一写出（见下）。）

    # ---- 14 管道 ----
    ducts = []
    for t in trunks:
        ducts.append([f"DUCT-{t['code'][4:]}", t["waypoint_ids"][0], t["waypoint_ids"][-1],
                      f"{t['anchor_km']}", "4"])
    reg("ducts", len(ducts))
    write_xlsx("ducts", "管道表", ["管道编码", "起点局站", "终点局站", "长度", "孔数"], ducts)

    # ---- 15 人井（真实走廊锚点内插坐标）----
    manholes = []
    for t in trunks:
        wps = [by_id[wid] for wid in t["waypoint_ids"]]
        for idx, f in ((0, 1 / 3), (1, 2 / 3)):
            a, b = wps[idx], wps[idx + 1]
            lng = round(a["lng"] + (b["lng"] - a["lng"]) * f, 5)
            lat = round(a["lat"] + (b["lat"] - a["lat"]) * f, 5)
            manholes.append([f"MH-{t['code'][4:]}-{idx + 1:02d}", f"DUCT-{t['code'][4:]}",
                             lng, lat, f"{lng},{lat}"])
    reg("manholes", len(manholes))
    write_xlsx("manholes", "人井表", ["人井编码", "所属管道", "经度", "纬度", "经纬度"], manholes)

    # ---- 16 杆路 ----
    poles = [[f"POLE-{t['code'][4:]}-001", t["waypoints"][0], t["waypoints"][-1], "001"]
             for t in trunks]
    reg("poles", len(poles))
    write_xlsx("poles", "杆路表", ["杆路编码", "起点", "终点", "杆号"], poles)

    # ---- 17 光缆（真实：A/Z 局站 + 锚点链里程；芯数/连接设备为模拟）----
    dev_by_siteid = {d["site"]["site_id"]: d for d in devices}
    cables = []
    for t in trunks:
        a_id, z_id = t["waypoint_ids"][0], t["waypoint_ids"][-1]
        a_dev = dev_by_siteid.get(a_id, {}).get("code", "")
        cores = 48 if t["code"] in ("TRK-QJ", "TRK-JM2") else 96
        cables.append([f"OC-{t['code'][4:]}", f"{t['name']}光缆", a_id, z_id, cores,
                       f"{t['anchor_km']}", a_dev])
    reg("optical_cables", len(cables))
    write_xlsx("optical_cables", "光缆表",
               ["光缆编码", "光缆名称", "A端局站", "Z端局站", "芯数", "长度", "连接设备"], cables)

    # ---- 18 光缆段（端点=走廊锚点局；端点接头盒为模拟编码）----
    segs = []
    for t in trunks:
        tc = t["code"][4:]
        for i in range(len(t["waypoint_ids"]) - 1):
            segs.append([f"SEG-{tc}-{i + 1:02d}", f"OC-{tc}", i + 1,
                         f"SB-{tc}-{i + 1:02d}-A", f"SB-{tc}-{i + 1:02d}-Z"])
    reg("cable_segments", len(segs))
    write_xlsx("cable_segments", "光缆段表",
               ["光缆段编码", "所属光缆", "段序", "A端接头盒", "Z端接头盒"], segs)

    # ---- 19 接头盒（端点坐标=真实锚点局坐标）----
    sbs = []
    for t in trunks:
        tc = t["code"][4:]
        for i in range(len(t["waypoint_ids"]) - 1):
            for side, wp_idx in (("A", i), ("Z", i + 1)):
                s = by_id[t["waypoint_ids"][wp_idx]]
                sbs.append([f"SB-{tc}-{i + 1:02d}-{side}", "48", "8" if side == "A" else "4",
                            s["lng"], s["lat"], f"{s['lng']},{s['lat']}"])
    reg("splice_boxes", len(sbs))
    write_xlsx("splice_boxes", "接头盒表",
               ["接头盒编码", "容量", "已用芯数", "经度", "纬度", "经纬度"], sbs)

    # ---- 20 纤芯（每缆 8 芯；使用情况/局向为示范；A 端成端 ODF 端子）----
    fibers = []
    for t in trunks:
        tc = t["code"][4:]
        a_digits = t["waypoint_ids"][0][2:]
        usage = ["在用", "在用", "在用", "在用", "备用", "备用", "空闲", "空闲"]
        usage_dir = [f"{t['a']}→{t['z']}"] * 8
        for k in range(1, 9):
            fibers.append([f"OC-{tc}", f"{k}", f"ODF-{a_digits}-01|{k:02d}", "96",
                           usage[k - 1], usage_dir[k - 1],
                           "收发共纤" if k % 2 == 1 else "收发分纤"])
        fiber_by_digits.setdefault(a_digits, []).extend(
            [f"OC-{tc}|{k}" for k in range(1, 9)])
    reg("fibers", len(fibers))
    write_xlsx("fibers", "纤芯表",
               ["光缆编码", "纤芯号", "成端ODF端子", "纤芯总数", "使用情况", "使用局向", "收发关系"],
               fibers)

    # ---- 13b ODF 端子（回填对应纤芯后写出）----
    terms = []
    for s in dev_sites:
        digits = s["site_id"][2:]
        fibs = fiber_by_digits.get(digits, [])
        for k in range(1, 9):
            terms.append([f"ODF-{digits}-01", f"{k:02d}",
                          fibs[k - 1] if k <= len(fibs) else ""])
    reg("odf_terminals", len(terms))
    write_xlsx("odf_terminals", "端子表", ["ODF编码", "端子号", "对应纤芯"], terms)

    # ---- 21 波道（波长=ITU 100GHz 真实网格；占用关系示范）----
    ochs = []
    for t in trunks:
        tc = t["code"][4:]
        for (ch, wl) in ITU_GRID[:2]:
            ochs.append([f"OCH-{tc}-{ch:02d}", ch, wl, "100G", f"OC-{tc}",
                         f"OC-{tc}|{ch}"])
    reg("otn_ochs", len(ochs))
    write_xlsx("otn_ochs", "波道表",
               ["波道编码", "波道号", "波长", "速率", "路由光缆", "占用纤芯"], ochs)

    # ---- 22 时隙 ----
    tslots = [[f"VC4-TRK-{i}", "VC-4", i, "155"] for i in range(1, 5)]
    reg("sdh_timeslots", len(tslots))
    write_xlsx("sdh_timeslots", "时隙表", ["时隙编码", "VC级别", "时隙号", "容量"], tslots)

    # ---- 23 电路（A/Z 设备=真实布点设备）----
    key_devs = [d for d in devices if d["name"].endswith("01")]
    circuits = []
    pairs = [(0, 2), (0, 4), (0, 6), (0, 7), (2, 4), (8, 9), (0, 8), (10, 11), (12, 13), (14, 9)]
    for i, (a_i, z_i) in enumerate(pairs, 1):
        if a_i >= len(key_devs) or z_i >= len(key_devs):
            continue
        a, z = key_devs[a_i], key_devs[z_i]
        tc = trunks[(i - 1) % len(trunks)]["code"][4:]
        circuits.append([f"CIR-{i:03d}", a["code"], z["code"], "10000",
                         f"OCH-{tc}-01" if i <= 7 else "",
                         "VC4-TRK-1" if i == 8 else "", "在网"])
    reg("circuits", len(circuits))
    write_xlsx("circuits", "电路表",
               ["电路编号", "A端设备", "Z端设备", "带宽Mbps", "承载波道", "承载时隙", "状态"],
               circuits)

    # ---- 24 地址池 ----
    pools = [[f"管理地址池-{i:03d}", f"10.{60 + i}.0.0/24", f"10.{60 + i}.0.1",
              100 + i, key_devs[i]["code"], "使用中"] for i in range(5)]
    reg("ippools", len(pools))
    write_xlsx("ippools", "地址池表",
               ["地址池名称", "CIDR", "网关", "VLAN", "归属设备", "状态"], pools)

    # ---- 25 业务 ----
    services = []
    for i in range(1, 9):
        cust = CUSTOMERS[i - 1][0]
        dev = key_devs[i % len(key_devs)]
        cir = f"CIR-{i:03d}" if i <= len(pairs) else ""
        stypes = ["MPLS专线", "互联网专线", "传输专线"]
        services.append([f"SVC-{i:03d}", stypes[(i - 1) % 3], cust, cir, dev["code"],
                         "10000", "开通"])
    reg("services", len(services))
    write_xlsx("services", "业务表",
               ["业务编码", "业务类型", "客户编码", "提供电路", "依赖设备", "带宽Mbps", "状态"],
               services)

    # ---- 26 专线（装机地址=真实局站名；客户/带宽为虚构示范）----
    lls = []
    ll_sites = [("哈尔滨市", "大庆市"), ("哈尔滨市", "齐齐哈尔市"), ("哈尔滨市", "牡丹江市"),
                ("哈尔滨市", "佳木斯市"), ("加格达奇区", "漠河市"), ("哈尔滨市", "伊春市")]
    for i, (a, z) in enumerate(ll_sites, 1):
        lls.append([f"LL-{i:03d}", f"{by_name[a]['site_name']}—{by_name[z]['site_name']}政企专线",
                    f"STN{i:010d}", f"{by_name[a]['site_name']}（演示装机地址）",
                    f"{by_name[z]['site_name']}（演示装机地址）", CUSTOMERS[i - 1][0], "1000"])
    reg("leased_lines", len(lls))
    write_xlsx("leased_lines", "专线表",
               ["专线编码", "专线名称", "集团STN编号", "A端装机地址", "Z端装机地址", "客户", "协议带宽"],
               lls)

    # ---- 27/28 BBU / RRU（挂真实基站）----
    # 选 5 个基站：哈尔滨/大庆各 2 + 唯一的中国电信站（诚实展示多运营商；
    # OpenCelliD 黑龙江覆盖集中于哈大两市——众源数据现实，见 manifest 披露）
    pool = [st for st in stations if st["city"] == "哈尔滨"][:2] + \
           [st for st in stations if st["city"] == "大庆"][:2]
    telecom = next((st for st in stations if st["operator"] == "中国电信"), None)
    picked = [st for st in pool if st is not None]
    if telecom is not None:
        picked.append(telecom)
    bbus = [[f"BBU-{i:03d}", st["code"], BBU_MODELS[i % 2]] for i, st in enumerate(picked, 1)]
    reg("bbus", len(bbus))
    write_xlsx("bbus", "BBU表", ["BBU编码", "所属基站", "设备型号"], bbus)
    rrus = [[f"RRU-{i:03d}", f"BBU-{i:03d}", picked[i - 1]["code"]] for i in range(1, len(picked) + 1)]
    reg("rrus", len(rrus))
    write_xlsx("rrus", "RRU表", ["RRU编码", "归属BBU", "覆盖基站"], rrus)

    # ---- 29-32 OLT / PON口 / ONU（真实局站机房归属；编码为模拟）----
    olt_sites = [s for s in dev_sites if s["site_name"] in
                 ("哈尔滨市", "大庆市", "齐齐哈尔市", "牡丹江市", "佳木斯市")][:5]
    olts = [[f"OLT-{s['site_id'][2:]}", "MR" + s["site_id"][2:], OLT_MODELS[i % 2], "16"]
            for i, s in enumerate(olt_sites)]
    reg("olts", len(olts))
    write_xlsx("olts", "OLT表", ["OLT编码", "所属机房", "型号", "PON口数"], olts)
    pons = []
    for i, s in enumerate(olt_sites):
        for k in range(1, 5):
            pons.append([f"OLT-{s['site_id'][2:]}", f"0/1/{k}", "1:64"])
    reg("pon_ports", len(pons))
    write_xlsx("pon_ports", "PON口表", ["OLT编码", "端口号", "分光比"], pons)
    onus = []
    for i, s in enumerate(olt_sites):
        for k in range(1, 3):
            onus.append([f"ONU-{s['site_id'][2:]}-{k:02d}", f"OLT-{s['site_id'][2:]}",
                         f"OLT-{s['site_id'][2:]}|0/1/{k}",
                         f"{s['site_name']}商务楼宇{k}号接入点（虚构演示）"])
    reg("onus", len(onus))
    write_xlsx("onus", "ONU表", ["ONU编码", "所属OLT", "接入PON口", "用户"], onus)

    # ---- manifest（溯源 + 真实/模拟披露 + 计数）----
    manifest = {
        "generated_by": "scripts/realdata/workbooks.py",
        "core_source": "realdata-core.json (extract.py: OSM ODbL + OpenCelliD CC BY-SA 4.0)",
        "real": {
            "sites": "OSM place 节点（名称/坐标/地市归属真实；局站ID=OSM node id 派生）",
            "base_stations": "OpenCelliD 真实小区（编码=MCC-MNC-LAC-CID 主小区 CGI；"
                             "位置/制式/运营商/地市真实；名称=最近局站派生）",
            "optical_cables": "A/Z 端局站真实；长度=走廊锚点链测地里程（沿线 haversine 累计）",
            "cable_segments": "端点=走廊锚点局真实（段界为走廊锚点，非运营商实际接头位置）",
            "splice_boxes": "端点坐标=真实锚点局坐标（接头盒本体为示意设施）",
            "manholes": "坐标=走廊锚点线性内插（真实坐标系内插，设施为示意）",
            "devices": "所在局站/机房/坐标真实（编码/名称/IP/型号为模拟）",
            "corridor_layers": "web/public/mapdata/hlj-cables*.geojson.gz=OSM 道路/铁路真实几何"
                               "（按线路名选取：绥满高速/滨洲线/嫩林线/京漠线等）",
        },
        "simulated": {
            "machine_rooms": "编号/名称（归属局站真实）",
            "fibers": "纤芯占用/使用情况/局向/收发关系（示范值）；成端端子为模拟编码",
            "otn_ochs": "波道占用关系（波长为 ITU 100GHz 网格真实值，速率为示范值）",
            "sdh_timeslots": "时隙/容量（示范值）",
            "circuits": "电路编号/带宽（A/Z 设备为真实布点）",
            "ippools": "CIDR/网关/VLAN（私网演示段）",
            "customers": "全部虚构（名称含虚构标记或明显虚构词）",
            "services/leased_lines": "业务/专线实例虚构（装机地址=真实局站名+演示标记）",
            "rings/igp_domains/vpn": "保护结构/路由域/VPN 为工程示意",
            "bbu/rru/olt/onu/pon": "挂真实基站/机房（编码/型号/用户为模拟）",
            "vendor/model values": "真实产品族名作为示范值（非任何现网真实配置）",
        },
        "counts": counts,
        "workbooks": sorted(counts.keys()),
    }
    MANIFEST.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True),
                        encoding="utf-8")
    total = sum(counts.values())
    print(f"workbooks: {len(counts)} files, {total} rows")
    for k in sorted(counts):
        print(f"  {k:18s} {counts[k]:5d}")


if __name__ == "__main__":
    main()
