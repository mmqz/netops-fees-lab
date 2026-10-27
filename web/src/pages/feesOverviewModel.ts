/**
 * 费用分析展示层的确定性核心（#3 二期，纯函数——同数据同输出，可对照测试）。
 *
 * 职责：
 * - 月度序列补位（12 槽，无数据月份为 null——补空白、**不造零**：空月是陈述，
 *   不是零）；
 * - 总览聚合（本月合计 / 环比 / 站址数——全部从月度序列推导，整数分进整数分出）；
 * - 趋势图几何（SVG 折线坐标纯函数计算，零图表库红线延续 MetricChart 的范式）；
 * - 跨场景线索合并 / 排序 / CSV 导出（分析路径第 5 步「出结论」的载体）。
 *
 * 教义：线索是待核实差异而非认定结论；导出 CSV 携带 note 口径列，
 * 账单存在 ≠ 认定结论在离机文件里也要在。
 */

import type { FeeMonthlyRow, RiskClue, RiskResult } from '../api/types';

/** 12 个月的补位槽：point 为 null = 该月无数据（渲染为空白）。 */
export interface MonthSlot {
  month: number;
  point: FeeMonthlyRow | null;
}

/** padMonthly 把稀疏序列补成 12 槽（升序），缺席月份 point=null。 */
export function padMonthly(items: FeeMonthlyRow[]): MonthSlot[] {
  const byMonth = new Map<number, FeeMonthlyRow>();
  for (const it of items) byMonth.set(it.month, it);
  const out: MonthSlot[] = [];
  for (let m = 1; m <= 12; m += 1) out.push({ month: m, point: byMonth.get(m) ?? null });
  return out;
}

/**
 * momRatio 环比：本月对上月的变化率。任一月缺席或上月为 0 时返回 null
 * （0 基期没有定义良好的比值——诚实空态，不显示假 0%）。
 */
export function momRatio(cur: number | null, prev: number | null): number | null {
  if (cur === null || prev === null || prev === 0) return null;
  return (cur - prev) / prev;
}

/** seriesLatest 返回序列中最后一个有数据的月份（0 = 全年无数据）。 */
export function seriesLatestMonth(slots: MonthSlot[]): number {
  let latest = 0;
  for (const s of slots) if (s.point !== null) latest = s.month;
  return latest;
}

/** slotOf 取某月的槽（越界月份返回 null——调用方不该发生，防御而已）。 */
export function slotOf(slots: MonthSlot[], month: number): MonthSlot | null {
  return slots.find((s) => s.month === month) ?? null;
}

export type YearEmptyKind = 'none' | 'all-empty' | 'year-empty' | 'generic';

/**
 * yearEmptyBanner — 年份诚实空态裁决（v0.20.3，纯函数）。
 *
 * 旧观感缺陷：所选年全年无数据时（resolvedMonth 解析为 0）总览整页空白
 * ——卡片横幅条件（resolvedMonth > 0）不成立、趋势不渲染、一键分析不跑，
 * 加上风险地图把 month=0 发给后端浮出校验错，运营者只好看图猜年份。
 *
 * 裁决四态（空态文案的分家依据——「该年无数据」「全库无数据」「清单可
 * 指引」是三个不同的陈述，绝不共用一句话）：
 * - 'none'：该年有数据（resolvedMonth<=0 时理论到不了这，防御性返回——
 *   不渲染任何横幅，让真正的空月横幅接管）；
 * - 'year-empty'：该年无数据且已知有数据的年份清单 → 横幅带「有数据的
 *   年份：…」指路（2024 数据环境打开 2026 时最需要的正是这句话）；
 * - 'all-empty'：全库确证无任何导入 → 横幅只引导导入，不谈「切年份」；
 * - 'generic'：年份发现未知（发现失败/在途）→ 横幅只说该年无数据，绝不
 *   猜清单（未知 ≠ 无数据——null 与 [] 语义分家）。
 */
export function yearEmptyBanner(dataYears: number[] | null, year: number): YearEmptyKind {
  if (dataYears === null) return 'generic';
  if (dataYears.length === 0) return 'all-empty';
  if (dataYears.includes(year)) return 'none';
  return 'year-empty';
}

/**
 * trendGeometry 把 1–4 条费用类型的 12 月序列投影进 SVG viewBox。
 * 坐标计算是纯函数（MetricChart 的确定性渲染教义）；max==0（全空）时
 * 返回空 paths——空数据画空图，不画假 0 线。spike 月份（≥2× 上一个
 * 有数据月）额外记下，渲染空心圆环高亮（环比异常点的目视锚）。
 */
