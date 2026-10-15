/**
 * 告警中心页 /alerts（v0.9-R1）：
 * - 筛选：状态（触发中/已确认/已清除）/ 级别（错误/警告/信息）；
 * - 表格：状态徽章、级别徽章、规则、资源（链接到资源抽屉）、触发时间（相对时间）、
 *   最新值 vs 阈值（含比较器中文短语）、处置（确认 / 清除按钮）；
 * - 行点击展开告警详情（规则 ID / 指标序列 / 窗口 / 首次出现 / 确认与清除审计行）；
 * - 确认：专用对话框收集「确认人」（后端必填的告警审计字段），firing → acked；
 * - 清除：危险确认对话框，firing|acked → cleared（告警保留为审计线索，无删除）；
 * - 支持 ?state= 深链（默认 firing——运营第一入口是待处置队列）。
 *
 * 生命周期语义（与后端契约一致）：
 * - ack 仅对 firing 可用（否则 409 not_firing）；
 * - clear 对 firing|acked 可用（已清除 409）；
 * - 前端按钮可见性按行状态渲染，后端仍是最终权威。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import type { AlertItem, AlertState, Paged, Severity } from '../api/types';
import { ackAlert, clearAlert, formatApiError, formatApiErrorText, listAlerts } from '../api/client';
import {
  ALERT_STATE_LABELS,
  ALR_ACTION_FAILED,
  ALR_ACKED_TOAST,
  ALR_ACK_HINT,
  ALR_ACK_LABEL,
  ALR_ACK_PLACEHOLDER,
  ALR_ACK_TITLE,
  ALR_ACK_WORKING,
  ALR_BTN_ACK,
  ALR_BTN_CLEAR,
  ALR_BTN_CLEAR_WORKING,
  ALR_BTN_EXPORT,
  ALR_CLEAR_MESSAGE,
  ALR_CLEAR_TITLE,
  ALR_CLEARED_TOAST,
  ALR_COL_ACTIONS,
  ALR_COL_FIRED,
  ALR_COL_RESOURCE,
  ALR_COL_RULE,
  ALR_COL_SEVERITY,
  ALR_COL_STATE,
  ALR_COL_VALUE,
  ALR_EXPORT_EMPTY,
  ALR_EXPORT_FAILED,
  ALR_EXPORT_FILENAME,
  ALR_EXPORT_TOAST,
  ALR_EXPORT_WORKING,
  ALERT_COMPARATOR_LABELS,
  ALR_DETAIL_ACK_LINE,
  ALR_DETAIL_CLEARED_LINE,
  ALR_DETAIL_FIRST_SEEN,
  ALR_DETAIL_LAST_FIRED,
  ALR_DETAIL_METRIC,
  ALR_DETAIL_RULE_ID,
  ALR_DETAIL_SERIES,
  ALR_DETAIL_TITLE,
  ALR_DETAIL_WINDOW,
  ALR_EMPTY,
  ALR_FILTER_SEVERITY,
  ALR_FILTER_STATE,
  ALR_HINT,
  ALR_RESOURCE_LINK_TITLE,
  ALR_VALUE_TEXT,
  BTN_CANCEL,
  BTN_CONFIRM,
  BTN_PROCESSING,
  FILTER_ALL,
  NAV_ALERTS,
  RUN_BTN_LABEL,
  SEVERITY_LABELS,
  format,
  formatDateTime,
  formatRelativeTime,
} from '../i18n';
import { AlertStateChip } from '../components/AlertStateChip';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ErrorBlock } from '../components/ErrorBlock';
import { Pagination } from '../components/Pagination';
import { RunDetectionDialog } from '../components/RunDetectionDialog';
import { SeverityBadge } from '../components/SeverityBadge';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';
import { useToast } from '../components/Toast';
import { useModalA11y } from '../hooks/useModalA11y';

const PAGE_SIZE = 20;

const STATE_OPTIONS: AlertState[] = ['firing', 'acked', 'cleared'];
const SEVERITY_OPTIONS: Severity[] = ['error', 'warning', 'info'];

const CSV_COLUMNS = [
  'alertId',
  'state',
  'severity',
  'ruleId',
  'ruleLabel',
  'metricKey',
  'comparator',
  'threshold',
  'window',
  'lastValue',
  'seriesId',
  'resourceId',
  'resourceType',
  'resourceNaturalKey',
  'resourceName',
  'firedAt',
  'firstSeen',
  'ackedAt',
  'ackedBy',
  'clearedAt',
] as const;

/** CSV 字段转义：含逗号/引号/换行时包引号并双写内部引号。 */
function csvCell(value: string): string {
  if (/[",\r\n]/.test(value)) {
    return `"${value.replace(/"/g, '""')}"`;
  }
  return value;
}

/** 告警行 → CSV 行（列序 = CSV_COLUMNS）。 */
function alertCsvRow(alert: AlertItem): string {
  const cells: string[] = [
    alert.alertId,
    alert.state,
    alert.severity,
    alert.ruleId,
    alert.ruleLabel,
    alert.metricKey,
    alert.comparator,
    String(alert.threshold),
    String(alert.window),
    String(alert.lastValue),
    alert.seriesId,
    alert.resource.id,
    alert.resource.type,
    alert.resource.naturalKey,
    alert.resource.name,
    alert.firedAt,
    alert.firstSeen,
    alert.ackedAt ?? '',
    alert.ackedBy ?? '',
    alert.clearedAt ?? '',
  ];
  return cells.map(csvCell).join(',');
}

/** 数字显示：整数无小数位，浮点保留最多 4 位有效（阈值/最新值通用）。 */
function fmtNum(value: number): string {
  if (Number.isInteger(value)) return String(value);
  return String(Number(value.toPrecision(4)));
}

/**
 * 确认告警对话框：收集必填「确认人」。
 * 独立于通用 ConfirmDialog（那个没有输入框）；可达性对齐 useModalA11y。
 */
function AckDialog({
  alert,
  busy,
  onConfirm,
  onCancel,
}: {
  alert: AlertItem | null;
  busy: boolean;
  onConfirm: (ackedBy: string) => void;
  onCancel: () => void;
}) {
  const modalRef = useModalA11y(alert !== null);
  const [ackedBy, setAckedBy] = useState('');

  // 每次打开重置输入（上一单的署名不串台）
  useEffect(() => {
    if (alert !== null) setAckedBy('');
  }, [alert]);

  if (alert === null) return null;

  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={() => (busy ? undefined : onCancel())} />
      <div
        ref={modalRef}
        className="modal"
        role="alertdialog"
        aria-modal="true"
        aria-label={ALR_ACK_TITLE}
        tabIndex={-1}
      >
        <h3 className="modal-title">{ALR_ACK_TITLE}</h3>
        <div className="modal-message">
          {format(ALR_VALUE_TEXT, {
            value: fmtNum(alert.lastValue),
            comparator: ALERT_COMPARATOR_LABELS[alert.comparator],
            threshold: fmtNum(alert.threshold),
          })}
          <span className="muted-note"> · {alert.ruleLabel}</span>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="ack-acked-by">
            {ALR_ACK_LABEL}
          </label>
          <input
            id="ack-acked-by"
            className="input"
            value={ackedBy}
            placeholder={ALR_ACK_PLACEHOLDER}
            disabled={busy}
            autoFocus
            onChange={(event) => setAckedBy(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && ackedBy.trim() !== '' && !busy) {
                onConfirm(ackedBy.trim());
              }
            }}
          />
          <div className="muted-note">{ALR_ACK_HINT}</div>
        </div>
        <div className="modal-actions">
          <button type="button" className="btn" disabled={busy} onClick={onCancel}>
            {BTN_CANCEL}
          </button>
          <button
            type="button"
            className="btn btn--primary"
            disabled={busy || ackedBy.trim() === ''}
            onClick={() => onConfirm(ackedBy.trim())}
          >
            {busy ? ALR_ACK_WORKING : BTN_CONFIRM}
          </button>
        </div>
      </div>
    </div>
  );
}

