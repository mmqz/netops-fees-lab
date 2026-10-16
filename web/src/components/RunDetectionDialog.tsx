/**
 * 立即检测对话框（v0.10-R7）：POST /api/detection/run 的 UI 入口。
 *
 * 三态一体：确认（说明全库扫描语义）→ 运行中（busy 守卫）→ 运行回执
 * （RunResult 摘要统计 + 告警变更明细表）。告警页与规则页共用——
 * 运营侧两个入口（待处置队列的「现在跑一次」与改规则后的立即验证）。
 *
 * 回执语义（与 internal/detect/engine.go 契约一致）：
 * - findings 计入全部越限（含告警已存在的序列）；alertsCreated/Cleared/
 *   runStatesWritten 是本次运行的变更计数；evaluatedAt 来自数据中
 * 最新观测点（无数据为空）。
 * - 数据与规则未变时重跑报零变更——确定性，不是错误。
 */

import { useEffect, useState } from 'react';
import type { DetectionRunResponse } from '../api/types';
import { runDetection } from '../api/client';
import {
  BTN_CANCEL,
  CORRELATION_ROLE_LABELS,
  RUN_BTN_WORKING,
  RUN_CLEARED,
  RUN_CONFIRM_MESSAGE,
  RUN_CONFIRM_TITLE,
  RUN_CREATED,
  RUN_DONE,
  RUN_EVALUATED_AT,
  RUN_EVALUATED_NONE,
  RUN_FAILED,
  RUN_FINDINGS,
  RUN_FLAP_HELD,
  RUN_MUTATIONS_EMPTY,
  RUN_MUTATIONS_TITLE,
  RUN_RUNSTATES,
  RUN_SUPPRESSED,
  RUN_RESULT_TITLE,
  RUN_ACTION_CLEARED,
  RUN_ACTION_CREATED,
  RUN_ACTION_FLAP_HELD,
  RUN_ACTION_SUPPRESSED,
  RUN_TOAST_DONE,
  format,
  formatDateTime,
} from '../i18n';
import { SeverityBadge } from './SeverityBadge';
import { useToast } from './Toast';
import { useModalA11y } from '../hooks/useModalA11y';

/** 一次告警变更行（回执明细表）。suppressed 行（v0.12-R15）无告警行，
 * 徽标用中性色 + 「抑制」文案与 created/cleared 区分；flap_held 行
 * （v0.12-R17）同为无告警行的被拦潜在告警，但成因是序列自身抖动而非
 * 维护计划——用 info 色 + 「抖动抑制」文案区分两种「没告」；created
 * 行带根因/次生裁定徽标（v0.12-R16）。 */
function MutationRow({ line }: { line: DetectionRunResponse['alerts'][number] }) {
  const actionLabel =
    line.action === 'created'
      ? RUN_ACTION_CREATED
      : line.action === 'suppressed'
        ? RUN_ACTION_SUPPRESSED
        : line.action === 'flap_held'
          ? RUN_ACTION_FLAP_HELD
          : RUN_ACTION_CLEARED;
  const chipClass =
    line.action === 'created'
      ? 'chip--error'
      : line.action === 'suppressed'
        ? 'chip--warning'
        : line.action === 'flap_held'
          ? 'chip--info'
          : 'chip--muted';
  return (
    <tr>
      <td>
        <span className={`chip ${chipClass}`}>
          <span className="chip-dot" aria-hidden="true" />
          {actionLabel}
        </span>
      </td>
      <td>
        <span className="mono">{line.metricKey}</span>
      </td>
      <td>
        <span className="mono">{line.resourceId}</span>
      </td>
      {line.action === 'created' && line.correlationRole ? (
        <td>
          <span
            className={`chip ${line.correlationRole === 'primary' ? 'chip--warning' : 'chip--muted'}`}
            title={line.correlationRole}
          >
            <span className="chip-dot" aria-hidden="true" />
            {CORRELATION_ROLE_LABELS[line.correlationRole] ?? line.correlationRole}
          </span>
        </td>
      ) : (
        <td>
          <span className="muted-note">—</span>
        </td>
      )}
      <td>
        <SeverityBadge severity={line.severity} />
      </td>
      <td>
        <span className="mono">{line.lastValue}</span>
      </td>
      <td>
        <span title={formatDateTime(line.firedAt)}>{line.firedAt.slice(0, 19).replace('T', ' ')}</span>
      </td>
    </tr>
  );
}

