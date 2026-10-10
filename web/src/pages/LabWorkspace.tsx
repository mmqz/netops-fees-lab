/**
 * 拓扑工作台页 /lab/:id：
 * - 页头：名称/描述/状态哈希 chip（mono 截断 + title 全量）/ 评分徽章 /
 *   设备·接口·链路计数 chips（up/down 分色）+ 六个动作按钮；
 * - 左 60% Cytoscape 拓扑画布 + 右 40% 标签页（设备/接口/链路/检查结果/变更集/检查点）；
 * - 画布 ↔ 表格双向选中联动；任何变更（ops/undo/restore/启停/配置）后统一刷新
 *   拓扑 + 工作台详情 + 各标签页（版本计数器）。
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import type { ApplyResult, CheckpointItem, SimOp, SimTopology, WorkspaceDetail } from '../api/types';
import {
  ApiError,
  applyOps,
  createCheckpoint,
  filledSeverityCounts,
  formatApiError,
  getTopology,
  getWorkspace,
  restoreCheckpoint,
  runChecks,
  undo,
} from '../api/client';
import {
  BTN_PROCESSING,
  LAB_BTN_APPLY_OPS,
  LAB_BTN_CREATE_CHECKPOINT,
  LAB_BTN_DIFF,
  LAB_BTN_EXPORT_REPORT,
  LAB_BTN_RUN_CHECKS,
  LAB_BTN_UNDO,
  LAB_SCORE_VALUE,
  LAB_TAB_CHANGESETS,
  LAB_TAB_CHECKPOINTS,
  LAB_TAB_CHECKS,
  LAB_TAB_DEVICES,
  LAB_TAB_INTERFACES,
  LAB_TAB_LINKS,
  LAB_UNDO_MESSAGE,
  LAB_UNDO_TITLE,
  LABWS_BACK_LIST,
  LABWS_CHIP_DEVICES,
  LABWS_CHIP_INTERFACES,
  LABWS_CHIP_LINKS,
  LABWS_DOWN_TITLE,
  LABWS_HASH_LABEL,
  LABWS_NOT_FOUND,
  LABWS_UP_TITLE,
  NO_VALUE,
  TOAST_CHECKS_DONE,
  TOAST_CHECKPOINT_CREATED,
  TOAST_OPS_DONE,
  TOAST_RESTORE_DONE,
  TOAST_RESTORE_NOOP,
  TOAST_UNDO_DONE,
  TOAST_VIEW_CHANGESETS,
  format,
} from '../i18n';
import { LoadingPanel } from '../components/Spinner';
import { ErrorBlock } from '../components/ErrorBlock';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { useToast } from '../components/Toast';
import type { ToastAction } from '../components/Toast';
import { TopologyCanvas } from '../components/lab/TopologyCanvas';
import { OpsDialog } from '../components/lab/OpsDialog';
import { DiffDialog } from '../components/lab/DiffDialog';
import { DeviceTab, InterfaceTab, LinkTab } from '../components/lab/EntityTabs';
import { ChecksTab } from '../components/lab/ChecksTab';
import { ChangeSetsTab } from '../components/lab/ChangeSetsTab';
import { CheckpointsTab } from '../components/lab/CheckpointsTab';
import { buildTopologyView } from '../components/lab/selection';
import type { SelectedEntity } from '../components/lab/selection';
import { clearLastWorkspace, saveLastWorkspace } from '../lastWorkspace';
import { useSeq } from '../hooks/useSeq';

type LabTab = 'devices' | 'interfaces' | 'links' | 'checks' | 'changesets' | 'checkpoints';

const TABS: Array<{ value: LabTab; label: string }> = [
  { value: 'devices', label: LAB_TAB_DEVICES },
  { value: 'interfaces', label: LAB_TAB_INTERFACES },
  { value: 'links', label: LAB_TAB_LINKS },
  { value: 'checks', label: LAB_TAB_CHECKS },
  { value: 'changesets', label: LAB_TAB_CHANGESETS },
  { value: 'checkpoints', label: LAB_TAB_CHECKPOINTS },
];

function scoreChipClass(score: number): string {
  if (score >= 90) return 'chip--success';
  if (score >= 60) return 'chip--warning';
  return 'chip--error';
}

const LAB_TAB_VALUES: LabTab[] = ['devices', 'interfaces', 'links', 'checks', 'changesets', 'checkpoints'];

/** 标签页选择按工作台持久化（sessionStorage，刷新后保留，P2）。 */
function readPersistedTab(workspaceId: string): LabTab {
  if (typeof sessionStorage === 'undefined') return 'devices';
  try {
    const value = sessionStorage.getItem(`netops-lab.tab.${workspaceId}`);
    if (value !== null && (LAB_TAB_VALUES as string[]).includes(value)) return value as LabTab;
  } catch {
    // 隐私模式 / 配额异常：静默回退默认标签
  }
  return 'devices';
}

