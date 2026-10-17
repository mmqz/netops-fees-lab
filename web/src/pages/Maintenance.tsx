/**
 * 维护窗口页 /maintenance（v0.12-R15）：
 * - 窗口全量列表（后端 starts_at DESC）：事由、时间区间（本地时区格式化）、
 *   抑制范围（全局 / N 个资源）、状态（计划中/进行中/已结束——按浏览器时钟
 *   客户端计算，服务端保持无时钟）、行内删除；
 * - 行展开详情：窗口 ID、备注、创建时间、资源范围 id 清单（kv 审计视图）；
 * - 新建 WindowDialog：事由 + 起止 datetime-local + 范围（全局/指定资源）+
 *   备注；指定资源时经资源搜索（GET /api/resources?q=）点选加入 chips；
 * - 删除走 ConfirmDialog（历史运行回执不受影响，文案明说）。
 *
 * 契约（与 internal/api/maintenance.go 一致）：
 * - POST 201（RFC3339 起止，ends > starts；resourceIds 含不存在 id → 400）；
 * - DELETE 未知 id 404；无 PATCH（编辑 = 删除 + 重建，窗口是计划不是档案）；
 * - 后端是最终权威，前端校验只为减少往返。
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { MaintenanceWindow, ResourceItem } from '../api/types';
import {
  createMaintenanceWindow,
  deleteMaintenanceWindow,
  formatApiError,
  formatApiErrorText,
  listDetectionRuns,
  listMaintenanceWindows,
  listResources,
} from '../api/client';
import {
  BTN_CANCEL,
  BTN_PROCESSING,
  RES_FILTER_SEARCH,
  MNT_ACTION_FAILED,
  MNT_BTN_DELETE,
  MNT_BTN_NEW,
  MNT_COL_ACTIONS,
  MNT_COL_LABEL,
  MNT_COL_RANGE,
  MNT_COL_SCOPE,
  MNT_COL_STATUS,
  MNT_CREATE_TITLE,
  MNT_CREATE_TOAST,
  MNT_DELETED_TOAST,
  MNT_DELETE_MESSAGE,
  MNT_DELETE_TITLE,
  MNT_EMPTY,
  MNT_FIELD_END,
  MNT_FIELD_LABEL,
  MNT_FIELD_LABEL_PLACEHOLDER,
  MNT_FIELD_NOTE,
  MNT_FIELD_NOTE_PLACEHOLDER,
  MNT_FIELD_SCOPE,
  MNT_FIELD_START,
  MNT_HINT,
  MNT_TIME_BASIS,
  MNT_LABEL_REQUIRED,
  MNT_LOAD_FAILED,
  MNT_SCOPE_EMPTY_HINT,
  MNT_SCOPE_GLOBAL,
  MNT_SCOPE_OPTION_GLOBAL,
  MNT_SCOPE_OPTION_SCOPED,
  MNT_SCOPE_RESOURCES,
  MNT_SCOPE_SEARCH_PLACEHOLDER,
  MNT_STATUS_ACTIVE,
  MNT_STATUS_ACTIVE_HINT,
  MNT_STATUS_ENDED,
  MNT_STATUS_PLANNED,
  MNT_TIMES_INVALID,
  MNT_TIMES_ORDER,
  MNT_TIMES_REQUIRED,
  NAV_MAINTENANCE,
  format,
  formatDateTime,
} from '../i18n';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ErrorBlock } from '../components/ErrorBlock';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';
import { useToast } from '../components/Toast';
import { useModalA11y } from '../hooks/useModalA11y';

/** 窗口相对浏览器时钟的状态（纯客户端计算，服务端无时钟）。 */
type WindowPhase = 'planned' | 'active' | 'ended';

