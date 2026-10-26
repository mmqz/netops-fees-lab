/**
 * 费用分析总览与分析路径（#3 二期——「能算但看不到」的最后一段）：
 *
 * - 总览卡片：各费用类型本月费用 / 环比 / 站址数——全部由月度序列推导
 *   （GET /api/fees/query/monthly，整数分契约，前端 /100 万元展示）；
 * - 月度趋势图：纯 SVG 折线（零图表库红线——延续 MetricChart 的确定性
 *   渲染教义，坐标纯函数计算于 feesOverviewModel.ts，可对照测试）；空月断线
 *   不造零；环比异常点空心环高亮；
 * - 一键分析（分析路径五步）：十场景顺序执行（治理默认阈值——单场景
 *   调参仍在下方风险面板），跨场景合并线索列表：按触发指标排序、
 *   展开行级证据锚点（sourceFile:sourceRow + 整数分 + 批次）、
 *   #119 人工裁定（核实/驳回）、CSV 导出（note 口径随行——离机文件
 *   也要有「账单存在 ≠ 认定结论」）。
 *
 * 教义：线索是待核实差异而非认定结论；「无线索」≠「无问题」
 * （未导入的费用类型/账期不可见——空态文案明示）。
 */

import { Fragment, useCallback, useEffect, useMemo, useState } from 'react';
import type { FeeMonthlyResult, RiskClue, RiskResult } from '../api/types';
import { decideFeeRiskClue, fetchFeeMonthly, fetchFeeRisks, formatApiErrorText } from '../api/client';
import { useToast } from '../components/Toast';
import { Stepper } from '../components/Stepper';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';
import { centsToYuan, clueKey, formatRiskMetric } from './fees-format';
import {
  centsToWan,
  clueStateLabel,
  cluesCsv,
  downloadCsv,
  mergeClues,
  momRatio,
  padMonthly,
  seriesLatestMonth,
  slotOf,
  sortMergedClues,
  trendGeometry,
  TREND_VIEW_H,
  TREND_VIEW_W,
  type ClueSortKey,
  type MonthSlot,
  type TrendSeries,
} from './feesOverviewModel';
import {
  FEES_501_HINT,
  FEES_OV_CARD_MOM,
  FEES_OV_CARD_MONTH_TOTAL,
  FEES_OV_CARD_SITES,
  FEES_OV_CLUES_EMPTY,
  FEES_OV_CLUES_EXPORT,
  FEES_OV_CLUES_EXPORT_OK,
  FEES_OV_CLUES_OF,
  FEES_OV_CLUES_SORT,
  FEES_OV_CLUES_SORT_AMOUNT,
  FEES_OV_CLUES_SORT_METRIC,
  FEES_OV_CLUES_TITLE,
  FEES_OV_COL_METRIC,
  FEES_OV_COL_PERIOD,
  FEES_OV_COL_SCENARIO,
  FEES_OV_COL_SITE,
  FEES_OV_COL_STATE,
  FEES_OV_DESC,
  FEES_OV_MOM_DOWN,
  FEES_OV_MOM_NONE,
  FEES_OV_MOM_UP,
  FEES_OV_MONTH,
  FEES_OV_MONTH_AUTO,
  FEES_OV_NOTE,
  FEES_OV_RERUN,
  FEES_OV_RUN,
  FEES_OV_RUNNING,
  FEES_OV_STEP_CLUES,
  FEES_OV_STEP_CROSS,
  FEES_OV_STEP_DETAIL,
  FEES_OV_STEP_EXPORT,
  FEES_OV_STEP_TOTAL,
  FEES_OV_STEPS,
  FEES_OV_STATE_PENDING,
  FEES_OV_TITLE,
  FEES_OV_TREND_EMPTY,
  FEES_OV_TREND_LEGEND_SPIKE,
  FEES_OV_TREND_SPIKE,
  FEES_OV_TREND_TITLE,
  FEES_OV_YEAR,
  FEES_RISKS_DECIDE_DISMISS,
  FEES_RISKS_DECIDE_VERIFY,
  FEES_RISKS_DECISION_DISMISSED,
  FEES_RISKS_DECISION_VERIFIED,
  FEES_RISKS_EVIDENCE_BATCH,
  FEES_RISKS_PERIOD_ALL_YEAR,
  FEE_TYPE_ELECTRICITY,
  FEE_TYPE_MAINTENANCE,
  FEE_TYPE_REPAIR,
  FEE_TYPE_TOWER_LEASE,
} from '../i18n';

