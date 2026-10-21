/**
 * LayerInfoTab（#51）：图层覆盖层的只读信息面板。
 *
 * 画布上点选覆盖层实体（聚簇 / 基站 / OLT / 客户 / 业务 / 逻辑电路边…）后
 * 在右侧呈现：
 * - 节点：种类徽章 + 编码/名称/状态 + 类型化细节（OLT 的 PON 口清单、
 *   客户的服务基站数）+ 锚设备；
 * - 聚簇（基站）：成员分页下钻（GET layer-members）——1,198 基站默认聚合，
 *   这里逐页翻看（F.3 的「点击展开」面板形态）；
 * - 边：语义（逻辑承载/业务关联/接入上联/分组归属）+ 两端 + 血缘资源。
 *
 * 只读：覆盖层不进操作对话框（演练操作仍面向 device/interface/link）。
 */

import { useEffect, useMemo, useState } from 'react';
import type { LayerEdgePayload, LayerMember, LayerMembersPage, LayerNodePayload, SimTopology } from '../../api/types';
import { getLayerMembers } from '../../api/client';
import type { SelectedEntity } from './selection';
import { Pagination } from '../Pagination';
import {
  format,
  LAYER_INFO_A_END,
  LAYER_INFO_ANCHOR,
  LAYER_INFO_EMPTY_DESC,
  LAYER_INFO_HINT,
  LAYER_INFO_MEMBER_COUNT,
  LAYER_INFO_NAME,
  LAYER_INFO_NOTE,
  LAYER_INFO_PON_PORT,
  LAYER_INFO_SAMPLE,
  LAYER_INFO_SEMANTIC,
  LAYER_INFO_STATION_SERVES,
  LAYER_INFO_STATUS,
  LAYER_INFO_TITLE,
  LAYER_INFO_Z_END,
  LAYER_KIND_LABELS,
  LAYER_MEMBERS_CODE,
  LAYER_MEMBERS_LOAD_ERR,
  LAYER_MEMBERS_NAME,
  LAYER_MEMBERS_STATUS,
  LAYER_MEMBERS_TITLE,
  LAYER_PON_PORTS,
  LAYER_SEM_LABELS,
  LAYER_STATION_SERVES,
  NO_VALUE,
} from '../../i18n';
import { EmptyState } from '../EmptyState';
import { Spinner } from '../Spinner';

const MEMBERS_PAGE_SIZE = 20;

interface LayerInfoTabProps {
  topology: SimTopology | null;
  selected: SelectedEntity | null;
  workspaceId: string;
}

/** 成员表行。 */
function memberRow(m: LayerMember): Array<string> {
  return [m.code, m.name || NO_VALUE, m.status || NO_VALUE];
}

