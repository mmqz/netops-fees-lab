/**
 * 检查点标签页：
 * - 顶部创建行（标签输入 + [创建检查点]，空标签客户端拦截；服务端确认
 *   成功后才清空输入——AUD2-16）；
 * - 列表（id mono / 标签 / 状态哈希 chip 截断 + title 全量 / 创建时间）；
 * - [恢复] 确认（警告：作为新变更集执行）→ 父级 onRestore（no-op → info toast）。
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import type { CheckpointItem } from '../../api/types';
import { formatApiError, listCheckpoints } from '../../api/client';
import {
  BTN_PROCESSING,
  LAB_BTN_CREATE_CHECKPOINT,
  LAB_COL_ACTIONS,
  LAB_CP_BTN_RESTORE,
  LAB_CP_COL_CREATED,
  LAB_CP_COL_HASH,
  LAB_CP_COL_ID,
  LAB_CP_COL_LABEL,
  LAB_CP_EMPTY,
  LAB_CP_ERR_LABEL,
  LAB_CP_LABEL,
  LAB_CP_LABEL_PLACEHOLDER,
  LAB_CP_RESTORE_MESSAGE,
  LAB_CP_RESTORE_TITLE,
  LAB_CP_TABLE_LABEL,
  format,
  formatDateTime,
  formatRelativeTime,
} from '../../i18n';
import { Table } from '../Table';
import type { TableColumn } from '../Table';
import { ErrorBlock } from '../ErrorBlock';
import { Spinner } from '../Spinner';
import { ConfirmDialog } from '../ConfirmDialog';
import { useSeq } from '../../hooks/useSeq';

interface CheckpointsTabProps {
  workspaceId: string;
  /** 父级刷新计数：任何变更后 +1。 */
  version: number;
  /** 创建回调：成功 resolve(true)，失败 resolve(false)——成功后才清空输入（AUD2-16）。 */
  onCreateCheckpoint: (label: string) => Promise<boolean>;
  onRestore: (checkpoint: CheckpointItem) => void;
  cpBusy: boolean;
  /** 聚焦信号：每次点击页头「创建检查点」都会 +1（AUD2-17）。 */
  focusSignal?: number;
}

export function CheckpointsTab({
  workspaceId,
  version,
  onCreateCheckpoint,
  onRestore,
  cpBusy,
  focusSignal = 0,
}: CheckpointsTabProps) {
  const [items, setItems] = useState<CheckpointItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [label, setLabel] = useState('');
  const [labelError, setLabelError] = useState<string | null>(null);
  const [confirmTarget, setConfirmTarget] = useState<CheckpointItem | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  /** 取数守卫（AUD2-8）。 */
  const loadGuard = useSeq();

  const load = useCallback((): void => {
    const isCurrent = loadGuard.begin();
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    listCheckpoints(workspaceId)
      .then((result) => {
        if (!isCurrent()) return;
        setItems(result.items);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [workspaceId, loadGuard]);

  useEffect(() => {
    load();
  }, [load, version]);

  useEffect(() => {
    if (focusSignal > 0) {
      inputRef.current?.focus();
    }
  }, [focusSignal]);

  function submitCreate(): void {
    const trimmed = label.trim();
    if (trimmed === '') {
      setLabelError(LAB_CP_ERR_LABEL);
      return;
    }
    setLabelError(null);
    // AUD2-16：服务端确认成功后才清空输入，失败时保留已输入的标签
    void onCreateCheckpoint(trimmed).then((ok: boolean) => {
      if (ok) {
        setLabel('');
      }
    });
  }

  function confirmRestore(): void {
    const target = confirmTarget;
    if (target === null) return;
    setConfirmTarget(null);
    onRestore(target);
  }

  const columns: TableColumn<CheckpointItem>[] = [
    { key: 'id', header: LAB_CP_COL_ID, render: (row) => <span className="mono cell-strong">{row.checkpointId}</span> },
    { key: 'label', header: LAB_CP_COL_LABEL, render: (row) => row.label },
    {
      key: 'hash',
      header: LAB_CP_COL_HASH,
      render: (row) => (
        <span className="chip chip--mono lab-hash-chip" title={row.workspaceHash}>
          {row.workspaceHash}
        </span>
      ),
    },
    { key: 'createdAt', header: LAB_CP_COL_CREATED, render: (row) => <span title={formatDateTime(row.createdAt)}>{formatRelativeTime(row.createdAt)}</span> },
    {
      key: 'actions',
      header: LAB_COL_ACTIONS,
      render: (row) => (
        <button
          type="button"
          className="btn btn--secondary btn--sm"
          disabled={cpBusy}
          onClick={() => setConfirmTarget(row)}
        >
          {cpBusy && confirmTarget?.checkpointId === row.checkpointId ? <Spinner size={12} /> : null}
          {LAB_CP_BTN_RESTORE}
        </button>
      ),
    },
  ];

  return (
    <div>
      {error !== null && <ErrorBlock message={error} detail={errorDetail} onRetry={load} />}
      <div className="lab-cp-create">
        <input
          ref={inputRef}
          className="input"
          value={label}
          disabled={cpBusy}
          placeholder={LAB_CP_LABEL_PLACEHOLDER}
          aria-label={LAB_CP_LABEL}
          onChange={(event) => {
            setLabel(event.target.value);
            setLabelError(null);
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              submitCreate();
            }
          }}
        />
        <button type="button" className="btn btn--primary" disabled={cpBusy} onClick={submitCreate}>
          {cpBusy ? <Spinner size={14} /> : null}
          {cpBusy ? BTN_PROCESSING : LAB_BTN_CREATE_CHECKPOINT}
        </button>
      </div>
      {labelError !== null && (
        <div className="form-error" role="alert">
          {labelError}
        </div>
      )}
      <div className="table-wrap">
        <Table
          columns={columns}
          rows={items}
          rowKey={(row) => row.checkpointId}
          loading={loading}
          emptyText={LAB_CP_EMPTY}
          ariaLabel={LAB_CP_TABLE_LABEL}
        />
      </div>
      <ConfirmDialog
        open={confirmTarget !== null}
        title={LAB_CP_RESTORE_TITLE}
        message={format(LAB_CP_RESTORE_MESSAGE, { label: confirmTarget?.label ?? '' })}
        confirmLabel={LAB_CP_BTN_RESTORE}
        onConfirm={confirmRestore}
        onCancel={() => setConfirmTarget(null)}
      />
    </div>
  );
}
