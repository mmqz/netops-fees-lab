/**
 * 费用明细补录面板（#16 完成线第 6 条）：
 *
 * - 差额对账卡：Σ明细 vs 系统表该键金额——**只提示不阻断**（可能明细
 *   待补，也可能系统表待解释；明细永不覆盖系统表）；默认构建（无
 *   DuckDB 引擎）明细侧可答、系统侧诚实缺席不造零；
 * - 录入表单：三强制（关联键/金额/录入留痕由令牌层级承担）+ 全部
 *   可选字段；分类=建议集 datalist + 本地自建自由文本（#16 明示
 *   「提供分类建议，但允许本地自建分类」）；
 * - 台账列表：过滤 + 裁定（核实/驳回〔原因必填〕——管理级人类通道，
 *   #119 同款纪律）+ 状态 chip 与留痕（谁/何时/原因）。
 *
 * 教义：明细是佐证不是权威——系统表金额以此为准，差额只是路标。
 */

import { Fragment, useCallback, useEffect, useState } from 'react';
import type { FeeDetailItem, FeeDetailReconcile } from '../api/types';
import {
  addFeeDetail,
  fetchFeeDetails,
  formatApiErrorText,
  reconcileFeeDetails,
  setFeeDetailStatus,
} from '../api/client';
import { useToast } from '../components/Toast';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';
import { centsToYuan } from './fees-format';
import {
  FEE_TYPE_ELECTRICITY,
  FEE_TYPE_MAINTENANCE,
  FEE_TYPE_REPAIR,
  FEE_TYPE_TOWER_LEASE,
  FEES_DET_403_HINT,
  FEES_DET_ADD_AMOUNT,
  FEES_DET_ADD_CATEGORY,
  FEES_DET_ADD_ITEM,
  FEES_DET_ADD_OK,
  FEES_DET_ADD_REASON,
  FEES_DET_ADD_REQUIRED,
  FEES_DET_ADD_SOURCE,
  FEES_DET_ADD_SUBMIT,
  FEES_DET_ADD_TITLE,
  FEES_DET_COL_AMOUNT,
  FEES_DET_COL_CATEGORY,
  FEES_DET_COL_LINK,
  FEES_DET_COL_PERIOD,
  FEES_DET_COL_STATUS,
  FEES_DET_CONFIRM,
  FEES_DET_DESC,
  FEES_DET_EMPTY,
  FEES_DET_LIST_LINK,
  FEES_DET_RECON_DELTA,
  FEES_DET_RECON_DETAIL_SUM,
  FEES_DET_RECON_LINK,
  FEES_DET_RECON_PERIOD,
  FEES_DET_RECON_ROWS,
  FEES_DET_RECON_RUN,
  FEES_DET_RECON_SYSTEM,
  FEES_DET_RECON_TITLE,
  FEES_DET_REJECT,
  FEES_DET_REJECT_PH,
  FEES_DET_REJECT_REASON,
  FEES_DET_STATUS_BY,
  FEES_DET_STATUS_CONFIRMED,
  FEES_DET_STATUS_PENDING,
  FEES_DET_STATUS_REJECTED,
  FEES_DET_STATUS_UNMATCHED,
  FEES_DET_TITLE,
} from '../i18n';

const DET_TYPES = [
  { value: 'electricity', label: FEE_TYPE_ELECTRICITY },
  { value: 'tower_lease', label: FEE_TYPE_TOWER_LEASE },
  { value: 'repair', label: FEE_TYPE_REPAIR },
  { value: 'maintenance', label: FEE_TYPE_MAINTENANCE },
];

/** #16：建议集 + 本地自建——datalist 只给建议，不锁输入。 */
const CATEGORY_SUGGESTIONS = [
  '划转纤芯绩效', '计件工资', '加班餐补', '住宿费', '低值易耗品',
  '维修工器具', '油费', '车辆维修', '资质培训', '线路改迁', '绩效分配', '冲销',
];

function statusChip(s: string): { cls: string; label: string } {
  if (s === 'confirmed') return { cls: 'chip chip--ok', label: FEES_DET_STATUS_CONFIRMED };
  if (s === 'rejected') return { cls: 'chip chip--muted', label: FEES_DET_STATUS_REJECTED };
  if (s === 'unmatched') return { cls: 'chip chip--warn', label: FEES_DET_STATUS_UNMATCHED };
  return { cls: 'chip', label: FEES_DET_STATUS_PENDING };
}

