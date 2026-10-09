/**
 * 执行操作对话框：
 * - 11 个中文操作入口（10 个 §4 前向操作，配置/清除地址拆成两项）；
 * - 动态表单：id 下拉以当前拓扑预填（设备 code·名称 / DEV:eth 接口 / 链路 A↔Z）；
 * - 客户端校验（必填、dotted-quad、前缀 0–32、链路两端不同设备）；
 * - JSON 模式：直接提交操作数组或 {ops,description,runChecks}（JSON.parse 校验）。
 */

import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import type { ApplyResult, SimEntityStatus, SimOp } from '../../api/types';
import { ApiError, applyOps } from '../../api/client';
import {
  BTN_CONFIRM,
  LAB_I_ADDR_PLACEHOLDER,
  LAB_I_ERR_ADDR,
  LAB_I_ERR_BOTH,
  LAB_I_ERR_PREFIX,
  LAB_I_F_ADDR,
  LAB_I_F_PREFIX,
  LAB_I_PREFIX_PLACEHOLDER,
  LAB_OPS_ERR_REQUIRED,
  LAB_OPS_ERR_SAME_IFACE,
  LAB_OPS_F_A_IFACE,
  LAB_OPS_F_CODE,
  LAB_OPS_F_DESC,
  LAB_OPS_F_DEVICE,
  LAB_OPS_F_IF_NAME,
  LAB_OPS_F_INIT_STATUS,
  LAB_OPS_F_INTERFACE,
  LAB_OPS_F_LINK,
  LAB_OPS_F_LINK_LABEL,
  LAB_OPS_F_NAME,
  LAB_OPS_F_STATUS,
  LAB_OPS_F_Z_IFACE,
  LAB_OPS_RAW_HINT,
  LAB_OPS_RAW_NOT_ARRAY,
  LAB_OPS_RAW_NO_OPS,
  LAB_OPS_RAW_PARSE_ERROR,
  LAB_OPS_RAW_PLACEHOLDER,
  LAB_OPS_RAW_TOGGLE,
  LAB_OPS_RUN_CHECKS,
  LAB_OPS_TITLE,
  LAB_OPS_TYPE,
  OP_LABEL_CLEAR_IPV4,
  OP_LABEL_CONFIG_IPV4,
  OP_LABEL_CREATE_DEVICE,
  OP_LABEL_CREATE_INTERFACE,
  OP_LABEL_CREATE_LINK,
  OP_LABEL_DELETE_DEVICE,
  OP_LABEL_DELETE_INTERFACE,
  OP_LABEL_DELETE_LINK,
  OP_LABEL_SET_DEVICE_STATUS,
  OP_LABEL_SET_INTERFACE_STATUS,
  OP_LABEL_SET_LINK_STATUS,
  SIM_STATUS_LABELS,
  format,
} from '../../i18n';
import { LabDialog } from './LabDialog';
import { isValidIpv4 } from './selection';
import type { TopologyView } from './selection';

/** 对话框操作入口（clear_interface_ipv4 是 config_interface_ipv4 的空参数形态）。 */
type OpFormKind =
  | 'create_device'
  | 'set_device_status'
  | 'delete_device'
  | 'create_interface'
  | 'set_interface_status'
  | 'config_interface_ipv4'
  | 'clear_interface_ipv4'
  | 'delete_interface'
  | 'create_link'
  | 'set_link_status'
  | 'delete_link';

type CreateInterfaceOp = Extract<SimOp, { op: 'create_interface' }>;
type CreateLinkOp = Extract<SimOp, { op: 'create_link' }>;

const OP_FORM_KINDS: Array<{ value: OpFormKind; label: string }> = [
  { value: 'set_device_status', label: OP_LABEL_SET_DEVICE_STATUS },
  { value: 'create_device', label: OP_LABEL_CREATE_DEVICE },
  { value: 'delete_device', label: OP_LABEL_DELETE_DEVICE },
  { value: 'create_interface', label: OP_LABEL_CREATE_INTERFACE },
  { value: 'set_interface_status', label: OP_LABEL_SET_INTERFACE_STATUS },
  { value: 'config_interface_ipv4', label: OP_LABEL_CONFIG_IPV4 },
  { value: 'clear_interface_ipv4', label: OP_LABEL_CLEAR_IPV4 },
  { value: 'delete_interface', label: OP_LABEL_DELETE_INTERFACE },
  { value: 'create_link', label: OP_LABEL_CREATE_LINK },
  { value: 'set_link_status', label: OP_LABEL_SET_LINK_STATUS },
  { value: 'delete_link', label: OP_LABEL_DELETE_LINK },
];