function windowPhase(w: MaintenanceWindow, nowMs: number): WindowPhase {
  const start = Date.parse(w.startsAt);
  const end = Date.parse(w.endsAt);
  if (Number.isNaN(start) || Number.isNaN(end)) return 'ended';
  if (nowMs < start) return 'planned';
  if (nowMs < end) return 'active';
  return 'ended';
}

const PHASE_CHIP: Record<WindowPhase, string> = {
  planned: 'chip--muted',
  active: 'chip--warning',
  ended: 'chip--success',
};

const PHASE_LABEL: Record<WindowPhase, string> = {
  planned: MNT_STATUS_PLANNED,
  active: MNT_STATUS_ACTIVE,
  ended: MNT_STATUS_ENDED,
};

/** datetime-local 输入的本地值 → RFC3339 UTC（无效返回 null）。 */
function localToRFC3339(local: string): string | null {
  if (local === '') return null;
  const ms = Date.parse(local);
  if (Number.isNaN(ms)) return null;
  return new Date(ms).toISOString();
}

/** 选中资源的显示名（搜索结果与 chips 共用）。 */
interface ScopePick {
  id: string;
  name: string;
}

/** WindowDialog 的受控表单值。 */
interface WindowFormValue {
  label: string;
  startLocal: string;
  endLocal: string;
  scoped: boolean;
  picks: ScopePick[];
  note: string;
}

const EMPTY_FORM: WindowFormValue = {
  label: '',
  startLocal: '',
  endLocal: '',
  scoped: false,
  picks: [],
  note: '',
};

/**
 * 新建维护窗口对话框。资源范围选择器：关键字搜索（Enter 触发）→ 结果点击
 * 加入 chips → chips 点击移除；重复点选幂等（id 判重）。
 */
