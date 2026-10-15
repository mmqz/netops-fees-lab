/**
 * 检测规则管理页 /rules（v0.10-R6）：
 * - 规则全量列表（后端 rule_id 顺序，懒播种默认规则集）：启停徽章、级别徽章、
 *   指标（key + 目录中文标签）、触发条件（比较器 + 阈值）、窗口（连续越限点数）、
 *   更新时间、行内操作（启停 / 编辑 / 删除）；
 * - 行展开详情：规则 ID、标签、创建/更新时间与完整参数（kv 审计视图）；
 * - 新建 / 编辑共用 RuleDialog：指标来自封闭目录（GET /api/metrics/keys，
 *   展示中文标签与合法区间提示）、比较器、阈值、窗口、级别、标签、启停；
 *   编辑态指标只读（metricKey 是身份字段，后端 400）；
 * - 删除走 ConfirmDialog：已被告警引用的规则后端 409（审计保护），前端 toast
 *   透传后端理由，并在文案中引导「改用停用」；
 * - 筛选：仅看启用中（开关）+ 指标下拉（列表数据派生）。
 *
 * 契约（与 internal/api/detection.go 一致）：
 * - POST 规则 201（重复 ruleId 409，ruleId 省略时后端生成 dr_<uuid>）；
 * - PATCH 部分更新，metricKey/ruleId 不可改（400）；
 * - DELETE 有告警的规则 409 rule_has_alerts；
 * - 后端是最终权威，前端校验只为减少往返。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type {
  AlertComparator,
  DetectionRuleItem,
  MetricKeyItem,
  Severity,
} from '../api/types';
import {
  createDetectionRule,
  deleteDetectionRule,
  formatApiError,
  formatApiErrorText,
  listDetectionRules,
  listMetricKeys,
  patchDetectionRule,
} from '../api/client';
import {
  ALERT_COMPARATOR_LABELS,
  BTN_CANCEL,
  BTN_PROCESSING,
  FILTER_ALL,
  NAV_RULES,
  RUL_ACTION_FAILED,
  RUL_BTN_DELETE,
  RUL_BTN_DISABLE,
  RUL_BTN_EDIT,
  RUL_BTN_ENABLE,
  RUL_BTN_NEW,
  RUL_BTN_SAVE,
  RUL_COL_ACTIONS,
  RUL_COL_CONDITION,
  RUL_COL_ENABLED,
  RUL_COL_LABEL,
  RUL_COL_METRIC,
  RUL_COL_SEVERITY,
  RUL_COL_UPDATED,
  RUL_COL_WINDOW,
  RUL_CREATE_TITLE,
  RUL_DELETED_TOAST,
  RUL_DELETE_MESSAGE,
  RUL_DELETE_TITLE,
  RUL_DISABLED_LABEL,
  RUL_EDIT_IDENTITY_HINT,
  RUL_EDIT_TITLE,
  RUL_EMPTY,
  RUL_ENABLED_LABEL,
  RUL_FIELD_COMPARATOR,
  RUL_FIELD_ENABLED,
  RUL_FIELD_LABEL,
  RUL_FIELD_LABEL_PLACEHOLDER,
  RUL_FIELD_METRIC,
  RUL_FIELD_METRIC_NEW,
  RUL_FIELD_SEVERITY,
  RUL_FIELD_THRESHOLD,
  RUL_FIELD_WINDOW,
  RUL_FILTER_ENABLED,
  RUL_FILTER_METRIC,
  RUL_HINT,
  RUL_METRIC_REQUIRED,
  RUL_METRIC_UNIT_HINT,
  RUL_METRIC_UNIT_NO_RANGE,
  RUL_CREATED_TOAST,
  RUL_RULE_CREATED_AT,
  RUL_RULE_META,
  RUL_RULE_META_ID,
  RUL_RULE_META_LABEL,
  RUL_RULE_THRESHOLD_LINE,
  RUL_RULE_UPDATED_AT,
  RUL_THRESHOLD_INVALID,
  RUL_TOGGLED_OFF_TOAST,
  RUL_TOGGLED_ON_TOAST,
  RUL_UPDATED_TOAST,
  RUL_WINDOW_INVALID,
  RUL_WINDOW_TEXT,
  SEVERITY_LABELS,
  format,
  formatDateTime,
  formatRelativeTime,
} from '../i18n';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ErrorBlock } from '../components/ErrorBlock';
import { SeverityBadge } from '../components/SeverityBadge';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';
import { useToast } from '../components/Toast';
import { useModalA11y } from '../hooks/useModalA11y';

const SEVERITY_OPTIONS: Severity[] = ['error', 'warning', 'info'];
const COMPARATOR_OPTIONS: AlertComparator[] = ['gt', 'gte', 'lt', 'lte'];
const MAX_WINDOW = 365;

/** 数字显示：整数无小数位，浮点保留最多 4 位有效（阈值通用）。 */
function fmtNum(value: number): string {
  if (Number.isInteger(value)) return String(value);
  return String(Number(value.toPrecision(4)));
}

