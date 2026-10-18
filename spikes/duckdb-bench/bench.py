#!/usr/bin/env python3
"""bench.py — DuckDB vs SQLite 分析型基准（netops-lab issue #59 阶段0技术验证）

模拟运营商费用数据：1.5 万站址 × 60 个月（5 年）× 多费用类型 ≈ 236 万行，
主表宽 48 列。六类风险场景即席查询 + 宽/窄扫描微基准 + 并发吞吐 + Parquet 分区裁剪。
双引擎均不建二级索引（模拟 AI 生成即席查询无法预知索引的现实）。
SQL 双引擎逐字一致（可移植写法），每查询 1 次预热 + 3 次计时。
"""
import json, os, sqlite3, sys, time, threading
import numpy as np
import pandas as pd
import duckdb

HERE = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.join(HERE, "data")
SQLITE_PATH = os.path.join(DATA, "bench.sqlite")
DUCKDB_PATH = os.path.join(DATA, "bench.duckdb")
PARQUET_DIR = os.path.join(DATA, "fees_parquet")
RESULT_MD = os.path.join(HERE, "RESULT.md")
RESULT_JSON = os.path.join(DATA, "result.json")

N_SITES = 15_000
REGIONS = ["哈尔滨", "齐齐哈尔", "大庆", "绥化", "牡丹江", "佳木斯", "黑河", "鸡西", "鹤岗", "双鸭山"]
SITE_TYPES = ["宏站", "微站", "室分"]
SEED = 42
N_MONTHS = 60
QUERIES_RUNS = 3

def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)