function WindowDialog({
  open,
  busy,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  busy: boolean;
  onConfirm: (value: WindowFormValue) => void;
  onCancel: () => void;
}) {
  const modalRef = useModalA11y(open);
  const [value, setValue] = useState<WindowFormValue>(EMPTY_FORM);
  const [error, setError] = useState<string | null>(null);
  const [searchText, setSearchText] = useState('');
  const [results, setResults] = useState<ResourceItem[] | null>(null);
  const [searching, setSearching] = useState(false);

  // 打开时重置（上一条草稿不串台）
  useEffect(() => {
    if (open) {
      setValue(EMPTY_FORM);
      setError(null);
      setSearchText('');
      setResults(null);
    }
  }, [open]);

  if (!open) return null;

  function runSearch(): void {
    const q = searchText.trim();
    if (q === '') return;
    setSearching(true);
    listResources({ q, page: 1, pageSize: 10 })
      .then((paged) => {
        setSearching(false);
        setResults(paged.items);
      })
      .catch(() => {
        setSearching(false);
        setResults([]);
      });
  }

  function addPick(res: ResourceItem): void {
    setValue((v) =>
      v.picks.some((p) => p.id === res.id)
        ? v
        : { ...v, picks: [...v.picks, { id: res.id, name: res.name !== '' ? res.name : res.naturalKey }] },
    );
  }

  function submit(): void {
    const startRFC = localToRFC3339(value.startLocal);
    const endRFC = localToRFC3339(value.endLocal);
    if (value.label.trim() === '') {
      setError(MNT_LABEL_REQUIRED);
      return;
    }
    if (startRFC === null || endRFC === null) {
      setError(value.startLocal === '' || value.endLocal === '' ? MNT_TIMES_REQUIRED : MNT_TIMES_INVALID);
      return;
    }
    if (endRFC <= startRFC) {
      setError(MNT_TIMES_ORDER);
      return;
    }
    setError(null);
    onConfirm(value);
  }

  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={() => (busy ? undefined : onCancel())} />
      <div
        ref={modalRef}
        className="modal modal--form"
        role="dialog"
        aria-modal="true"
        aria-label={MNT_CREATE_TITLE}
        tabIndex={-1}
      >
        <h3 className="modal-title">{MNT_CREATE_TITLE}</h3>
        <div className="field">
          <label className="field-label" htmlFor="mnt-label">
            {MNT_FIELD_LABEL}
          </label>
          <input
            id="mnt-label"
            className="input"
            value={value.label}
            disabled={busy}
            placeholder={MNT_FIELD_LABEL_PLACEHOLDER}
            onChange={(event) => setValue((v) => ({ ...v, label: event.target.value }))}
          />
        </div>
        <div className="field-row">
          <div className="field">
            <label className="field-label" htmlFor="mnt-start">
              {MNT_FIELD_START}
            </label>
            <input
              id="mnt-start"
              className="input"
              type="datetime-local"
              value={value.startLocal}
              disabled={busy}
              onChange={(event) => setValue((v) => ({ ...v, startLocal: event.target.value }))}
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="mnt-end">
              {MNT_FIELD_END}
            </label>
            <input
              id="mnt-end"
              className="input"
              type="datetime-local"
              value={value.endLocal}
              disabled={busy}
              onChange={(event) => setValue((v) => ({ ...v, endLocal: event.target.value }))}
            />
          </div>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="mnt-scope">
            {MNT_FIELD_SCOPE}
          </label>
          <select
            id="mnt-scope"
            className="select"
            value={value.scoped ? 'scoped' : 'global'}
            disabled={busy}
            onChange={(event) => setValue((v) => ({ ...v, scoped: event.target.value === 'scoped' }))}
          >
            <option value="global">{MNT_SCOPE_OPTION_GLOBAL}</option>
            <option value="scoped">{MNT_SCOPE_OPTION_SCOPED}</option>
          </select>
          {!value.scoped && <div className="muted-note">{MNT_SCOPE_EMPTY_HINT}</div>}
        </div>
        {value.scoped && (
          <>
            <div className="filter-bar">
              <input
                className="input"
                value={searchText}
                disabled={busy || searching}
                placeholder={MNT_SCOPE_SEARCH_PLACEHOLDER}
                onChange={(event) => setSearchText(event.target.value)}
                onKeyUp={(event) => {
                  if (event.key === 'Enter') runSearch();
                }}
              />
              <button type="button" className="btn btn--secondary" disabled={busy || searching} onClick={runSearch}>
                {RES_FILTER_SEARCH}
              </button>
            </div>
            {results !== null && (
              <div className="scope-results">
                {results.length === 0 ? (
                  <span className="muted-note">—</span>
                ) : (
                  results
                    .filter((r) => !value.picks.some((p) => p.id === r.id))
                    .map((r) => (
                      <button
                        key={r.id}
                        type="button"
                        className="scope-result-item"
                        title={r.id}
                        disabled={busy}
                        onClick={() => addPick(r)}
                      >
                        <span>{r.name !== '' ? r.name : r.naturalKey}</span>
                        <span className="mono muted-note">{r.type}</span>
                      </button>
                    ))
                )}
              </div>
            )}
            {value.picks.length > 0 && (
              <div className="scope-picks">
                {value.picks.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    className="chip chip--muted scope-pick"
                    title={p.id}
                    disabled={busy}
                    onClick={() =>
                      setValue((v) => ({ ...v, picks: v.picks.filter((x) => x.id !== p.id) }))
                    }
                  >
                    {p.name} ×
                  </button>
                ))}
              </div>
            )}
          </>
        )}
        <div className="field">
          <label className="field-label" htmlFor="mnt-note">
            {MNT_FIELD_NOTE}
          </label>
          <input
            id="mnt-note"
            className="input"
            value={value.note}
            disabled={busy}
            placeholder={MNT_FIELD_NOTE_PLACEHOLDER}
            onChange={(event) => setValue((v) => ({ ...v, note: event.target.value }))}
          />
        </div>
        {error !== null && <div className="form-error-summary">{error}</div>}
        <div className="modal-actions">
          <button type="button" className="btn" disabled={busy} onClick={onCancel}>
            {BTN_CANCEL}
          </button>
          <button type="button" className="btn btn--primary" disabled={busy} onClick={submit}>
            {busy ? BTN_PROCESSING : MNT_CREATE_TITLE}
          </button>
        </div>
      </div>
    </div>
  );
}