/**
 * 立即检测对话框。受控 open + onFinished 回执回调（调用方刷新列表）。
 * 内部状态机：confirm → busy → result（result 态由 result prop 非空判定）。
 */
export function RunDetectionDialog({
  open,
  onClose,
  onFinished,
}: {
  open: boolean;
  onClose: () => void;
  onFinished: (res: DetectionRunResponse) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<DetectionRunResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const modalRef = useModalA11y(open);
  const toast = useToast();

  // 打开时重置上一轮回执
  useEffect(() => {
    if (open) {
      setResult(null);
      setError(null);
      setBusy(false);
    }
  }, [open]);

  if (!open) return null;

  function start(): void {
    setBusy(true);
    setError(null);
    runDetection()
      .then((res) => {
        setBusy(false);
        setResult(res);
        toast.show(
          format(RUN_TOAST_DONE, { created: res.alertsCreated, cleared: res.alertsCleared }),
          'success',
        );
        onFinished(res);
      })
      .catch((err: unknown) => {
        setBusy(false);
        const display = err instanceof Error ? err.message : String(err);
        setError(format(RUN_FAILED, { message: display }));
      });
  }

  const isResult = result !== null;

  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={() => (busy ? undefined : onClose())} />
      <div
        ref={modalRef}
        className="modal modal--form"
        role="dialog"
        aria-modal="true"
        aria-label={isResult ? RUN_RESULT_TITLE : RUN_CONFIRM_TITLE}
        tabIndex={-1}
      >
        <h3 className="modal-title">{isResult ? RUN_RESULT_TITLE : RUN_CONFIRM_TITLE}</h3>

        {isResult && result !== null ? (
          <>
            <div className="run-stats">
              <div className="run-stat">
                <div className="run-stat-value">{result.findings}</div>
                <div className="run-stat-label">{format(RUN_FINDINGS, { n: result.findings })}</div>
              </div>
              <div className="run-stat">
                <div className="run-stat-value">{result.alertsCreated}</div>
                <div className="run-stat-label">{format(RUN_CREATED, { n: result.alertsCreated })}</div>
              </div>
              <div className="run-stat">
                <div className="run-stat-value">{result.alertsCleared}</div>
                <div className="run-stat-label">{format(RUN_CLEARED, { n: result.alertsCleared })}</div>
              </div>
              <div className="run-stat">
                <div className="run-stat-value">{result.runStatesWritten}</div>
                <div className="run-stat-label">
                  {format(RUN_RUNSTATES, { n: result.runStatesWritten })}
                </div>
              </div>
              <div className="run-stat">
                <div className="run-stat-value">{result.suppressed}</div>
                <div className="run-stat-label">{format(RUN_SUPPRESSED, { n: result.suppressed })}</div>
              </div>
              <div className="run-stat">
                <div className="run-stat-value">{result.flapHeld}</div>
                <div className="run-stat-label">{format(RUN_FLAP_HELD, { n: result.flapHeld })}</div>
              </div>
            </div>
            <div className="muted-note">
              {result.evaluatedAt !== ''
                ? format(RUN_EVALUATED_AT, { at: formatDateTime(result.evaluatedAt) })
                : RUN_EVALUATED_NONE}
            </div>
            <div className="evidence-title">{RUN_MUTATIONS_TITLE}</div>
            {result.alerts.length > 0 ? (
              <table className="kv-table run-mutations">
                <tbody>
                  {result.alerts.map((line) => (
                    <MutationRow key={`${line.alertId}-${line.action}`} line={line} />
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="muted-note">{RUN_MUTATIONS_EMPTY}</div>
            )}
            <div className="modal-actions">
              <button type="button" className="btn btn--primary" onClick={onClose}>
                {RUN_DONE}
              </button>
            </div>
          </>
        ) : (
          <>
            <div className="modal-message">{RUN_CONFIRM_MESSAGE}</div>
            {error !== null && <div className="form-error-summary">{error}</div>}
            <div className="modal-actions">
              <button type="button" className="btn" disabled={busy} onClick={onClose}>
                {BTN_CANCEL}
              </button>
              <button
                type="button"
                className="btn btn--primary"
                disabled={busy}
                onClick={() => start()}
              >
                {busy ? RUN_BTN_WORKING : RUN_CONFIRM_TITLE}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
