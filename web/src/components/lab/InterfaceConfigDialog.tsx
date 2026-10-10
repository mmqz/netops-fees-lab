/**
 * 配置接口地址对话框：
 * - addr + prefix 输入，客户端 dotted-quad 与 0–32 校验；
 * - 留空提交或点「清除地址」= config_interface_ipv4 {null,null}（解除配置）；
 * - 提交动作委托给父级（统一 applyOps + toast + 刷新），本组件只做表单与校验。
 */

import { useEffect, useState } from 'react';
import type { SimOp } from '../../api/types';
import {
  BTN_CLEAR_ADDR,
  BTN_CONFIRM,
  LAB_I_ADDR_PLACEHOLDER,
  LAB_I_CLEAR_HINT,
  LAB_I_CONFIG_TARGET,
  LAB_I_CONFIG_TITLE,
  LAB_I_ERR_BOTH,
  LAB_I_F_ADDR,
  LAB_I_F_PREFIX,
  LAB_I_IPV4_UNSET,
  LAB_I_IPV4_VALUE,
  LAB_I_LABEL,
  LAB_I_PREFIX_PLACEHOLDER,
  format,
} from '../../i18n';
import { LabDialog } from './LabDialog';
import { validateIpv4Pair } from './selection';
import type { InterfaceRow } from './selection';

interface InterfaceConfigDialogProps {
  open: boolean;
  iface: InterfaceRow | null;
  busy?: boolean;
  onClose: () => void;
  onSubmit: (ops: SimOp[]) => void;
}

export function InterfaceConfigDialog({
  open,
  iface,
  busy = false,
  onClose,
  onSubmit,
}: InterfaceConfigDialogProps) {
  const [addr, setAddr] = useState('');
  const [prefix, setPrefix] = useState('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open || iface === null) return;
    setAddr(iface.ipv4Addr ?? '');
    setPrefix(iface.ipv4Prefix === null ? '' : String(iface.ipv4Prefix));
    setError(null);
  }, [open, iface]);

  function submit(): void {
    if (iface === null) return;
    if (addr.trim() === '' && prefix.trim() === '') {
      onSubmit([{ op: 'config_interface_ipv4', interfaceId: iface.id, ipv4Addr: null, ipv4Prefix: null }]);
      return;
    }
    if (addr.trim() === '' || prefix.trim() === '') {
      setError(LAB_I_ERR_BOTH);
      return;
    }
    // 与 OpsDialog/新建接口共用同一套纯函数校验（AUD2-1/FX2-2）
    const ipv4Error = validateIpv4Pair(addr, prefix);
    if (ipv4Error !== null) {
      setError(ipv4Error);
      return;
    }
    onSubmit([{ op: 'config_interface_ipv4', interfaceId: iface.id, ipv4Addr: addr.trim(), ipv4Prefix: Number(prefix) }]);
  }

  function clearAddr(): void {
    if (iface === null) return;
    onSubmit([{ op: 'config_interface_ipv4', interfaceId: iface.id, ipv4Addr: null, ipv4Prefix: null }]);
  }

  const current =
    iface === null
      ? ''
      : iface.ipv4Addr === null
        ? LAB_I_IPV4_UNSET
        : format(LAB_I_IPV4_VALUE, { addr: iface.ipv4Addr, prefix: iface.ipv4Prefix ?? 0 });

  return (
    <LabDialog
      open={open}
      title={LAB_I_CONFIG_TITLE}
      onClose={onClose}
      busy={busy}
      confirmLabel={BTN_CONFIRM}
      onConfirm={submit}
    >
      <div className="field">
        <label className="field-label">{LAB_I_CONFIG_TARGET}</label>
        <div className="cell-strong mono">
          {iface === null ? '' : format(LAB_I_LABEL, { device: iface.deviceCode, name: iface.name })}（{current}）
        </div>
      </div>
      <div className="lab-form-grid">
        <div className="field">
          <label className="field-label" htmlFor="lab-if-addr">
            {LAB_I_F_ADDR}
          </label>
          <input
            id="lab-if-addr"
            className="input"
            style={{ width: '100%' }}
            value={addr}
            disabled={busy}
            placeholder={LAB_I_ADDR_PLACEHOLDER}
            onChange={(event) => setAddr(event.target.value)}
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="lab-if-prefix">
            {LAB_I_F_PREFIX}
          </label>
          <input
            id="lab-if-prefix"
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
      <div className="modal-note">{LAB_I_CLEAR_HINT}</div>
      {error !== null && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="modal-form-actions" style={{ justifyContent: 'flex-start' }}>
        <button type="button" className="btn btn--danger-ghost" disabled={busy} onClick={clearAddr}>
          {BTN_CLEAR_ADDR}
        </button>
      </div>
    </LabDialog>
  );
}
