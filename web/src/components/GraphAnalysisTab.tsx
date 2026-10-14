/**
 * 资源详情「影响分析」页签（v0.9-R3）：
 *
 * 四个子模式（分段控件，默认「根因分析」）：
 * - 根因分析（impact direction=down，§10.5③）：从本资源向下追查异常
 *   根因——候选含中文判定理由 + BFS 距离 + 受影响面；
 * - 影响面（impact direction=up，§10.6② 后端默认）：本资源（异常源）
 *   向上汇总业务受损面；
 * - 向下穿透（traverse down）：层序 BFS 逐层下探（节点按 depth 分组）；
 * - 向上汇总（traverse up）：反向到达业务/客户层。
 *
 * 设计约束：
 * - 懒加载：首次切到页签才发请求（与 metrics 页签同一模式）；
 * - **只读**：本页签是分析视图，不提供任何写操作（资源域写仅限人类
 *   通过既有确认通道——与 MCP 红线同源）；
 * - 节点行点击 → /resources?focus=<id>（复用深链，抽屉换资源——
 *   W31 地图↔台账联动同款语义）；
 * - 空集合渲染为「无」文案而非空白（audit §2.6 的 UI 面）。
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import type { ImpactDocument, TraverseDocument } from '../api/types';
import { formatApiError, getImpact, getTraverse } from '../api/client';
import {
  format,
  GRAPH_CANDIDATES_COUNT,
  RESOURCE_TYPE_LABELS,
  GRAPH_MODE_IMPACT,
  GRAPH_MODE_RCA,
  GRAPH_MODE_TRAVERSE_DOWN,
  GRAPH_MODE_TRAVERSE_UP,
  GRAPH_CANDIDATES_TITLE,
  GRAPH_CANDIDATES_EMPTY,
  GRAPH_IMPACTED_TITLE,
  GRAPH_IMPACTED_EMPTY,
  GRAPH_TRAVERSE_TITLE,
  GRAPH_TRAVERSE_EMPTY,
  GRAPH_NODES_UNIT,
  GRAPH_EDGES_UNIT,
  GRAPH_CROSSLAYER_UNIT,
  GRAPH_TRUNCATED_HINT,
  GRAPH_DEPTH_UNIT,
  GRAPH_RUNSTATE_ABNORMAL,
  GRAPH_RUNSTATE_NORMAL,
  GRAPH_NODE_LINK_TITLE,
} from '../i18n';
import { ErrorBlock } from './ErrorBlock';
import { Spinner } from './Spinner';

/** 层号 → 层名（设计 §3.1 七层模型；L0 空间 → L6 业务）。 */
const LAYER_NAMES: Record<number, string> = {
  0: 'L0 空间层',
  1: 'L1 物理层·设备',
  2: 'L2 物理层·光',
  3: 'L3 传输层',
  4: 'L4 接入层',
  5: 'L5 逻辑层',
  6: 'L6 业务层',
};

function layerName(layer: number): string {
  return LAYER_NAMES[layer] ?? `L${layer}`;
}

/** 层族色板（镜像 TypeChip 的层族分配）：节点自带 layer，按层配色；
 *  未知层号回落灰 chip（防御——图查询的 type/layer 以 string/number 透传）。 */
const TYPE_VARIANTS_BY_LAYER: Record<number, string> = {
  0: 'chip--site',
  1: 'chip--device',
  2: 'chip--port',
  3: 'chip--circuit',
  4: 'chip--base-station',
  5: 'chip--ptp-port',
  6: 'chip--machine-room',
};

function graphTypeVariant(layer: number, type: string): string {
  if (!(type in RESOURCE_TYPE_LABELS)) return 'chip--muted';
  return TYPE_VARIANTS_BY_LAYER[layer] ?? 'chip--muted';
}

type GraphMode = 'rca' | 'impactUp' | 'traverseDown' | 'traverseUp';

interface NodeRow {
  id: string;
  type: string;
  name: string;
  layer: number;
  depth?: number;
  runState: string;
}

/** 节点行：类型 chip + 名称/键 + 层 chip + 运行态 + 距根距离。 */
function NodeLine({ node, extra }: { node: NodeRow; extra?: string }) {
  // 异常判定与引擎一致（§10.5）：run_state ∈ {down, degraded}；缺测≠异常。
  const abnormal = node.runState === 'down' || node.runState === 'degraded';
  const label = node.name !== '' ? node.name : node.id;
  return (
    <div className="graph-node-row">
      <span className={`chip ${graphTypeVariant(node.layer, node.type)}`}>
        {RESOURCE_TYPE_LABELS[node.type as keyof typeof RESOURCE_TYPE_LABELS] ?? node.type}
      </span>
      <Link
        className="entity-link"
        to={`/resources?focus=${encodeURIComponent(node.id)}`}
        title={GRAPH_NODE_LINK_TITLE}
      >
        {label}
      </Link>
      <span className="chip chip--mono">{layerName(node.layer)}</span>
      {abnormal ? (
        <span className="chip chip--error">{GRAPH_RUNSTATE_ABNORMAL}</span>
      ) : node.runState === 'normal' ? (
        <span className="chip chip--muted">{GRAPH_RUNSTATE_NORMAL}</span>
      ) : null}
      {extra !== undefined && extra !== '' && <span className="muted-note">{extra}</span>}
    </div>
  );
}

