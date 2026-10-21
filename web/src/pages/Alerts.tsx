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
import type {
  AlertCorrelationRole,
  AlertItem,
  AlertState,
  DetectionRunItem,
  Paged,
  SchedulerStatus,
  Severity,
} from '../api/types';
import {
  ackAlert,
  clearAlert,
  formatApiError,
  formatApiErrorText,
  getHealth,
  listAlerts,
  listDetectionRuns,
} from '../api/client';
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
  ALR_COL_CORRELATION,
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
  ALR_CORR_CAUSE_LINE,
  ALR_CORR_DETAIL_LABEL,
  ALR_CORR_NONE_LINE,
  ALR_CORR_PRIMARY,
  ALR_CORR_PRIMARY_HINT,
  ALR_CORR_PRIMARY_LINE,
  ALR_CORR_SECONDARY,
  ALR_CORR_SECONDARY_HINT,
  ALR_DETAIL_ACK_LINE,
  ALR_DETAIL_CLEARED_LINE,
  ALR_DETAIL_LIFECYCLE,
  ALR_DETAIL_FIRST_SEEN,
  ALR_DETAIL_LAST_FIRED,
  ALR_DETAIL_METRIC,
  ALR_DETAIL_RULE_ID,
  ALR_DETAIL_SERIES,
  ALR_DETAIL_TITLE,
  ALR_DETAIL_WINDOW,
  ALR_EMPTY,
  ALR_FILTER_ROLE,
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
  PATROL_BTN_HISTORY,
  PATROL_COL_COUNTS,
  PATROL_COL_DATA_TS,
  PATROL_COL_DURATION,
  PATROL_COL_SOURCE,
  PATROL_COL_STARTED,
  PATROL_COL_STATUS,
  PATROL_COL_WEBHOOK,
  PATROL_COL_WINDOW,
  WEBHOOK_DELIVERED_LABEL,
  WEBHOOK_FAILED_LABEL,
  LIFECYCLE_DELIVERED_TITLE,
  LIFECYCLE_EVENT_ACK_LABEL,
  LIFECYCLE_EVENT_CLEAR_LABEL,
  LIFECYCLE_FAILED_TITLE,
  LIFECYCLE_NONE_TITLE,
  WEBHOOK_FAILED_ATTEMPTS_TITLE,
  WEBHOOK_OFF_LABEL,
  PATROL_DURATION_TEXT,
  PATROL_INTERVAL_HOURS_TEXT,
  PATROL_INTERVAL_MINUTES_TEXT,
  PATROL_INTERVAL_SECONDS_TEXT,
  PATROL_LAST_FAILED,
  PATROL_LAST_LABEL,
  PATROL_LAST_LINE,
  PATROL_LAST_NONE,
  PATROL_NEXT_LABEL,
  PATROL_NEXT_LINE,
  PATROL_OFF,
  PATROL_RUNS_EMPTY,
  PATROL_RUNS_HINT,
  PATROL_RUNS_LOAD_FAILED,
  PATROL_RUNS_TITLE,
  PATROL_STATUS_FAILED,
  PATROL_STATUS_OK,
  PATROL_WINDOW_ALL,
  PATROL_WINDOW_RANGE,
  RUN_BTN_LABEL,
  RUN_SOURCE_LABELS,
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
  'correlationRole',
  'correlatedAlertId',
  'correlatedResourceId',
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
    alert.correlationRole,
    alert.correlatedAlertId,
    alert.correlatedResourceId,
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

/** 巡检调度间隔的人话表达（health.intervalSeconds → 「x 小时 / x 分钟 / x 秒」）。
 *  文案从 i18n 常量填充（#111-D：原先常量写好但此处硬编码，改措辞时两处漂移）。 */
function patrolIntervalText(seconds: number): string {
  if (seconds >= 3600 && seconds % 3600 === 0) {
    return format(PATROL_INTERVAL_HOURS_TEXT, { n: seconds / 3600 });
  }
  if (seconds >= 60 && seconds % 60 === 0) {
    return format(PATROL_INTERVAL_MINUTES_TEXT, { n: seconds / 60 });
  }
  return format(PATROL_INTERVAL_SECONDS_TEXT, { n: seconds });
}

/** 评估窗口列文案：空窗口=全库，否则「from ~ to」（缩到日期）。 */
function patrolWindowText(run: DetectionRunItem): string {
  if (run.from === '' && run.to === '') return PATROL_WINDOW_ALL;
  const from = run.from === '' ? '…' : formatDateTime(run.from);
  const to = run.to === '' ? '…' : formatDateTime(run.to);
  return format(PATROL_WINDOW_RANGE, { from, to });
}

