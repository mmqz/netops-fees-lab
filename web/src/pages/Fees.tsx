/**
 * 费用分析页 /fees（#58 阶段 5，前端工作台入口）：
 * - 导入费用文件（multipart：file + feeType + year + month → POST /api/fees/imports，
 *   201 新导入 / 200 幂等重复双形态提示）；
 * - 费用批次列表（最新在前）+ 回滚（ConfirmDialog：删分析行、保快照证据）；
 * - 费用-资源关联（#63 分级关联的前端人类通道）：重新扫描 + 状态/级别筛选 +
 *   模糊提案的确认/驳回（驳回走 ConfirmDialog——审计保留不可撤）；
 * - 风险七场景查询（GET /api/fees/risks/{scenario}）：线索表（站址/账期/触发指标/
 *   显式阈值）+ 行展开证据锚点（sourceFile+sourceRow+importBatch+整数分）+
 *   excluded 计数说明；501 engine_not_built 渲染为引导横幅而非裸错误。
 *
 * 教义：线索是待核实差异而非认定结论（账单存在 ≠ 重复付款认定）——页面文案
 * 与 note 字段同源，均由后端契约携带。
 */

import { Fragment, useCallback, useEffect, useRef, useState } from 'react';
import type { FeeLinkItem, FeeLinkScanResult, RiskResult } from '../api/types';
import {
  confirmFeeLink,
  dismissFeeLink,
  fetchFeeBatches,
  fetchFeeLinks,
  fetchFeeRisks,
  formatApiErrorText,
  rollbackFeeBatch,
  scanFeeLinks,
  uploadFeeFile,
} from '../api/client';
import { useToast } from '../components/Toast';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';
import { Pagination } from '../components/Pagination';
import {
  clueKey,
  centsToYuan,
  formatRiskMetric,
  linkGradeLabel,
  linkStatusLabel,
  selectLinkFilter,
} from './fees-format';
import type { LinkFilterSelection } from './fees-format';
import {
  FEES_501_HINT,
  FEES_BATCHES_COL_FILE,
  FEES_BATCHES_COL_PERIOD,
  FEES_BATCHES_COL_ROWS,
  FEES_BATCHES_COL_STATUS,
  FEES_BATCHES_COL_TYPE,
  FEES_BATCHES_EMPTY,
  FEES_BATCHES_FAILED_ROWS,
  FEES_BATCHES_ROLLED_BACK,
  FEES_BATCHES_ROLLBACK,
  FEES_BATCHES_ROLLBACK_BUSY,
  FEES_BATCHES_ROLLBACK_CONFIRM,
  FEES_BATCHES_TITLE,
  FEES_DESC,
  FEES_IMPORT_DUP,
  FEES_IMPORT_FILE,
  FEES_IMPORT_MONTH,
  FEES_IMPORT_OK,
  FEES_IMPORT_PERIOD_INVALID,
  FEES_IMPORT_SUBMIT,
  FEES_IMPORT_TITLE,
  FEES_IMPORT_TYPE,
  FEES_IMPORT_YEAR,
  FEES_LINKS_ACTIONS,
  FEES_LINKS_ALL,
  FEES_LINKS_CODE,
  FEES_LINKS_CONFIRM,
  FEES_LINKS_CONFIRMED,
  FEES_LINKS_DESC,
  FEES_LINKS_DISMISS,
  FEES_LINKS_DISMISS_CONFIRM,
  FEES_LINKS_DISMISSED,
  FEES_LINKS_DISMISSING,
  FEES_LINKS_EMPTY,
  FEES_LINKS_GRADE,
  FEES_LINKS_RESOURCE,
  FEES_LINKS_SCAN,
  FEES_LINKS_SCAN_RESULT,
  FEES_LINKS_SIMILARITY,
  FEES_LINKS_SOURCE,
  FEES_LINKS_STATUS,
  FEES_LINKS_TITLE,
  FEES_RISKS_CLUE_COUNT,
  FEES_RISKS_COL_EVIDENCE,
  FEES_RISKS_COL_METRIC,
  FEES_RISKS_COL_PERIOD,
  FEES_RISKS_COL_SITE,
  FEES_RISKS_COL_THRESHOLD,
  FEES_RISKS_EVIDENCE_BATCH,
  FEES_RISKS_EVIDENCE_ROWS,
  FEES_RISKS_EMPTY,
  FEES_RISKS_EVIDENCE,
  FEES_RISKS_EVIDENCE_MORE,
  FEES_RISKS_EXCLUDED,
  FEES_RISKS_MONTH_ALL,
  FEES_RISKS_PERIOD_ALL_YEAR,
  FEES_RISKS_STATUS,
  FEES_RISKS_STATUS_HINT,
  FEES_RISKS_YEAR_INVALID,
  FEES_RISKS_DIFF_CENTS,
  FEES_RISKS_RATIO,
  FEES_RISKS_Z,
  FEES_RISKS_MIN_SITES,
  FEES_RISKS_LIMIT,
  FEES_RISKS_RUN,
  FEES_RISKS_SCENARIO,
  FEES_RISKS_TITLE,
  FEES_TITLE,
  FEE_TYPE_ELECTRICITY,
  FEE_TYPE_MAINTENANCE,
  FEE_TYPE_REPAIR,
  FEE_TYPE_TOWER_LEASE,
  LINKS_GRADE_EXACT,
  LINKS_GRADE_FUZZY,
  LINKS_GRADE_RULE,
  LINKS_SOURCE_AUTO,
  LINKS_SOURCE_HUMAN,
  LINKS_STATUS_CONFIRMED,
  LINKS_STATUS_DISMISSED,
  LINKS_STATUS_PENDING,
  RISK_SCENARIO_DUPLICATE_SETTLEMENT,
  RISK_SCENARIO_LEDGER_MISMATCH,
  RISK_SCENARIO_OUTLIER,
  RISK_SCENARIO_RETIRED_BILLING,
  RISK_SCENARIO_SPIKE,
  RISK_SCENARIO_UNLINKED,
  RISK_SCENARIO_IDLE_BILLING,
  RISK_SCENARIO_MISSING_BILLING,
} from '../i18n';

