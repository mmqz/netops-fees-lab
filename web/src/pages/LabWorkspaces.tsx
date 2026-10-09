/**
 * 仿真工作台列表页 /lab：
 * - 工作台表格（名称/描述/设备/接口/链路/评分/最近检查/创建时间），行点击进入拓扑工作台；
 * - 「新建工作台」对话框：名称必填 + 描述 + 数据来源批次多选（completed 批次，默认全选）；
 * - 创建成功即进入新工作台；删除需确认（danger）；
 * - 空态 → 引导至「数据导入」。
 */

import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { BatchListItem, Paged, WorkspaceSummary } from '../api/types';
import { ApiError, createWorkspace, deleteWorkspace, listBatches, listWorkspaces } from '../api/client';
import {
  BTN_CREATE,
  BTN_CREATE_WORKSPACE,
  BTN_DELETE_WORKSPACE,
  BTN_GO_IMPORT,
  BTN_PROCESSING,
  LAB_BATCH_OPTION,
  LAB_BATCH_SELECT_ALL,
  LAB_BATCH_SELECT_NONE,
  LAB_COL_ACTIONS,
  LAB_COL_CREATED_AT,
  LAB_COL_DESC,
  LAB_COL_DEVICES,
  LAB_COL_INTERFACES,
  LAB_COL_LAST_RUN,
  LAB_COL_LINKS,
  LAB_COL_NAME,
  LAB_COL_SCORE,
  LAB_CREATE_TITLE,
  LAB_DELETE_MESSAGE,
  LAB_DELETE_TITLE,
  LAB_EMPTY_DESC,
  LAB_EMPTY_TITLE,
  LAB_F_BATCHES,
  LAB_F_BATCHES_HINT,
  LAB_F_DESC,
  LAB_F_NAME,
  LAB_F_NAME_REQUIRED,
  LAB_NO_COMPLETED_BATCHES,
  LAB_SCORE_VALUE,
  LAB_TABLE_LABEL,
  NO_VALUE,
  TOAST_WORKSPACE_CREATED,
  TOAST_WORKSPACE_DELETED,
  format,
  formatDateTime,
} from '../i18n';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';
import { Pagination } from '../components/Pagination';
import { LoadingPanel } from '../components/Spinner';
import { ErrorBlock } from '../components/ErrorBlock';
import { EmptyState } from '../components/EmptyState';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { useToast } from '../components/Toast';
import { LabDialog } from '../components/lab/LabDialog';
import { IconFlask, IconUpload } from '../components/icons';
import { saveLastWorkspace } from '../lastWorkspace';

const PAGE_SIZE = 20;
const BATCHES_PAGE_SIZE = 100;

interface CreateWorkspaceDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated: (workspaceId: string) => void;
}