export function FeesDetails() {
  const toast = useToast();

  // ---- 差额对账 ----
  const [recLink, setRecLink] = useState('');
  const [recPeriod, setRecPeriod] = useState('');
  const [recType, setRecType] = useState('electricity');
  const [recon, setRecon] = useState<FeeDetailReconcile | null>(null);
  const [recRunning, setRecRunning] = useState(false);
  const [recError, setRecError] = useState('');

  const runReconcile = useCallback(async () => {
    const link = recLink.trim();
    const period = recPeriod.trim();
    if (link === '' || period === '') {
      setRecError(FEES_DET_ADD_REQUIRED);
      return;
    }
    setRecRunning(true);
    setRecError('');
    try {
      setRecon(await reconcileFeeDetails({ linkKey: link, period, feeType: recType }));
    } catch (err) {
      setRecError(formatApiErrorText(err));
      setRecon(null);
    } finally {
      setRecRunning(false);
    }
  }, [recLink, recPeriod, recType]);

  // ---- 录入 ----
  const [formOpen, setFormOpen] = useState(false);
  const [inLink, setInLink] = useState('');
  const [inPeriod, setInPeriod] = useState('');
  const [inType, setInType] = useState('electricity');
  const [inCategory, setInCategory] = useState('');
  const [inAmount, setInAmount] = useState('');
  const [inItem, setInItem] = useState('');
  const [inReason, setInReason] = useState('');
  const [inSource, setInSource] = useState('');
  const [adding, setAdding] = useState(false);

  const doAdd = useCallback(async () => {
    const amount = Number(inAmount);
    if (
      inLink.trim() === '' || inPeriod.trim() === '' || inCategory.trim() === '' ||
      inAmount.trim() === '' || !Number.isFinite(amount)
    ) {
      toast.show(FEES_DET_ADD_REQUIRED, 'error');
      return;
    }
    setAdding(true);
    try {
      const d = await addFeeDetail({
        linkKey: inLink.trim(), period: inPeriod.trim(), feeType: inType,
        category: inCategory.trim(), amountYuan: amount,
        itemName: inItem.trim() || undefined, reason: inReason.trim() || undefined,
        sourceFile: inSource.trim() || undefined,
      });
      toast.show(FEES_DET_ADD_OK(d.id), 'success');
      setInCategory('');
      setInAmount('');
      setInItem('');
      setInReason('');
      await loadDetails();
      // the reconcile card refreshes when it matches the fresh triple.
      if (recon !== null && recon.linkKey === inLink.trim() &&
          recon.period === inPeriod.trim() && recon.feeType === inType) {
        setRecon(await reconcileFeeDetails({ linkKey: inLink.trim(), period: inPeriod.trim(), feeType: inType }));
      }
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setAdding(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inLink, inPeriod, inType, inCategory, inAmount, inItem, inReason, inSource, recon, toast]);

  // ---- 台账 ----
  const [details, setDetails] = useState<FeeDetailItem[] | null>(null);
  const [listLink, setListLink] = useState('');
  const [listStatus, setListStatus] = useState('');
  const [listError, setListError] = useState('');
  const [rejectTarget, setRejectTarget] = useState<FeeDetailItem | null>(null);
  const [rejectReason, setRejectReason] = useState('');
  const [decidingId, setDecidingId] = useState<string | null>(null);

  const loadDetails = useCallback(async () => {
    setListError('');
    try {
      const res = await fetchFeeDetails({
        linkKey: listLink.trim() || undefined,
        status: listStatus || undefined,
      });
      setDetails(res.items);
    } catch (err) {
      setListError(formatApiErrorText(err));
      setDetails(null);
    }
  }, [listLink, listStatus]);

  useEffect(() => {
    void loadDetails();
  }, [loadDetails]);

  const decide = useCallback(async (d: FeeDetailItem, status: string) => {
    setDecidingId(d.id);
    setListError('');
    try {
      await setFeeDetailStatus(d.id, status);
      toast.show(`${d.linkKey} · ${status === 'confirmed' ? FEES_DET_STATUS_CONFIRMED : status === 'rejected' ? FEES_DET_STATUS_REJECTED : FEES_DET_STATUS_UNMATCHED}`, 'success');
      await loadDetails();
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'forbidden' || code === 'forbidden_admin') {
        toast.show(FEES_DET_403_HINT, 'error');
      } else {
        setListError(formatApiErrorText(err));
      }
    } finally {
      setDecidingId(null);
    }
  }, [loadDetails, toast]);

  const doReject = useCallback(async () => {
    if (rejectTarget === null) return;
    if (rejectReason.trim() === '') {
      toast.show(FEES_DET_REJECT_REASON, 'error');
      return;
    }
    setDecidingId(rejectTarget.id);
    try {
      await setFeeDetailStatus(rejectTarget.id, 'rejected', rejectReason.trim());
      toast.show(`${rejectTarget.linkKey} · ${FEES_DET_STATUS_REJECTED}`, 'success');
      setRejectTarget(null);
      setRejectReason('');
      await loadDetails();
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'forbidden' || code === 'forbidden_admin') {
        toast.show(FEES_DET_403_HINT, 'error');
      } else {
        setListError(formatApiErrorText(err));
      }
    } finally {
      setDecidingId(null);
    }
  }, [rejectTarget, rejectReason, loadDetails, toast]);

  const deltaCls =
    recon?.deltaCents == null ? '' : recon.deltaCents === 0 ? 'ov-mom--down' : 'ov-mom--up';

  return (
    <section className="panel">
      <h2 className="panel__title">{FEES_DET_TITLE}</h2>
      <div className="panel__body">
        <p className="panel__desc">{FEES_DET_DESC}</p>

        <h3 className="panel__title panel__title--sub">{FEES_DET_RECON_TITLE}</h3>
        <div className="filters">
          <label className="field">
            <span>{FEES_DET_RECON_LINK}</span>
            <input className="input" value={recLink} onChange={(e) => { setRecLink(e.target.value); }} />
          </label>
          <label className="field field--short">
            <span>{FEES_DET_RECON_PERIOD}</span>
            <input className="input" value={recPeriod} onChange={(e) => { setRecPeriod(e.target.value); }} inputMode="numeric" />
          </label>
          <label className="field">
            <span>费用类型</span>
            <select className="select" value={recType} onChange={(e) => { setRecType(e.target.value); }}>
              {DET_TYPES.map((t) => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
          </label>
          <button className="btn" disabled={recRunning} onClick={() => { void runReconcile(); }}>
            {FEES_DET_RECON_RUN}
          </button>
        </div>
        {recError !== '' && <ErrorBlock message={recError} />}
        {recRunning && <Spinner />}
        {recon !== null && (
          <div className="ov-cards">
            <div className="ov-card">
              <p className="ov-card__title">{FEES_DET_RECON_DETAIL_SUM} · {FEES_DET_RECON_ROWS(recon.rows)}</p>
              <p className="ov-card__value">{centsToYuan(recon.detailSumCents)}</p>
              <p className="ov-card__sub muted">元</p>
            </div>
            <div className="ov-card">
              <p className="ov-card__title">{FEES_DET_RECON_SYSTEM}</p>
              <p className="ov-card__value">
                {recon.systemCents == null ? '—' : centsToYuan(recon.systemCents)}
              </p>
              <p className="ov-card__sub muted">
                {recon.engineCentsAvailable ? '' : '（本构建无分析引擎——系统侧诚实缺席，不造零）'}
              </p>
            </div>
            <div className="ov-card">
              <p className="ov-card__title">{FEES_DET_RECON_DELTA}</p>
              <p className={`ov-card__value ${deltaCls}`}>{recon.deltaCents == null ? '—' : centsToYuan(recon.deltaCents)}</p>
              <p className="ov-card__sub muted">{recon.note}</p>
            </div>
          </div>
        )}

        <h3 className="panel__title panel__title--sub">
          {FEES_DET_ADD_TITLE}
          {' '}
          <button className="btn btn--small" onClick={() => { setFormOpen(!formOpen); }}>
            {formOpen ? '−' : '+'}
          </button>
        </h3>
        {formOpen && (
          <div className="filters">
            <label className="field">
              <span>{FEES_DET_COL_LINK}</span>
              <input className="input" value={inLink} onChange={(e) => { setInLink(e.target.value); }} />
            </label>
            <label className="field field--short">
              <span>{FEES_DET_RECON_PERIOD}</span>
              <input className="input" value={inPeriod} onChange={(e) => { setInPeriod(e.target.value); }} inputMode="numeric" />
            </label>
            <label className="field">
              <span>费用类型</span>
              <select className="select" value={inType} onChange={(e) => { setInType(e.target.value); }}>
                {DET_TYPES.map((t) => (
                  <option key={t.value} value={t.value}>{t.label}</option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{FEES_DET_ADD_CATEGORY}</span>
              <input className="input"
                value={inCategory}
                onChange={(e) => { setInCategory(e.target.value); }}
                list="fees-det-category-suggestions"
              />
              <datalist id="fees-det-category-suggestions">
                {CATEGORY_SUGGESTIONS.map((c) => (
                  <option key={c} value={c} />
                ))}
              </datalist>
            </label>
            <label className="field field--short">
              <span>{FEES_DET_ADD_AMOUNT}</span>
              <input className="input" value={inAmount} onChange={(e) => { setInAmount(e.target.value); }} inputMode="decimal" />
            </label>
            <label className="field">
              <span>{FEES_DET_ADD_ITEM}</span>
              <input className="input" value={inItem} onChange={(e) => { setInItem(e.target.value); }} />
            </label>
            <label className="field">
              <span>{FEES_DET_ADD_REASON}</span>
              <input className="input" value={inReason} onChange={(e) => { setInReason(e.target.value); }} />
            </label>
            <label className="field">
              <span>{FEES_DET_ADD_SOURCE}</span>
              <input className="input" value={inSource} onChange={(e) => { setInSource(e.target.value); }} />
            </label>
            <button className="btn" disabled={adding} onClick={() => { void doAdd(); }}>
              {FEES_DET_ADD_SUBMIT}
            </button>
          </div>
        )}

        <div className="filters">
          <label className="field">
            <span>{FEES_DET_LIST_LINK}</span>
            <input className="input"
              value={listLink}
              onChange={(e) => { setListLink(e.target.value); }}
              placeholder="HLJ-0001 / W8522883"
            />
          </label>
          <label className="field">
            <span>{FEES_DET_COL_STATUS}</span>
            <select className="select" value={listStatus} onChange={(e) => { setListStatus(e.target.value); }}>
              <option value="">全部</option>
              <option value="pending">{FEES_DET_STATUS_PENDING}</option>
              <option value="confirmed">{FEES_DET_STATUS_CONFIRMED}</option>
              <option value="rejected">{FEES_DET_STATUS_REJECTED}</option>
              <option value="unmatched">{FEES_DET_STATUS_UNMATCHED}</option>
            </select>
          </label>
        </div>
        {listError !== '' && <ErrorBlock message={listError} />}
        {details !== null && details.length === 0 && (
          <p className="muted">{FEES_DET_EMPTY}</p>
        )}
        {details !== null && details.length > 0 && (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>{FEES_DET_COL_LINK}</th>
                  <th>{FEES_DET_COL_PERIOD}</th>
                  <th>{FEES_DET_COL_CATEGORY}</th>
                  <th>{FEES_DET_ADD_ITEM}</th>
                  <th>{FEES_DET_COL_AMOUNT}</th>
                  <th>{FEES_DET_COL_STATUS}</th>
                </tr>
              </thead>
              <tbody>
                {details.map((d) => {
                  const chip = statusChip(d.status);
                  return (
                    <Fragment key={d.id}>
                      <tr>
                        <td>{d.linkKey}</td>
                        <td>{d.period}</td>
                        <td>{d.category}</td>
                        <td className="muted">{d.itemName ?? ''}</td>
                        <td>{centsToYuan(d.amountCents)}</td>
                        <td>
                          <span className={chip.cls}>{chip.label}</span>
                          {d.verifiedAt != null && d.verifiedAt !== '' && (
                            <span className="muted muted--small">
                              {' '}
                              {FEES_DET_STATUS_BY(d.verifiedBy ?? '', d.verifiedAt.slice(0, 10))}
                              {d.rejectReason != null && d.rejectReason !== '' ? ` · ${d.rejectReason}` : ''}
                            </span>
                          )}
                          {' '}
                          <button
                            className="btn btn--small"
                            disabled={decidingId === d.id || d.status === 'confirmed'}
                            onClick={() => { void decide(d, 'confirmed'); }}
                          >
                            {FEES_DET_CONFIRM}
                          </button>{' '}
                          {rejectTarget !== null && rejectTarget.id === d.id ? (
                            <span className="det-reject-inline">
                              <input className="input"
                                value={rejectReason}
                                onChange={(e) => { setRejectReason(e.target.value); }}
                                placeholder={FEES_DET_REJECT_PH}
                                aria-label={FEES_DET_REJECT_REASON}
                                onKeyDown={(e) => {
                                  if (e.key === 'Enter') { void doReject(); }
                                  if (e.key === 'Escape') { setRejectTarget(null); }
                                }}
                              />
                              <button
                                className="btn btn--small"
                                disabled={decidingId !== null}
                                onClick={() => { void doReject(); }}
                              >
                                {FEES_DET_REJECT}
                              </button>
                              <button
                                className="btn btn--small"
                                disabled={decidingId !== null}
                                onClick={() => { setRejectTarget(null); }}
                              >
                                取消
                              </button>
                            </span>
                          ) : (
                            <button
                              className="btn btn--small"
                              disabled={decidingId === d.id || d.status === 'rejected'}
                              onClick={() => { setRejectTarget(d); setRejectReason(''); }}
                            >
                              {FEES_DET_REJECT}
                            </button>
                          )}
                        </td>
                      </tr>
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>


    </section>
  );
}
