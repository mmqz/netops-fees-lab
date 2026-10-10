/**
 * 拓扑工作台页 /lab/:id：
 * - 页头：名称/描述/状态哈希 chip（mono 截断 + title 全量）/ 评分徽章 /
 *   设备·接口·链路计数 chips（up/down 分色）+ 七个动作按钮（含折叠面板）；
 * - 左画布 Cytoscape + 右标签页（设备/接口/链路/检查结果/变更集/检查点）；
 *   分栏比例可拖拽（把手 + ← → 键盘微调，localStorage 持久化）；右侧面板可
 *   折叠（画布占满工作区宽）；画布支持 zen 全屏工作模式（Esc 退出，
 *   sessionStorage 持久化；见 TopologyCanvas）；
 * - 画布 ↔ 表格双向选中联动；任何变更（ops/undo/restore/启停/配置）后统一刷新
 *   拓扑 + 工作台详情 + 各标签页（版本计数器）。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties, KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent } from 'react';
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
  LAB_SPLIT_COLLAPSE,
  LAB_SPLIT_COLLAPSE_TITLE,
  LAB_SPLIT_EXPAND,
  LAB_SPLIT_EXPAND_TITLE,
  LAB_SPLIT_HANDLE_LABEL,
  LAB_SPLIT_HANDLE_TITLE,
  LAB_SPLIT_PCT_VALUE,
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
import { IconPanelCollapse, IconPanelExpand } from '../components/icons';
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

// —— FX-5：zen 全屏 / 可拖拽分栏 / 面板折叠的持久化 ——

/** 分栏比例（画布列占比 %）：clamp 范围与默认值。 */
const SPLIT_MIN = 30;
const SPLIT_MAX = 85;
const SPLIT_DEFAULT = 60;
/** 键盘微调步长（%）。 */
const SPLIT_KEYBOARD_STEP = 2;
/** 分栏几何（与 global.css .lab-split 一致）：两侧 gap 5px + 把手 6px = 16px。 */
const SPLIT_GAP_PX = 5;
const SPLIT_HANDLE_PX = 6;
const SPLIT_GUTTER_PX = SPLIT_GAP_PX * 2 + SPLIT_HANDLE_PX;

const clampSplit = (pct: number): number => Math.min(Math.max(Math.round(pct), SPLIT_MIN), SPLIT_MAX);

/** 分栏比例持久化（localStorage：跨会话保留用户习惯）。 */
function readSplitPct(workspaceId: string): number {
  try {
    const raw = localStorage.getItem(`netops-lab.split.${workspaceId}`);
    if (raw !== null) {
      const value = Number(raw);
      if (Number.isFinite(value)) return clampSplit(value);
    }
  } catch {
    // 隐私模式 / 配额异常：回退默认
  }
  return SPLIT_DEFAULT;
}

function writeSplitPct(workspaceId: string, pct: number): void {
  try {
    localStorage.setItem(`netops-lab.split.${workspaceId}`, String(clampSplit(pct)));
  } catch {
    // 写失败不影响功能
  }
}

/** 右侧面板折叠持久化（localStorage，跨会话）。 */
function readPanelCollapsed(workspaceId: string): boolean {
  try {
    return localStorage.getItem(`netops-lab.panel.${workspaceId}`) === '1';
  } catch {
    return false;
  }
}

function writePanelCollapsed(workspaceId: string, collapsed: boolean): void {
  try {
    localStorage.setItem(`netops-lab.panel.${workspaceId}`, collapsed ? '1' : '0');
  } catch {
    // 写失败不影响功能
  }
}

/** zen 状态持久化（sessionStorage：刷新保持，但不跨会话——用户下次进来还想要正常布局）。 */
function readZen(workspaceId: string): boolean {
  try {
    return sessionStorage.getItem(`netops-lab.zen.${workspaceId}`) === '1';
  } catch {
    return false;
  }
}