function CreateWorkspaceDialog({ open, onClose, onCreated }: CreateWorkspaceDialogProps) {
  const toast = useToast();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [batches, setBatches] = useState<BatchListItem[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [nameError, setNameError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setName('');
    setDescription('');
    setNameError(null);
    setLoading(true);
    listBatches(1, BATCHES_PAGE_SIZE)
      .then((paged: Paged<BatchListItem>) => {
        const completed = paged.items.filter((batch) => batch.status === 'completed');
        setBatches(completed);
        setSelected(new Set(completed.map((batch) => batch.id)));
        setLoading(false);
      })
      .catch((err: unknown) => {
        setLoading(false);
        toast.show(err instanceof ApiError ? err.message : String(err), 'error');
      });
  }, [open, toast]);

  function toggleBatch(id: string): void {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  function selectAll(): void {
    setSelected(new Set(batches.map((batch) => batch.id)));
  }

  function selectNone(): void {
    setSelected(new Set());
  }

  function submit(): void {
    if (name.trim() === '') {
      setNameError(LAB_F_NAME_REQUIRED);
      return;
    }
    setNameError(null);
    setBusy(true);
    createWorkspace({
      name: name.trim(),
      description: description.trim() === '' ? undefined : description.trim(),
      sourceBatchIds: [...selected],
    })
      .then((result) => {
        setBusy(false);
        toast.show(
          format(TOAST_WORKSPACE_CREATED, {
            name: result.name,
            devices: result.counts.devices,
            interfaces: result.counts.interfaces,
            links: result.counts.links,
          }),
          'success',
        );
        onCreated(result.workspaceId);
      })
      .catch((err: unknown) => {
        setBusy(false);
        toast.show(err instanceof ApiError ? err.message : String(err), 'error');
      });
  }

  return (
    <LabDialog
      open={open}
      title={LAB_CREATE_TITLE}
      onClose={onClose}
      busy={busy}
      confirmLabel={BTN_CREATE}
      onConfirm={submit}
    >
      <div className="field">
        <label className="field-label" htmlFor="lab-ws-name">
          {LAB_F_NAME}
        </label>
        <input
          id="lab-ws-name"
          className="input"
          style={{ width: '100%' }}
          value={name}
          disabled={busy}
          onChange={(event) => {
            setName(event.target.value);
            setNameError(null);
          }}
        />
        {nameError !== null && (
          <div className="form-error" role="alert">
            {nameError}
          </div>
        )}
      </div>
      <div className="field">
        <label className="field-label" htmlFor="lab-ws-desc">
          {LAB_F_DESC}
        </label>
        <input
          id="lab-ws-desc"
          className="input"
          style={{ width: '100%' }}
          value={description}
          disabled={busy}
          onChange={(event) => setDescription(event.target.value)}
        />
      </div>
      <div className="field">
        <label className="field-label">{LAB_F_BATCHES}</label>
        <div className="modal-note" style={{ marginBottom: 8 }}>
          {LAB_F_BATCHES_HINT}
        </div>
        {loading ? (
          <div className="muted-note">{NO_VALUE}</div>
        ) : batches.length === 0 ? (
          <div className="muted-note">{LAB_NO_COMPLETED_BATCHES}</div>
        ) : (
          <>
            <div className="batch-check-tools">
              <button type="button" className="btn btn--secondary btn--sm" disabled={busy} onClick={selectAll}>
                {LAB_BATCH_SELECT_ALL}
              </button>
              <button type="button" className="btn btn--secondary btn--sm" disabled={busy} onClick={selectNone}>
                {LAB_BATCH_SELECT_NONE}
              </button>
            </div>
            <div className="batch-check-list">
              {batches.map((batch) => (
                <label key={batch.id} className="batch-check-item">
                  <input
                    type="checkbox"
                    checked={selected.has(batch.id)}
                    disabled={busy}
                    onChange={() => toggleBatch(batch.id)}
                  />
                  <span>{format(LAB_BATCH_OPTION, { workbook: batch.workbookName, imported: batch.rowsImported, total: batch.rowsTotal })}</span>
                </label>
              ))}
            </div>
          </>
        )}
      </div>
      {busy && (
        <div className="muted-note" role="status">
          {BTN_PROCESSING}
        </div>
      )}
    </LabDialog>
  );
}

export function LabWorkspacesPage() {
  const navigate = useNavigate();
  const toast = useToast();
  const [data, setData] = useState<Paged<WorkspaceSummary> | null>(null);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<WorkspaceSummary | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const load = useCallback((): void => {
    setLoading(true);
    setError(null);
    listWorkspaces(page, PAGE_SIZE)
      .then((paged) => {
        setData(paged);
        setLoading(false);
      })
      .catch((err: unknown) => {
        setError(err instanceof ApiError ? err.message : null);
        setLoading(false);
      });
  }, [page]);

  useEffect(() => {
    load();
  }, [load]);

  function confirmDelete(): void {
    const target = deleteTarget;
    if (target === null) return;
    setDeleteBusy(true);
    deleteWorkspace(target.id)
      .then(() => {
        setDeleteBusy(false);
        setDeleteTarget(null);
        toast.show(TOAST_WORKSPACE_DELETED, 'success');
        load();
      })
      .catch((err: unknown) => {
        setDeleteBusy(false);
        setDeleteTarget(null);
        toast.show(err instanceof ApiError ? err.message : String(err), 'error');
      });
  }

  const columns: TableColumn<WorkspaceSummary>[] = [
    { key: 'name', header: LAB_COL_NAME, render: (row) => <span className="cell-strong">{row.name}</span> },
    { key: 'description', header: LAB_COL_DESC, render: (row) => (row.description === '' ? <span className="muted-note">{NO_VALUE}</span> : row.description) },
    { key: 'devices', header: LAB_COL_DEVICES, render: (row) => row.deviceCount, align: 'right' },
    { key: 'interfaces', header: LAB_COL_INTERFACES, render: (row) => row.interfaceCount, align: 'right' },
    { key: 'links', header: LAB_COL_LINKS, render: (row) => row.linkCount, align: 'right' },
    {
      key: 'score',
      header: LAB_COL_SCORE,
      render: (row) =>
        row.lastRunScore === null || row.lastRunScore === undefined ? (
          <span className="muted-note">{NO_VALUE}</span>
        ) : (
          <span className={`chip ${row.lastRunScore >= 90 ? 'chip--success' : row.lastRunScore >= 60 ? 'chip--warning' : 'chip--error'}`}>
            {format(LAB_SCORE_VALUE, { score: row.lastRunScore })}
          </span>
        ),
      align: 'right',
    },
    { key: 'lastRun', header: LAB_COL_LAST_RUN, render: (row) => (row.lastRunId === null || row.lastRunId === undefined ? <span className="muted-note">{NO_VALUE}</span> : <span className="mono" title={row.lastRunId}>{row.lastRunId}</span>) },
    { key: 'createdAt', header: LAB_COL_CREATED_AT, render: (row) => formatDateTime(row.createdAt) },
    {
      key: 'actions',
      header: LAB_COL_ACTIONS,
      render: (row) => (
        <button
          type="button"
          className="btn btn--danger-ghost btn--sm"
          onClick={(event) => {
            event.stopPropagation();
            setDeleteTarget(row);
          }}
        >
          {BTN_DELETE_WORKSPACE}
        </button>
      ),
    },
  ];

  if (loading && data === null) {
    return <LoadingPanel />;
  }

  if (error !== null && data === null) {
    return <ErrorBlock message={error} onRetry={load} />;
  }

  if (data !== null && data.total === 0 && !error) {
    // Keep the create action reachable in the empty state — otherwise a
    // user with imported batches but no workspace yet could never create
    // the first one from this page (found in the orchestrator browser
    // smoke; the header button must never disappear). The dialogs must
    // ALSO mount here — this branch returns early, skipping the mounts at
    // the bottom of the main return.
    return (
      <>
        <div className="card">
          <div className="card-head">
            <h2 className="card-title">{LAB_TABLE_LABEL}</h2>
            <button type="button" className="btn btn--primary" onClick={() => setCreateOpen(true)}>
              <IconFlask />
              {BTN_CREATE_WORKSPACE}
            </button>
          </div>
          <EmptyState
            title={LAB_EMPTY_TITLE}
            description={LAB_EMPTY_DESC}
            icon={<IconFlask size={40} />}
            action={
              <Link className="btn btn--primary" to="/import">
                <IconUpload />
                {BTN_GO_IMPORT}
              </Link>
            }
          />
        </div>
        <CreateWorkspaceDialog
          open={createOpen}
          onClose={() => setCreateOpen(false)}
          onCreated={(workspaceId) => {
            setCreateOpen(false);
            saveLastWorkspace(workspaceId);
            navigate(`/lab/${workspaceId}`);
          }}
        />
      </>
    );
  }

  return (
    <>
      <div className="card">
        <div className="card-head">
          <h2 className="card-title">{LAB_TABLE_LABEL}</h2>
          <button type="button" className="btn btn--primary" onClick={() => setCreateOpen(true)}>
            <IconFlask />
            {BTN_CREATE_WORKSPACE}
          </button>
        </div>
        {error !== null && <ErrorBlock message={error} onRetry={load} />}
        <div className="table-wrap">
          <Table
            columns={columns}
            rows={data?.items ?? []}
            rowKey={(row) => row.id}
            loading={loading}
            onRowClick={(row) => {
              saveLastWorkspace(row.id);
              navigate(`/lab/${row.id}`);
            }}
            ariaLabel={LAB_TABLE_LABEL}
          />
        </div>
        {data !== null && data.total > 0 && (
          <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />
        )}
      </div>

      <CreateWorkspaceDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(workspaceId) => {
          setCreateOpen(false);
          saveLastWorkspace(workspaceId);
          navigate(`/lab/${workspaceId}`);
        }}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        title={LAB_DELETE_TITLE}
        message={format(LAB_DELETE_MESSAGE, { name: deleteTarget?.name ?? '' })}
        confirmLabel={BTN_DELETE_WORKSPACE}
        danger
        busy={deleteBusy}
        onConfirm={confirmDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  );
}
