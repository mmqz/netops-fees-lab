/**
 * 跨数据集对账面板（#12）：甲方核对乙方的三件套——
 *
 * - 对账表：两套系统按主编码归并（电费 vs 塔租），三类分拣
 *   （只在电费台账=疑似自建站或编码不一致 / 只在塔表=多为共享站 /
 *   双方都有）+ 差额（电−塔）+ 映射痕迹（别名重定向的可见面）；
 * - 扫描提议：两库站址骨架比对，同骨架不同字面=疑为同站双码——
 *   **只提议不落库**，确认走建立通道（留痕）；
 * - 映射台账：登记 + 手工建立 + 移除（管理级治理）。
 *
 * 教义：对账表是待核实差异而非认定结论（账单存在 ≠ 认定结论）。
 */

import { useCallback, useEffect, useState } from 'react';
import type { FeeAliasProposal, FeeCrossReconcileResult, FeeSiteCodeAlias } from '../api/types';
import { useFeesDataYear } from '../hooks/useFeesDataYear';
import {
  addFeeAlias,
  fetchFeeAliases,
  fetchFeeCrossReconcile,
  formatApiErrorText,
  removeFeeAlias,
  scanFeeAliases,
} from '../api/client';
import { useToast } from '../components/Toast';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';
import { centsToYuan } from './fees-format';
import {
  FEES_X_ALIAS_403,
  FEES_X_ALIAS_ADD,
  FEES_X_ALIAS_ADDED,
  FEES_X_ALIAS_ALIAS,
  FEES_X_ALIAS_CANONICAL,
  FEES_X_ALIAS_CONFIRM,
  FEES_X_ALIAS_CONFIDENCE,
  FEES_X_ALIAS_DESC,
  FEES_X_ALIAS_EMPTY,
  FEES_X_ALIAS_PROPOSALS,
  FEES_X_ALIAS_REMOVE,
  FEES_X_ALIAS_REQUIRED,
  FEES_X_ALIAS_SCAN,
  FEES_X_ALIAS_SOURCE,
  FEES_X_ALIAS_SUBMIT,
  FEES_X_ALIAS_TITLE,
  FEES_X_CLASS_BOTH,
  FEES_X_CLASS_ONLY_ELEC,
  FEES_X_CLASS_ONLY_TOWER,
  FEES_X_COL_DELTA,
  FEES_X_COL_ELEC,
  FEES_X_COL_SITE,
  FEES_X_COL_TOWER,
  FEES_X_COL_TRAIL,
  FEES_X_COUNTS,
  FEES_X_DESC,
  FEES_X_EMPTY,
  FEES_X_MONTH,
  FEES_X_RUN,
  FEES_X_TITLE,
  FEES_X_YEAR,
} from '../i18n';

function classChip(cls: string): { cls: string; label: string } {
  if (cls === 'both') return { cls: 'chip chip--ok', label: FEES_X_CLASS_BOTH };
  if (cls === 'only_electricity') return { cls: 'chip chip--warn', label: FEES_X_CLASS_ONLY_ELEC };
  return { cls: 'chip', label: FEES_X_CLASS_ONLY_TOWER };
}

const ALIAS_SOURCES = [
  { value: 'tower_lease', label: '塔类详单' },
  { value: 'electricity', label: '电费台账' },
  { value: 'repair', label: '维修' },
  { value: 'maintenance', label: '综维' },
  { value: 'manual', label: '人工' },
];