const STATUS_OPTIONS: Array<{ value: SimEntityStatus; label: string }> = [
  { value: 'up', label: SIM_STATUS_LABELS.up },
  { value: 'down', label: SIM_STATUS_LABELS.down },
];

interface Option {
  value: string;
  label: string;
}

interface RawParsed {
  ops: SimOp[];
  description?: string;
  runChecks?: boolean;
}

interface OpsDialogProps {
  open: boolean;
  workspaceId: string;
  view: TopologyView;
  onClose: () => void;
  onApplied: (result: ApplyResult) => void;
  onError: (message: string) => void;
}

/** 表单构建结果：操作数组或阻止提交的错误文案。 */
type FormResult = { ops: SimOp[] } | { error: string };

export function OpsDialog({ open, workspaceId, view, onClose, onApplied, onError }: OpsDialogProps) {
  const [opKind, setOpKind] = useState<OpFormKind>('set_device_status');
  const [deviceId, setDeviceId] = useState('');
  const [deviceCode, setDeviceCode] = useState('');
  const [deviceName, setDeviceName] = useState('');
  const [ifDeviceId, setIfDeviceId] = useState('');
  const [ifName, setIfName] = useState('');
  const [ifStatus, setIfStatus] = useState('');
  const [interfaceId, setInterfaceId] = useState('');
  const [status, setStatus] = useState<SimEntityStatus>('down');
  const [addr, setAddr] = useState('');
  const [prefix, setPrefix] = useState('');
  const [aInterfaceId, setAInterfaceId] = useState('');
  const [zInterfaceId, setZInterfaceId] = useState('');
  const [linkLabel, setLinkLabel] = useState('');
  const [linkId, setLinkId] = useState('');
  const [description, setDescription] = useState('');
  const [runChecks, setRunChecks] = useState(true);
  const [rawMode, setRawMode] = useState(false);
  const [rawText, setRawText] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // 打开时重置为安全初值
  useEffect(() => {
    if (!open) return;
    setOpKind('set_device_status');
    setDeviceId('');
    setDeviceCode('');
    setDeviceName('');
    setIfDeviceId('');
    setIfName('');
    setIfStatus('');
    setInterfaceId('');
    setStatus('down');
    setAddr('');
    setPrefix('');
    setAInterfaceId('');
    setZInterfaceId('');
    setLinkLabel('');
    setLinkId('');
    setDescription('');
    setRawText('');
    setError(null);
  }, [open]);

  const deviceOptions = useMemo<Option[]>(
    () =>
      view.nodes.map((node) => ({
        value: node.id,
        label: node.name === '' ? node.code : `${node.code} · ${node.name}`,
      })),
    [view.nodes],
  );
  const ifaceOptions = useMemo<Option[]>(
    () =>
      view.interfaces.map((iface) => ({
        value: iface.id,
        label: `${iface.deviceCode}:${iface.name}`,
      })),
    [view.interfaces],
  );
  const linkOptions = useMemo<Option[]>(
    () =>
      view.links.map((link) => ({
        value: link.id,
        label: `${link.aDeviceCode}:${link.aInterfaceName} ↔ ${link.zDeviceCode}:${link.zInterfaceName}`,
      })),
    [view.links],
  );

  function buildFormOps(): FormResult {
    switch (opKind) {
      case 'set_device_status':
        if (deviceId === '') return { error: LAB_OPS_ERR_REQUIRED };
        return { ops: [{ op: 'set_device_status', deviceId, status }] };
      case 'create_device':
        if (deviceCode.trim() === '' || deviceName.trim() === '') return { error: LAB_OPS_ERR_REQUIRED };
        return { ops: [{ op: 'create_device', code: deviceCode.trim(), name: deviceName.trim() }] };
      case 'delete_device':
        if (deviceId === '') return { error: LAB_OPS_ERR_REQUIRED };
        return { ops: [{ op: 'delete_device', deviceId }] };
      case 'create_interface': {
        if (ifDeviceId === '' || ifName.trim() === '') return { error: LAB_OPS_ERR_REQUIRED };
        const op: CreateInterfaceOp = { op: 'create_interface', deviceId: ifDeviceId, name: ifName.trim() };
        if (ifStatus !== '') op.status = ifStatus as SimEntityStatus;
        if (addr.trim() !== '' && prefix.trim() !== '') {
          op.ipv4Addr = addr.trim();
          op.ipv4Prefix = Number(prefix);
        }
        return { ops: [op] };
      }
      case 'set_interface_status':
        if (interfaceId === '') return { error: LAB_OPS_ERR_REQUIRED };
        return { ops: [{ op: 'set_interface_status', interfaceId, status }] };
      case 'config_interface_ipv4': {
        if (interfaceId === '') return { error: LAB_OPS_ERR_REQUIRED };
        if (addr.trim() === '' || prefix.trim() === '') return { error: LAB_I_ERR_BOTH };
        if (!isValidIpv4(addr.trim())) return { error: LAB_I_ERR_ADDR };
        const prefixNum = Number(prefix);
        if (!Number.isInteger(prefixNum) || prefixNum < 0 || prefixNum > 32) {
          return { error: LAB_I_ERR_PREFIX };
        }
        return { ops: [{ op: 'config_interface_ipv4', interfaceId, ipv4Addr: addr.trim(), ipv4Prefix: prefixNum }] };
      }
      case 'clear_interface_ipv4':
        if (interfaceId === '') return { error: LAB_OPS_ERR_REQUIRED };
        return { ops: [{ op: 'config_interface_ipv4', interfaceId, ipv4Addr: null, ipv4Prefix: null }] };
      case 'create_link': {
        if (aInterfaceId === '' || zInterfaceId === '') return { error: LAB_OPS_ERR_REQUIRED };
        const aIface = view.interfaces.find((iface) => iface.id === aInterfaceId);
        const zIface = view.interfaces.find((iface) => iface.id === zInterfaceId);
        if (aIface === undefined || zIface === undefined) return { error: LAB_OPS_ERR_REQUIRED };
        if (aIface.deviceId === zIface.deviceId) return { error: LAB_OPS_ERR_SAME_IFACE };
        const op: CreateLinkOp = { op: 'create_link', aInterfaceId, zInterfaceId };
        if (linkLabel.trim() !== '') op.label = linkLabel.trim();
        return { ops: [op] };
      }
      case 'set_link_status':
        if (linkId === '') return { error: LAB_OPS_ERR_REQUIRED };
        return { ops: [{ op: 'set_link_status', linkId, status }] };
      case 'delete_link':
        if (linkId === '') return { error: LAB_OPS_ERR_REQUIRED };
        return { ops: [{ op: 'delete_link', linkId }] };
      default:
        return { error: LAB_OPS_ERR_REQUIRED };
    }
  }

  function parseRawJson(): RawParsed | { error: string } {
    let parsed: unknown;
    try {
      parsed = JSON.parse(rawText);
    } catch (err: unknown) {
      return {
        error: format(LAB_OPS_RAW_PARSE_ERROR, {
          message: err instanceof Error ? err.message : String(err),
        }),
      };
    }
    if (Array.isArray(parsed)) {
      if (parsed.length === 0) return { error: LAB_OPS_RAW_NO_OPS };
      return { ops: parsed as SimOp[] };
    }
    if (typeof parsed === 'object' && parsed !== null && Array.isArray((parsed as { ops?: unknown }).ops)) {
      const rec = parsed as { ops: unknown[]; description?: unknown; runChecks?: unknown };
      if (rec.ops.length === 0) return { error: LAB_OPS_RAW_NO_OPS };
      return {
        ops: rec.ops as SimOp[],
        description: typeof rec.description === 'string' ? rec.description : undefined,
        runChecks: typeof rec.runChecks === 'boolean' ? rec.runChecks : undefined,
      };
    }
    return { error: LAB_OPS_RAW_NOT_ARRAY };
  }

  function submit(): void {
    setError(null);
    let request: { ops: SimOp[]; description?: string; runChecks?: boolean };
    if (rawMode) {
      const parsed = parseRawJson();
      if ('error' in parsed) {
        setError(parsed.error);
        return;
      }
      request = {
        ops: parsed.ops,
        description: parsed.description,
        runChecks: parsed.runChecks,
      };
    } else {
      const form = buildFormOps();
      if ('error' in form) {
        setError(form.error);
        return;
      }
      request = {
        ops: form.ops,
        description: description.trim() === '' ? undefined : description.trim(),
        runChecks,
      };
    }
    setBusy(true);
    applyOps(workspaceId, request)
      .then((result) => {
        setBusy(false);
        onApplied(result);
      })
      .catch((err: unknown) => {
        setBusy(false);
        if (err instanceof ApiError) {
          onError(err.message);
        } else {
          setError(String(err));
        }
      });
  }

  const needDevice = opKind === 'set_device_status' || opKind === 'delete_device';
  const needInterface =
    opKind === 'set_interface_status' || opKind === 'config_interface_ipv4' || opKind === 'clear_interface_ipv4';
  const needLink = opKind === 'set_link_status' || opKind === 'delete_link';

  function renderSelect(
    id: string,
    label: string,
    value: string,
    options: Option[],
    onChange: (value: string) => void,
  ): ReactNode {
    return (
      <div className="field">
        <label className="field-label" htmlFor={id}>
          {label}
        </label>
        <select
          id={id}
          className="select select--full"
          value={value}
          disabled={busy}
          onChange={(event) => onChange(event.target.value)}
        >
          <option value="" />
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      </div>
    );
  }

  return (
    <LabDialog
      open={open}
      title={LAB_OPS_TITLE}
      onClose={onClose}
      wide
      busy={busy}
      confirmLabel={BTN_CONFIRM}
      onConfirm={submit}
    >
      <div className="field">
        <label className="switch">
          <input
            type="checkbox"
            checked={rawMode}
            disabled={busy}
            onChange={(event) => {
              setRawMode(event.target.checked);
              setError(null);
            }}
          />
          {LAB_OPS_RAW_TOGGLE}
        </label>
      </div>

      {rawMode ? (
        <div className="field">
          <label className="field-label" htmlFor="lab-ops-raw">
            {LAB_OPS_TITLE}
          </label>
          <textarea
            id="lab-ops-raw"
            className="lab-textarea"
            value={rawText}
            disabled={busy}
            placeholder={LAB_OPS_RAW_PLACEHOLDER}
            onChange={(event) => setRawText(event.target.value)}
          />
          <div className="modal-note">{LAB_OPS_RAW_HINT}</div>
        </div>
      ) : (
        <>
          <div className="field">
            <label className="field-label" htmlFor="lab-ops-kind">
              {LAB_OPS_TYPE}
            </label>
            <select
              id="lab-ops-kind"
              className="select select--full"
              value={opKind}
              disabled={busy}
              onChange={(event) => {
                setOpKind(event.target.value as OpFormKind);
                setError(null);
              }}
            >
              {OP_FORM_KINDS.map((kind) => (
                <option key={kind.value} value={kind.value}>
                  {kind.label}
                </option>
              ))}
            </select>
          </div>

          {opKind === 'create_device' && (
            <div className="lab-form-grid">
              <div className="field">
                <label className="field-label" htmlFor="lab-ops-code">
                  {LAB_OPS_F_CODE}
                </label>
                <input
                  id="lab-ops-code"
                  className="input"
                  style={{ width: '100%' }}
                  value={deviceCode}
                  disabled={busy}
                  placeholder="DEV-100"
                  onChange={(event) => setDeviceCode(event.target.value)}
                />
              </div>
              <div className="field">
                <label className="field-label" htmlFor="lab-ops-name">
                  {LAB_OPS_F_NAME}
                </label>
                <input
                  id="lab-ops-name"
                  className="input"
                  style={{ width: '100%' }}
                  value={deviceName}
                  disabled={busy}
                  onChange={(event) => setDeviceName(event.target.value)}
                />
              </div>
            </div>
          )}

          {needDevice && renderSelect('lab-ops-device', LAB_OPS_F_DEVICE, deviceId, deviceOptions, setDeviceId)}

          {opKind === 'create_interface' && (
            <>
              <div className="lab-form-grid">
                {renderSelect('lab-ops-if-device', LAB_OPS_F_DEVICE, ifDeviceId, deviceOptions, setIfDeviceId)}
                <div className="field">
                  <label className="field-label" htmlFor="lab-ops-if-name">
                    {LAB_OPS_F_IF_NAME}
                  </label>
                  <input
                    id="lab-ops-if-name"
                    className="input"
                    style={{ width: '100%' }}
                    value={ifName}
                    disabled={busy}
                    placeholder="eth0"
                    onChange={(event) => setIfName(event.target.value)}
                  />
                </div>
              </div>
              <div className="lab-form-grid">
                {renderSelect('lab-ops-if-status', LAB_OPS_F_INIT_STATUS, ifStatus, STATUS_OPTIONS, setIfStatus)}
                <div className="field">
                  <label className="field-label" htmlFor="lab-ops-if-addr">
                    {LAB_I_F_ADDR}（可选）
                  </label>
                  <input
                    id="lab-ops-if-addr"
                    className="input"
                    style={{ width: '100%' }}
                    value={addr}
                    disabled={busy}
                    placeholder={LAB_I_ADDR_PLACEHOLDER}
                    onChange={(event) => setAddr(event.target.value)}
                  />
                </div>
              </div>
              <div className="field">
                <label className="field-label" htmlFor="lab-ops-if-prefix">
                  {LAB_I_F_PREFIX}（可选）
                </label>
                <input
                  id="lab-ops-if-prefix"
                  className="input"
                  style={{ width: '100%' }}
                  value={prefix}
                  disabled={busy}
                  placeholder={LAB_I_PREFIX_PLACEHOLDER}
                  inputMode="numeric"
                  onChange={(event) => setPrefix(event.target.value)}
                />
              </div>
            </>
          )}

          {needInterface && renderSelect('lab-ops-iface', LAB_OPS_F_INTERFACE, interfaceId, ifaceOptions, setInterfaceId)}

          {opKind === 'config_interface_ipv4' && (
            <div className="lab-form-grid">
              <div className="field">
                <label className="field-label" htmlFor="lab-ops-addr">
                  {LAB_I_F_ADDR}
                </label>
                <input
                  id="lab-ops-addr"
                  className="input"
                  style={{ width: '100%' }}
                  value={addr}
                  disabled={busy}
                  placeholder={LAB_I_ADDR_PLACEHOLDER}
                  onChange={(event) => setAddr(event.target.value)}
                />
              </div>
              <div className="field">
                <label className="field-label" htmlFor="lab-ops-prefix">
                  {LAB_I_F_PREFIX}
                </label>
                <input
                  id="lab-ops-prefix"
                  className="input"
                  style={{ width: '100%' }}
                  value={prefix}
                  disabled={busy}
                  placeholder={LAB_I_PREFIX_PLACEHOLDER}
                  inputMode="numeric"
                  onChange={(event) => setPrefix(event.target.value)}
                />
              </div>
            </div>
          )}

          {opKind === 'create_link' && (
            <>
              <div className="lab-form-grid">
                {renderSelect('lab-ops-a-iface', LAB_OPS_F_A_IFACE, aInterfaceId, ifaceOptions, setAInterfaceId)}
                {renderSelect('lab-ops-z-iface', LAB_OPS_F_Z_IFACE, zInterfaceId, ifaceOptions, setZInterfaceId)}
              </div>
              <div className="field">
                <label className="field-label" htmlFor="lab-ops-link-label">
                  {LAB_OPS_F_LINK_LABEL}
                </label>
                <input
                  id="lab-ops-link-label"
                  className="input"
                  style={{ width: '100%' }}
                  value={linkLabel}
                  disabled={busy}
                  placeholder="CKT-100"
                  onChange={(event) => setLinkLabel(event.target.value)}
                />
              </div>
            </>
          )}

          {needLink && renderSelect('lab-ops-link', LAB_OPS_F_LINK, linkId, linkOptions, setLinkId)}

          {(opKind === 'set_device_status' || opKind === 'set_interface_status' || opKind === 'set_link_status') &&
            renderSelect('lab-ops-status', LAB_OPS_F_STATUS, status, STATUS_OPTIONS, (value) =>
              setStatus(value as SimEntityStatus),
            )}

          <div className="field">
            <label className="field-label" htmlFor="lab-ops-desc">
              {LAB_OPS_F_DESC}
            </label>
            <input
              id="lab-ops-desc"
              className="input"
              style={{ width: '100%' }}
              value={description}
              disabled={busy}
              onChange={(event) => setDescription(event.target.value)}
            />
          </div>
          <div className="field">
            <label className="switch">
              <input
                type="checkbox"
                checked={runChecks}
                disabled={busy}
                onChange={(event) => setRunChecks(event.target.checked)}
              />
              {LAB_OPS_RUN_CHECKS}
            </label>
          </div>
        </>
      )}

      {error !== null && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </LabDialog>
  );
}