/** 维护窗口管理页。 */
export function MaintenancePage() {
  const toast = useToast();
  const [windows, setWindows] = useState<MaintenanceWindow[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [dialogBusy, setDialogBusy] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<MaintenanceWindow | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  // 状态列的客户端时钟（每分钟刷新一次「进行中/已结束」判定）
  const [nowMs, setNowMs] = useState(Date.now());
  // issue #55：最近一次检测的数据时刻（evaluatedAt）——双时基提示里的
  // 「当前数据时刻」，失败降级为「未知」（提示块不阻塞页面）。
  const [dataTime, setDataTime] = useState<string | null>(null);

  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    listDetectionRuns({ page: 1, pageSize: 1 })
      .then((res) => {
        if (res.items.length > 0) setDataTime(res.items[0].evaluatedAt);
      })
      .catch(() => {
        /* 无运行历史或失败：保持 null，提示显示「未知」 */
      });
  }, []);

  const fetchWindows = useCallback(() => {
    setLoading(true);
    setError(null);
    listMaintenanceWindows()
      .then((res) => {
        setLoading(false);
        setWindows(res.items);
      })
      .catch((err: unknown) => {
        setLoading(false);
        const { title, detail } = formatApiError(err);
        setError(format(MNT_LOAD_FAILED, { message: title }));
        setErrorDetail(detail);
      });
  }, []);

  useEffect(() => {
    fetchWindows();
  }, [fetchWindows]);

  function failToast(err: unknown): void {
    const text = formatApiErrorText(err);
    toast.show(format(MNT_ACTION_FAILED, { message: text }), 'error');
  }

  function submitCreate(value: WindowFormValue): void {
    const startRFC = localToRFC3339(value.startLocal);
    const endRFC = localToRFC3339(value.endLocal);
    if (startRFC === null || endRFC === null) return;
    setDialogBusy(true);
    createMaintenanceWindow({
      label: value.label.trim(),
      startsAt: startRFC,
      endsAt: endRFC,
      resourceIds: value.scoped ? value.picks.map((p) => p.id) : undefined,
      note: value.note.trim(),
    })
      .then((win) => {
        setDialogBusy(false);
        setCreateOpen(false);
        toast.show(format(MNT_CREATE_TOAST, { id: win.windowId }), 'success');
        fetchWindows();
      })
      .catch((err: unknown) => {
        setDialogBusy(false);
        failToast(err);
      });
  }

  function confirmDelete(): void {
    if (deleteTarget === null) return;
    setDeleteBusy(true);
    deleteMaintenanceWindow(deleteTarget.windowId)
      .then((res) => {
        setDeleteBusy(false);
        setDeleteTarget(null);
        toast.show(format(MNT_DELETED_TOAST, { id: res.windowId }), 'success');
        fetchWindows();
      })
      .catch((err: unknown) => {
        setDeleteBusy(false);
        failToast(err);
      });
  }

  const columns = useMemo<TableColumn<MaintenanceWindow>[]>(
    () => [
      {
        key: 'label',
        header: MNT_COL_LABEL,
        render: (row) => row.label,
      },
      {
        key: 'range',
        header: MNT_COL_RANGE,
        render: (row) => (
          <span className="mono" title={`${row.startsAt} → ${row.endsAt}`}>
            {formatDateTime(row.startsAt)} → {formatDateTime(row.endsAt)}
          </span>
        ),
      },
      {
        key: 'scope',
        header: MNT_COL_SCOPE,
        render: (row) =>
          row.resourceIds.length === 0 ? (
            <span className="chip chip--muted">
              <span className="chip-dot" aria-hidden="true" />
              {MNT_SCOPE_GLOBAL}
            </span>
          ) : (
            <span className="chip">
              <span className="chip-dot" aria-hidden="true" />
              {format(MNT_SCOPE_RESOURCES, { n: row.resourceIds.length })}
            </span>
          ),
      },
      {
        key: 'status',
        header: MNT_COL_STATUS,
        render: (row) => {
          const phase = windowPhase(row, nowMs);
          return (
            <span
              className={`chip ${PHASE_CHIP[phase]}`}
              title={phase === 'active' ? MNT_STATUS_ACTIVE_HINT : undefined}
            >
              <span className="chip-dot" aria-hidden="true" />
              {PHASE_LABEL[phase]}
            </span>
          );
        },
      },
      {
        key: 'actions',
        header: MNT_COL_ACTIONS,
        render: (row) => (
          <span className="alert-actions">
            <button
              type="button"
              className="btn btn--sm btn--danger-ghost"
              title={row.windowId}
              disabled={deleteBusy}
              onClick={(event) => {
                event.stopPropagation();
                setDeleteTarget(row);
              }}
            >
              {MNT_BTN_DELETE}
            </button>
          </span>
        ),
      },
    ],
    [nowMs, deleteBusy],
  );

  return (
    <div className="card">
      <div className="filter-bar">
        <button
          type="button"
          className="btn btn--primary filter-push"
          onClick={() => setCreateOpen(true)}
        >
          {MNT_BTN_NEW}
        </button>
      </div>

      <div className="muted-note table-hint">{MNT_HINT}</div>

      <div className="muted-note table-hint">
        {format(MNT_TIME_BASIS, {
          dataTime: dataTime !== null ? formatDateTime(dataTime) : '未知（尚未执行过检测）',
        })}
      </div>

      {error !== null ? (
        <ErrorBlock message={error} detail={errorDetail} onRetry={fetchWindows} />
      ) : (
        <Table
          columns={columns}
          rows={windows ?? []}
          rowKey={(row) => row.windowId}
          loading={loading}
          emptyText={MNT_EMPTY}
          renderExpanded={(row) => (
            <div className="evidence">
              <div className="evidence-title">{row.label}</div>
              <table className="kv-table">
                <tbody>
                  <tr>
                    <th scope="row">ID</th>
                    <td>
                      <span className="mono">{row.windowId}</span>
                    </td>
                  </tr>
                  <tr>
                    <th scope="row">{MNT_COL_SCOPE}</th>
                    <td>
                      {row.resourceIds.length === 0
                        ? MNT_SCOPE_GLOBAL
                        : row.resourceIds.map((id) => (
                            <span key={id} className="mono scope-id">
                              {id}
                            </span>
                          ))}
                    </td>
                  </tr>
                  <tr>
                    <th scope="row">{MNT_FIELD_NOTE}</th>
                    <td>{row.note !== '' ? row.note : '—'}</td>
                  </tr>
                  <tr>
                    <th scope="row">UTC</th>
                    <td>
                      <span className="mono">
                        {row.startsAt} → {row.endsAt}
                      </span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
          ariaLabel={NAV_MAINTENANCE}
        />
      )}

      <WindowDialog
        open={createOpen}
        busy={dialogBusy}
        onConfirm={submitCreate}
        onCancel={() => {
          if (!dialogBusy) setCreateOpen(false);
        }}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        title={MNT_DELETE_TITLE}
        message={deleteTarget !== null ? format(MNT_DELETE_MESSAGE, { label: deleteTarget.label }) : MNT_DELETE_TITLE}
        danger
        busy={deleteBusy}
        confirmLabel={deleteBusy ? BTN_PROCESSING : MNT_BTN_DELETE}
        onConfirm={confirmDelete}
        onCancel={() => {
          if (!deleteBusy) setDeleteTarget(null);
        }}
      />
    </div>
  );
}
