/**
 * 变更集标签页：
 * - 倒序列表（id mono / 描述 / 触发中文 / 状态 chips / 操作数 / 创建·撤销时间）；
 * - [撤销] 仅最新已应用变更集可用（其余禁用 + tooltip 提示）；
 * - 撤销确认 → 父级 onUndo（toast 含新变更集 id + 刷新）。
 */

import { useCallback, useEffect, useState } from 'react';
import type { ChangeSetItem } from '../../api/types';
import { ApiError, listChangeSets } from '../../api/client';
import {
  CS_STATUS_LABELS,
  CS_TRIGGER_LABELS,
  LAB_CS_BTN_UNDO,
  LAB_CS_COL_CREATED,
  LAB_CS_COL_DESC,
  LAB_CS_COL_ID,
  LAB_CS_COL_OPS,
  LAB_CS_COL_REVERTED,
  LAB_CS_COL_STATUS,
  LAB_CS_COL_TRIGGER,
  LAB_CS_EMPTY,
  LAB_CS_ONLY_LATEST_HINT,
  LAB_CS_TABLE_LABEL,
  LAB_CS_UNDO_MESSAGE,
  LAB_CS_UNDO_TITLE,
  NO_VALUE,
  format,
  formatDateTime,
} from '../../i18n';
import { Table } from '../Table';
import type { TableColumn } from '../Table';
import { Pagination } from '../Pagination';
import { ErrorBlock } from '../ErrorBlock';
import { Spinner } from '../Spinner';
import { ConfirmDialog } from '../ConfirmDialog';

const PAGE_SIZE = 20;

const STATUS_CHIP: Record<ChangeSetItem['status'], string> = {
  applied: 'chip--success',
  reverted: 'chip--muted',
  failed: 'chip--error',
};

interface ChangeSetsTabProps {
  workspaceId: string;
  /** 父级刷新计数：任何变更后 +1。 */
  version: number;
  onUndo: (changesetId?: string) => void;
  undoBusy: boolean;
}

export function ChangeSetsTab({ workspaceId, version, onUndo, undoBusy }: ChangeSetsTabProps) {
  const [items, setItems] = useState<ChangeSetItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [confirmTarget, setConfirmTarget] = useState<ChangeSetItem | null>(null);

  const load = useCallback((): void => {
    setLoading(true);
    setError(null);
    listChangeSets(workspaceId, page, PAGE_SIZE)
      .then((paged) => {
        setItems(paged.items);
        setTotal(paged.total);
        setLoading(false);
      })
      .catch((err: unknown) => {
        setError(err instanceof ApiError ? err.message : null);
        setLoading(false);
      });
  }, [workspaceId, page]);

  useEffect(() => {
    load();
  }, [load, version]);

  // 倒序列表中第一个 applied 即最新已应用变更集（trailing failed 不阻塞 LIFO）
  const latestAppliedId: string | undefined = items.find((item) => item.status === 'applied')?.id;

  function confirmUndo(): void {
    const target = confirmTarget;
    if (target === null) return;
    setConfirmTarget(null);
    onUndo(target.id);
  }

  const columns: TableColumn<ChangeSetItem>[] = [
    { key: 'id', header: LAB_CS_COL_ID, render: (row) => <span className="mono cell-strong">{row.id}</span> },
    { key: 'description', header: LAB_CS_COL_DESC, render: (row) => (row.description === '' ? <span className="muted-note">{NO_VALUE}</span> : row.description) },
    { key: 'trigger', header: LAB_CS_COL_TRIGGER, render: (row) => CS_TRIGGER_LABELS[row.trigger] ?? row.trigger },
    {
      key: 'status',
      header: LAB_CS_COL_STATUS,
      render: (row) => <span className={`chip ${STATUS_CHIP[row.status]}`}>{CS_STATUS_LABELS[row.status]}</span>,
    },
    { key: 'opsApplied', header: LAB_CS_COL_OPS, render: (row) => row.opsApplied, align: 'right' },
    { key: 'createdAt', header: LAB_CS_COL_CREATED, render: (row) => formatDateTime(row.createdAt) },
    {
      key: 'revertedAt',
      header: LAB_CS_COL_REVERTED,
      render: (row) => (row.revertedAt === null || row.revertedAt === undefined ? <span className="muted-note">{NO_VALUE}</span> : formatDateTime(row.revertedAt)),
    },
    {
      key: 'actions',
      header: LAB_CS_COL_STATUS,
      render: (row) => (
        <button
          type="button"
          className="btn btn--secondary btn--sm"
          disabled={undoBusy || row.status !== 'applied' || row.id !== latestAppliedId}
          title={row.id === latestAppliedId ? undefined : LAB_CS_ONLY_LATEST_HINT}
          onClick={() => setConfirmTarget(row)}
        >
          {undoBusy && confirmTarget?.id === row.id ? <Spinner size={12} /> : null}
          {LAB_CS_BTN_UNDO}
        </button>
      ),
    },
  ];

  return (
    <div>
      {error !== null && <ErrorBlock message={error} onRetry={load} />}
      <div className="table-wrap">
        <Table
          columns={columns}
          rows={items}
          rowKey={(row) => row.id}
          loading={loading}
          emptyText={LAB_CS_EMPTY}
          ariaLabel={LAB_CS_TABLE_LABEL}
        />
      </div>
      {!loading && total > PAGE_SIZE && (
        <Pagination page={page} pageSize={PAGE_SIZE} total={total} onChange={setPage} />
      )}
      <ConfirmDialog
        open={confirmTarget !== null}
        title={LAB_CS_UNDO_TITLE}
        message={format(LAB_CS_UNDO_MESSAGE, { id: confirmTarget?.id ?? '' })}
        confirmLabel={LAB_CS_BTN_UNDO}
        onConfirm={confirmUndo}
        onCancel={() => setConfirmTarget(null)}
      />
    </div>
  );
}