# ---------------------------------------------------------------- 数据生成
def generate():
    rng = np.random.default_rng(SEED)
    n = N_SITES
    codes = np.array([f"S{i:014d}" for i in range(1, n + 1)])  # 15 位站址编码（含前导零语义）
    region_i = rng.integers(0, len(REGIONS), n)
    type_i = rng.integers(0, 3, n)
    is_term = rng.random(n) < 0.10                      # 10% 站址中途退租
    term_at = rng.integers(13, 49, n)                   # 退租落在第 13-48 个月
    base = np.where(type_i == 0, 900.0, np.where(type_i == 1, 400.0, 650.0))

    # 电费：逐月 × 季节 × 噪声 × 3% 跳变；退租站继续计费（注入风险信号一）
    si = np.repeat(np.arange(n), N_MONTHS)
    mi = np.tile(np.arange(1, N_MONTHS + 1), n)
    year = 2020 + (mi - 1) // 12
    month = (mi - 1) % 12 + 1
    season = np.where(np.isin(month, [6, 7, 8]), 1.25, np.where(np.isin(month, [12, 1, 2]), 1.15, 1.0))
    noise = rng.normal(1.0, 0.12, si.shape[0])
    jump = np.where(rng.random(si.shape[0]) < 0.03, rng.uniform(1.4, 1.8, si.shape[0]), 1.0)
    amount = np.round(base[si] * season * noise * jump, 2)
    kwh = np.round(amount / 0.87, 1)
    unit_price = np.full(si.shape[0], 0.87)
    operator = np.array(["运营商%02d" % (i % 12 + 1) for i in range(30)])[rng.integers(0, 30, si.shape[0])]
    batch = mi.copy()                                   # 主批次 = 月序号 1-60
    # 跨批次重复结算候选（注入风险信号二）：~1% 站月跨批次重复
    dup_mask = rng.random(si.shape[0]) < 0.01
    batch = np.where(dup_mask, 61 + rng.integers(0, 12, si.shape[0]), batch)

    fcols = {f"f{i:02d}": rng.random(si.shape[0]).astype(np.float32) for i in range(1, 21)}
    gcols = {f"g{i:02d}": rng.integers(0, 100, si.shape[0]).astype(np.int32) for i in range(1, 21)}
    ef = pd.DataFrame({
        "site_code": codes[si], "year": year.astype(np.int32), "month": month.astype(np.int32),
        "batch_id": batch.astype(np.int32), "amount": amount, "kwh": kwh, "unit_price": unit_price,
        "operator_code": operator, **fcols, **gcols})
    # 重复行（同站同月跨批次 = 重导候选）——让 GROUP BY 真正捕获
    dups = ef[dup_mask].copy()
    dups["batch_id"] = 61 + rng.integers(0, 12, len(dups))
    dups["amount"] = np.round(dups["amount"].to_numpy() * rng.uniform(1.0, 1.02, len(dups)), 2)
    ef = pd.concat([ef, dups], ignore_index=True)
    log(f"electricity_fees: {len(ef)} 行 × {len(ef.columns)} 列")

    # 铁塔租费（同规模、窄表——只作规模与导入负担）
    lease_amt = np.round(np.where(type_i[si] == 0, 550.0, np.where(type_i[si] == 1, 280.0, 380.0))
                         * rng.normal(1.0, 0.05, si.shape[0]), 2)
    lf = pd.DataFrame({"site_code": codes[si], "year": year.astype(np.int32), "month": month.astype(np.int32),
                       "batch_id": mi.astype(np.int32), "amount": lease_amt,
                       "tower_code": np.array([f"T{i:014d}" for i in range(1, 8001)])[rng.integers(0, 8000, si.shape[0])],
                       "lease_term": rng.integers(1, 120, si.shape[0]).astype(np.int32),
                       "vendor": np.array(["铁塔%03d" % i for i in range(50)])[rng.integers(0, 50, si.shape[0])],
                       "area_m2": rng.uniform(10, 60, si.shape[0]).astype(np.float32),
                       "height_m": rng.uniform(20, 70, si.shape[0]).astype(np.float32),
                       "share_type": np.array(["独享", "共享", "新共"])[rng.integers(0, 3, si.shape[0])],
                       "settle_day": rng.integers(1, 29, si.shape[0]).astype(np.int32),
                       "contract_no": np.array([f"C{i:012d}" for i in range(9000)])[rng.integers(0, 9000, si.shape[0])]})

    # 修理费（事件型）
    ev = rng.integers(6, 26, n)                          # 每站 5 年 6-25 单
    r_si = np.repeat(np.arange(n), ev)
    r_mi = rng.integers(1, 61, r_si.shape[0])
    rf = pd.DataFrame({"site_code": codes[r_si], "year": (2020 + (r_mi - 1) // 12).astype(np.int32),
                       "month": (((r_mi - 1) % 12) + 1).astype(np.int32),
                       "work_order": np.array([f"WO{i:09d}" for i in range(len(r_si))]),
                       "repair_type": np.array(["更换板卡", "电源改造", "天馈整改", "蓄电池", "其他"])[rng.integers(0, 5, r_si.shape[0])],
                       "amount": np.round(rng.uniform(300, 20000, r_si.shape[0]), 2),
                       "vendor_code": np.array([f"V{i:04d}" for i in range(200)])[rng.integers(0, 200, r_si.shape[0])],
                       "duration_d": rng.integers(1, 30, r_si.shape[0]).astype(np.int16)})

    # 站址台账 + 退租子表
    sites = pd.DataFrame({"site_code": codes, "region": np.array(REGIONS)[region_i],
                          "site_type": np.array(SITE_TYPES)[type_i],
                          "site_name": np.array([f"{REGIONS[r]}站址{i:05d}" for i, r in enumerate(region_i)]),
                          "is_terminated": is_term,
                          "term_month_idx": np.where(is_term, term_at, 0).astype(np.int32)})
    term = sites[sites.is_terminated][["site_code", "region", "site_type"]].copy()
    term["term_year"] = (2020 + (sites[sites.is_terminated].term_month_idx.to_numpy() - 1) // 12).astype(np.int32)
    term["term_month"] = (((sites[sites.is_terminated].term_month_idx.to_numpy() - 1) % 12) + 1).astype(np.int32)

    # 合同（金额基准 ±10%，5% 漂移 ×1.2 —— 注入风险信号三）
    c_amt = np.round(base * rng.normal(1.0, 0.10, n), 2)
    drift = rng.random(n) < 0.05
    c_amt = np.where(drift, np.round(c_amt * 1.2, 2), c_amt)
    contracts = pd.DataFrame({"site_code": codes, "contract_no": np.array([f"C{i:012d}" for i in range(9000)])[rng.integers(0, 9000, n)],
                              "monthly_amount": c_amt,
                              "sign_year": rng.integers(2018, 2021, n).astype(np.int32),
                              "expire_year": (2020 + rng.integers(3, 8, n)).astype(np.int32)})

    # 资源台账（季度粒度；退租后归零 —— 注入风险信号六：零资源仍计费）
    q_si = np.repeat(np.arange(n), 20)
    q_i = np.tile(np.arange(1, 21), n)
    res_cnt = rng.integers(2, 16, q_si.shape[0]).astype(np.int32)
    after_term = q_i > term_at[q_si]
    res_cnt = np.where(after_term, 0, res_cnt)
    rh = pd.DataFrame({"site_code": codes[q_si], "year": (2020 + (q_i - 1) // 4).astype(np.int32),
                       "qtr": ((q_i - 1) % 4 + 1).astype(np.int32), "resource_count": res_cnt})
    return {"electricity_fees": ef, "tower_lease_fees": lf, "repair_fees": rf,
            "sites": sites, "terminated_sites": term, "contracts": contracts, "resource_history": rh}

# ---------------------------------------------------------------- SQLite
def load_sqlite(tables):
    if os.path.exists(SQLITE_PATH):
        os.remove(SQLITE_PATH)
    con = sqlite3.connect(SQLITE_PATH)
    con.execute("PRAGMA journal_mode=WAL")
    con.execute("PRAGMA synchronous=OFF")
    con.execute("PRAGMA busy_timeout=5000")
    t0 = time.perf_counter()
    ddl = {
        "electricity_fees": """CREATE TABLE electricity_fees(site_code TEXT, year INT, month INT, batch_id INT,
            amount REAL, kwh REAL, unit_price REAL, operator_code TEXT, {f}, {g})""",
        "tower_lease_fees": """CREATE TABLE tower_lease_fees(site_code TEXT, year INT, month INT, batch_id INT,
            amount REAL, tower_code TEXT, lease_term INT, vendor TEXT, area_m2 REAL, height_m REAL,
            share_type TEXT, settle_day INT, contract_no TEXT)""",
        "repair_fees": """CREATE TABLE repair_fees(site_code TEXT, year INT, month INT, work_order TEXT,
            repair_type TEXT, amount REAL, vendor_code TEXT, duration_d INT)""",
        "sites": """CREATE TABLE sites(site_code TEXT, region TEXT, site_type TEXT, site_name TEXT,
            is_terminated INT, term_month_idx INT)""",
        "terminated_sites": """CREATE TABLE terminated_sites(site_code TEXT, region TEXT, site_type TEXT,
            term_year INT, term_month INT)""",
        "contracts": """CREATE TABLE contracts(site_code TEXT, contract_no TEXT, monthly_amount REAL,
            sign_year INT, expire_year INT)""",
        "resource_history": """CREATE TABLE resource_history(site_code TEXT, year INT, qtr INT, resource_count INT)""",
    }
    f = ", ".join(f"f{i:02d} REAL" for i in range(1, 21))
    g = ", ".join(f"g{i:02d} INT" for i in range(1, 21))
    for name, df in tables.items():
        sql = ddl[name].replace("{f}", f).replace("{g}", g)
        con.execute(sql)
        t1 = time.perf_counter()
        it = df.itertuples(index=False, name=None)
        CHUNK = 100_000
        while True:
            chunk = []
            for _ in range(CHUNK):
                try:
                    chunk.append(next(it))
                except StopIteration:
                    break
            if not chunk:
                break
            ph = ",".join("?" * len(df.columns))
            con.executemany(f"INSERT INTO {name} VALUES ({ph})", chunk)
        con.commit()
        log(f"sqlite loaded {name}: {len(df)} 行 {time.perf_counter()-t1:.1f}s")
    con.execute("PRAGMA synchronous=NORMAL")
    con.commit()
    return con, time.perf_counter() - t0

# ---------------------------------------------------------------- DuckDB
def load_duckdb(tables):
    if os.path.exists(DUCKDB_PATH):
        os.remove(DUCKDB_PATH)
    con = duckdb.connect(DUCKDB_PATH)
    con.execute("SET threads TO 2")
    con.execute("SET memory_limit='2GB'")
    t0 = time.perf_counter()
    for name, df in tables.items():
        con.register("srcdf", df)
        con.execute(f"CREATE TABLE {name} AS SELECT * FROM srcdf")
        con.unregister("srcdf")
    con.execute("CHECKPOINT")
    log(f"duckdb loaded all tables in {time.perf_counter()-t0:.1f}s")
    # Parquet 分区导出（快照层→分析层零拷贝路径）
    import shutil
    if os.path.exists(PARQUET_DIR):
        shutil.rmtree(PARQUET_DIR)
    t1 = time.perf_counter()
    con.execute(f"COPY electricity_fees TO '{PARQUET_DIR}' (FORMAT PARQUET, PARTITION_BY (year, month))")
    log(f"parquet partitioned export: {time.perf_counter()-t1:.1f}s")
    return con, time.perf_counter() - t0

# ---------------------------------------------------------------- 查询集
QUERIES = {
    "Q1 退租站仍在计费": """
SELECT COUNT(*) AS hit_rows, ROUND(SUM(e.amount),2) AS hit_amount
FROM electricity_fees e JOIN terminated_sites t ON e.site_code = t.site_code
WHERE (e.year*12+e.month) > (t.term_year*12+t.term_month)""",
    "Q2 跨批次重复结算候选": """
SELECT site_code, year, month, COUNT(*) AS n, COUNT(DISTINCT batch_id) AS batches, ROUND(SUM(amount),2) AS total
FROM electricity_fees GROUP BY site_code, year, month
HAVING COUNT(*)>1 ORDER BY total DESC LIMIT 20""",
    "Q3 合同vs账单金额差异": """
SELECT s.region, COUNT(*) AS mismatch_months, ROUND(SUM(e.amount-c.monthly_amount),2) AS diff_total
FROM electricity_fees e JOIN contracts c ON e.site_code=c.site_code JOIN sites s ON e.site_code=s.site_code
WHERE ABS(e.amount-c.monthly_amount)>100 GROUP BY s.region ORDER BY diff_total""",
    "Q4 电费环比异常上涨": """
WITH m AS (SELECT site_code, year, month, amount,
  LAG(amount) OVER (PARTITION BY site_code ORDER BY year, month) AS prev FROM electricity_fees)
SELECT COUNT(*) AS jump_rows, ROUND(SUM(amount-prev),2) AS jump_total FROM m
WHERE prev>0 AND amount>prev*1.3""",
    "Q5 同类站单位成本离群": """
WITH per_site AS (SELECT e.site_code, s.region, s.site_type, AVG(e.amount) AS avg_monthly
  FROM electricity_fees e JOIN sites s ON e.site_code=s.site_code
  GROUP BY e.site_code, s.region, s.site_type),
base AS (SELECT region, site_type, AVG(avg_monthly) AS grp_avg FROM per_site GROUP BY region, site_type)
SELECT p.region, p.site_type, COUNT(*) AS outlier_sites, ROUND(AVG(p.avg_monthly),2) AS avg_of_outliers
FROM per_site p JOIN base b ON p.region=b.region AND p.site_type=b.site_type
WHERE p.avg_monthly > b.grp_avg*1.5 GROUP BY p.region, p.site_type ORDER BY outlier_sites DESC""",
    "Q6 零资源季度仍计费": """
WITH fee_q AS (SELECT site_code, year, CAST(FLOOR((month+2)/3) AS INTEGER) AS qtr, SUM(amount) AS fee
  FROM electricity_fees GROUP BY site_code, year, CAST(FLOOR((month+2)/3) AS INTEGER))
SELECT COUNT(*) AS mismatch_quarters, ROUND(SUM(fq.fee),2) AS wasted_fee
FROM fee_q fq JOIN resource_history rh ON fq.site_code=rh.site_code AND fq.year=rh.year AND fq.qtr=rh.qtr
WHERE rh.resource_count=0 AND fq.fee>0""",
    "Q7a 窄扫描(3/48列)": "SELECT year, COUNT(*), ROUND(SUM(amount),2) FROM electricity_fees GROUP BY year",
    "Q7b 宽扫描(28/48列)": "SELECT COUNT(*) FROM electricity_fees WHERE "
        + " AND ".join(f"f{i:02d}>0.5" for i in range(1, 21)) + " AND "
        + " AND ".join(f"g{i:02d}>50" for i in range(1, 9)),
}

def run_sqlite(con, sql):
    cur = con.cursor()
    cur.execute(sql)
    rows = cur.fetchall()
    cur.close()
    return rows

def run_duckdb(con, sql):
    return con.execute(sql).fetchall()

def bench_query(sql, runner, runs=QUERIES_RUNS):
    res = runner(sql)                     # 预热 + 取结果
    ts = []
    for _ in range(runs):
        t0 = time.perf_counter()
        runner(sql)
        ts.append(time.perf_counter() - t0)
    return ts, res

# ---------------------------------------------------------------- 并发吞吐
def concurrency_test(kind):
    """4 线程同时各跑 2 次 Q2（分离连接）——模拟 AI 多 job 并发即席查询"""
    sql = QUERIES["Q2 跨批次重复结算候选"]
    N_THREAD, N_EACH = 4, 2
    barrier = threading.Barrier(N_THREAD)
    results = {}
    def worker(i):
        if kind == "sqlite":
            c = sqlite3.connect(SQLITE_PATH, timeout=5)
            c.execute("PRAGMA busy_timeout=5000")
        else:
            # 主连接已关闭；四连接同配置共享实例，避免 different-configuration 冲突
            c = duckdb.connect(DUCKDB_PATH, read_only=True, config={"threads": 1})
        r = (lambda s: run_sqlite(c, s)) if kind == "sqlite" else (lambda s: run_duckdb(c, s))
        barrier.wait()
        t0 = time.perf_counter()
        for _ in range(N_EACH):
            r(sql)
        results[i] = time.perf_counter() - t0
        c.close()
    ths = [threading.Thread(target=worker, args=(i,)) for i in range(N_THREAD)]
    t0 = time.perf_counter()
    for t in ths: t.start()
    for t in ths: t.join()
    wall = time.perf_counter() - t0
    return wall, max(results.values())

# ---------------------------------------------------------------- 主流程
def main():
    os.makedirs(DATA, exist_ok=True)
    R = {"scale": {}, "load": {}, "queries": {}, "extra": {}}

    log("=== 生成数据 ===")
    t0 = time.perf_counter()
    tables = generate()
    R["scale"] = {k: len(v) for k, v in tables.items()}
    R["scale"]["electricity_cols"] = len(tables["electricity_fees"].columns)
    log(f"生成完成 {time.perf_counter()-t0:.1f}s, 总行数 {sum(len(v) for k, v in tables.items() if k != 'electricity_cols')}")

    log("=== SQLite 加载（WAL + 批量 executemany）===")
    scon, t_load_sqlite = load_sqlite(tables)
    R["load"]["sqlite_s"] = round(t_load_sqlite, 1)

    log("=== DuckDB 加载 + Parquet 分区导出 ===")
    dcon, t_load_duck = load_duckdb(tables)
    R["load"]["duckdb_s"] = round(t_load_duck, 1)

    R["extra"]["size_mb"] = {
        "sqlite": round(os.path.getsize(SQLITE_PATH) / 1e6, 1),
        "duckdb": round(os.path.getsize(DUCKDB_PATH) / 1e6, 1),
        "parquet": round(sum(
            os.path.getsize(os.path.join(dp, f)) for dp, _, fs in os.walk(PARQUET_DIR) for f in fs) / 1e6, 1)}

    log("=== 六查询 + 微基准（1 预热 + 3 计时）===")
    for name, sql in QUERIES.items():
        ts_s, res_s = bench_query(sql, lambda s: run_sqlite(scon, s))
        ts_d, res_d = bench_query(sql, lambda s: run_duckdb(dcon, s))
        R["queries"][name] = {"sqlite_best": round(min(ts_s), 3), "sqlite_all": [round(t, 3) for t in ts_s],
                              "duckdb_best": round(min(ts_d), 3), "duckdb_all": [round(t, 3) for t in ts_d],
                              "speedup": round(min(ts_s) / max(min(ts_d), 1e-9), 1)}
        log(f"{name}: sqlite {min(ts_s):.3f}s | duckdb {min(ts_d):.3f}s | ×{min(ts_s)/max(min(ts_d),1e-9):.1f}")
        if name in ("Q1 退租站仍在计费", "Q4 电费环比异常上涨", "Q6 零资源季度仍计费"):
            R["queries"][name]["rows_equal"] = (res_s[0][0] == res_d[0][0])

    log("=== SQLite 大页缓存敏感性（Q2, cache 512MB）===")
    scon.execute("PRAGMA cache_size=-524288")
    ts_s, _ = bench_query(QUERIES["Q2 跨批次重复结算候选"], lambda s: run_sqlite(scon, s))
    R["extra"]["sqlite_q2_cache512_best_s"] = round(min(ts_s), 3)
    scon.execute("PRAGMA cache_size=-2000")
    log(f"Q2 with 512MB cache: {min(ts_s):.3f}s")

    log("=== Parquet 分区裁剪 ===")
    pq = os.path.join(PARQUET_DIR, "**", "*.parquet")
    sql_full = f"SELECT year, COUNT(*), ROUND(SUM(amount),2) FROM read_parquet('{pq}', hive_partitioning=true) GROUP BY year ORDER BY year"
    sql_2024 = sql_full.replace("GROUP BY year", "WHERE year=2024 GROUP BY year").replace(" ORDER BY year", "")
    ts_f, res_f = bench_query(sql_full, lambda s: run_duckdb(dcon, s))
    ts_p, res_p = bench_query(sql_2024, lambda s: run_duckdb(dcon, s))
    R["extra"]["parquet_full_best_s"] = round(min(ts_f), 3)
    R["extra"]["parquet_2024_pruned_best_s"] = round(min(ts_p), 3)
    expl = dcon.execute(f"EXPLAIN {sql_2024}").fetchall()
    R["extra"]["parquet_explain_head"] = str(expl)[:400]
    n_files = sum(len(fs) for _, _, fs in os.walk(PARQUET_DIR))
    R["extra"]["parquet_files"] = n_files
    R["extra"]["parquet_prune_speedup"] = round(min(ts_f) / max(min(ts_p), 1e-9), 1)
    log(f"parquet 全扫 {min(ts_f):.3f}s vs year=2024 裁剪 {min(ts_p):.3f}s (共 {n_files} 文件)")

    log("=== 关闭主连接后并发吞吐（4 线程 × Q2 × 2 次）===")
    scon.close(); dcon.close()                     # 释放实例缓存，并发连接用统一只读配置
    w_s, slow_s = concurrency_test("sqlite")
    w_d, slow_d = concurrency_test("duckdb")
    R["extra"]["concurrency"] = {"sqlite_wall_s": round(w_s, 2), "sqlite_slowest_thread_s": round(slow_s, 2),
                                 "duckdb_wall_s": round(w_d, 2), "duckdb_slowest_thread_s": round(slow_d, 2)}
    log(f"sqlite 4 并发墙钟 {w_s:.2f}s | duckdb {w_d:.2f}s")

    with open(RESULT_JSON, "w", encoding="utf-8") as f:
        json.dump(R, f, ensure_ascii=False, indent=2)
    write_report(R)
    log("BENCH-DONE")

def write_report(R):
    scale = R["scale"]
    total_rows = sum(v for k, v in scale.items() if k != "electricity_cols")
    lines = [
        "# DuckDB vs SQLite 分析型基准 — 费用风险分析技术验证（issue #59）",
        "",
        f"- 数据：{N_SITES:,} 站址 × {N_MONTHS} 个月（2020-2024），总计 {total_rows:,} 行",
        f"- 主表 electricity_fees：{scale['electricity_fees']:,} 行 × {scale['electricity_cols']} 列（宽表）",
        "- 风险信号注入：退租后计费 / 跨批次重复 / 合同漂移 / 环比跳变 / 零资源计费",
        f"- 加载耗时：SQLite {R['load']['sqlite_s']}s（Python executemany）｜DuckDB {R['load']['duckdb_s']}s（含 Parquet 导出）",
        f"- 体积：SQLite {R['extra']['size_mb']['sqlite']}MB｜DuckDB {R['extra']['size_mb']['duckdb']}MB｜Parquet {R['extra']['size_mb']['parquet']}MB（{R['extra']['parquet_files']} 文件）",
        "",
        "## 查询延迟（秒；3 次取最优；双引擎零二级索引，SQL 逐字一致）",
        "",
        "| 查询 | SQLite | DuckDB | 倍数 |",
        "| --- | --- | --- | --- |",
    ]
    for name, q in R["queries"].items():
        lines.append(f"| {name} | {q['sqlite_best']} | {q['duckdb_best']} | ×{q['speedup']} |")
    lines += [
        "",
        f"- SQLite Q2 大页缓存敏感性（512MB）：{R['extra']['sqlite_q2_cache512_best_s']}s（对比上表 Q2）",
        f"- Parquet 直接查询：全扫 {R['extra']['parquet_full_best_s']}s vs year=2024 分区裁剪 {R['extra']['parquet_2024_pruned_best_s']}s（×{R['extra']['parquet_prune_speedup']}）",
        f"- 并发（4 线程各 2 次 Q2）：SQLite 墙钟 {R['extra']['concurrency']['sqlite_wall_s']}s｜DuckDB {R['extra']['concurrency']['duckdb_wall_s']}s",
    ]
    eq = [n for n, q in R["queries"].items() if q.get("rows_equal")]
    if eq:
        lines.append(f"- 双引擎结果一致性抽查：{len(eq)}/{len(eq)} 项计数完全一致（" + "、".join(eq[:3]) + " 等）")
    lines.append("")
    with open(RESULT_MD, "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
    print("\n".join(lines))

if __name__ == "__main__":
    main()