function writeZen(workspaceId: string, zen: boolean): void {
  try {
    if (zen) {
      sessionStorage.setItem(`netops-lab.zen.${workspaceId}`, '1');
    } else {
      sessionStorage.removeItem(`netops-lab.zen.${workspaceId}`);
    }
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
  // —— FX-5：zen 全屏 / 可拖拽分栏 / 面板折叠 ——
  /** zen 全屏工作模式（sessionStorage 持久化，刷新保持；Esc 退出）。 */
  const [zen, setZen] = useState(() => readZen(workspaceId));
  /** 分栏比例（画布列 %；拖动中 rAF 节流 setState，拖完落盘 localStorage）。 */
  const [splitPct, setSplitPct] = useState(() => readSplitPct(workspaceId));
  /** 分栏拖拽进行中（body 光标/禁选 + 挂起画布逐帧 resize）。 */
  const [splitDragging, setSplitDragging] = useState(false);
  /** 右侧面板折叠（画布占满工作区宽；localStorage 持久化）。 */
  const [panelCollapsed, setPanelCollapsed] = useState(() => readPanelCollapsed(workspaceId));
  /** splitPct 镜像（拖动/键盘闭包读最新值）。 */
  const splitPctRef = useRef(splitPct);
  /** 拖拽激活镜像（React 状态渲染滞后于首帧 pointermove 时用 ref 判定）。 */
  const splitDragActiveRef = useRef(false);
  /** 分栏容器（拖动坐标的参照系）。 */
  const splitRef = useRef<HTMLDivElement | null>(null);
  /** 拖拽 rAF 节流句柄。 */
  const splitRafRef = useRef<number | null>(null);
  /** 抓取点相对把手中心的偏移（拖动不跳变）。 */
  const grabOffsetRef = useRef(0);

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

  // 工作台切换（路由复用组件）→ 恢复各自的 zen/分栏/面板折叠偏好（FX-5）
  useEffect(() => {
    setZen(readZen(workspaceId));
    const pct = readSplitPct(workspaceId);
    splitPctRef.current = pct;
    setSplitPct(pct);
    setPanelCollapsed(readPanelCollapsed(workspaceId));
  }, [workspaceId]);

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

  // —— FX-5：zen / 分栏拖拽 / 面板折叠 ——

  const handleZenChange = useCallback(
    (next: boolean): void => {
      setZen(next);
      writeZen(workspaceId, next);
    },
    [workspaceId],
  );

  /** 卸载安全：拖拽中离开页面（路由切换/关闭）时清 body 光标类与挂起 rAF。 */
  useEffect(
    () => () => {
      document.body.classList.remove('lab-split-dragging');
      if (splitRafRef.current !== null) cancelAnimationFrame(splitRafRef.current);
      splitRafRef.current = null;
    },
    [],
  );

  /** 把手中心 X（client 坐标）：画布列宽 + 左 gap + 把手半宽。 */
  function handleCenterX(): number {
    const el = splitRef.current;
    if (el === null) return 0;
    const rect = el.getBoundingClientRect();
    const col1 = (rect.width - SPLIT_GUTTER_PX) * (splitPctRef.current / 100);
    return rect.left + col1 + SPLIT_GAP_PX + SPLIT_HANDLE_PX / 2;
  }

  function onHandlePointerDown(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0 || splitRef.current === null) return;
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // 旧浏览器无 pointer capture：退化为点击式调整
    }
    grabOffsetRef.current = event.clientX - handleCenterX();
    splitDragActiveRef.current = true;
    setSplitDragging(true);
    // 拖动中：全局列光标 + 禁止文本选择（body 类，结束/卸载时移除）
    document.body.classList.add('lab-split-dragging');
  }

  function applyPctFromPointer(clientX: number): void {
    const el = splitRef.current;
    if (el === null) return;
    const rect = el.getBoundingClientRect();
    const usable = rect.width - SPLIT_GUTTER_PX;
    if (usable <= 0) return;
    const col1 = clientX - grabOffsetRef.current - SPLIT_GAP_PX - SPLIT_HANDLE_PX / 2 - rect.left;
    const pct = clampSplit((col1 / usable) * 100);
    splitPctRef.current = pct;
    // rAF 节流 setState（每帧至多一次重渲染；拖完 pointerup 冲刷）
    if (splitRafRef.current !== null) return;
    splitRafRef.current = requestAnimationFrame(() => {
      splitRafRef.current = null;
      setSplitPct(splitPctRef.current);
    });
  }

  function onHandlePointerMove(event: ReactPointerEvent<HTMLDivElement>): void {
    if (!splitDragActiveRef.current) return;
    applyPctFromPointer(event.clientX);
  }

  function onHandlePointerUp(event: ReactPointerEvent<HTMLDivElement>): void {
    if (!splitDragActiveRef.current) return;
    splitDragActiveRef.current = false;
    setSplitDragging(false);
    document.body.classList.remove('lab-split-dragging');
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      // 忽略（未捕获时）
    }
    if (splitRafRef.current !== null) {
      cancelAnimationFrame(splitRafRef.current);
      splitRafRef.current = null;
    }
    // 冲刷最后一帧 + 落盘；TopologyCanvas 在 resizeSuspended 翻回 false 后补一次 resize
    setSplitPct(splitPctRef.current);
    writeSplitPct(workspaceId, splitPctRef.current);
  }

  /** 键盘微调：← → ±2%（a11y：role=separator 可聚焦）。 */
  function onHandleKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const delta = event.key === 'ArrowLeft' ? -SPLIT_KEYBOARD_STEP : SPLIT_KEYBOARD_STEP;
    const next = clampSplit(splitPctRef.current + delta);
    if (next === splitPctRef.current) return;
    splitPctRef.current = next;
    setSplitPct(next);
    writeSplitPct(workspaceId, next);
  }

  function togglePanelCollapsed(): void {
    const next = !panelCollapsed;
    setPanelCollapsed(next);
    writePanelCollapsed(workspaceId, next);
  }

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

  // 注意：不能在此处（早退 return 之后）调用 useMemo——Hook 顺序必须稳定（React #310）；
  // splitStyle 是轻量字面量，直接构造即可。
  const pctRounded = Math.round(splitPct);
  const splitStyle = {
    '--split-a': `${pctRounded}fr`,
    '--split-b': `${100 - pctRounded}fr`,
  } as CSSProperties;

  return (
    <>
      <div className="card lab-pagehead">
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
          {/* FX-5：折叠/展开右侧数据面板（localStorage 持久化） */}
          <button
            type="button"
            className="btn btn--secondary"
            title={panelCollapsed ? LAB_SPLIT_EXPAND_TITLE : LAB_SPLIT_COLLAPSE_TITLE}
            onClick={togglePanelCollapsed}
          >
            {panelCollapsed ? <IconPanelExpand size={16} /> : <IconPanelCollapse size={16} />}
            {panelCollapsed ? LAB_SPLIT_EXPAND : LAB_SPLIT_COLLAPSE}
          </button>
          <Link className="btn btn--secondary" to="/lab" style={{ marginLeft: 'auto' }}>
            {LABWS_BACK_LIST}
          </Link>
        </div>
        {error !== null && <ErrorBlock message={error} detail={errorDetail} onRetry={load} />}
      </div>

      {/* FX-5：可拖拽分栏（画布 | 6px 把手 | 面板；比例经 CSS 变量注入，折叠/zen/
          窄屏由修饰类与媒体查询覆盖——变量不阻碍层叠）。zen 时画布卡片 fixed
          inset:0 盖住整页，把手不渲染。 */}
      <div
        ref={splitRef}
        className={`lab-split${panelCollapsed ? ' lab-split--collapsed' : ''}${zen ? ' lab-split--zen' : ''}`}
        style={splitStyle}
      >
        <TopologyCanvas
          topology={topology}
          selected={selected}
          onSelect={handleCanvasSelect}
          persistKey={workspaceId}
          zen={zen}
          onZenChange={handleZenChange}
          resizeSuspended={splitDragging}
        />
        {!panelCollapsed && !zen && (
          <div
            role="separator"
            aria-orientation="vertical"
            aria-label={LAB_SPLIT_HANDLE_LABEL}
            aria-valuemin={SPLIT_MIN}
            aria-valuemax={SPLIT_MAX}
            aria-valuenow={pctRounded}
            aria-valuetext={format(LAB_SPLIT_PCT_VALUE, { pct: pctRounded, rest: 100 - pctRounded })}
            title={format(LAB_SPLIT_HANDLE_TITLE, { pct: pctRounded })}
            tabIndex={0}
            className={`lab-split-handle${splitDragging ? ' is-dragging' : ''}`}
            onPointerDown={onHandlePointerDown}
            onPointerMove={onHandlePointerMove}
            onPointerUp={onHandlePointerUp}
            onPointerCancel={onHandlePointerUp}
            onKeyDown={onHandleKeyDown}
          />
        )}

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