export function AlertsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const urlState = searchParams.get('state') ?? '';

  const [state, setState] = useState<AlertState | ''>(
    urlState === '' ? 'firing' : (urlState as AlertState),
  );
  const [severity, setSeverity] = useState<Severity | ''>('');
  const [page, setPage] = useState(1);

  const [data, setData] = useState<Paged<AlertItem> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);

  const requestSeqRef = useRef(0);

  // ack / clear 生命周期
  const [ackTarget, setAckTarget] = useState<AlertItem | null>(null);
  const [ackBusy, setAckBusy] = useState(false);
  const [clearTarget, setClearTarget] = useState<AlertItem | null>(null);
  const [clearBusy, setClearBusy] = useState(false);

  // 立即检测（v0.10-R7）+ CSV 导出
  const [runOpen, setRunOpen] = useState(false);
  const [exporting, setExporting] = useState(false);

  const toast = useToast();

  // 深链同步：?state= 变化时刷新筛选（非法值按默认 firing 处理）
  useEffect(() => {
    if (urlState === '' || STATE_OPTIONS.includes(urlState as AlertState)) {
      setState(urlState === '' ? 'firing' : (urlState as AlertState));
      setPage(1);
    }
  }, [urlState]);

  const fetchAlerts = useCallback((): void => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    listAlerts({
      state: state === '' ? undefined : state,
      severity: severity === '' ? undefined : severity,
      page,
      pageSize: PAGE_SIZE,
    })
      .then((paged) => {
        if (requestSeqRef.current !== seq) return;
        setData(paged);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (requestSeqRef.current !== seq) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [state, severity, page]);

  useEffect(() => {
    fetchAlerts();
  }, [fetchAlerts]);

  function changeState(value: string): void {
    setState(value === '' ? '' : (value as AlertState));
    setPage(1);
    const next = new URLSearchParams();
    if (value !== '') next.set('state', value);
    setSearchParams(next, { replace: true });
  }

  function failToast(err: unknown): void {
    const display = formatApiErrorText(err);
    toast.show(format(ALR_ACTION_FAILED, { message: display }), 'error');
  }

  /** 导出当前筛选的全量告警（分页拉全后拼 CSV，含 BOM 便于 Excel 识别 UTF-8）。 */
  function exportCsv(): void {
    if (exporting) return;
    if (data !== null && data.total === 0) {
      toast.show(ALR_EXPORT_EMPTY, 'info');
      return;
    }
    setExporting(true);
    const filter = {
      state: state === '' ? undefined : (state as AlertState),
      severity: severity === '' ? undefined : (severity as Severity),
    };
    const collected: AlertItem[] = [];
    let pageNow = 1;
    const fetchPage = (): void => {
      listAlerts({ ...filter, page: pageNow, pageSize: 200 })
        .then((paged) => {
          collected.push(...paged.items);
          if (collected.length < paged.total && pageNow < 100) {
            pageNow += 1;
            fetchPage();
            return;
          }
          setExporting(false);
          if (collected.length === 0) {
            toast.show(ALR_EXPORT_EMPTY, 'info');
            return;
          }
          const lines = [CSV_COLUMNS.join(','), ...collected.map(alertCsvRow)];
          const blob = new Blob([`\ufeff${lines.join('\r\n')}\r\n`], {
            type: 'text/csv;charset=utf-8',
          });
          const url = URL.createObjectURL(blob);
          const a = document.createElement('a');
          a.href = url;
          a.download = `${ALR_EXPORT_FILENAME}-${new Date().toISOString().slice(0, 10)}.csv`;
          document.body.appendChild(a);
          a.click();
          a.remove();
          // 延迟撤销：立即 revoke 与浏览器抓取下载句柄存在竞态（大文件/慢设备上可能中断下载）
          window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
          toast.show(format(ALR_EXPORT_TOAST, { n: collected.length }), 'success');
        })
        .catch((err: unknown) => {
          setExporting(false);
          toast.show(format(ALR_EXPORT_FAILED, { message: formatApiErrorText(err) }), 'error');
        });
    };
    fetchPage();
  }

  function confirmAck(ackedBy: string): void {
    if (ackTarget === null) return;
    setAckBusy(true);
    ackAlert(ackTarget.alertId, ackedBy)
      .then(() => {
        setAckBusy(false);
        setAckTarget(null);
        toast.show(format(ALR_ACKED_TOAST, { who: ackedBy }), 'success');
        fetchAlerts();
      })
      .catch((err: unknown) => {
        setAckBusy(false);
        failToast(err);
      });
  }

  function confirmClear(): void {
    if (clearTarget === null) return;
    setClearBusy(true);
    clearAlert(clearTarget.alertId)
      .then(() => {
        setClearBusy(false);
        setClearTarget(null);
        toast.show(ALR_CLEARED_TOAST, 'success');
        fetchAlerts();
      })
      .catch((err: unknown) => {
        setClearBusy(false);
        failToast(err);
      });
  }

  const columns = useMemo<TableColumn<AlertItem>[]>(
    () => [
      { key: 'state', header: ALR_COL_STATE, render: (row) => <AlertStateChip state={row.state} /> },
      {
        key: 'severity',
        header: ALR_COL_SEVERITY,
        render: (row) => <SeverityBadge severity={row.severity} />,
      },
      { key: 'rule', header: ALR_COL_RULE, render: (row) => row.ruleLabel },
      {
        key: 'resource',
        header: ALR_COL_RESOURCE,
        render: (row) =>
          row.resource.id !== '' ? (
            <Link
              className="entity-link"
              to={`/resources?focus=${encodeURIComponent(row.resource.id)}`}
              title={ALR_RESOURCE_LINK_TITLE}
              onClick={(event) => event.stopPropagation()}
            >
              {row.resource.name !== '' ? row.resource.name : row.resource.naturalKey}
            </Link>
          ) : (
            <span className="muted-note">{row.resource.naturalKey}</span>
          ),
      },
      {
        key: 'firedAt',
        header: ALR_COL_FIRED,
        render: (row) => (
          <span title={formatDateTime(row.firedAt)}>{formatRelativeTime(row.firedAt)}</span>
        ),
      },
      {
        key: 'value',
        header: ALR_COL_VALUE,
        render: (row) => (
          <span className="mono">
            {format(ALR_VALUE_TEXT, {
              value: fmtNum(row.lastValue),
              comparator: ALERT_COMPARATOR_LABELS[row.comparator],
              threshold: fmtNum(row.threshold),
            })}
          </span>
        ),
      },
      {
        key: 'actions',
        header: ALR_COL_ACTIONS,
        render: (row) => (
          <span className="alert-actions">
            {row.state === 'firing' && (
              <button
                type="button"
                className="btn btn--sm"
                disabled={ackBusy || clearBusy}
                onClick={(event) => {
                  event.stopPropagation();
                  setAckTarget(row);
                }}
              >
                {ALR_BTN_ACK}
              </button>
            )}
            {(row.state === 'firing' || row.state === 'acked') && (
              <button
                type="button"
                className="btn btn--sm btn--danger-ghost"
                disabled={ackBusy || clearBusy}
                onClick={(event) => {
                  event.stopPropagation();
                  setClearTarget(row);
                }}
              >
                {clearBusy && clearTarget !== null && clearTarget.alertId === row.alertId
                  ? ALR_BTN_CLEAR_WORKING
                  : ALR_BTN_CLEAR}
              </button>
            )}
            {row.state === 'cleared' && <span className="muted-note">—</span>}
          </span>
        ),
      },
    ],
    [ackBusy, clearBusy, clearTarget],
  );

  return (
    <div className="card">
      <div className="filter-bar">
        <label className="filter-field">
          <span className="filter-label">{ALR_FILTER_STATE}</span>
          <select
            className="select"
            value={state}
            onChange={(event) => changeState(event.target.value)}
          >
            <option value="">{FILTER_ALL}</option>
            {STATE_OPTIONS.map((value) => (
              <option key={value} value={value}>
                {ALERT_STATE_LABELS[value]}
              </option>
            ))}
          </select>
        </label>
        <label className="filter-field">
          <span className="filter-label">{ALR_FILTER_SEVERITY}</span>
          <select
            className="select"
            value={severity}
            onChange={(event) => {
              setSeverity(event.target.value === '' ? '' : (event.target.value as Severity));
              setPage(1);
            }}
          >
            <option value="">{FILTER_ALL}</option>
            {SEVERITY_OPTIONS.map((value) => (
              <option key={value} value={value}>
                {SEVERITY_LABELS[value]}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="btn btn--secondary filter-push"
          disabled={exporting}
          onClick={exportCsv}
          title={ALR_BTN_EXPORT}
        >
          {exporting ? ALR_EXPORT_WORKING : ALR_BTN_EXPORT}
        </button>
        <button
          type="button"
          className="btn btn--primary"
          onClick={() => setRunOpen(true)}
        >
          {RUN_BTN_LABEL}
        </button>
      </div>

      <div className="muted-note table-hint">{ALR_HINT}</div>

      {error !== null ? (
        <ErrorBlock message={error} detail={errorDetail} onRetry={fetchAlerts} />
      ) : (
        <>
          <Table
            columns={columns}
            rows={data?.items ?? []}
            rowKey={(row) => row.alertId}
            loading={loading}
            emptyText={ALR_EMPTY}
            renderExpanded={(row) => (
              <div className="evidence">
                <div className="evidence-title">{ALR_DETAIL_TITLE}</div>
                <table className="kv-table">
                  <tbody>
                    <tr>
                      <th scope="row">{ALR_DETAIL_RULE_ID}</th>
                      <td>
                        <span className="mono">{row.ruleId}</span>
                      </td>
                    </tr>
                    <tr>
                      <th scope="row">{ALR_DETAIL_METRIC}</th>
                      <td>
                        <span className="mono">{row.metricKey}</span>
                      </td>
                    </tr>
                    <tr>
                      <th scope="row">{ALR_DETAIL_SERIES}</th>
                      <td>
                        <span className="mono">{row.seriesId}</span>
                      </td>
                    </tr>
                    <tr>
                      <th scope="row">{ALR_DETAIL_WINDOW}</th>
                      <td>{row.window}</td>
                    </tr>
                    <tr>
                      <th scope="row">{ALR_DETAIL_FIRST_SEEN}</th>
                      <td>{formatDateTime(row.firstSeen)}</td>
                    </tr>
                    <tr>
                      <th scope="row">{ALR_DETAIL_LAST_FIRED}</th>
                      <td>{formatDateTime(row.firedAt)}</td>
                    </tr>
                    {row.ackedAt != null && row.ackedAt !== '' && (
                      <tr>
                        <th scope="row">{ALERT_STATE_LABELS.acked}</th>
                        <td>
                          {format(ALR_DETAIL_ACK_LINE, {
                            who: row.ackedBy ?? '',
                            at: formatDateTime(row.ackedAt),
                          })}
                        </td>
                      </tr>
                    )}
                    {row.clearedAt != null && row.clearedAt !== '' && (
                      <tr>
                        <th scope="row">{ALERT_STATE_LABELS.cleared}</th>
                        <td>{format(ALR_DETAIL_CLEARED_LINE, { at: formatDateTime(row.clearedAt) })}</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            )}
            ariaLabel={NAV_ALERTS}
          />
          {data !== null && data.total > 0 && (
            <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />
          )}
        </>
      )}

      <AckDialog
        alert={ackTarget}
        busy={ackBusy}
        onConfirm={confirmAck}
        onCancel={() => {
          if (!ackBusy) setAckTarget(null);
        }}
      />
      <RunDetectionDialog
        open={runOpen}
        onClose={() => setRunOpen(false)}
        onFinished={() => {
          // 检测运行可能新建/清除了告警：立即刷新当前筛选
          fetchAlerts();
        }}
      />
      <ConfirmDialog
        open={clearTarget !== null}
        title={ALR_CLEAR_TITLE}
        message={ALR_CLEAR_MESSAGE}
        danger
        busy={clearBusy}
        confirmLabel={clearBusy ? BTN_PROCESSING : ALR_BTN_CLEAR}
        onConfirm={confirmClear}
        onCancel={() => {
          if (!clearBusy) setClearTarget(null);
        }}
      />
    </div>
  );
}