export interface TrendSeries {
  key: string;
  label: string;
  color: string;
  slots: MonthSlot[];
}

export interface TrendPath {
  key: string;
  label: string;
  color: string;
  /** SVG polyline 的 points 串（仅相邻有数据月连线——跨空月断线）。 */
  points: string;
  /** 每段独立记点，供 <circle> 与 <title> 悬浮（月 · 万元）。 */
  dots: Array<{ x: number; y: number; month: number; cents: number }>;
}

export interface TrendGeometry {
  paths: TrendPath[];
  yTicks: Array<{ x: number; y: number; label: string }>;
  xTicks: Array<{ x: number; label: number }>;
  spikeMonths: number[];
  maxCents: number;
  /** #19：Y 轴自适应域（非零基）——压扁 ±8% 波动的 0 基轴已废除；
   * yLo/yMax 如实暴露给 UI（轴刻度已带单位，无需额外提示）。 */
  yLo: number;
  yHi: number;
}

export const TREND_VIEW_W = 640;
export const TREND_VIEW_H = 220;
const PAD_L = 52;
const PAD_R = 14;
const PAD_T = 14;
const PAD_B = 26;
/** y 轴刻度线数（含 0 与 max）。 */
const Y_TICKS = 4;

/** centsToWan 元/整数分 → 万元显示（两位小数，去尾零）。 */
export function centsToWan(cents: number): string {
  const wan = cents / 1_000_000;
  return `${wan.toFixed(2).replace(/\.?0+$/, '')}`;
}

export function trendGeometry(series: TrendSeries[]): TrendGeometry {
  const all = series.flatMap((s) => s.slots.filter((m) => m.point !== null).map((m) => m.point!.totalCents));
  const maxCents = all.length === 0 ? 0 : Math.max(...all);
  // #19：Y 轴自适应域——8 个月 ±8% 的波动在 0 基轴下是一条水平线（issue #19
  // 实测），数据驱动域让波动可见；刻度标签如实标注量级，绝不伪装。
  const minCents = all.length === 0 ? 0 : Math.min(...all);
  let yLo = minCents;
  let yHi = maxCents;
  if (yHi === yLo) {
    // 单值序列：以值本身 5% 作上下界（值 0 → ±1 分），图形仍是一条平线
    const pad = Math.max(Math.abs(yHi) * 0.05, 1);
    yLo -= pad;
    yHi += pad;
  } else {
    const pad = (yHi - yLo) * 0.1;
    yLo -= pad;
    yHi += pad;
  }
  if (minCents >= 0 && yLo < 0) yLo = 0; // 非负金额不下探负轴（冲销负值除外）
  const innerW = TREND_VIEW_W - PAD_L - PAD_R;
  const innerH = TREND_VIEW_H - PAD_T - PAD_B;
  const xOf = (month: number): number => PAD_L + ((month - 1) / 11) * innerW;
  const yOf =
    (cents: number): number =>
      yHi === yLo ? TREND_VIEW_H - PAD_B : TREND_VIEW_H - PAD_B - ((cents - yLo) / (yHi - yLo)) * innerH;

  const yTicks: Array<{ x: number; y: number; label: string }> = [];
  for (let i = 0; i < Y_TICKS; i += 1) {
    const cents = yLo + ((yHi - yLo) / (Y_TICKS - 1)) * i;
    yTicks.push({
      x: PAD_L - 8,
      y: yOf(cents) + 4,
      label: centsToWan(Math.round(cents)),
    });
  }

  const paths: TrendPath[] = [];
  for (const s of series) {
    const dots: TrendPath['dots'] = [];
    const segs: string[] = [];
    let seg: string[] = [];
    for (const slot of s.slots) {
      if (slot.point === null) {
        if (seg.length > 0) segs.push(seg.join(' '));
        seg = [];
        continue;
      }
      const x = xOf(slot.month);
      const y = yOf(slot.point.totalCents);
      seg.push(`${x.toFixed(1)},${y.toFixed(1)}`);
      dots.push({ x, y, month: slot.month, cents: slot.point.totalCents });
    }
    if (seg.length > 0) segs.push(seg.join(' '));
    paths.push({ key: s.key, label: s.label, color: s.color, points: segs.join(' '), dots });
  }

  // spike 月份：对首条序列（当前选中费用类型）≥2× 上一个有数据月。
  const spikeMonths: number[] = [];
  const primary = series.length > 0 ? series[0].slots : [];
  let prev: number | null = null;
  for (const slot of primary) {
    if (slot.point === null) continue;
    if (prev !== null && prev > 0 && slot.point.totalCents >= prev * 2) spikeMonths.push(slot.month);
    prev = slot.point.totalCents;
  }

  const xTicks: Array<{ x: number; label: number }> = [];
  for (let m = 1; m <= 12; m += 1) xTicks.push({ x: xOf(m), label: m });

  return { paths, yTicks, xTicks, spikeMonths, maxCents, yLo, yHi };
}