/**
 * 检测运行历史对话框（v0.11-R11）：每次扫描的落库回执列表。
 * 来源徽章 / 开始时间 / 耗时 / 三计数 / 成功失败 / 窗口 / 数据截止；
 * 分页拉取（PATROL 页大小 20），关闭即卸载（父组件条件渲染）。
 */
function RunHistoryDialog({ onClose }: { onClose: () => void }) {
  const modalRef = useModalA11y(true);
  const [data, setData] = useState<Paged<DetectionRunItem> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const seqRef = useRef(0);

  const fetchRuns = useCallback((): void => {
    const seq = ++seqRef.current;
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    listDetectionRuns({ page, pageSize: 20 })
      .then((paged) => {
        if (seqRef.current !== seq) return;
        setData(paged);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (seqRef.current !== seq) return;
        const display = formatApiError(err);
        setError(PATROL_RUNS_LOAD_FAILED);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [page]);

  useEffect(() => {
    fetchRuns();
  }, [fetchRuns]);

  const columns: TableColumn<DetectionRunItem>[] = useMemo(
    () => [
      {
        key: 'source',
        header: PATROL_COL_SOURCE,
        render: (run) => <span className="chip chip--muted">{RUN_SOURCE_LABELS[run.source]}</span>,
      },
      {
        key: 'startedAt',
        header: PATROL_COL_STARTED,
        render: (run) => (
          <span title={formatDateTime(run.startedAt)}>{formatRelativeTime(run.startedAt)}</span>
        ),
      },
      {
        key: 'duration',
        header: PATROL_COL_DURATION,
        render: (run) => <span className="mono">{format(PATROL_DURATION_TEXT, { n: run.durationMs })}</span>,
      },
      {
        key: 'counts',
        header: PATROL_COL_COUNTS,
        render: (run) => (
          <span className="mono">
            {run.findings} / {run.alertsCreated} / {run.alertsCleared} / {run.suppressed} / {run.flapHeld}
          </span>
        ),
      },
      {
        key: 'status',
        header: PATROL_COL_STATUS,
        render: (run) =>
          run.error === '' ? (
            <span className="patrol-status patrol-status--ok">{PATROL_STATUS_OK}</span>
          ) : (
            <span className="patrol-status patrol-status--failed" title={run.error}>
              {PATROL_STATUS_FAILED}
            </span>
          ),
      },
      {
        key: 'webhook',
        header: PATROL_COL_WEBHOOK,
        render: (run) =>
          run.webhookStatus === 'delivered' ? (
            <span
              className="patrol-status patrol-status--ok"
              title={WEBHOOK_DELIVERED_LABEL}
            >
              {WEBHOOK_DELIVERED_LABEL}
            </span>
          ) : run.webhookStatus === 'failed' ? (
            <span
              className="patrol-status patrol-status--failed"
              title={format(WEBHOOK_FAILED_ATTEMPTS_TITLE, {
                n: run.webhookAttempts,
                error: run.webhookError,
              })}
            >
              {WEBHOOK_FAILED_LABEL}
            </span>
          ) : (
            <span className="muted-note">{WEBHOOK_OFF_LABEL}</span>
          ),
      },
      {
        key: 'window',
        header: PATROL_COL_WINDOW,
        render: (run) => <span className="muted-note">{patrolWindowText(run)}</span>,
      },
      {
        key: 'evaluatedAt',
        header: PATROL_COL_DATA_TS,
        render: (run) => (
          <span className="muted-note">{run.evaluatedAt === '' ? '—' : formatDateTime(run.evaluatedAt)}</span>
        ),
      },
    ],
    [],
  );

  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={onClose} />
      <div
        ref={modalRef}
        className="modal modal--wide"
        role="dialog"
        aria-modal="true"
        aria-label={PATROL_RUNS_TITLE}
        tabIndex={-1}
      >
        <h3 className="modal-title">{PATROL_RUNS_TITLE}</h3>
        <div className="muted-note" style={{ marginBottom: 8 }}>
          {PATROL_RUNS_HINT}
        </div>
        {error !== null ? (
          <ErrorBlock message={error} detail={errorDetail} onRetry={fetchRuns} />
        ) : (
          <>
            <Table
              columns={columns}
              rows={data?.items ?? []}
              rowKey={(run) => run.runId}
              loading={loading}
              emptyText={PATROL_RUNS_EMPTY}
              ariaLabel={PATROL_RUNS_TITLE}
            />
            {data !== null && data.total > 20 && (
              <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />
            )}
          </>
        )}
        <div className="modal-actions">
          <button type="button" className="btn" onClick={onClose}>
            {BTN_CANCEL}
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
  const [role, setRole] = useState<AlertCorrelationRole | ''>('');
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

  // 巡检状态条（v0.11-R11）：最近一次回执 + 调度器状态
  const [lastRun, setLastRun] = useState<DetectionRunItem | null>(null);
  const [scheduler, setScheduler] = useState<SchedulerStatus | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const patrolSeqRef = useRef(0);

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
      role: role === '' ? undefined : role,
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
  }, [state, severity, role, page]);

  useEffect(() => {
    fetchAlerts();
  }, [fetchAlerts]);

  // 巡检条：拉最近一次回执（pageSize=1）+ health 的调度器块。
  // 失败静默降级（条本身是辅助信息，不与主表争错误面）；
  // 检测运行完成后会主动重拉（onFinished 钩子）。
  const fetchPatrol = useCallback((): void => {
    const seq = ++patrolSeqRef.current;
    Promise.all([
      listDetectionRuns({ page: 1, pageSize: 1 }).catch(() => null),
      getHealth().catch(() => null),
    ]).then((results) => {
      if (patrolSeqRef.current !== seq) return;
      const runs = results[0];
      setLastRun(runs === null || runs.items.length === 0 ? null : runs.items[0]);
      setScheduler(results[1]?.scheduler ?? null);
    });
  }, []);

  useEffect(() => {
    fetchPatrol();
  }, [fetchPatrol]);

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
      role: role === '' ? undefined : (role as AlertCorrelationRole),
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

  /** 生命周期通知 chip（v0.13-R19）：确认/清除动作是否触达值班渠道。 */
  const lifecycleEventLabel = (event: AlertItem['lifecycleDelivery']['event']): string => {
    switch (event) {
      case 'ack':
        return LIFECYCLE_EVENT_ACK_LABEL;
      case 'clear':
        return LIFECYCLE_EVENT_CLEAR_LABEL;
      default:
        return '';
    }
  };

  function lifecycleNotifyChip(row: AlertItem): React.ReactNode {
    const lc = row.lifecycleDelivery;
    if (lc.status === 'delivered') {
      return (
        <span
          className="patrol-status patrol-status--ok"
          title={format(LIFECYCLE_DELIVERED_TITLE, {
            event: lifecycleEventLabel(lc.event),
            n: lc.attempts,
          })}
        >
          {WEBHOOK_DELIVERED_LABEL}
        </span>
      );
    }
    if (lc.status === 'failed') {
      return (
        <span
          className="patrol-status patrol-status--failed"
          title={format(LIFECYCLE_FAILED_TITLE, {
            event: lifecycleEventLabel(lc.event),
            n: lc.attempts,
          })}
        >
          {WEBHOOK_FAILED_LABEL}
        </span>
      );
    }
    return (
      <span className="muted-note" title={LIFECYCLE_NONE_TITLE}>
        {WEBHOOK_OFF_LABEL}
      </span>
    );
  }

  const columns = useMemo<TableColumn<AlertItem>[]>(
    () => [
      { key: 'state', header: ALR_COL_STATE, render: (row) => <AlertStateChip state={row.state} /> },
      {
        key: 'severity',
        header: ALR_COL_SEVERITY,
        render: (row) => <SeverityBadge severity={row.severity} />,
      },
      {
        key: 'correlation',
        header: ALR_COL_CORRELATION,
        render: (row) =>
          row.correlationRole === 'primary' ? (
            <span className="chip chip--warning" title={ALR_CORR_PRIMARY_HINT}>
              <span className="chip-dot" aria-hidden="true" />
              {ALR_CORR_PRIMARY}
            </span>
          ) : row.correlationRole === 'secondary' ? (
            <span className="chip chip--muted" title={ALR_CORR_SECONDARY_HINT}>
              <span className="chip-dot" aria-hidden="true" />
              {ALR_CORR_SECONDARY}
            </span>
          ) : (
            <span className="muted-note">—</span>
          ),
      },
      { key: 'rule', header: ALR_COL_RULE, render: (row) => row.ruleLabel },
      {
        key: 'resource',
        header: ALR_COL_RESOURCE,
        // issue #57① 修复：主行 name（端口名），副行 naturalKey（含设备前缀）。
        // 端口/板卡类资源的 name 本身无区分度（多台设备同有 xgei-1/1/1），
        // 只显 name 会让运维无法分辨告警属于哪台设备；副行永远带上
        // naturalKey（与资源浏览页的双行展示一致），同名端口的行文本不同。
        render: (row) =>
          row.resource.id !== '' ? (
            <Link
              className="entity-link alert-resource-cell"
              to={`/resources?focus=${encodeURIComponent(row.resource.id)}`}
              title={ALR_RESOURCE_LINK_TITLE}
              onClick={(event) => event.stopPropagation()}
            >
              <span>{row.resource.name !== '' ? row.resource.name : row.resource.naturalKey}</span>
              {row.resource.naturalKey !== '' &&
                row.resource.naturalKey !== row.resource.name && (
                  <span className="muted-note alert-resource-key">{row.resource.naturalKey}</span>
                )}
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
        <label className="filter-field">
          <span className="filter-label">{ALR_FILTER_ROLE}</span>
          <select
            className="select"
            value={role}
            onChange={(event) => {
              setRole(event.target.value === '' ? '' : (event.target.value as AlertCorrelationRole));
              setPage(1);
            }}
          >
            <option value="">{FILTER_ALL}</option>
            <option value="primary">{ALR_CORR_PRIMARY}</option>
            <option value="secondary">{ALR_CORR_SECONDARY}</option>
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

      {/* 巡检状态条（v0.11-R11）：上次回执 + 下次调度；右侧运行历史入口 */}
      <div className="patrol-bar">
        <span className="patrol-bar__label">{PATROL_LAST_LABEL}</span>
        <span className="patrol-bar__main">
          {lastRun === null ? (
            <span className="muted-note">{PATROL_LAST_NONE}</span>
          ) : lastRun.error !== '' ? (
            <span className="patrol-bar__value--failed" title={lastRun.error}>
              {format(PATROL_LAST_LINE, {
                source: RUN_SOURCE_LABELS[lastRun.source],
                time: formatRelativeTime(lastRun.startedAt),
                duration: format(PATROL_DURATION_TEXT, { n: lastRun.durationMs }),
                created: PATROL_LAST_FAILED,
                cleared: '—',
                suppressed: '—',
                flapHeld: '—',
              })}
            </span>
          ) : (
            <span
              className="patrol-bar__value"
              title={`${formatDateTime(lastRun.startedAt)} · runId ${lastRun.runId}`}
            >
              {format(PATROL_LAST_LINE, {
                source: RUN_SOURCE_LABELS[lastRun.source],
                time: formatRelativeTime(lastRun.startedAt),
                duration: format(PATROL_DURATION_TEXT, { n: lastRun.durationMs }),
                created: lastRun.alertsCreated,
                cleared: lastRun.alertsCleared,
                suppressed: lastRun.suppressed,
                flapHeld: lastRun.flapHeld,
              })}
            </span>
          )}
        </span>
        <span className="patrol-bar__push" />
        <span className="patrol-bar__next">
          {scheduler !== null && scheduler.enabled && scheduler.nextRunAt !== '' ? (
            <span title={PATROL_NEXT_LABEL}>
              {PATROL_NEXT_LABEL}：
              {format(PATROL_NEXT_LINE, {
                time: formatRelativeTime(scheduler.nextRunAt),
                interval: patrolIntervalText(scheduler.intervalSeconds),
              })}
            </span>
          ) : (
            <span>{PATROL_OFF}</span>
          )}
          <button
            type="button"
            className="btn btn--small"
            onClick={() => setHistoryOpen(true)}
            title={PATROL_RUNS_TITLE}
          >
            {PATROL_BTN_HISTORY}
          </button>
        </span>
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
                    <tr>
                      <th scope="row">{ALR_CORR_DETAIL_LABEL}</th>
                      <td>
                        {row.correlationRole === 'secondary' && row.correlatedResourceId !== '' ? (
                          <>
                            {format(ALR_CORR_CAUSE_LINE, {
                              resource: row.correlatedResourceId,
                              alertId: row.correlatedAlertId,
                            })}{' '}
                            <Link
                              className="entity-link"
                              to={`/resources?focus=${encodeURIComponent(row.correlatedResourceId)}`}
                              title={ALR_RESOURCE_LINK_TITLE}
                            >
                              [→]
                            </Link>
                          </>
                        ) : row.correlationRole === 'primary' ? (
                          ALR_CORR_PRIMARY_LINE
                        ) : (
                          ALR_CORR_NONE_LINE
                        )}
                      </td>
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
                    <tr>
                      <th scope="row">{ALR_DETAIL_LIFECYCLE}</th>
                      <td>{lifecycleNotifyChip(row)}</td>
                    </tr>
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
          // 检测运行可能新建/清除了告警：立即刷新当前筛选 + 巡检条
          fetchAlerts();
          fetchPatrol();
        }}
      />
      {historyOpen && <RunHistoryDialog onClose={() => setHistoryOpen(false)} />}
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
