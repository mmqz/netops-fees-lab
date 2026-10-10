/**
 * 拓扑工作台右侧实体标签页：设备 / 接口 / 链路。
 * - 表格行点击 → 画布选中联动；画布/外部选中 → 行高亮并滚动定位；
 * - 行动作：启停（设备停用需确认）、配置地址（接口）、删除（确认）；
 * - 所有单操作变更经父级 applyOps 提交（统一 toast + 刷新）。
 */

import { useEffect, useMemo, useRef, useState } from 'react';
import type { RefObject } from 'react';
import type { SimEntityStatus, SimOp, TopologyNode } from '../../api/types';
import { isUnconfiguredIPv4 } from './selection';
import {
  BTN_CONFIG_ADDR,
  BTN_DELETE,
  BTN_DISABLE,
  BTN_ENABLE,
  LAB_COL_ACTIONS,
  LAB_D_COL_CODE,
  LAB_D_COL_IFACES,
  LAB_D_COL_IPV4,
  LAB_D_COL_NAME,
  LAB_D_COL_STATUS,
  LAB_D_DOWN_MESSAGE,
  LAB_D_DOWN_TITLE,
  LAB_D_TABLE_LABEL,
  LAB_FILTER_DEVICES,
  LAB_FILTER_INTERFACES,
  LAB_FILTER_LINKS,
  LAB_FILTER_NO_MATCH,
  LAB_I_COL_DEVICE,
  LAB_I_COL_IPV4,
  LAB_I_COL_LINKS,
  LAB_I_COL_NAME,
  LAB_I_COL_STATUS,
  LAB_I_DELETE_MESSAGE,
  LAB_I_DELETE_TITLE,
  LAB_I_IPV4_UNSET,
  LAB_I_IPV4_VALUE,
  LAB_I_LABEL,
  LAB_I_TABLE_LABEL,
  LAB_L_COL_A,
  LAB_L_COL_LABEL,
  LAB_L_COL_STATUS,
  LAB_L_COL_Z,
  LAB_L_DELETE_MESSAGE,
  LAB_L_DELETE_TITLE,
  LAB_L_ENDPOINT,
  LAB_L_TABLE_LABEL,
  LAB_L_UNNAMED,
  LAB_OP_DESC_CONFIG_IPV4,
  LAB_OP_DESC_DELETE_IFACE,
  LAB_OP_DESC_DELETE_LINK,
  LAB_OP_DESC_DISABLE,
  LAB_OP_DESC_ENABLE,
  format,
} from '../../i18n';
import { Table } from '../Table';
import type { TableColumn } from '../Table';
import { ConfirmDialog } from '../ConfirmDialog';
import { SimStatusChip } from './SimStatusChip';
import { InterfaceConfigDialog } from './InterfaceConfigDialog';
import { IconSearch } from '../icons';
import type { InterfaceRow, LinkRow, SelectedEntity, TopologyView } from './selection';

/** 实体状态排序序（运行在前）。 */
const STATUS_RANK: Record<SimEntityStatus, number> = { up: 0, down: 1 };

/** 实体表格客户端筛选框（P2）：搜索框 + 清除由 type=search 原生提供。 */
function TabFilter({
  placeholder,
  value,
  onChange,
}: {
  placeholder: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="lab-tab-filter">
      <div className="search-box">
        <IconSearch size={14} />
        <input
          className="input input--sm"
          type="search"
          value={value}
          placeholder={placeholder}
          aria-label={placeholder}
          onChange={(event) => onChange(event.target.value)}
        />
      </div>
    </div>
  );
}

/** 行选中联动：高亮 + 滚动定位到对应行。 */
function useRowScroll(
  selected: SelectedEntity | null,
  kind: SelectedEntity['kind'],
): RefObject<HTMLDivElement> {
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (selected === null || selected.kind !== kind) return;
    const el = ref.current?.querySelector(`tr[data-row-key="${selected.id}"]`);
    el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [selected, kind]);
  return ref;
}