const TYPES = [
  { value: 'electricity', label: FEE_TYPE_ELECTRICITY, color: '#2f5fe0' },
  { value: 'tower_lease', label: FEE_TYPE_TOWER_LEASE, color: '#e8842c' },
  { value: 'repair', label: FEE_TYPE_REPAIR, color: '#2ea44f' },
  { value: 'maintenance', label: FEE_TYPE_MAINTENANCE, color: '#8250df' },
];

const SCENARIOS_ALL = [
  'retired-billing',
  'duplicate-settlement',
  'ledger-mismatch',
  'spike',
  'outlier',
  'unlinked',
  'idle-billing',
  'missing-billing',
  'ledger-consistency',
  'custom-rules',
];

function periodLabel(c: RiskClue): string {
  return c.month > 0
    ? `${c.year}-${String(c.month).padStart(2, '0')}`
    : FEES_RISKS_PERIOD_ALL_YEAR(c.year);
}

export function FeesOverview() {
  const toast = useToast();

  // ---- 总览参数 ----
  const [ovYear, setOvYear] = useState(String(new Date().getFullYear()));
  const [ovMonthSel, setOvMonthSel] = useState(''); // '' = 最新有数据月
  const [ovType, setOvType] = useState('electricity');

  const [curMonthly, setCurMonthly] = useState<Record<string, FeeMonthlyResult | null> | null>(null);
  const [prevMonthly, setPrevMonthly] = useState<Record<string, FeeMonthlyResult | null> | null>(null);
  const [ovLoading, setOvLoading] = useState(false);
  const [ovError, setOvError] = useState('');
  const [ovEngineMissing, setOvEngineMissing] = useState(false);

  // ---- 一键分析 ----
  const [results, setResults] = useState<RiskResult[] | null>(null);
  const [runRunning, setRunRunning] = useState(false);
  const [runError, setRunError] = useState('');
  const [runEngineMissing, setRunEngineMissing] = useState(false);
  const [sortKey, setSortKey] = useState<ClueSortKey>('amount');
  const [expanded, setExpanded] = useState<string | null>(null);
  const [decidingKey, setDecidingKey] = useState<string | null>(null);
  const [ranSignature, setRanSignature] = useState('');

  const loadMonthly = useCallback(async () => {
    const year = Number(ovYear);
    if (!Number.isInteger(year) || year < 2000 || year > 2100) {
      setOvError('年份须为 2000–2100 的整数');
      return;
    }
    setOvLoading(true);
    setOvError('');
    setOvEngineMissing(false);
    try {
      const load = async (y: number): Promise<Record<string, FeeMonthlyResult | null>> => {
        const out: Record<string, FeeMonthlyResult | null> = {};
        await Promise.all(
          TYPES.map(async (t) => {
            try {
              out[t.value] = await fetchFeeMonthly({ feeType: t.value, year: y });
            } catch (err) {
              const code = (err as { code?: string }).code ?? '';
              if (code === 'engine_not_built') {
                out[t.value] = null;
                setOvEngineMissing(true);
              } else {
                throw err;
              }
            }
          }),
        );
        return out;
      };
      const [cur, prev] = await Promise.all([load(year), load(year - 1)]);
      setCurMonthly(cur);
      setPrevMonthly(prev);
    } catch (err) {
      setOvError(formatApiErrorText(err));
      setCurMonthly(null);
      setPrevMonthly(null);
    } finally {
      setOvLoading(false);
    }
  }, [ovYear]);

  useEffect(() => {
    void loadMonthly();
  }, [loadMonthly]);

  /** 各类型 12 槽补位序列（无数据月 null——补空白不造零）。 */
  const slots: Record<string, MonthSlot[]> = useMemo(() => {
    const out: Record<string, MonthSlot[]> = {};
    if (curMonthly === null) return out;
    for (const t of TYPES) out[t.value] = padMonthly(curMonthly[t.value]?.items ?? []);
    return out;
  }, [curMonthly]);

  const prevSlots: Record<string, MonthSlot[]> = useMemo(() => {
    const out: Record<string, MonthSlot[]> = {};
    if (prevMonthly === null) return out;
    for (const t of TYPES) out[t.value] = padMonthly(prevMonthly[t.value]?.items ?? []);
    return out;
  }, [prevMonthly]);

  /** 解析后的月份：显式选择优先，否则全类型最新有数据月（0=全年无数据）。 */
  const resolvedMonth = useMemo(() => {
    if (ovMonthSel !== '') return Number(ovMonthSel);
    let latest = 0;
    for (const t of TYPES) latest = Math.max(latest, seriesLatestMonth(slots[t.value] ?? []));
    return latest;
  }, [ovMonthSel, slots]);

  const dataMonths = useMemo(() => {
    const set = new Set<number>();
    for (const t of TYPES) {
      for (const s of slots[t.value] ?? []) if (s.point !== null) set.add(s.month);
    }
    return [...set].sort((a, b) => a - b);
  }, [slots]);

  const hasAnyData = dataMonths.length > 0;

  /** 一键分析：十场景顺序执行（治理默认阈值；单场景调参在风险面板）。 */
  const runAll = useCallback(async () => {
    const year = Number(ovYear);
    if (!Number.isInteger(year) || resolvedMonth <= 0) return;
    setRunRunning(true);
    setRunError('');
    setRunEngineMissing(false);
    setExpanded(null);
    const collected: RiskResult[] = [];
    try {
      for (const sc of SCENARIOS_ALL) {
        const res = await fetchFeeRisks(sc, {
          feeType: ovType,
          year,
          month: resolvedMonth,
          limit: 50,
        });
        collected.push(res);
      }
      setResults(collected);
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'engine_not_built') {
        setRunEngineMissing(true);
        setResults(null);
      } else {
        setRunError(formatApiErrorText(err));
      }
    } finally {
      setRunRunning(false);
    }
  }, [ovYear, ovType, resolvedMonth]);

  // 选择变化即自动跑一轮（仪表盘活着；按钮用于显式重跑）。签名防抖：
  // 相同选择不重复跑。
  const signature = `${ovType}|${ovYear}|${resolvedMonth}`;
  useEffect(() => {
    if (ovLoading || ovEngineMissing || resolvedMonth <= 0 || curMonthly === null) return;
    if (signature === ranSignature) return;
    setRanSignature(signature);
    void runAll();
  }, [signature, ranSignature, ovLoading, ovEngineMissing, resolvedMonth, curMonthly, runAll]);

  const merged = useMemo(
    () => (results === null ? [] : sortMergedClues(mergeClues(results), sortKey)),
    [results, sortKey],
  );

  const decideClue = useCallback(
    async (m: { scenario: string; clue: RiskClue }, status: 'verified' | 'dismissed') => {
      const key = clueKey(m.clue);
      setDecidingKey(`${m.scenario}|${key}`);
      setRunError('');
      try {
        await decideFeeRiskClue({
          scenario: m.scenario,
          feeType: ovType,
          siteCode: m.clue.siteCode,
          year: m.clue.year,
          month: m.clue.month,
          status,
        });
        await runAll();
      } catch (err) {
        setRunError(formatApiErrorText(err));
      } finally {
        setDecidingKey(null);
      }
    },
    [ovType, runAll],
  );

  const doExport = useCallback(() => {
    const typeLabel = TYPES.find((t) => t.value === ovType)?.label ?? ovType;
    const month = `${ovYear}-${String(resolvedMonth).padStart(2, '0')}`;
    downloadCsv(
      cluesCsv(merged),
      `fees-clues-${ovType}-${ovYear}-${String(resolvedMonth).padStart(2, '0')}.csv`,
    );
    toast.show(FEES_OV_CLUES_EXPORT_OK(typeLabel, month), 'success');
  }, [merged, ovType, ovYear, resolvedMonth, toast]);

  /** 趋势图几何：全部有数据的类型各一条线，选中类型为首条（异常点检测基准）。 */
  const trend = useMemo(() => {
    const withData = TYPES.filter((t) =>
      (slots[t.value] ?? []).some((s) => s.point !== null),
    );
    const primary = withData.find((t) => t.value === ovType) ?? withData[0];
    const ordered = primary === undefined
      ? []
      : [primary, ...withData.filter((t) => t !== primary)];
    const series: TrendSeries[] = ordered.map((t) => ({
      key: t.value,
      label: t.label,
      color: t.color,
      slots: slots[t.value] ?? [],
    }));
    return trendGeometry(series);
  }, [slots, ovType]);

  /** 卡片：类型 → {本月分, 环比, 站址数}（跨年环比取上年 12 月）。 */
  const cards = useMemo(() => {
    const out: Array<{
      value: string;
      label: string;
      cents: number;
      mom: number | null;
      sites: number;
    }> = [];
    for (const t of TYPES) {
      const cur = slotOf(slots[t.value] ?? [], resolvedMonth);
      if (cur === null || cur.point === null) continue;
      const prevMonth = resolvedMonth > 1 ? resolvedMonth - 1 : 12;
      const prev =
        resolvedMonth > 1
          ? slotOf(slots[t.value] ?? [], prevMonth)
          : slotOf(prevSlots[t.value] ?? [], prevMonth);
      out.push({
        value: t.value,
        label: t.label,
        cents: cur.point.totalCents,
        mom: momRatio(cur.point.totalCents, prev?.point?.totalCents ?? null),
        sites: cur.point.sites,
      });
    }
    return out;
  }, [slots, prevSlots, resolvedMonth]);

  const monthTotal = cards.reduce((acc, c) => acc + c.cents, 0);
  const typeLabel = TYPES.find((t) => t.value === ovType)?.label ?? ovType;
  const monthLabel = `${ovYear}-${String(resolvedMonth).padStart(2, '0')}`;

  return (
    <section className="panel">
      <h2 className="panel__title">{FEES_OV_TITLE}</h2>
      <div className="panel__body">
        <p className="panel__desc">{FEES_OV_DESC}</p>

        <div className="filters">
          <label className="field field--short">
            <span>{FEES_OV_YEAR}</span>
            <input
              value={ovYear}
              onChange={(e) => {
                setOvYear(e.target.value);
              }}
              inputMode="numeric"
            />
          </label>
          <label className="field">
            <span>{FEES_OV_MONTH}</span>
            <select
              value={ovMonthSel}
              onChange={(e) => {
                setOvMonthSel(e.target.value);
              }}
            >
              <option value="">{FEES_OV_MONTH_AUTO}</option>
              {dataMonths.map((m) => (
                <option key={m} value={String(m)}>
                  {ovYear}-{String(m).padStart(2, '0')}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>费用类型</span>
            <select
              value={ovType}
              onChange={(e) => {
                setOvType(e.target.value);
              }}
            >
              {TYPES.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </select>
          </label>
          <button
            className="btn"
            disabled={runRunning || ovLoading || ovEngineMissing || resolvedMonth <= 0}
            onClick={() => {
              void runAll();
            }}
          >
            {runRunning ? FEES_OV_RUNNING : results !== null ? FEES_OV_RERUN : FEES_OV_RUN}
          </button>
        </div>

        {ovError !== '' && <ErrorBlock message={ovError} />}
        {ovEngineMissing && <div className="banner banner--hint">{FEES_501_HINT}</div>}

        {ovLoading && <Spinner />}

        {!ovLoading && !ovError.includes('年份') && (
          <>
            {cards.length === 0 && resolvedMonth > 0 && (
              <div className="banner banner--hint">
                {FEES_OV_TREND_EMPTY}（{monthLabel}）
              </div>
            )}

            <div className="ov-cards">
              {cards.map((c) => {
                const momText =
                  c.mom === null
                    ? FEES_OV_MOM_NONE
                    : c.mom >= 0
                      ? FEES_OV_MOM_UP((c.mom * 100).toFixed(1))
                      : FEES_OV_MOM_DOWN((c.mom * 100).toFixed(1));
                const momCls = c.mom === null ? '' : c.mom >= 0 ? 'ov-mom--up' : 'ov-mom--down';
                return (
                  <div key={c.value} className="ov-card">
                    <p className="ov-card__title">{FEES_OV_CARD_MONTH_TOTAL(c.label)}</p>
                    <p className="ov-card__value">{centsToWan(c.cents)}</p>
                    <p className="ov-card__sub">
                      <span className={momCls}>
                        {FEES_OV_CARD_MOM} {momText}
                      </span>
                      <span className="muted"> · {FEES_OV_CARD_SITES(c.sites)}</span>
                    </p>
                  </div>
                );
              })}
            </div>

            {hasAnyData && (
              <div className="ov-trend">
                <p className="ov-trend__title">
                  {FEES_OV_TREND_TITLE}
                  <span className="muted muted--small"> {FEES_OV_TREND_LEGEND_SPIKE}</span>
                </p>
                <svg
                  viewBox={`0 0 ${TREND_VIEW_W} ${TREND_VIEW_H}`}
                  className="ov-trend__svg"
                  role="img"
                  aria-label={FEES_OV_TREND_TITLE}
                >
                  {trend.yTicks.map((tick, i) => (
                    <g key={i}>
                      <text x={tick.x} y={tick.y} textAnchor="end" className="ov-axis-text">
                        {tick.label}
                      </text>
                      <line
                        x1={52}
                        y1={tick.y - 4}
                        x2={TREND_VIEW_W - 14}
                        y2={tick.y - 4}
                        className="ov-grid"
                      />
                    </g>
                  ))}
                  {trend.xTicks.map((t) => (
                    <text
                      key={t.label}
                      x={t.x}
                      y={TREND_VIEW_H - 8}
                      textAnchor="middle"
                      className="ov-axis-text"
                    >
                      {t.label}
                    </text>
                  ))}
                  {trend.paths.map((p) => (
                    <g key={p.key}>
                      <polyline
                        points={p.points}
                        fill="none"
                        stroke={p.color}
                        strokeWidth={2}
                        className="ov-line"
                      />
                      {p.dots.map((d) => {
                        const spike = trend.spikeMonths.includes(d.month) && p.key === ovType;
                        return (
                          <circle
                            key={`${p.key}-${d.month}`}
                            cx={d.x}
                            cy={d.y}
                            r={spike ? 5 : 3.5}
                            fill={spike ? 'none' : p.color}
                            stroke={spike ? p.color : 'none'}
                            strokeWidth={spike ? 2 : 0}
                            className="ov-dot"
                          >
                            <title>
                              {`${p.label} ${d.month}月 · ${centsToWan(d.cents)} 万元`}
                              {spike ? ` · ${FEES_OV_TREND_SPIKE}` : ''}
                            </title>
                          </circle>
                        );
                      })}
                    </g>
                  ))}
                </svg>
                <div className="ov-legend">
                  {trend.paths.map((p) => (
                    <span key={p.key} className="ov-legend__item">
                      <span className="ov-legend__swatch" style={{ background: p.color }} />
                      {p.label}
                    </span>
                  ))}
                </div>
              </div>
            )}

            <div className="ov-steps">
              <Stepper
                steps={FEES_OV_STEPS.map((s) => ({ title: s }))}
                current={results !== null ? 2 : 0}
              />
              <div className="ov-steps__notes">
                <p>{results !== null ? FEES_OV_STEP_TOTAL(centsToWan(monthTotal)) : '—'}</p>
                <p>
                  {results !== null
                    ? FEES_OV_STEP_CLUES(
                        results.filter((r) => r.clues.length > 0).length,
                        merged.length,
                      )
                    : runRunning
                      ? FEES_OV_RUNNING
                      : '—'}
                </p>
                <p className="muted">{FEES_OV_STEP_DETAIL}</p>
                <p className="muted">{FEES_OV_STEP_CROSS}</p>
                <p className="muted">{FEES_OV_STEP_EXPORT}</p>
              </div>
            </div>

            {runError !== '' && <ErrorBlock message={runError} />}
            {runEngineMissing && <div className="banner banner--hint">{FEES_501_HINT}</div>}

            <div className="ov-clues__head">
              <h3 className="panel__title panel__title--sub">
                {FEES_OV_CLUES_TITLE}
                <span className="muted muted--small">
                  {' '}
                  {FEES_OV_CLUES_OF(typeLabel, monthLabel)}
                </span>
              </h3>
              <div className="ov-clues__actions">
                <label className="field field--inline">
                  <span>{FEES_OV_CLUES_SORT}</span>
                  <select
                    value={sortKey}
                    onChange={(e) => {
                      setSortKey(e.target.value === 'metric' ? 'metric' : 'amount');
                    }}
                  >
                    <option value="amount">{FEES_OV_CLUES_SORT_AMOUNT}</option>
                    <option value="metric">{FEES_OV_CLUES_SORT_METRIC}</option>
                  </select>
                </label>
                <button
                  className="btn btn--small"
                  disabled={merged.length === 0}
                  onClick={doExport}
                >
                  {FEES_OV_CLUES_EXPORT}
                </button>
              </div>
            </div>

            {runRunning && <Spinner />}
            {!runRunning && results !== null && merged.length === 0 && (
              <div className="banner banner--hint">{FEES_OV_CLUES_EMPTY}</div>
            )}
            {!runRunning && results !== null && merged.length > 0 && (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>{FEES_OV_COL_SITE}</th>
                      <th>{FEES_OV_COL_PERIOD}</th>
                      <th>{FEES_OV_COL_SCENARIO}</th>
                      <th>{FEES_OV_COL_METRIC}</th>
                      <th>{FEES_OV_COL_STATE}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {merged.map((m) => {
                      const key = `${m.scenario}|${clueKey(m.clue)}`;
                      const open = expanded === key;
                      return (
                        <Fragment key={key}>
                          <tr
                            className="row--expandable"
                            onClick={() => {
                              setExpanded(open ? null : key);
                            }}
                          >
                            <td>{m.clue.siteCode}</td>
                            <td>{periodLabel(m.clue)}</td>
                            <td>{m.scenarioCn}</td>
                            <td>
                              {formatRiskMetric(m.clue.metricValue, m.clue.metricLabel)}
                              {/* #23: 空值兜底——后端漏给口径时渲染 null，不出空括号 */}
                              {m.clue.thresholdLabel ? (
                                <span className="muted muted--small">（{m.clue.thresholdLabel}）</span>
                              ) : null}
                            </td>
                            <td>
                              {clueStateLabel(m.clue.decision) === 'verified' && (
                                <span className="chip chip--ok">{FEES_RISKS_DECISION_VERIFIED}</span>
                              )}
                              {clueStateLabel(m.clue.decision) === 'dismissed' && (
                                <span className="chip chip--muted">
                                  {FEES_RISKS_DECISION_DISMISSED}
                                </span>
                              )}
                              {clueStateLabel(m.clue.decision) === 'pending' && (
                                <span className="chip">{FEES_OV_STATE_PENDING}</span>
                              )}
                            </td>
                          </tr>
                          {open && (
                            <tr className="row--detail">
                              <td colSpan={5}>
                                {m.clue.note !== undefined && m.clue.note !== '' && (
                                  <p className="muted">{m.clue.note}</p>
                                )}
                                <p>
                                  <button
                                    className="btn btn--small"
                                    disabled={
                                      decidingKey === key || m.clue.decision === 'verified'
                                    }
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      void decideClue(m, 'verified');
                                    }}
                                  >
                                    {FEES_RISKS_DECIDE_VERIFY}
                                  </button>{' '}
                                  <button
                                    className="btn btn--small"
                                    disabled={
                                      decidingKey === key || m.clue.decision === 'dismissed'
                                    }
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      void decideClue(m, 'dismissed');
                                    }}
                                  >
                                    {FEES_RISKS_DECIDE_DISMISS}
                                  </button>
                                </p>
                                <ul className="kv">
                                  {(m.clue.evidence ?? []).map((a, i) => (
                                    <li key={i} className="kv__item">
                                      <span className="kv__key">
                                        {a.sourceFile}:{a.sourceRow}
                                      </span>
                                      <span className="kv__value">
                                        ¥{centsToYuan(a.amountCents)} ·{' '}
                                        {FEES_RISKS_EVIDENCE_BATCH(a.importBatch)}
                                      </span>
                                    </li>
                                  ))}
                                </ul>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      );
                    })}
                  </tbody>
                </table>
                <p className="muted muted--small">{FEES_OV_NOTE}</p>
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}