export function GraphAnalysisTab({ resourceId }: { resourceId: string }) {
  const [mode, setMode] = useState<GraphMode>('rca');
  const [impact, setImpact] = useState<ImpactDocument | null>(null);
  const [traverse, setTraverse] = useState<TraverseDocument | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const seqRef = useRef(0);

  const load = useCallback((): void => {
    const seq = ++seqRef.current;
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    const promise =
      mode === 'rca'
        ? getImpact(resourceId, 'down')
        : mode === 'impactUp'
          ? getImpact(resourceId, 'up')
          : getTraverse(resourceId, mode === 'traverseDown' ? 'down' : 'up');
    promise
      .then((doc) => {
        if (seqRef.current !== seq) return;
        if (mode === 'rca' || mode === 'impactUp') {
          setImpact(doc as ImpactDocument);
        } else {
          setTraverse(doc as TraverseDocument);
        }
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (seqRef.current !== seq) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [resourceId, mode]);

  useEffect(() => {
    load();
  }, [load]);

  /** traverse 节点按 depth 分组（depth 内按层号排序）。 */
  function traverseGroups(): Array<{ depth: number; nodes: NodeRow[] }> {
    if (traverse === null) return [];
    const byDepth = new Map<number, NodeRow[]>();
    for (const n of traverse.nodes) {
      if (n.id === traverse.root.id) continue;
      const list = byDepth.get(n.depth) ?? [];
      list.push({ id: n.id, type: n.type, name: n.name, layer: n.layer, depth: n.depth, runState: n.runState });
      byDepth.set(n.depth, list);
    }
    const groups = [...byDepth.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([depth, nodes]) => ({
        depth,
        nodes: nodes.sort((a, b) => a.layer - b.layer),
      }));
    return groups;
  }

  const crossLayerCount =
    traverse?.edges.filter((e) => e.crossLayer).length ?? 0;

  return (
    <div className="graph-tab">
      <div className="graph-mode" role="group">
        {(
          [
            ['rca', GRAPH_MODE_RCA],
            ['impactUp', GRAPH_MODE_IMPACT],
            ['traverseDown', GRAPH_MODE_TRAVERSE_DOWN],
            ['traverseUp', GRAPH_MODE_TRAVERSE_UP],
          ] as Array<[GraphMode, string]>
        ).map(([value, label]) => (
          <button
            key={value}
            type="button"
            className={`btn btn--sm${mode === value ? ' btn--primary' : ' btn--secondary'}`}
            aria-pressed={mode === value}
            onClick={() => setMode(value)}
          >
            {label}
          </button>
        ))}
      </div>

      {error !== null && <ErrorBlock message={error} detail={errorDetail} onRetry={load} />}
      {loading && <Spinner size={16} />}

      {!loading && error === null && (mode === 'rca' || mode === 'impactUp') && impact !== null && (
        <div className="graph-impact">
          <div className="graph-summary">
            <span className="muted-note">
              {format(GRAPH_NODES_UNIT, { n: impact.impacted.length })} ·{' '}
              {format(GRAPH_CANDIDATES_COUNT, { n: impact.candidates.length })}
            </span>
          </div>
          <div className="graph-section-title">{GRAPH_CANDIDATES_TITLE}</div>
          {impact.candidates.length === 0 ? (
            <div className="muted-note">{GRAPH_CANDIDATES_EMPTY}</div>
          ) : (
            impact.candidates.map((c) => (
              <div className="graph-candidate" key={c.node.id}>
                <NodeLine node={c.node} extra={format(GRAPH_DEPTH_UNIT, { n: c.distance })} />
                <div className="graph-reason">{c.reason}</div>
              </div>
            ))
          )}
          <div className="graph-section-title">{GRAPH_IMPACTED_TITLE}</div>
          {impact.impacted.length === 0 ? (
            <div className="muted-note">{GRAPH_IMPACTED_EMPTY}</div>
          ) : (
            impact.impacted.map((item) => (
              <NodeLine
                key={item.node.id}
                node={item.node}
                extra={format(GRAPH_DEPTH_UNIT, { n: item.distance })}
              />
            ))
          )}
          {impact.truncated && <div className="muted-note">{GRAPH_TRUNCATED_HINT}</div>}
        </div>
      )}

      {!loading && error === null && (mode === 'traverseDown' || mode === 'traverseUp') && traverse !== null && (
        <div className="graph-traverse">
          <div className="graph-summary">
            <span className="muted-note">
              {format(GRAPH_NODES_UNIT, { n: traverse.nodes.length })} ·{' '}
              {format(GRAPH_EDGES_UNIT, { n: traverse.edges.length })} ·{' '}
              {format(GRAPH_CROSSLAYER_UNIT, { n: crossLayerCount })}
            </span>
          </div>
          <div className="graph-section-title">{GRAPH_TRAVERSE_TITLE}</div>
          {traverse.nodes.length <= 1 ? (
            <div className="muted-note">{GRAPH_TRAVERSE_EMPTY}</div>
          ) : (
            traverseGroups().map((group) => (
              <div className="graph-depth-group" key={group.depth}>
                <div className="graph-depth-label">
                  {format(GRAPH_DEPTH_UNIT, { n: group.depth })}
                </div>
                {group.nodes.map((node) => (
                  <NodeLine key={node.id} node={node} />
                ))}
              </div>
            ))
          )}
          {traverse.truncated && <div className="muted-note">{GRAPH_TRUNCATED_HINT}</div>}
        </div>
      )}
    </div>
  );
}