interface EntityTabProps {
  view: TopologyView;
  selected: SelectedEntity | null;
  onSelect: (entity: SelectedEntity) => void;
  /** 单操作提交（父级统一 busy/toast/刷新）。 */
  applyOps: (ops: SimOp[], description: string) => void;
  opsBusy: boolean;
}

interface DeviceRow extends TopologyNode {
  interfaceCount: number;
  ipv4Count: number;
}

export function DeviceTab({ view, selected, onSelect, applyOps, opsBusy }: EntityTabProps) {
  const containerRef = useRowScroll(selected, 'device');
  const [confirmTarget, setConfirmTarget] = useState<DeviceRow | null>(null);
  const [filter, setFilter] = useState('');

  const rows: DeviceRow[] = useMemo(
    () =>
      view.nodes.map((node) => ({
        ...node,
        interfaceCount: node.interfaces.length,
        ipv4Count: node.interfaces.filter((iface) => !isUnconfiguredIPv4(iface.ipv4Addr, iface.ipv4Prefix)).length,
      })),
    [view],
  );

  const filtered = useMemo(() => {
    const keyword = filter.trim().toLowerCase();
    if (keyword === '') return rows;
    return rows.filter(
      (row) => row.code.toLowerCase().includes(keyword) || row.name.toLowerCase().includes(keyword),
    );
  }, [rows, filter]);

  function confirmDown(): void {
    const target = confirmTarget;
    if (target === null) return;
    setConfirmTarget(null);
    applyOps(
      [{ op: 'set_device_status', deviceId: target.id, status: 'down' }],
      format(LAB_OP_DESC_DISABLE, { target: target.code }),
    );
  }

  const columns: TableColumn<DeviceRow>[] = [
    {
      key: 'code',
      header: LAB_D_COL_CODE,
      render: (row) => <span className="cell-strong mono">{row.code}</span>,
      sortValue: (row) => row.code,
    },
    { key: 'name', header: LAB_D_COL_NAME, render: (row) => row.name, sortValue: (row) => row.name },
    {
      key: 'status',
      header: LAB_D_COL_STATUS,
      render: (row) => <SimStatusChip status={row.status} />,
      sortValue: (row) => STATUS_RANK[row.status],
    },
    {
      key: 'interfaceCount',
      header: LAB_D_COL_IFACES,
      render: (row) => row.interfaceCount,
      align: 'right',
      sortValue: (row) => row.interfaceCount,
    },
    {
      key: 'ipv4Count',
      header: LAB_D_COL_IPV4,
      render: (row) => row.ipv4Count,
      align: 'right',
      sortValue: (row) => row.ipv4Count,
    },
    {
      key: 'actions',
      header: LAB_COL_ACTIONS,
      render: (row) =>
        row.status === 'up' ? (
          <button
            type="button"
            className="btn btn--danger-ghost btn--sm"
            disabled={opsBusy}
            onClick={() => setConfirmTarget(row)}
          >
            {BTN_DISABLE}
          </button>
        ) : (
          <button
            type="button"
            className="btn btn--secondary btn--sm"
            disabled={opsBusy}
            onClick={() =>
              applyOps(
                [{ op: 'set_device_status', deviceId: row.id, status: 'up' }],
                format(LAB_OP_DESC_ENABLE, { target: row.code }),
              )
            }
          >
            {BTN_ENABLE}
          </button>
        ),
    },
  ];

  return (
    <div ref={containerRef}>
      <TabFilter placeholder={LAB_FILTER_DEVICES} value={filter} onChange={setFilter} />
      <div className="table-wrap">
        <Table
          columns={columns}
          rows={filtered}
          rowKey={(row) => row.id}
          emptyText={filter.trim() === '' ? undefined : LAB_FILTER_NO_MATCH}
          onRowClick={(row) => onSelect({ kind: 'device', id: row.id })}
          rowClassName={(row) =>
            [
              row.status === 'down' ? 'lab-row-muted' : undefined,
              selected?.kind === 'device' && selected.id === row.id ? 'lab-row-selected' : undefined,
            ]
              .filter(Boolean)
              .join(' ') || undefined
          }
          ariaLabel={LAB_D_TABLE_LABEL}
        />
      </div>
      <ConfirmDialog
        open={confirmTarget !== null}
        title={LAB_D_DOWN_TITLE}
        message={format(LAB_D_DOWN_MESSAGE, { code: confirmTarget?.code ?? '' })}
        confirmLabel={BTN_DISABLE}
        danger
        onConfirm={confirmDown}
        onCancel={() => setConfirmTarget(null)}
      />
    </div>
  );
}