function writePersistedTab(workspaceId: string, tab: LabTab): void {
  if (typeof sessionStorage === 'undefined') return;
  try {
    sessionStorage.setItem(`netops-lab.tab.${workspaceId}`, tab);
  } catch {
    // 写失败不影响功能
  }
}

function CountChip({ label, total, up, down }: { label: string; total: number; up: number; down: number }) {
  return (
    <span className="chip lab-chip-split" title={`${label} ${total}`}>
      {label} {total}
      {up > 0 && (
        <span className="lab-up" title={format(LABWS_UP_TITLE, { n: up })}>
          ↑{up}
        </span>
      )}
      {down > 0 && (
        <span className="lab-down" title={format(LABWS_DOWN_TITLE, { n: down })}>
          ↓{down}
        </span>
      )}
    </span>
  );
}

export function LabWorkspacePage() {
  const { id } = useParams<{ id: string }>();
  const workspaceId = id ?? '';
  const toast = useToast();

  const [detail, setDetail] = useState<WorkspaceDetail | null>(null);
  const [topology, setTopology] = useState<SimTopology | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  /** 错误次行技术原文（FX2-4）。 */
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [version, setVersion] = useState(0);
  const [selected, setSelected] = useState<SelectedEntity | null>(null);
  const [tab, setTab] = useState<LabTab>(() => readPersistedTab(workspaceId));
  const [opsOpen, setOpsOpen] = useState(false);
  const [diffOpen, setDiffOpen] = useState(false);
  const [undoConfirm, setUndoConfirm] = useState(false);
  const [opsBusy, setOpsBusy] = useState(false);
  const [checksBusy, setChecksBusy] = useState(false);
  const [undoBusy, setUndoBusy] = useState(false);
  const [cpBusy, setCpBusy] = useState(false);
  /** 检查点输入聚焦信号：每次点击页头按钮都 +1（AUD2-17）。 */
  const [focusCpSignal, setFocusCpSignal] = useState(0);
  /** toast「查看变更集」定位后要闪亮的变更集行（W9）。 */
  const [highlightCs, setHighlightCs] = useState<string | undefined>(undefined);
  /** 取数守卫（AUD2-8）：/lab/:idA → /lab/:idB 路由复用 + refreshAll 重叠时丢弃旧响应。 */
  const loadGuard = useSeq();

  const load = useCallback((): void => {
    if (workspaceId === '') return;
    const isCurrent = loadGuard.begin();
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    setNotFound(false);
    Promise.all([getWorkspace(workspaceId), getTopology(workspaceId)])
      .then((results) => {
        if (!isCurrent()) return;
        setDetail(results[0]);
        setTopology(results[1]);
        setLoading(false);
        // 仅在确认存在后持久化（避免把 404 的死 id 重新写回侧栏快捷入口——AUD2-11）
        saveLastWorkspace(workspaceId);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        setLoading(false);
        if (err instanceof ApiError && (err.status === 404 || err.code === 'not_found')) {
          setNotFound(true);
          clearLastWorkspace(workspaceId);
        }
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
      });
  }, [workspaceId, loadGuard]);

  useEffect(() => {
    load();
    setSelected(null);
    // 工作台切换时恢复各自的标签页选择（P2）
    setTab(readPersistedTab(workspaceId));
  }, [load, workspaceId]);

  useEffect(() => {
    writePersistedTab(workspaceId, tab);
  }, [workspaceId, tab]);

  // —— 统一动作 ——

  const refresh = useCallback((): void => {
    setVersion((v) => v + 1);
  }, []);

  const refreshAll = useCallback((): void => {
    load();
    refresh();
  }, [load, refresh]);

  const showApiError = useCallback(
    (err: unknown): void => {
      const display = formatApiError(err);
      toast.show(display.title, 'error', { detail: display.detail });
    },
    [toast],
  );

  /** 成功 toast 的「查看变更集」动作（W9）：跳转右侧标签页并闪光定位新变更集。 */
  const viewChangesetsAction = useCallback(
    (changesetId?: string): { action: ToastAction } => ({
      action: {
        label: TOAST_VIEW_CHANGESETS,
        onClick: () => {
          setTab('changesets');
          if (changesetId !== undefined && changesetId !== '') {
            setHighlightCs(changesetId);
          }
        },
      },
    }),
    [],
  );

  const applyOpsRequest = useCallback(
    (ops: SimOp[], description: string): void => {
      setOpsBusy(true);
      applyOps(workspaceId, { ops, description, runChecks: true })
        .then((result) => {
          setOpsBusy(false);
          toast.show(
            format(TOAST_OPS_DONE, { id: result.changesetId, n: result.opsApplied }),
            'success',
            viewChangesetsAction(result.changesetId),
          );
          refreshAll();
        })
        .catch((err: unknown) => {
          setOpsBusy(false);
          showApiError(err);
        });
    },
    [workspaceId, toast, refreshAll, showApiError, viewChangesetsAction],
  );

  const runChecksNow = useCallback((): void => {
    setChecksBusy(true);
    runChecks(workspaceId)
      .then((result) => {
        setChecksBusy(false);
        const counts = filledSeverityCounts(result.counts);
        toast.show(
          format(TOAST_CHECKS_DONE, {
            error: counts.error,
            warning: counts.warning,
            info: counts.info,
            score: result.score,
          }),
          'success',
        );
        refreshAll();
      })
      .catch((err: unknown) => {
        setChecksBusy(false);
        showApiError(err);
      });
  }, [workspaceId, toast, refreshAll, showApiError]);

  const undoLatest = useCallback(
    (changesetId?: string): void => {
      setUndoBusy(true);
      undo(workspaceId, changesetId)
        .then((result) => {
          setUndoBusy(false);
          toast.show(
            format(TOAST_UNDO_DONE, { id: result.reverted, newId: result.changesetId }),
            'success',
            viewChangesetsAction(result.changesetId),
          );
          refreshAll();
        })
        .catch((err: unknown) => {
          setUndoBusy(false);
          showApiError(err);
        });
    },
    [workspaceId, toast, refreshAll, showApiError, viewChangesetsAction],
  );

  const createCheckpointNow = useCallback(
    (label: string): Promise<boolean> => {
      setCpBusy(true);
      return createCheckpoint(workspaceId, label)
        .then(() => {
          setCpBusy(false);
          toast.show(format(TOAST_CHECKPOINT_CREATED, { label }), 'success');
          refreshAll();
          return true;
        })
        .catch((err: unknown) => {
          setCpBusy(false);
          showApiError(err);
          return false;
        });
    },
    [workspaceId, toast, refreshAll, showApiError],
  );

  const restoreCheckpointNow = useCallback(
    (checkpoint: CheckpointItem): void => {
      setCpBusy(true);
      restoreCheckpoint(workspaceId, checkpoint.checkpointId)
        .then((result) => {
          setCpBusy(false);
          if (result.changesetId === null || result.changesetId === undefined || result.changesetId === '') {
            toast.show(TOAST_RESTORE_NOOP, 'info');
          } else {
            toast.show(
              format(TOAST_RESTORE_DONE, { label: checkpoint.label, id: result.changesetId }),
              'success',
              viewChangesetsAction(result.changesetId),
            );
          }
          refreshAll();
        })
        .catch((err: unknown) => {
          setCpBusy(false);
          showApiError(err);
        });
    },
    [workspaceId, toast, refreshAll, showApiError, viewChangesetsAction],
  );

  // —— 画布选中 → 右侧切换标签并定位 ——

  const handleCanvasSelect = useCallback((entity: SelectedEntity): void => {
    setSelected(entity);
    switch (entity.kind) {
      case 'device':
        setTab('devices');
        break;
      case 'interface':
        setTab('interfaces');
        break;
      case 'link':
        setTab('links');
        break;
    }
  }, []);

  const view = useMemo(() => buildTopologyView(topology), [topology]);

  function exportReport(): void {
    window.open(`/api/lab/report?workspaceId=${encodeURIComponent(workspaceId)}&format=html`, '_blank', 'noopener');
  }

  function jumpToCheckpoints(): void {
    setTab('checkpoints');
    setFocusCpSignal((n) => n + 1);
  }

  const anyBusy = opsBusy || checksBusy || undoBusy || cpBusy;

  if (loading && detail === null) {
    return <LoadingPanel />;
  }

  if (notFound) {
    return (
      <div className="card">
        <ErrorBlock message={LABWS_NOT_FOUND} />
        <div style={{ marginTop: 12 }}>
          <Link className="btn btn--primary" to="/lab">
            {LABWS_BACK_LIST}
          </Link>
        </div>
      </div>
    );
  }

  if (error !== null && detail === null) {
    return <ErrorBlock message={error} detail={errorDetail} onRetry={load} />;
  }

  if (detail === null) {
    return <ErrorBlock onRetry={load} />;
  }

  const counts = detail.counts;
  const score = detail.latestRun?.score ?? null;

  return (
    <>
      <div className="card" style={{ marginBottom: 16 }}>
        <div className="lab-head-meta">
          <h1 className="lab-head-name">{detail.name}</h1>
          <span className="chip chip--mono lab-hash-chip" title={`${LABWS_HASH_LABEL} ${detail.workspaceHash}`}>
            {detail.workspaceHash}
          </span>
          {score === null ? (
            <span className="chip chip--muted">{NO_VALUE}</span>
          ) : (
            <span className={`chip ${scoreChipClass(score)}`}>{format(LAB_SCORE_VALUE, { score })}</span>
          )}
          <CountChip label={LABWS_CHIP_DEVICES} total={counts.devices} up={counts.upDevices} down={counts.downDevices} />
          <CountChip
            label={LABWS_CHIP_INTERFACES}
            total={counts.interfaces}
            up={counts.upInterfaces}
            down={counts.interfaces - counts.upInterfaces}
          />
          <CountChip label={LABWS_CHIP_LINKS} total={counts.links} up={counts.upLinks} down={counts.links - counts.upLinks} />
        </div>
        {detail.description !== '' && <p className="lab-desc">{detail.description}</p>}
        <div className="lab-head-actions">
          <button type="button" className="btn btn--primary" disabled={anyBusy} onClick={runChecksNow}>
            {checksBusy ? <span className="spinner" style={{ width: 14, height: 14 }} /> : null}
            {checksBusy ? BTN_PROCESSING : LAB_BTN_RUN_CHECKS}
          </button>
          <button type="button" className="btn btn--secondary" disabled={anyBusy} onClick={() => setOpsOpen(true)}>
            {LAB_BTN_APPLY_OPS}
          </button>
          <button type="button" className="btn btn--secondary" disabled={anyBusy} onClick={() => setUndoConfirm(true)}>
            {undoBusy ? <span className="spinner" style={{ width: 14, height: 14 }} /> : null}
            {undoBusy ? BTN_PROCESSING : LAB_BTN_UNDO}
          </button>
          <button type="button" className="btn btn--secondary" disabled={anyBusy} onClick={jumpToCheckpoints}>
            {LAB_BTN_CREATE_CHECKPOINT}
          </button>
          <button type="button" className="btn btn--secondary" disabled={anyBusy} onClick={() => setDiffOpen(true)}>
            {LAB_BTN_DIFF}
          </button>
          <button type="button" className="btn btn--secondary" onClick={exportReport}>
            {LAB_BTN_EXPORT_REPORT}
          </button>
          <Link className="btn btn--secondary" to="/lab" style={{ marginLeft: 'auto' }}>
            {LABWS_BACK_LIST}
          </Link>
        </div>
        {error !== null && <ErrorBlock message={error} detail={errorDetail} onRetry={load} />}
      </div>

      <div className="lab-split">
        <TopologyCanvas topology={topology} selected={selected} onSelect={handleCanvasSelect} persistKey={workspaceId} />

        <div className="card lab-panel">
          <div className="tabs" role="tablist">
            {TABS.map((item) => (
              <button
                key={item.value}
                type="button"
                role="tab"
                aria-selected={tab === item.value}
                className={`tab${tab === item.value ? ' tab--active' : ''}`}
                onClick={() => {
                  setTab(item.value);
                  if (item.value === 'checkpoints') {
                    setFocusCpSignal(0);
                  }
                }}
              >
                {item.label}
              </button>
            ))}
          </div>
          <div className="lab-panel-body">
            {tab === 'devices' && (
              <DeviceTab view={view} selected={selected} onSelect={setSelected} applyOps={applyOpsRequest} opsBusy={opsBusy} />
            )}
            {tab === 'interfaces' && (
              <InterfaceTab view={view} selected={selected} onSelect={setSelected} applyOps={applyOpsRequest} opsBusy={opsBusy} />
            )}
            {tab === 'links' && (
              <LinkTab view={view} selected={selected} onSelect={setSelected} applyOps={applyOpsRequest} opsBusy={opsBusy} />
            )}
            {tab === 'checks' && (
              <ChecksTab workspaceId={workspaceId} version={version} onRunChecks={runChecksNow} checksBusy={checksBusy} />
            )}
            {tab === 'changesets' && (
              <ChangeSetsTab
                workspaceId={workspaceId}
                version={version}
                latestChangeset={detail.latestChangeset}
                onUndo={undoLatest}
                undoBusy={undoBusy}
                highlightId={highlightCs}
              />
            )}
            {tab === 'checkpoints' && (
              <CheckpointsTab
                workspaceId={workspaceId}
                version={version}
                onCreateCheckpoint={createCheckpointNow}
                onRestore={restoreCheckpointNow}
                cpBusy={cpBusy}
                focusSignal={focusCpSignal}
              />
            )}
          </div>
        </div>
      </div>

      <OpsDialog
        open={opsOpen}
        workspaceId={workspaceId}
        view={view}
        onClose={() => setOpsOpen(false)}
        onApplied={(result: ApplyResult) => {
          setOpsOpen(false);
          toast.show(
            format(TOAST_OPS_DONE, { id: result.changesetId, n: result.opsApplied }),
            'success',
            viewChangesetsAction(result.changesetId),
          );
          refreshAll();
        }}
        onError={showApiError}
      />

      <DiffDialog open={diffOpen} workspaceId={workspaceId} onClose={() => setDiffOpen(false)} onError={showApiError} />

      <ConfirmDialog
        open={undoConfirm}
        title={LAB_UNDO_TITLE}
        message={LAB_UNDO_MESSAGE}
        confirmLabel={LAB_BTN_UNDO}
        busy={undoBusy}
        onConfirm={() => {
          setUndoConfirm(false);
          undoLatest();
        }}
        onCancel={() => setUndoConfirm(false)}
      />
    </>
  );
}