/** 线索行键与金额展示逻辑见 fees-format.ts（#68 抽出纯函数，表测钉死） */

const FEE_TYPES = [
  { value: 'electricity', label: FEE_TYPE_ELECTRICITY },
  { value: 'tower_lease', label: FEE_TYPE_TOWER_LEASE },
  { value: 'repair', label: FEE_TYPE_REPAIR },
  { value: 'maintenance', label: FEE_TYPE_MAINTENANCE },
];

const SCENARIOS = [
  { value: 'retired-billing', label: RISK_SCENARIO_RETIRED_BILLING },
  { value: 'duplicate-settlement', label: RISK_SCENARIO_DUPLICATE_SETTLEMENT },
  { value: 'ledger-mismatch', label: RISK_SCENARIO_LEDGER_MISMATCH },
  { value: 'spike', label: RISK_SCENARIO_SPIKE },
  { value: 'outlier', label: RISK_SCENARIO_OUTLIER },
  { value: 'unlinked', label: RISK_SCENARIO_UNLINKED },
  { value: 'idle-billing', label: RISK_SCENARIO_IDLE_BILLING },
  { value: 'missing-billing', label: RISK_SCENARIO_MISSING_BILLING },
];

interface FeeBatchRow {
  id: string;
  workbookName: string;
  feeTypeLabel: string;
  period: string;
  status: string;
  rowsTotal: number;
  rowsImported: number;
  rowsFailed: number;
}