export function InterfaceTab({ view, selected, onSelect, applyOps, opsBusy }: EntityTabProps) {
  const containerRef = useRowScroll(selected, 'interface');
  const [configTarget, setConfigTarget] = useState<InterfaceRow | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<InterfaceRow | null>(null);
  const [filter, setFilter] = useState('');

  const filtered = useMemo(() => {
    const keyword = filter.trim().toLowerCase();
    if (keyword === '') return view.interfaces;
    return view.interfaces.filter(
      (row) =>
        row.deviceCode.toLowerCase().includes(keyword) ||
        row.name.toLowerCase().includes(keyword) ||
        (row.ipv4Addr !== null && row.ipv4Addr.includes(keyword)),
    );
  }, [view, filter]);

  function ifaceLabel(row: InterfaceRow): string {
    return format(LAB_I_LABEL, { device: row.deviceCode, name: row.name });
  }

  function toggleStatus(row: InterfaceRow, status: SimEntityStatus): void {
    applyOps(
      [{ op: 'set_interface_status', interfaceId: row.id, status }],
      format(status === 'up' ? LAB_OP_DESC_ENABLE : LAB_OP_DESC_DISABLE, { target: ifaceLabel(row) }),
    );
  }

  function confirmDelete(): void {
    const target = deleteTarget;
    if (target === null) return;
    setDeleteTarget(null);
    applyOps(
      [{ op: 'delete_interface', interfaceId: target.id }],
      format(LAB_OP_DESC_DELETE_IFACE, { target: ifaceLabel(target) }),
    );
  }

  function submitConfig(ops: SimOp[]): void {
    const target = configTarget;
    setConfigTarget(null);
    applyOps(
      ops,
      format(LAB_OP_DESC_CONFIG_IPV4, { target: target === null ? '' : ifaceLabel(target) }),
    );
  }

  const columns: TableColumn<InterfaceRow>[] = [
    {
      key: 'device',
      header: LAB_I_COL_DEVICE,
      render: (row) => <span className="mono">{row.deviceCode}</span>,
      sortValue: (row) => row.deviceCode,
    },
    {
      key: 'name',
      header: LAB_I_COL_NAME,
      render: (row) => <span className="cell-strong">{row.name}</span>,
      sortValue: (row) => row.name,
    },
    {
      key: 'status',
      header: LAB_I_COL_STATUS,
      render: (row) => <SimStatusChip status={row.status} />,
      sortValue: (row) => STATUS_RANK[row.status],
    },
    {
      key: 'ipv4',
      header: LAB_I_COL_IPV4,
      render: (row) =>
        row.ipv4Addr === null ? (
          <span className="muted-note">{LAB_I_IPV4_UNSET}</span>
        ) : (
          <span className="mono">{format(LAB_I_IPV4_VALUE, { addr: row.ipv4Addr, prefix: row.ipv4Prefix ?? 0 })}</span>
        ),
      sortValue: (row) => row.ipv4Addr ?? '',
    },
    {
      key: 'linkCount',
      header: LAB_I_COL_LINKS,
      render: (row) => row.linkCount,
      align: 'right',
      sortValue: (row) => row.linkCount,
    },
    {
      key: 'actions',
      header: LAB_COL_ACTIONS,
      render: (row) => (
        <span style={{ display: 'inline-flex', gap: 6, flexWrap: 'wrap' }}>
          {row.status === 'up' ? (
            <button
              type="button"
              className="btn btn--danger-ghost btn--sm"
              disabled={opsBusy}
              onClick={() => toggleStatus(row, 'down')}
            >
              {BTN_DISABLE}
            </button>
          ) : (
            <button
              type="button"
              className="btn btn--secondary btn--sm"
              disabled={opsBusy}
              onClick={() => toggleStatus(row, 'up')}
            >
              {BTN_ENABLE}
            </button>
          )}
          <button
            type="button"
            className="btn btn--secondary btn--sm"
            disabled={opsBusy}
            onClick={() => setConfigTarget(row)}
          >
            {BTN_CONFIG_ADDR}
          </button>
          <button
            type="button"
            className="btn btn--danger-ghost btn--sm"
            disabled={opsBusy}
            onClick={() => setDeleteTarget(row)}
          >
            {BTN_DELETE}
          </button>
        </span>
      ),
    },
  ];

  return (
    <div ref={containerRef}>
      <TabFilter placeholder={LAB_FILTER_INTERFACES} value={filter} onChange={setFilter} />
      <div className="table-wrap">
        <Table
          columns={columns}
          rows={filtered}
          rowKey={(row) => row.id}
          emptyText={filter.trim() === '' ? undefined : LAB_FILTER_NO_MATCH}
          onRowClick={(row) => onSelect({ kind: 'interface', id: row.id })}
          rowClassName={(row) =>
            [
              row.status === 'down' ? 'lab-row-muted' : undefined,
              selected?.kind === 'interface' && selected.id === row.id ? 'lab-row-selected' : undefined,
            ]
              .filter(Boolean)
              .join(' ') || undefined
          }
          ariaLabel={LAB_I_TABLE_LABEL}
        />
      </div>
      <InterfaceConfigDialog
        open={configTarget !== null}
        iface={configTarget}
        busy={opsBusy}
        onClose={() => setConfigTarget(null)}
        onSubmit={submitConfig}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        title={LAB_I_DELETE_TITLE}
        message={format(LAB_I_DELETE_MESSAGE, { iface: deleteTarget === null ? '' : ifaceLabel(deleteTarget) })}
        confirmLabel={BTN_DELETE}
        danger
        onConfirm={confirmDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  );
}

export function LinkTab({ view, selected, onSelect, applyOps, opsBusy }: EntityTabProps) {
  const containerRef = useRowScroll(selected, 'link');
  const [deleteTarget, setDeleteTarget] = useState<LinkRow | null>(null);
  const [filter, setFilter] = useState('');

  const filtered = useMemo(() => {
    const keyword = filter.trim().toLowerCase();
    if (keyword === '') return view.links;
    return view.links.filter(
      (row) =>
        row.label.toLowerCase().includes(keyword) ||
        row.aDeviceCode.toLowerCase().includes(keyword) ||
        row.zDeviceCode.toLowerCase().includes(keyword) ||
        row.aInterfaceName.toLowerCase().includes(keyword) ||
        row.zInterfaceName.toLowerCase().includes(keyword),
    );
  }, [view, filter]);

  function endpoint(row: LinkRow, side: 'a' | 'z'): string {
    return format(LAB_L_ENDPOINT, {
      device: side === 'a' ? row.aDeviceCode : row.zDeviceCode,
      iface: side === 'a' ? row.aInterfaceName : row.zInterfaceName,
    });
  }

  function linkLabel(row: LinkRow): string {
    return row.label === '' ? `${endpoint(row, 'a')}↔${endpoint(row, 'z')}` : row.label;
  }

  function toggleStatus(row: LinkRow, status: SimEntityStatus): void {
    applyOps(
      [{ op: 'set_link_status', linkId: row.id, status }],
      format(status === 'up' ? LAB_OP_DESC_ENABLE : LAB_OP_DESC_DISABLE, { target: linkLabel(row) }),
    );
  }

  function confirmDelete(): void {
    const target = deleteTarget;
    if (target === null) return;
    setDeleteTarget(null);
    applyOps(
      [{ op: 'delete_link', linkId: target.id }],
      format(LAB_OP_DESC_DELETE_LINK, { target: target.label === '' ? LAB_L_UNNAMED : target.label }),
    );
  }

  const columns: TableColumn<LinkRow>[] = [
    {
      key: 'label',
      header: LAB_L_COL_LABEL,
      render: (row) =>
        row.label === '' ? (
          <span className="muted-note">{LAB_L_UNNAMED}</span>
        ) : (
          <span className="cell-strong mono">{row.label}</span>
        ),
      sortValue: (row) => row.label,
    },
    {
      key: 'a',
      header: LAB_L_COL_A,
      render: (row) => <span className="mono">{endpoint(row, 'a')}</span>,
      sortValue: (row) => row.aDeviceCode,
    },
    {
      key: 'z',
      header: LAB_L_COL_Z,
      render: (row) => <span className="mono">{endpoint(row, 'z')}</span>,
      sortValue: (row) => row.zDeviceCode,
    },
    {
      key: 'status',
      header: LAB_L_COL_STATUS,
      render: (row) => <SimStatusChip status={row.status} />,
      sortValue: (row) => STATUS_RANK[row.status],
    },
    {
      key: 'actions',
      header: LAB_COL_ACTIONS,
      render: (row) => (
        <span style={{ display: 'inline-flex', gap: 6, flexWrap: 'wrap' }}>
          {row.status === 'up' ? (
            <button
              type="button"
              className="btn btn--danger-ghost btn--sm"
              disabled={opsBusy}
              onClick={() => toggleStatus(row, 'down')}
            >
              {BTN_DISABLE}
            </button>
          ) : (
            <button
              type="button"
              className="btn btn--secondary btn--sm"
              disabled={opsBusy}
              onClick={() => toggleStatus(row, 'up')}
            >
              {BTN_ENABLE}
            </button>
          )}
          <button
            type="button"
            className="btn btn--danger-ghost btn--sm"
            disabled={opsBusy}
            onClick={() => setDeleteTarget(row)}
          >
            {BTN_DELETE}
          </button>
        </span>
      ),
    },
  ];

  return (
    <div ref={containerRef}>
      <TabFilter placeholder={LAB_FILTER_LINKS} value={filter} onChange={setFilter} />
      <div className="table-wrap">
        <Table
          columns={columns}
          rows={filtered}
          rowKey={(row) => row.id}
          emptyText={filter.trim() === '' ? undefined : LAB_FILTER_NO_MATCH}
          onRowClick={(row) => onSelect({ kind: 'link', id: row.id })}
          rowClassName={(row) =>
            [
              row.status === 'down' ? 'lab-row-muted' : undefined,
              selected?.kind === 'link' && selected.id === row.id ? 'lab-row-selected' : undefined,
            ]
              .filter(Boolean)
              .join(' ') || undefined
          }
          ariaLabel={LAB_L_TABLE_LABEL}
        />
      </div>
      <ConfirmDialog
        open={deleteTarget !== null}
        title={LAB_L_DELETE_TITLE}
        message={format(LAB_L_DELETE_MESSAGE, {
          label: deleteTarget === null ? '' : deleteTarget.label === '' ? LAB_L_UNNAMED : deleteTarget.label,
          a: deleteTarget === null ? '' : endpoint(deleteTarget, 'a'),
          z: deleteTarget === null ? '' : endpoint(deleteTarget, 'z'),
        })}
        confirmLabel={BTN_DELETE}
        danger
        onConfirm={confirmDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  );
}