/** 跨场景合并后的线索视图（分析路径第 3 步「看详情」的行模型）。 */
export interface MergedClue {
  scenario: string;
  scenarioCn: string;
  feeType: string;
  clue: RiskClue;
}

/** mergeClues 把多个场景信封摊平成一张线索列表（场景标签随行携带）。 */
export function mergeClues(results: RiskResult[]): MergedClue[] {
  const out: MergedClue[] = [];
  for (const r of results) {
    // 防御：旧服务端零线索场景曾答 null（#93 补丁前的形态）——
    // 边缘层容错，源头已在 Go 信封层根治。
    for (const clue of r.clues ?? []) {
      out.push({ scenario: r.scenario, scenarioCn: r.scenarioCn, feeType: r.feeType, clue });
    }
  }
  return out;
}

/**
 * sortMergedClues：'amount' 按金额线索降序（metricValue 即触发指标，
 * spike/outlier 为比值、duplicate/mismatch 为分数——同键稳定排序，
 * 场景名次序做 tiebreaker 保证确定性）；'metric' 按指标值降序。
 */
export type ClueSortKey = 'amount' | 'metric';

export function sortMergedClues(list: MergedClue[], key: ClueSortKey): MergedClue[] {
  const copy = [...list];
  // 'amount' = 证据锚点金额合计（分）——「可按金额排序」的诚实落点；
  // 'metric' = 触发指标值（比值类场景的原始信号强度）。
  const evidenceCents = (m: MergedClue): number =>
    (m.clue.evidence ?? []).reduce((acc, a) => acc + a.amountCents, 0);
  const primaryOf = key === 'metric' ? (m: MergedClue) => m.clue.metricValue : evidenceCents;
  copy.sort((a, b) => {
    const av = primaryOf(a);
    const bv = primaryOf(b);
    if (av !== bv) return bv - av;
    if (a.scenario !== b.scenario) return a.scenario < b.scenario ? -1 : 1;
    const siteCmp = a.clue.siteCode.localeCompare(b.clue.siteCode);
    if (siteCmp !== 0) return siteCmp;
    if (a.clue.year !== b.clue.year) return b.clue.year - a.clue.year;
    return b.clue.month - a.clue.month;
  });
  return copy;
}

/** 线索状态标签：'' = 待核实 / verified = 已核实 / dismissed = 已驳回。 */
export function clueStateLabel(decision: string | undefined): string {
  if (decision === 'verified') return 'verified';
  if (decision === 'dismissed') return 'dismissed';
  return 'pending';
}

/** csvCell 最小 CSV 转义（引号包裹含逗号/引号/换行的值）。 */
function csvCell(raw: string): string {
  if (/[",\n]/.test(raw)) return `"${raw.replace(/"/g, '""')}"`;
  return raw;
}

/**
 * cluesCsv 把合并线索导出为 CSV（分析路径第 5 步「出结论」——给业务
 * 对账的离机清单）。列契约固定；note（口径与教义）随行携带——离机
 * 文件里也要有「账单存在 ≠ 认定结论」。
 */
export function cluesCsv(list: MergedClue[]): string {
  const head = [
    '场景', '场景ID', '费用类型', '站址编码', '记录键', '账期', '触发指标',
    '指标值', '阈值', '证据行数', '证据总数', '状态', '备注',
  ];
  const lines = [head.join(',')];
  for (const m of list) {
    const c = m.clue;
    lines.push([
      m.scenarioCn,
      m.scenario,
      m.feeType,
      c.siteCode,
      c.recordKey ?? '',
      `${c.year}-${String(c.month).padStart(2, '0')}`,
      c.metricLabel,
      String(c.metricValue),
      c.thresholdLabel ?? '',
      String(c.evidence?.length ?? 0),
      String(c.evidenceTotal),
      clueStateLabel(c.decision),
      c.note ?? '',
    ].map(csvCell).join(','));
  }
  return lines.join('\n');
}

/** downloadCsv 触发浏览器下载（文件名含费用类型与账期，确定性命名）。 */
export function downloadCsv(text: string, filename: string): void {
  const blob = new Blob([`\uFEFF${text}`], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