export function FeesPage() {
  const toast = useToast();

  // ---- 批次 ----
  const [batches, setBatches] = useState<FeeBatchRow[] | null>(null);
  const [batchesError, setBatchesError] = useState<string>('');
  const [rollbackTarget, setRollbackTarget] = useState<FeeBatchRow | null>(null);
  const [rollingBack, setRollingBack] = useState(false);

  const loadBatches = useCallback(async () => {
    setBatchesError('');
    try {
      const res = await fetchFeeBatches();
      setBatches(res.items);
    } catch (err) {
      setBatchesError(formatApiErrorText(err));
      setBatches(null);
    }
  }, []);

  useEffect(() => {
    void loadBatches();
  }, [loadBatches]);

  const doRollback = useCallback(async () => {
    if (rollbackTarget === null) return;
    setRollingBack(true);
    try {
      await rollbackFeeBatch(rollbackTarget.id);
      toast.show(FEES_BATCHES_ROLLED_BACK(rollbackTarget.workbookName), 'success');
      setRollbackTarget(null);
      await loadBatches();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setRollingBack(false);
    }
  }, [rollbackTarget, loadBatches, toast]);

  // ---- 导入 ----
  const fileInput = useRef<HTMLInputElement | null>(null);
  const [importType, setImportType] = useState('electricity');
  const [importYear, setImportYear] = useState(String(new Date().getFullYear()));
  const [importMonth, setImportMonth] = useState(String(new Date().getMonth() + 1));
  const [importing, setImporting] = useState(false);

  const doImport = useCallback(async () => {
    const file = fileInput.current?.files?.[0];
    if (file === undefined) return;
    const year = Number(importYear);
    const month = Number(importMonth);
    if (!Number.isInteger(year) || year < 2000 || year > 2100 || !Number.isInteger(month) || month < 1 || month > 12) {
      toast.show(FEES_IMPORT_PERIOD_INVALID, 'error');
      return;
    }
    setImporting(true);
    try {
      const sum = await uploadFeeFile(file, importType, year, month);
      if (sum.skippedDuplicate) {
        toast.show(FEES_IMPORT_DUP(sum.existingBatch ?? sum.batchId), 'info');
      } else {
        toast.show(FEES_IMPORT_OK(sum.batchId, sum.rowsImported), 'success');
        if (sum.rowsFailed > 0 && sum.errors !== undefined && sum.errors.length > 0) {
          toast.show(sum.errors[0], 'info');
        }
      }
      if (fileInput.current !== null) fileInput.current.value = '';
      await loadBatches();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setImporting(false);
    }
  }, [importType, importYear, importMonth, loadBatches, toast]);

  // ---- 关联（费用-资源分级关联的人类确认通道）----
  const [links, setLinks] = useState<FeeLinkItem[] | null>(null);
  const [linksTotal, setLinksTotal] = useState(0);
  const [linkPage, setLinkPage] = useState(1);
  const [linksError, setLinksError] = useState('');
  const [linkStatus, setLinkStatus] = useState('pending');
  const [linkGrade, setLinkGrade] = useState('');
  const [scanning, setScanning] = useState(false);
  const [scanResult, setScanResult] = useState<FeeLinkScanResult | null>(null);
  const [dismissTarget, setDismissTarget] = useState<FeeLinkItem | null>(null);
  const [dismissing, setDismissing] = useState(false);

  const loadLinks = useCallback(async () => {
    setLinksError('');
    try {
      const res = await fetchFeeLinks({
        status: linkStatus === '' ? undefined : linkStatus,
        grade: linkGrade === '' ? undefined : linkGrade,
        page: linkPage,
        pageSize: 20,
      });
      setLinks(res.items);
      setLinksTotal(res.total);
    } catch (err) {
      setLinksError(formatApiErrorText(err));
      setLinks(null);
      setLinksTotal(0);
    }
  }, [linkStatus, linkGrade, linkPage]);

  useEffect(() => {
    void loadLinks();
  }, [loadLinks]);

  const doScan = useCallback(async () => {
    setScanning(true);
    try {
      const res = await scanFeeLinks();
      setScanResult(res);
      setLinkPage(1); // #90: a fresh scan re-shapes the list — restart from page 1
      await loadLinks();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setScanning(false);
    }
  }, [loadLinks, toast]);

  const doConfirm = useCallback(async (l: FeeLinkItem) => {
    try {
      await confirmFeeLink(l.id);
      toast.show(FEES_LINKS_CONFIRMED(l.feeSiteCode), 'success');
      await loadLinks();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    }
  }, [loadLinks, toast]);

  const doDismiss = useCallback(async () => {
    if (dismissTarget === null) return;
    setDismissing(true);
    try {
      await dismissFeeLink(dismissTarget.id);
      toast.show(FEES_LINKS_DISMISSED(dismissTarget.feeSiteCode), 'success');
      setDismissTarget(null);
      await loadLinks();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setDismissing(false);
    }
  }, [dismissTarget, loadLinks, toast]);

  // 标签映射函数 linkStatusLabel / linkGradeLabel 见 fees-format.ts（#68）

  /** 状态/级别筛选 onChange 的唯一入口（#109-A）：筛选值与页码同拍迁移
   *  （裁决点在 fees-format.selectLinkFilter）——否则第 5 页切到小数据筛选
   *  会渲染「暂无关联」假空态。与 doScan 的 setLinkPage(1)（#90）同款。 */
  function applyLinkFilter(patch: Partial<Pick<LinkFilterSelection, 'status' | 'grade'>>): void {
    const next = selectLinkFilter({ status: linkStatus, grade: linkGrade, page: linkPage }, patch);
    setLinkStatus(next.status);
    setLinkGrade(next.grade);
    setLinkPage(next.page);
  }

  // ---- 风险查询 ----
  const [scenario, setScenario] = useState('duplicate-settlement');
  const [riskType, setRiskType] = useState('electricity');
  const [riskYear, setRiskYear] = useState(String(new Date().getFullYear()));
  const [riskMonth, setRiskMonth] = useState('');
  const [riskLimit, setRiskLimit] = useState('50');
  // #89: the per-scenario thresholds — visible only for their scenario, so
  // the human channel can tune exactly what the scenario tunes.
  const [riskDiffCents, setRiskDiffCents] = useState('');
  const [riskRatio, setRiskRatio] = useState('');
  const [riskZ, setRiskZ] = useState('');
  const [riskMinSites, setRiskMinSites] = useState('');
  const [riskStatus, setRiskStatus] = useState('');
  const [running, setRunning] = useState(false);
  const [riskResult, setRiskResult] = useState<RiskResult | null>(null);
  const [riskError, setRiskError] = useState('');
  const [engineMissing, setEngineMissing] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);

  const runRisks = useCallback(async () => {
    const year = Number(riskYear);
    if (!Number.isInteger(year) || year < 2000 || year > 2100) {
      setRiskError(FEES_RISKS_YEAR_INVALID);
      return;
    }
    setRunning(true);
    setRiskError('');
    setEngineMissing(false);
    setExpanded(null);
    try {
      const res = await fetchFeeRisks(scenario, {
        feeType: riskType,
        year,
        month: riskMonth === '' ? undefined : Number(riskMonth),
        limit: riskLimit === '' ? undefined : Number(riskLimit),
        // #89: per-scenario thresholds — empty input = the governed default.
        status: scenario === 'retired-billing' && riskStatus.trim() !== '' ? riskStatus.trim() : undefined,
        diffCents:
          scenario === 'ledger-mismatch' && riskDiffCents.trim() !== ''
            ? Number(riskDiffCents)
            : undefined,
        ratio: scenario === 'spike' && riskRatio.trim() !== '' ? Number(riskRatio) : undefined,
        z: scenario === 'outlier' && riskZ.trim() !== '' ? Number(riskZ) : undefined,
        minSites:
          scenario === 'outlier' && riskMinSites.trim() !== '' ? Number(riskMinSites) : undefined,
      });
      setRiskResult(res);
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'engine_not_built') {
        setEngineMissing(true);
        setRiskResult(null);
      } else {
        setRiskError(formatApiErrorText(err));
      }
    } finally {
      setRunning(false);
    }
  }, [
    scenario,
    riskType,
    riskYear,
    riskMonth,
    riskLimit,
    riskStatus,
    riskDiffCents,
    riskRatio,
    riskZ,
    riskMinSites,
  ]);

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <h1 className="page__title">{FEES_TITLE}</h1>
          <p className="page__desc">{FEES_DESC}</p>
        </div>
      </header>

      <section className="panel">
        <h2 className="panel__title">{FEES_RISKS_TITLE}</h2>
        <div className="filters">
          <label className="field">
            <span>{FEES_RISKS_SCENARIO}</span>
            <select value={scenario} onChange={(e) => setScenario(e.target.value)}>
              {SCENARIOS.map((s) => (
                <option key={s.value} value={s.value}>{s.label}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>{FEES_IMPORT_TYPE}</span>
            <select value={riskType} onChange={(e) => setRiskType(e.target.value)}>
              {FEE_TYPES.map((t) => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
          </label>
          <label className="field field--xs">
            <span>{FEES_IMPORT_YEAR}</span>
            <input value={riskYear} onChange={(e) => setRiskYear(e.target.value)} inputMode="numeric" />
          </label>
          <label className="field field--xs">
            <span>{FEES_IMPORT_MONTH}</span>
            <input
              value={riskMonth}
              onChange={(e) => setRiskMonth(e.target.value)}
              inputMode="numeric"
              placeholder={FEES_RISKS_MONTH_ALL}
            />
          </label>
          <label className="field field--xs">
            <span>{FEES_RISKS_LIMIT}</span>
            <input value={riskLimit} onChange={(e) => setRiskLimit(e.target.value)} inputMode="numeric" />
          </label>
          {scenario === 'retired-billing' && (
            <label className="field">
              <span>{FEES_RISKS_STATUS}</span>
              <input
                value={riskStatus}
                onChange={(e) => setRiskStatus(e.target.value)}
                placeholder={FEES_RISKS_STATUS_HINT}
              />
            </label>
          )}
          {scenario === 'ledger-mismatch' && (
            <label className="field field--xs">
              <span>{FEES_RISKS_DIFF_CENTS}</span>
              <input
                value={riskDiffCents}
                onChange={(e) => setRiskDiffCents(e.target.value)}
                inputMode="numeric"
                placeholder="0"
              />
            </label>
          )}
          {scenario === 'spike' && (
            <label className="field field--xs">
              <span>{FEES_RISKS_RATIO}</span>
              <input
                value={riskRatio}
                onChange={(e) => setRiskRatio(e.target.value)}
                inputMode="decimal"
                placeholder="0.5"
              />
            </label>
          )}
          {scenario === 'outlier' && (
            <>
              <label className="field field--xs">
                <span>{FEES_RISKS_Z}</span>
                <input value={riskZ} onChange={(e) => setRiskZ(e.target.value)} inputMode="decimal" placeholder="3" />
              </label>
              <label className="field field--xs">
                <span>{FEES_RISKS_MIN_SITES}</span>
                <input
                  value={riskMinSites}
                  onChange={(e) => setRiskMinSites(e.target.value)}
                  inputMode="numeric"
                  placeholder="8"
                />
              </label>
            </>
          )}
          <button className="btn btn--primary" disabled={running} onClick={() => void runRisks()}>
            {running ? <Spinner /> : FEES_RISKS_RUN}
          </button>
        </div>

        {engineMissing && <div className="banner banner--info">{FEES_501_HINT}</div>}
        {riskError !== '' && <ErrorBlock message={riskError} />}
        {riskResult !== null && (
          <div className="panel__body">
            <p className="muted">
              {riskResult.scenarioCn} · {FEES_RISKS_CLUE_COUNT(riskResult.total)}
              {riskResult.excluded > 0 && <> · {FEES_RISKS_EXCLUDED(riskResult.excluded)}</>}
            </p>
            <p className="muted muted--small">{riskResult.note}</p>
            {riskResult.clues.length === 0 ? (
              <p className="empty">{FEES_RISKS_EMPTY}</p>
            ) : (
              <table className="table table--interactive">
                <thead>
                  <tr>
                    <th>{FEES_RISKS_COL_SITE}</th>
                    <th>{FEES_RISKS_COL_PERIOD}</th>
                    <th>{FEES_RISKS_COL_METRIC}</th>
                    <th>{FEES_RISKS_COL_THRESHOLD}</th>
                    <th>{FEES_RISKS_COL_EVIDENCE}</th>
                  </tr>
                </thead>
                <tbody>
                  {riskResult.clues.map((c) => {
                    const key = clueKey(c);
                    const open = expanded === key;
                    return (
                      <Fragment key={key}>
                        <tr
                          className="row--expandable"
                          onClick={() => {
                            setExpanded(open ? null : key);
                          }}
                        >
                          <td>{c.siteCode}</td>
                          <td>{c.month > 0 ? `${c.year}-${String(c.month).padStart(2, '0')}` : FEES_RISKS_PERIOD_ALL_YEAR(c.year)}</td>
                          <td>
                            {formatRiskMetric(c.metricValue, c.metricLabel)}（{c.metricLabel}）
                          </td>
                          <td className="muted">{c.thresholdLabel}</td>
                          <td>{FEES_RISKS_EVIDENCE_ROWS(c.evidenceTotal)}</td>
                        </tr>
                        {open && (
                          <tr className="row--detail">
                            <td colSpan={5}>
                              {c.note !== undefined && c.note !== '' && <p className="muted">{c.note}</p>}
                              <p className="kv__title">{FEES_RISKS_EVIDENCE}</p>
                              <ul className="kv">
                                {(c.evidence ?? []).map((a, i) => (
                                  <li key={i} className="kv__item">
                                    <span className="kv__key">{a.sourceFile}:{a.sourceRow}</span>
                                    <span className="kv__value">
                                      ¥{centsToYuan(a.amountCents)} · {FEES_RISKS_EVIDENCE_BATCH(a.importBatch)}
                                    </span>
                                  </li>
                                ))}
                                {c.evidenceTotal > c.evidence.length && (
                                  <li className="kv__item muted">
                                    {FEES_RISKS_EVIDENCE_MORE(c.evidenceTotal - c.evidence.length)}
                                  </li>
                                )}
                              </ul>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>
        )}
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_LINKS_TITLE}</h2>
        <p className="page__desc">{FEES_LINKS_DESC}</p>
        <div className="filters">
          <label className="field">
            <span>{FEES_LINKS_STATUS}</span>
            <select value={linkStatus} onChange={(e) => applyLinkFilter({ status: e.target.value })}>
              <option value="pending">{LINKS_STATUS_PENDING}</option>
              <option value="confirmed">{LINKS_STATUS_CONFIRMED}</option>
              <option value="dismissed">{LINKS_STATUS_DISMISSED}</option>
              <option value="">{FEES_LINKS_ALL}</option>
            </select>
          </label>
          <label className="field">
            <span>{FEES_LINKS_GRADE}</span>
            <select value={linkGrade} onChange={(e) => applyLinkFilter({ grade: e.target.value })}>
              <option value="exact">{LINKS_GRADE_EXACT}</option>
              <option value="rule">{LINKS_GRADE_RULE}</option>
              <option value="fuzzy">{LINKS_GRADE_FUZZY}</option>
              <option value="">{FEES_LINKS_ALL}</option>
            </select>
          </label>
          <button className="btn btn--primary" disabled={scanning} onClick={() => void doScan()}>
            {scanning ? <Spinner /> : FEES_LINKS_SCAN}
          </button>
        </div>

        {scanResult !== null && (
          <p className="muted">
            {FEES_LINKS_SCAN_RESULT(
              scanResult.codes, scanResult.exactLinks, scanResult.ruleLinks,
              scanResult.fuzzyProposed, scanResult.unlinked, scanResult.humanKept,
            )}
          </p>
        )}
        {linksError !== '' && <ErrorBlock message={linksError} />}
        <div className="panel__body">
          {links !== null && links.length === 0 && <p className="empty">{FEES_LINKS_EMPTY}</p>}
          {links !== null && links.length > 0 && (
            <Pagination page={linkPage} pageSize={20} total={linksTotal} onChange={setLinkPage} />
          )}
          {links !== null && links.length > 0 && (
            <table className="table table--interactive">
              <thead>
                <tr>
                  <th>{FEES_LINKS_CODE}</th>
                  <th>{FEES_LINKS_RESOURCE}</th>
                  <th>{FEES_LINKS_GRADE}</th>
                  <th>{FEES_LINKS_STATUS}</th>
                  <th>{FEES_LINKS_SOURCE}</th>
                  <th>{FEES_LINKS_ACTIONS}</th>
                </tr>
              </thead>
              <tbody>
                {links.map((l) => (
                  <tr key={l.id}>
                    <td>{l.feeSiteCode}</td>
                    <td>
                      {l.resourceName !== undefined ? `${l.resourceName}（` : ''}
                      {l.resourceKey}
                      {l.resourceName !== undefined ? '）' : ''}
                      {l.similarity !== undefined && l.similarity > 0 && (
                        <span className="chip chip--muted">{FEES_LINKS_SIMILARITY((l.similarity * 100).toFixed(0))}</span>
                      )}
                    </td>
                    <td>
                      <span className="chip chip--muted">{linkGradeLabel(l.grade)}</span>
                      {l.rule !== undefined && l.rule !== '' && <span className="muted"> {l.rule}</span>}
                    </td>
                    <td>
                      <span className={`chip chip--${l.status === 'confirmed' ? 'ok' : l.status === 'pending' ? 'warn' : 'muted'}`}>
                        {linkStatusLabel(l.status)}
                      </span>
                    </td>
                    <td className="muted">{l.source === 'human' ? LINKS_SOURCE_HUMAN : LINKS_SOURCE_AUTO}</td>
                    <td>
                      {l.status === 'pending' && (
                        <>
                          <button className="btn btn--ghost" onClick={() => void doConfirm(l)}>
                            {FEES_LINKS_CONFIRM}
                          </button>
                          <button className="btn btn--ghost" onClick={() => setDismissTarget(l)}>
                            {FEES_LINKS_DISMISS}
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_BATCHES_TITLE}</h2>
        <div className="panel__body">
          {batchesError !== '' && <ErrorBlock message={batchesError} />}
          {batches !== null && batches.length === 0 && (
            <p className="empty">{FEES_BATCHES_EMPTY}</p>
          )}
          {batches !== null && batches.length > 0 && (
            <table className="table table--interactive">
              <thead>
                <tr>
                  <th>{FEES_BATCHES_COL_FILE}</th>
                  <th>{FEES_BATCHES_COL_TYPE}</th>
                  <th>{FEES_BATCHES_COL_PERIOD}</th>
                  <th>{FEES_BATCHES_COL_ROWS}</th>
                  <th>{FEES_BATCHES_COL_STATUS}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {batches.map((b) => (
                  <tr key={b.id}>
                    <td title={b.id}>{b.workbookName}</td>
                    <td>{b.feeTypeLabel}</td>
                    <td>{b.period}</td>
                    <td>
                      {b.rowsImported}/{b.rowsTotal}
                      {b.rowsFailed > 0 && <span className="chip chip--warn">{FEES_BATCHES_FAILED_ROWS(b.rowsFailed)}</span>}
                    </td>
                    <td>
                      <span className={`chip chip--${b.status === 'completed' ? 'ok' : b.status === 'failed' ? 'error' : 'muted'}`}>
                        {b.status}
                      </span>
                    </td>
                    <td>
                      {b.status === 'completed' && (
                        <button className="btn btn--ghost" onClick={() => setRollbackTarget(b)}>
                          {FEES_BATCHES_ROLLBACK}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_IMPORT_TITLE}</h2>
        <div className="panel__body">
          <div className="filters">
            <label className="field">
              <span>{FEES_IMPORT_TYPE}</span>
              <select value={importType} onChange={(e) => setImportType(e.target.value)}>
                {FEE_TYPES.map((t) => (
                  <option key={t.value} value={t.value}>{t.label}</option>
                ))}
              </select>
            </label>
            <label className="field field--xs">
              <span>{FEES_IMPORT_YEAR}</span>
              <input value={importYear} onChange={(e) => setImportYear(e.target.value)} inputMode="numeric" />
            </label>
            <label className="field field--xs">
              <span>{FEES_IMPORT_MONTH}</span>
              <input value={importMonth} onChange={(e) => setImportMonth(e.target.value)} inputMode="numeric" />
            </label>
            <label className="field">
              <span>{FEES_IMPORT_FILE}</span>
              <input
                ref={fileInput}
                type="file"
                accept=".csv,.xlsx"
              />
            </label>
            <button className="btn btn--primary" disabled={importing} onClick={() => void doImport()}>
              {importing ? <Spinner /> : FEES_IMPORT_SUBMIT}
            </button>
          </div>
        </div>
      </section>

      <ConfirmDialog
        open={rollbackTarget !== null}
        title={FEES_BATCHES_ROLLBACK}
        message={rollbackTarget !== null ? FEES_BATCHES_ROLLBACK_CONFIRM(rollbackTarget.workbookName, rollbackTarget.period) : ''}
        confirmLabel={rollingBack ? FEES_BATCHES_ROLLBACK_BUSY : FEES_BATCHES_ROLLBACK}
        busy={rollingBack}
        onConfirm={() => void doRollback()}
        onCancel={() => {
          setRollbackTarget(null);
        }}
      />

      <ConfirmDialog
        open={dismissTarget !== null}
        title={FEES_LINKS_DISMISS}
        message={dismissTarget !== null
          ? FEES_LINKS_DISMISS_CONFIRM(dismissTarget.feeSiteCode, dismissTarget.resourceKey)
          : ''}
        confirmLabel={dismissing ? FEES_LINKS_DISMISSING : FEES_LINKS_DISMISS}
        busy={dismissing}
        onConfirm={() => void doDismiss()}
        onCancel={() => {
          setDismissTarget(null);
        }}
      />
    </div>
  );
}