export function FeesCrossReconcile() {
  const toast = useToast();

  // ---- 对账表 ----
  // v0.20.3 数据优先年份默认：对账两面的年份输入同盲区修复（useFeesDataYear
  // 套用纪律三条——初始日历年、发现落地一次性切最新有数据年、用户先改则不抢）。
  const { year: xYear, setYear: setXYear } = useFeesDataYear();
  const [xMonth, setXMonth] = useState('');
  const [xResult, setXResult] = useState<FeeCrossReconcileResult | null>(null);
  const [xRunning, setXRunning] = useState(false);
  const [xError, setXError] = useState('');
  const [xEngineMissing, setXEngineMissing] = useState(false);

  const runReconcile = useCallback(async () => {
    const year = Number(xYear);
    if (!Number.isInteger(year) || year < 2000 || year > 2100) {
      setXError('年份须为 2000–2100 的整数');
      return;
    }
    setXRunning(true);
    setXError('');
    setXEngineMissing(false);
    try {
      setXResult(await fetchFeeCrossReconcile({
        year,
        month: xMonth === '' ? undefined : Number(xMonth),
      }));
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'engine_not_built') {
        setXEngineMissing(true);
        setXResult(null);
      } else {
        setXError(formatApiErrorText(err));
      }
    } finally {
      setXRunning(false);
    }
  }, [xYear, xMonth]);

  // ---- 映射台账 + 扫描提议 ----
  const [aliases, setAliases] = useState<FeeSiteCodeAlias[] | null>(null);
  const [proposals, setProposals] = useState<FeeAliasProposal[] | null>(null);
  const [scanning, setScanning] = useState(false);
  const [aliasError, setAliasError] = useState('');
  const [confirmingKey, setConfirmingKey] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [inCanonical, setInCanonical] = useState('');
  const [inAlias, setInAlias] = useState('');
  const [inSource, setInSource] = useState('tower_lease');
  const [inConfidence, setInConfidence] = useState('manual');
  const [adding, setAdding] = useState(false);

  const loadAliases = useCallback(async () => {
    setAliasError('');
    try {
      const res = await fetchFeeAliases();
      setAliases(res.items);
    } catch (err) {
      setAliasError(formatApiErrorText(err));
      setAliases(null);
    }
  }, []);

  useEffect(() => {
    void loadAliases();
  }, [loadAliases]);

  const doScan = useCallback(async () => {
    setScanning(true);
    setAliasError('');
    try {
      const res = await scanFeeAliases();
      setProposals(res.items);
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'engine_not_built') {
        toast.show('扫描需要 DuckDB 引擎构建——默认构建答 501（两库站址宇宙在分析层）。', 'info');
        setProposals(null);
      } else {
        setAliasError(formatApiErrorText(err));
      }
    } finally {
      setScanning(false);
    }
  }, [toast]);

  const confirmProposal = useCallback(async (p: FeeAliasProposal) => {
    const key = p.canonical + '|' + p.alias;
    setConfirmingKey(key);
    setAliasError('');
    try {
      await addFeeAlias({ canonical: p.canonical, alias: p.alias, source: p.source, confidence: 'rule' });
      toast.show(FEES_X_ALIAS_ADDED, 'success');
      await loadAliases();
      setProposals((prev) => (prev ?? []).filter((q) => q.canonical + '|' + q.alias !== key));
    } catch (err) {
      setAliasError(formatApiErrorText(err));
    } finally {
      setConfirmingKey(null);
    }
  }, [loadAliases, toast]);

  const doRemove = useCallback(async (a: FeeSiteCodeAlias) => {
    setRemovingId(a.id);
    setAliasError('');
    try {
      await removeFeeAlias(a.id);
      toast.show(`映射 ${a.canonical} ↔ ${a.alias} 已移除`, 'success');
      await loadAliases();
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'forbidden' || code === 'forbidden_admin') {
        toast.show(FEES_X_ALIAS_403, 'error');
      } else {
        setAliasError(formatApiErrorText(err));
      }
    } finally {
      setRemovingId(null);
    }
  }, [loadAliases, toast]);

  const doAdd = useCallback(async () => {
    if (inCanonical.trim() === '' || inAlias.trim() === '') {
      toast.show(FEES_X_ALIAS_REQUIRED, 'error');
      return;
    }
    setAdding(true);
    try {
      await addFeeAlias({
        canonical: inCanonical.trim(), alias: inAlias.trim(),
        source: inSource, confidence: inConfidence,
      });
      toast.show(FEES_X_ALIAS_ADDED, 'success');
      setInCanonical('');
      setInAlias('');
      await loadAliases();
    } catch (err) {
      setAliasError(formatApiErrorText(err));
    } finally {
      setAdding(false);
    }
  }, [inCanonical, inAlias, inSource, inConfidence, loadAliases, toast]);

  return (
    <section className="panel">
      <h2 className="panel__title">{FEES_X_TITLE}</h2>
      <div className="panel__body">
        <p className="panel__desc">{FEES_X_DESC}</p>

        <div className="filters">
          <label className="field field--short">
            <span>{FEES_X_YEAR}</span>
            <input className="input" value={xYear} onChange={(e) => { setXYear(e.target.value); }} inputMode="numeric" />
          </label>
          <label className="field field--short">
            <span>{FEES_X_MONTH}</span>
            <input className="input" value={xMonth} onChange={(e) => { setXMonth(e.target.value); }} inputMode="numeric" />
          </label>
          <button className="btn" disabled={xRunning} onClick={() => { void runReconcile(); }}>
            {FEES_X_RUN}
          </button>
        </div>
        {xError !== '' && <ErrorBlock message={xError} />}
        {xEngineMissing && (
          <div className="banner banner--hint">
            对账需要 DuckDB 引擎构建（两库金额在分析层）——默认构建答 501；导入/映射建立不受影响。
          </div>
        )}
        {xRunning && <Spinner />}
        {xResult !== null && xResult.rows.length === 0 && (
          <p className="muted">{FEES_X_EMPTY}</p>
        )}
        {xResult !== null && xResult.rows.length > 0 && (
          <>
            <p className="muted">
              {FEES_X_COUNTS(xResult.onlyElectricity, xResult.onlyTower, xResult.both)}
              {xResult.aliasesApplied > 0 ? ` · 已应用映射 ${xResult.aliasesApplied} 条` : ''}
            </p>
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>{FEES_X_COL_SITE}</th>
                    <th>类别</th>
                    <th>{FEES_X_COL_ELEC}</th>
                    <th>{FEES_X_COL_TOWER}</th>
                    <th>{FEES_X_COL_DELTA}</th>
                    <th>{FEES_X_COL_TRAIL}</th>
                  </tr>
                </thead>
                <tbody>
                  {xResult.rows.map((r) => {
                    const chip = classChip(r.class);
                    return (
                      <tr key={r.canonical}>
                        <td>{r.canonical}</td>
                        <td><span className={chip.cls}>{chip.label}</span></td>
                        <td>{r.elecRows > 0 ? centsToYuan(r.electricityCents) : '—'}</td>
                        <td>{r.towerRows > 0 ? centsToYuan(r.towerCents) : '—'}</td>
                        <td className={r.deltaCents === 0 ? 'ov-mom--down' : 'ov-mom--up'}>
                          {centsToYuan(r.deltaCents)}
                        </td>
                        <td className="muted muted--small">{r.redirectedFrom ?? ''}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              <p className="muted muted--small">{xResult.note}</p>
            </div>
          </>
        )}

        <h3 className="panel__title panel__title--sub">{FEES_X_ALIAS_TITLE}</h3>
        <p className="panel__desc">{FEES_X_ALIAS_DESC}</p>
        <div className="filters">
          <button className="btn btn--small" disabled={scanning} onClick={() => { void doScan(); }}>
            {FEES_X_ALIAS_SCAN}
          </button>
          <button className="btn btn--small" onClick={() => { setFormOpen(!formOpen); }}>
            {FEES_X_ALIAS_ADD}{' '}{formOpen ? '−' : '+'}
          </button>
        </div>
        {formOpen && (
          <div className="filters">
            <label className="field">
              <span>{FEES_X_ALIAS_CANONICAL}</span>
              <input className="input" value={inCanonical} onChange={(e) => { setInCanonical(e.target.value); }} />
            </label>
            <label className="field">
              <span>{FEES_X_ALIAS_ALIAS}</span>
              <input className="input" value={inAlias} onChange={(e) => { setInAlias(e.target.value); }} />
            </label>
            <label className="field">
              <span>{FEES_X_ALIAS_SOURCE}</span>
              <select className="select" value={inSource} onChange={(e) => { setInSource(e.target.value); }}>
                {ALIAS_SOURCES.map((s) => (
                  <option key={s.value} value={s.value}>{s.label}</option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{FEES_X_ALIAS_CONFIDENCE}</span>
              <select className="select" value={inConfidence} onChange={(e) => { setInConfidence(e.target.value); }}>
                <option value="manual">manual（人工）</option>
                <option value="rule">rule（规则）</option>
                <option value="exact">exact（精确）</option>
              </select>
            </label>
            <button className="btn" disabled={adding} onClick={() => { void doAdd(); }}>
              {FEES_X_ALIAS_SUBMIT}
            </button>
          </div>
        )}
        {aliasError !== '' && <ErrorBlock message={aliasError} />}
        {scanning && <Spinner />}
        {proposals !== null && (
          <>
            <p className="muted">{FEES_X_ALIAS_PROPOSALS(proposals.length)}</p>
            {proposals.length === 0 && <p className="muted">{FEES_X_ALIAS_EMPTY}</p>}
            {proposals.length > 0 && (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>{FEES_X_ALIAS_CANONICAL}</th>
                      <th>{FEES_X_ALIAS_ALIAS}</th>
                      <th>{FEES_X_ALIAS_SOURCE}</th>
                      <th>依据</th>
                      <th>{FEES_X_ALIAS_CONFIRM}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {proposals.map((p) => {
                      const key = p.canonical + '|' + p.alias;
                      return (
                        <tr key={key}>
                          <td>{p.canonical}</td>
                          <td>{p.alias}</td>
                          <td>{p.source}</td>
                          <td className="muted muted--small">{p.why}</td>
                          <td>
                            <button
                              className="btn btn--small"
                              disabled={confirmingKey === key}
                              onClick={() => { void confirmProposal(p); }}
                            >
                              {FEES_X_ALIAS_CONFIRM}
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
        {aliases !== null && aliases.length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>{FEES_X_ALIAS_CANONICAL}</th>
                  <th>{FEES_X_ALIAS_ALIAS}</th>
                  <th>{FEES_X_ALIAS_SOURCE}</th>
                  <th>{FEES_X_ALIAS_CONFIDENCE}</th>
                  <th>建立（谁·何时）</th>
                  <th>{FEES_X_ALIAS_REMOVE}</th>
                </tr>
              </thead>
              <tbody>
                {aliases.map((a) => (
                  <tr key={a.id}>
                    <td>{a.canonical}</td>
                    <td>{a.alias}</td>
                    <td>{a.source}</td>
                    <td>{a.confidence}</td>
                    <td className="muted muted--small">{a.createdBy} · {a.createdAt.slice(0, 10)}</td>
                    <td>
                      <button
                        className="btn btn--small"
                        disabled={removingId === a.id}
                        onClick={() => { void doRemove(a); }}
                      >
                        {FEES_X_ALIAS_REMOVE}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}