/** RuleDialog 的受控表单值（创建/编辑共用）。 */
interface RuleFormValue {
  metricKey: string;
  comparator: AlertComparator;
  threshold: string;
  window: string;
  severity: Severity;
  label: string;
  enabled: boolean;
}

const EMPTY_FORM: RuleFormValue = {
  metricKey: '',
  comparator: 'gt',
  threshold: '',
  window: '1',
  severity: 'warning',
  label: '',
  enabled: true,
};

/**
 * 新建 / 编辑规则对话框。创建态 metricKey 为下拉（封闭目录 + 中文标签），
 * 编辑态只读展示（身份字段）并给出引导文案。
 */
function RuleDialog({
  mode,
  open,
  rule,
  metricKeys,
  busy,
  onConfirm,
  onCancel,
}: {
  mode: 'create' | 'edit';
  open: boolean;
  rule: DetectionRuleItem | null;
  metricKeys: MetricKeyItem[];
  busy: boolean;
  onConfirm: (value: RuleFormValue) => void;
  onCancel: () => void;
}) {
  const modalRef = useModalA11y(open);
  const [value, setValue] = useState<RuleFormValue>(EMPTY_FORM);

  // 打开时重置：create 清空 / edit 装载现有规则（上一条的草稿不串台）
  useEffect(() => {
    if (!open) return;
    if (mode === 'create') {
      setValue(EMPTY_FORM);
    } else if (rule !== null) {
      setValue({
        metricKey: rule.metricKey,
        comparator: rule.comparator,
        threshold: String(rule.threshold),
        window: String(rule.window),
        severity: rule.severity,
        label: rule.label,
        enabled: rule.enabled,
      });
    }
  }, [open, mode, rule]);

  if (!open) return null;

  const selectedKey = metricKeys.find((k) => k.metricKey === value.metricKey);
  const unitHint =
    selectedKey === undefined
      ? ''
      : selectedKey.minValue != null && selectedKey.maxValue != null
        ? format(RUL_METRIC_UNIT_HINT, {
            unit: selectedKey.unit,
            min: fmtNum(selectedKey.minValue),
            max: fmtNum(selectedKey.maxValue),
          })
        : format(RUL_METRIC_UNIT_NO_RANGE, { unit: selectedKey.unit });

  const thresholdValid = value.threshold.trim() !== '' && Number.isFinite(Number(value.threshold));
  const windowNum = Number(value.window);
  const windowValid =
    value.window.trim() !== '' &&
    Number.isInteger(windowNum) &&
    windowNum >= 1 &&
    windowNum <= MAX_WINDOW;
  const metricValid = mode === 'edit' || value.metricKey !== '';
  const formValid = thresholdValid && windowValid && metricValid;

  const title = mode === 'create' ? RUL_CREATE_TITLE : RUL_EDIT_TITLE;

  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={() => (busy ? undefined : onCancel())} />
      <div
        ref={modalRef}
        className="modal modal--form"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
      >
        <h3 className="modal-title">{title}</h3>
        {mode === 'edit' && rule !== null && (
          <>
            <div className="muted-note">
              {format(RUL_RULE_META, { id: rule.ruleId, at: formatDateTime(rule.createdAt) })}
            </div>
            <div className="muted-note">{RUL_EDIT_IDENTITY_HINT}</div>
          </>
        )}
        <div className="field">
          <label className="field-label" htmlFor="rule-metric">
            {mode === 'create' ? RUL_FIELD_METRIC_NEW : RUL_FIELD_METRIC}
          </label>
          {mode === 'create' ? (
            <select
              id="rule-metric"
              className="select"
              value={value.metricKey}
              disabled={busy}
              onChange={(event) => setValue((v) => ({ ...v, metricKey: event.target.value }))}
            >
              <option value="">{RUL_METRIC_REQUIRED}</option>
              {metricKeys.map((k) => (
                <option key={k.metricKey} value={k.metricKey}>
                  {k.metricKey} · {k.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              id="rule-metric"
              className="input mono"
              value={value.metricKey}
              readOnly
              disabled={busy}
            />
          )}
          {unitHint !== '' && <div className="muted-note">{unitHint}</div>}
        </div>
        <div className="field-row">
          <div className="field">
            <label className="field-label" htmlFor="rule-comparator">
              {RUL_FIELD_COMPARATOR}
            </label>
            <select
              id="rule-comparator"
              className="select"
              value={value.comparator}
              disabled={busy}
              onChange={(event) =>
                setValue((v) => ({ ...v, comparator: event.target.value as AlertComparator }))
              }
            >
              {COMPARATOR_OPTIONS.map((c) => (
                <option key={c} value={c}>
                  {ALERT_COMPARATOR_LABELS[c]}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label className="field-label" htmlFor="rule-threshold">
              {RUL_FIELD_THRESHOLD}
            </label>
            <input
              id="rule-threshold"
              className="input"
              type="number"
              step="any"
              value={value.threshold}
              disabled={busy}
              onChange={(event) => setValue((v) => ({ ...v, threshold: event.target.value }))}
            />
            {!thresholdValid && value.threshold.trim() !== '' && (
              <div className="form-error">{RUL_THRESHOLD_INVALID}</div>
            )}
          </div>
        </div>
        <div className="field-row">
          <div className="field">
            <label className="field-label" htmlFor="rule-window">
              {RUL_FIELD_WINDOW}
            </label>
            <input
              id="rule-window"
              className="input"
              type="number"
              min={1}
              max={MAX_WINDOW}
              step={1}
              value={value.window}
              disabled={busy}
              onChange={(event) => setValue((v) => ({ ...v, window: event.target.value }))}
            />
            {!windowValid && <div className="form-error">{RUL_WINDOW_INVALID}</div>}
          </div>
          <div className="field">
            <label className="field-label" htmlFor="rule-severity">
              {RUL_FIELD_SEVERITY}
            </label>
            <select
              id="rule-severity"
              className="select"
              value={value.severity}
              disabled={busy}
              onChange={(event) => setValue((v) => ({ ...v, severity: event.target.value as Severity }))}
            >
              {SEVERITY_OPTIONS.map((s) => (
                <option key={s} value={s}>
                  {SEVERITY_LABELS[s]}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="rule-label">
            {RUL_FIELD_LABEL}
          </label>
          <input
            id="rule-label"
            className="input"
            value={value.label}
            placeholder={RUL_FIELD_LABEL_PLACEHOLDER}
            disabled={busy}
            onChange={(event) => setValue((v) => ({ ...v, label: event.target.value }))}
          />
        </div>
        <label className="switch">
          <input
            type="checkbox"
            checked={value.enabled}
            disabled={busy}
            onChange={(event) => setValue((v) => ({ ...v, enabled: event.target.checked }))}
          />
          {RUL_FIELD_ENABLED}
        </label>
        <div className="modal-actions">
          <button type="button" className="btn" disabled={busy} onClick={onCancel}>
            {BTN_CANCEL}
          </button>
          <button
            type="button"
            className="btn btn--primary"
            disabled={busy || !formValid}
            onClick={() => onConfirm(value)}
          >
            {busy ? BTN_PROCESSING : RUL_BTN_SAVE}
          </button>
        </div>
      </div>
    </div>
  );
}

export function RulesPage() {
  const [rules, setRules] = useState<DetectionRuleItem[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);

  const [onlyEnabled, setOnlyEnabled] = useState(false);
  const [metricFilter, setMetricFilter] = useState('');

  // 封闭指标目录（RuleDialog 的下拉数据源，懒加载一次）
  const [metricKeys, setMetricKeys] = useState<MetricKeyItem[]>([]);

  // 创建 / 编辑 / 删除 / 行内启停
  const [createOpen, setCreateOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<DetectionRuleItem | null>(null);
  const [dialogBusy, setDialogBusy] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<DetectionRuleItem | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [toggleBusyId, setToggleBusyId] = useState<string | null>(null);

  const requestSeqRef = useRef(0);
  const toast = useToast();

  const fetchRules = useCallback((): void => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    listDetectionRules()
      .then((res) => {
        if (requestSeqRef.current !== seq) return;
        setRules(res.items);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (requestSeqRef.current !== seq) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, []);

  useEffect(() => {
    fetchRules();
  }, [fetchRules]);

  // 指标目录懒加载（对话框打开前就绪）
  useEffect(() => {
    listMetricKeys()
      .then((res) => setMetricKeys(res.items))
      .catch(() => {
        /* 目录加载失败不阻塞列表；对话框下拉为空并显示必选提示 */
      });
  }, []);

  function failToast(err: unknown): void {
    const text = formatApiErrorText(err);
    toast.show(format(RUL_ACTION_FAILED, { message: text }), 'error');
  }

  function submitCreate(value: RuleFormValue): void {
    setDialogBusy(true);
    createDetectionRule({
      metricKey: value.metricKey,
      comparator: value.comparator,
      threshold: Number(value.threshold),
      window: Number(value.window),
      severity: value.severity,
      label: value.label,
      enabled: value.enabled,
    })
      .then((rule) => {
        setDialogBusy(false);
        setCreateOpen(false);
        toast.show(format(RUL_CREATED_TOAST, { id: rule.ruleId }), 'success');
        fetchRules();
      })
      .catch((err: unknown) => {
        setDialogBusy(false);
        failToast(err);
      });
  }

  function submitEdit(value: RuleFormValue): void {
    if (editTarget === null) return;
    setDialogBusy(true);
    patchDetectionRule(editTarget.ruleId, {
      comparator: value.comparator,
      threshold: Number(value.threshold),
      window: Number(value.window),
      severity: value.severity,
      label: value.label,
      enabled: value.enabled,
    })
      .then((rule) => {
        setDialogBusy(false);
        setEditTarget(null);
        toast.show(format(RUL_UPDATED_TOAST, { id: rule.ruleId }), 'success');
        fetchRules();
      })
      .catch((err: unknown) => {
        setDialogBusy(false);
        failToast(err);
      });
  }

  function toggleEnabled(rule: DetectionRuleItem): void {
    setToggleBusyId(rule.ruleId);
    patchDetectionRule(rule.ruleId, { enabled: !rule.enabled })
      .then((updated) => {
        setToggleBusyId(null);
        toast.show(
          updated.enabled
            ? format(RUL_TOGGLED_ON_TOAST, { id: updated.ruleId })
            : format(RUL_TOGGLED_OFF_TOAST, { id: updated.ruleId }),
          'success',
        );
        fetchRules();
      })
      .catch((err: unknown) => {
        setToggleBusyId(null);
        failToast(err);
      });
  }

  function confirmDelete(): void {
    if (deleteTarget === null) return;
    setDeleteBusy(true);
    deleteDetectionRule(deleteTarget.ruleId)
      .then((res) => {
        setDeleteBusy(false);
        setDeleteTarget(null);
        toast.show(format(RUL_DELETED_TOAST, { id: res.ruleId }), 'success');
        fetchRules();
      })
      .catch((err: unknown) => {
        setDeleteBusy(false);
        failToast(err);
      });
  }

  // 指标目录的中文标签映射（表格列展示）
  const metricLabelOf = useMemo(() => {
    const map = new Map<string, string>();
    for (const k of metricKeys) map.set(k.metricKey, k.label);
    return (key: string): string => map.get(key) ?? '';
  }, [metricKeys]);

  // 筛选后的列表（客户端过滤：规则总量为个位数量级）
  const visibleRules = useMemo(() => {
    if (rules === null) return [];
    return rules.filter(
      (r) =>
        (!onlyEnabled || r.enabled) && (metricFilter === '' || r.metricKey === metricFilter),
    );
  }, [rules, onlyEnabled, metricFilter]);

  // 指标下拉选项（列表数据派生，确定性顺序）
  const metricOptions = useMemo(() => {
    if (rules === null) return [];
    return [...new Set(rules.map((r) => r.metricKey))].sort();
  }, [rules]);

  const columns = useMemo<TableColumn<DetectionRuleItem>[]>(
    () => [
      {
        key: 'enabled',
        header: RUL_COL_ENABLED,
        render: (row) => (
          <span className={`chip ${row.enabled ? 'chip--success' : 'chip--muted'}`}>
            <span className="chip-dot" aria-hidden="true" />
            {row.enabled ? RUL_ENABLED_LABEL : RUL_DISABLED_LABEL}
          </span>
        ),
      },
      {
        key: 'severity',
        header: RUL_COL_SEVERITY,
        render: (row) => <SeverityBadge severity={row.severity} />,
      },
      {
        key: 'label',
        header: RUL_COL_LABEL,
        render: (row) =>
          row.label !== '' ? (
            row.label
          ) : (
            <span className="muted-note">—</span>
          ),
      },
      {
        key: 'metric',
        header: RUL_COL_METRIC,
        render: (row) => (
          <span className="mono" title={metricLabelOf(row.metricKey)}>
            {row.metricKey}
          </span>
        ),
      },
      {
        key: 'condition',
        header: RUL_COL_CONDITION,
        render: (row) => (
          <span className="mono">
            {ALERT_COMPARATOR_LABELS[row.comparator]} {fmtNum(row.threshold)}
          </span>
        ),
      },
      {
        key: 'window',
        header: RUL_COL_WINDOW,
        render: (row) => format(RUL_WINDOW_TEXT, { n: row.window }),
      },
      {
        key: 'updatedAt',
        header: RUL_COL_UPDATED,
        render: (row) => (
          <span title={formatDateTime(row.updatedAt)}>{formatRelativeTime(row.updatedAt)}</span>
        ),
      },
      {
        key: 'actions',
        header: RUL_COL_ACTIONS,
        render: (row) => (
          <span className="alert-actions">
            <button
              type="button"
              className="btn btn--sm"
              title={row.ruleId}
              disabled={toggleBusyId !== null}
              onClick={(event) => {
                event.stopPropagation();
                toggleEnabled(row);
              }}
            >
              {row.enabled ? RUL_BTN_DISABLE : RUL_BTN_ENABLE}
            </button>
            <button
              type="button"
              className="btn btn--sm"
              title={row.ruleId}
              disabled={toggleBusyId !== null}
              onClick={(event) => {
                event.stopPropagation();
                setEditTarget(row);
              }}
            >
              {RUL_BTN_EDIT}
            </button>
            <button
              type="button"
              className="btn btn--sm btn--danger-ghost"
              title={row.ruleId}
              disabled={toggleBusyId !== null}
              onClick={(event) => {
                event.stopPropagation();
                setDeleteTarget(row);
              }}
            >
              {RUL_BTN_DELETE}
            </button>
          </span>
        ),
      },
    ],
    [metricLabelOf, toggleBusyId],
  );

  return (
    <div className="card">
      <div className="filter-bar">
        <label className="switch filter-field">
          <input
            type="checkbox"
            checked={onlyEnabled}
            onChange={(event) => setOnlyEnabled(event.target.checked)}
          />
          {RUL_FILTER_ENABLED}
        </label>
        <label className="filter-field">
          <span className="filter-label">{RUL_FILTER_METRIC}</span>
          <select
            className="select"
            value={metricFilter}
            onChange={(event) => setMetricFilter(event.target.value)}
          >
            <option value="">{FILTER_ALL}</option>
            {metricOptions.map((key) => (
              <option key={key} value={key}>
                {key}
                {metricLabelOf(key) !== '' ? ` · ${metricLabelOf(key)}` : ''}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="btn btn--primary filter-push"
          onClick={() => setCreateOpen(true)}
        >
          {RUL_BTN_NEW}
        </button>
      </div>

      <div className="muted-note table-hint">{RUL_HINT}</div>

      {error !== null ? (
        <ErrorBlock message={error} detail={errorDetail} onRetry={fetchRules} />
      ) : (
        <Table
          columns={columns}
          rows={visibleRules}
          rowKey={(row) => row.ruleId}
          loading={loading}
          emptyText={RUL_EMPTY}
          renderExpanded={(row) => (
            <div className="evidence">
              <div className="evidence-title">{row.label !== '' ? row.label : row.ruleId}</div>
              <table className="kv-table">
                <tbody>
                  <tr>
                    <th scope="row">{RUL_RULE_META_ID}</th>
                    <td>
                      <span className="mono">{row.ruleId}</span>
                    </td>
                  </tr>
                  <tr>
                    <th scope="row">{RUL_RULE_META_LABEL}</th>
                    <td>{row.label !== '' ? row.label : '—'}</td>
                  </tr>
                  <tr>
                    <th scope="row">{RUL_RULE_CREATED_AT}</th>
                    <td>{formatDateTime(row.createdAt)}</td>
                  </tr>
                  <tr>
                    <th scope="row">{RUL_RULE_UPDATED_AT}</th>
                    <td>{formatDateTime(row.updatedAt)}</td>
                  </tr>
                  <tr>
                    <th scope="row">{RUL_RULE_THRESHOLD_LINE}</th>
                    <td>
                      <span className="mono">
                        {row.metricKey} {ALERT_COMPARATOR_LABELS[row.comparator]}{' '}
                        {fmtNum(row.threshold)} · {format(RUL_WINDOW_TEXT, { n: row.window })}
                      </span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
          ariaLabel={NAV_RULES}
        />
      )}

      <RuleDialog
        mode="create"
        open={createOpen}
        rule={null}
        metricKeys={metricKeys}
        busy={dialogBusy}
        onConfirm={submitCreate}
        onCancel={() => {
          if (!dialogBusy) setCreateOpen(false);
        }}
      />
      <RuleDialog
        mode="edit"
        open={editTarget !== null}
        rule={editTarget}
        metricKeys={metricKeys}
        busy={dialogBusy}
        onConfirm={submitEdit}
        onCancel={() => {
          if (!dialogBusy) setEditTarget(null);
        }}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        title={RUL_DELETE_TITLE}
        message={
          deleteTarget !== null
            ? format(RUL_DELETE_MESSAGE, {
                label: deleteTarget.label !== '' ? deleteTarget.label : deleteTarget.metricKey,
                ruleId: deleteTarget.ruleId,
              })
            : RUL_DELETE_TITLE
        }
        danger
        busy={deleteBusy}
        confirmLabel={deleteBusy ? BTN_PROCESSING : RUL_BTN_DELETE}
        onConfirm={confirmDelete}
        onCancel={() => {
          if (!deleteBusy) setDeleteTarget(null);
        }}
      />
    </div>
  );
}