export function LayerInfoTab({ topology, selected, workspaceId }: LayerInfoTabProps) {
  const layers = topology?.layers;
  const node = useMemo<LayerNodePayload | null>(() => {
    if (layers === undefined || selected === null || selected.kind !== 'layerNode') return null;
    return layers.nodes.find((n) => n.id === selected.id) ?? null;
  }, [layers, selected]);
  const edge = useMemo<LayerEdgePayload | null>(() => {
    if (layers === undefined || selected === null || selected.kind !== 'layerLink') return null;
    return layers.edges.find((e) => e.id === selected.id) ?? null;
  }, [layers, selected]);

  // —— 聚簇成员分页下钻 ——
  const [members, setMembers] = useState<LayerMembersPage | null>(null);
  const [membersPage, setMembersPage] = useState(1);
  const [membersLoading, setMembersLoading] = useState(false);
  const [membersError, setMembersError] = useState<string | null>(null);
  const clusterKey = node?.aggregated === true ? node.anchorDeviceId ?? '' : '';

  useEffect(() => {
    if (clusterKey === '' || workspaceId === '') {
      setMembers(null);
      setMembersError(null);
      return;
    }
    let cancelled = false;
    setMembersLoading(true);
    setMembersError(null);
    getLayerMembers(workspaceId, clusterKey, membersPage, MEMBERS_PAGE_SIZE)
      .then((page) => {
        if (!cancelled) setMembers(page);
      })
      .catch((err: unknown) => {
        if (!cancelled) setMembersError(err instanceof Error ? err.message : LAYER_MEMBERS_LOAD_ERR);
      })
      .finally(() => {
        if (!cancelled) setMembersLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [clusterKey, workspaceId, membersPage]);

  if (layers === undefined) {
    return <EmptyState title={NO_VALUE} description={LAYER_INFO_EMPTY_DESC} />;
  }
  if (node === null && edge === null) {
    return (
      <EmptyState title={LAYER_INFO_TITLE} description={LAYER_INFO_HINT} />
    );
  }

  if (node !== null) {
    const kindLabel = LAYER_KIND_LABELS[node.kind] ?? node.kind;
    const anchorCode = topology?.nodes.find((n) => n.id === node.anchorDeviceId)?.code;
    return (
      <div className="layer-info">
        <div className="layer-info-head">
          <span className={`chip chip--muted layer-kind-chip layer-kind-chip--${node.layer}`}>{node.layer}</span>
          <span className="layer-info-kind">{kindLabel}</span>
          <span className="layer-info-code" title={node.code}>
            {node.code}
          </span>
        </div>
        <dl className="kv-table">
          <div className="kv-row">
            <dt>{LAYER_INFO_NAME}</dt>
            <dd title={node.name}>{node.name || NO_VALUE}</dd>
          </div>
          {node.status !== undefined && node.status !== '' && (
            <div className="kv-row">
              <dt>{LAYER_INFO_STATUS}</dt>
              <dd>{node.status}</dd>
            </div>
          )}
          {node.aggregated === true && (
            <div className="kv-row">
              <dt>{LAYER_INFO_MEMBER_COUNT}</dt>
              <dd>{node.memberCount ?? 0}</dd>
            </div>
          )}
          {anchorCode !== undefined && (
            <div className="kv-row">
              <dt>{LAYER_INFO_ANCHOR}</dt>
              <dd>{anchorCode}</dd>
            </div>
          )}
          {node.detail?.ponPorts !== undefined && (
            <div className="kv-row">
              <dt>{LAYER_INFO_PON_PORT}</dt>
              <dd>{format(LAYER_PON_PORTS, { n: node.detail.ponPorts.length })}</dd>
            </div>
          )}
          {node.detail?.stationServes !== undefined && (
            <div className="kv-row">
              <dt>{LAYER_INFO_STATION_SERVES}</dt>
              <dd>{format(LAYER_STATION_SERVES, { n: node.detail.stationServes })}</dd>
            </div>
          )}
          {node.sample !== undefined && node.sample.length > 0 && (
            <div className="kv-row">
              <dt>{LAYER_INFO_SAMPLE}</dt>
              <dd title={node.sample.join('、')}>{node.sample.join('、')}{node.memberCount !== undefined && node.memberCount > node.sample.length ? ' …' : ''}</dd>
            </div>
          )}
        </dl>

        {clusterKey !== '' && (
          <div className="layer-members">
            <div className="layer-members-title">
              {format(LAYER_MEMBERS_TITLE, { total: members?.total ?? node.memberCount ?? 0 })}
            </div>
            {membersError !== null && <div className="form-error">{LAYER_MEMBERS_LOAD_ERR}：{membersError}</div>}
            {membersLoading && <Spinner size={16} />}
            {members !== null && !membersLoading && (
              <>
                <table className="table table--compact">
                  <thead>
                    <tr>
                      <th>{LAYER_MEMBERS_CODE}</th>
                      <th>{LAYER_MEMBERS_NAME}</th>
                      <th>{LAYER_MEMBERS_STATUS}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {members.items.map((m) => (
                      <tr key={m.id}>
                        {memberRow(m).map((cell, i) => (
                          <td key={i} title={cell}>{cell}</td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
                <Pagination
                  page={members.page}
                  pageSize={members.pageSize}
                  total={members.total}
                  onChange={setMembersPage}
                />
              </>
            )}
          </div>
        )}
      </div>
    );
  }

  // —— 边详情（layerLink）——
  const e = edge as LayerEdgePayload;
  const semLabel = LAYER_SEM_LABELS[e.sem] ?? e.sem;
  const endpointLabel = (kind: string, ref: string, code?: string): string => {
    if (code !== undefined && code !== '') return `${code}（${ref}）`;
    if (kind === 'device') {
      const dev = topology?.nodes.find((n) => n.id === ref);
      return dev !== undefined ? dev.code : ref;
    }
    const ln = layers.nodes.find((n) => n.id === ref);
    return ln !== undefined ? ln.code : ref;
  };
  return (
    <div className="layer-info">
      <div className="layer-info-head">
        <span className={`chip chip--muted layer-kind-chip layer-kind-chip--${e.layer}`}>{e.layer}</span>
        <span className="layer-info-kind">{semLabel}</span>
        <span className="layer-info-code" title={e.label}>
          {e.label || e.id}
        </span>
      </div>
      <dl className="kv-table">
        <div className="kv-row">
          <dt>{LAYER_INFO_A_END}</dt>
          <dd title={e.aRef}>{endpointLabel(e.aKind, e.aRef, e.aCode)}</dd>
        </div>
        <div className="kv-row">
          <dt>{LAYER_INFO_Z_END}</dt>
          <dd title={e.zRef}>{endpointLabel(e.zKind, e.zRef, e.zCode)}</dd>
        </div>
        <div className="kv-row">
          <dt>{LAYER_INFO_SEMANTIC}</dt>
          <dd>{semLabel}</dd>
        </div>
      </dl>
      <p className="layer-info-note">{LAYER_INFO_NOTE}</p>
    </div>
  );
}
