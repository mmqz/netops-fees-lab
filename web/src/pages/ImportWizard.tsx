/**
 * 数据导入页 /import：三步向导。
 * ① 上传工作簿（拖拽 + 选择，.xlsx ≤ 10MB 客户端校验）
 * ② 映射确认（每工作表：导入/跳过、资源类型、逐列目标字段，必填校验）
 * ③ 执行结果（汇总卡片 + 按状态筛选的行明细 + 后续动作）
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type {
  ChangeEvent,
  DragEvent,
  KeyboardEvent as ReactKeyboardEvent,
  ReactElement,
} from 'react';
import type {
  BatchRow,
  ColumnAnalysis,
  ImportAnalysisResponse,
  ImportResultResponse,
  MappingAction,
  MappingModel,
  Paged,
  ResourceType,
  RowStatus,
  SheetAnalysis,
  Severity,
} from '../api/types';
import { ApiError, filledIssueCounts, formatApiErrorText, listBatchRows, submitMapping, uploadWorkbook } from '../api/client';
import {
  BATCH_STATUS_LABELS,
  BTN_IMPORT_ANOTHER,
  BTN_NEXT_STEP,
  BTN_PREV_STEP,
  BTN_VIEW_ISSUES,
  BTN_VIEW_REPORT,
  ERR_NETWORK,
  FIELD_KIND_LABELS,
  format,
  formatFileSize,
  IMP_COL_MESSAGE,
  IMP_COL_RULE,
  IMP_COL_SOURCE,
  IMP_COL_SOURCE2,
  IMP_COL_ROW_STATUS,
  IMP_COL_SUGGEST,
  IMP_COL_TARGET,
  IMP_CONFIDENCE_HIGH,
  IMP_CONFIDENCE_MEDIUM,
  IMP_DROPZONE_DESC,
  IMP_DROPZONE_RETRY,
  IMP_DROPZONE_TITLE,
  IMP_ERR_NOT_XLSX,
  IMP_ERR_REQUIRED_UNMAPPED,
  IMP_ERR_TOO_LARGE,
  IMP_ERR_RETRY_HINT,
  IMP_ISSUE_STATS,
  IMP_MAPPED_COUNT,
  IMP_NATURAL_KEY_HINT,
  IMP_NATURAL_KEY_TAG,
  IMP_NO_ROWS,
  IMP_OPT_UNMAPPED,
  IMP_REQUIRED_TAG,
  IMP_RESOURCE_TYPE,
  IMP_RESULT_SUB,
  IMP_RESULT_TITLE,
  IMP_ROW_SOURCE,
  IMP_ROWS_IMPORTED,
  IMP_ROWS_INFO,
  IMP_ROWS_SKIPPED,
  IMP_ROWS_TOTAL,
  IMP_ROWS_FAILED,
  IMP_SELECTED_FILE,
  IMP_STEP1,
  IMP_STEP2,
  IMP_STEP3,
  IMP_TAB_ALL,
  IMP_TOGGLE_IMPORT,
  IMP_TOGGLE_SKIP,
  IMP_UPLOADED_INFO,
  IMP_UPLOADING,
  IMP_VALIDATION_TITLE,
  NO_VALUE,
  RESOURCE_TYPE_LABELS,
  ROW_STATUS_LABELS,
  SEVERITY_LABELS,
} from '../i18n';
import { fieldsForType, fieldLabel, naturalKeyField, requiredFields } from '../schema';
import type { FieldSpec } from '../schema';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';
import { ErrorBlock } from '../components/ErrorBlock';
import { Pagination } from '../components/Pagination';
import { RuleChip } from '../components/RuleChip';
import { Spinner } from '../components/Spinner';
import { Stepper } from '../components/Stepper';
import { useToast } from '../components/Toast';
import { IconUpload } from '../components/icons';

const MAX_FILE_SIZE = 10 * 1024 * 1024;
const WIZARD_STEPS = [
  { title: IMP_STEP1 },
  { title: IMP_STEP2 },
  { title: IMP_STEP3 },
];
const ROWS_PAGE_SIZE = 20;

// ---------------------------------------------------------------------------
// 向导状态
// ---------------------------------------------------------------------------

interface SheetDraft {
  sheet: string;
  action: MappingAction;
  resourceType: ResourceType;
  /** 目标字段 → 源表头。 */
  columnMapping: Record<string, string>;
}

/** 规则代码前缀 → 严重级别（与 internal/domain/rules.go 的映射一致）。 */
function severityForRule(code: string): Severity {
  if (code.startsWith('E')) return 'error';
  if (code.startsWith('W')) return 'warning';
  return 'info';
}

function validateFile(file: File): string | null {
  if (!file.name.toLowerCase().endsWith('.xlsx')) {
    return IMP_ERR_NOT_XLSX;
  }
  if (file.size > MAX_FILE_SIZE) {
    return IMP_ERR_TOO_LARGE;
  }
  return null;
}

/** 未提供后端建议映射时，按列建议（confidence>0 且字段属于该类型）预填。 */
function prefillFromColumns(sheet: SheetAnalysis, type: ResourceType): Record<string, string> {
  const valid = new Set<string>(fieldsForType(type).map((f) => f.field));
  const mapping: Record<string, string> = {};
  for (const column of sheet.columns) {
    if (column.suggestedField !== '' && column.confidence > 0 && valid.has(column.suggestedField)) {
      mapping[column.suggestedField] = column.header;
    }
  }
  return mapping;
}

function buildDrafts(analysis: ImportAnalysisResponse): SheetDraft[] {
  return analysis.sheets.map((sheet) => {
    const suggested = analysis.suggestedMapping.sheets.find((m) => m.sheet === sheet.sheet);
    const type: ResourceType = suggested?.resourceType ?? sheet.suggestedType ?? 'device';
    const action: MappingAction =
      suggested?.action ?? (sheet.suggestedType !== undefined ? 'import' : 'skip');
    const columnMapping: Record<string, string> =
      suggested !== undefined &&
      suggested.action === 'import' &&
      suggested.resourceType === type &&
      suggested.columnMapping !== undefined
        ? { ...suggested.columnMapping }
        : prefillFromColumns(sheet, type);
    return { sheet: sheet.sheet, action, resourceType: type, columnMapping };
  });
}

function buildMapping(drafts: SheetDraft[]): MappingModel {
  return {
    sheets: drafts.map((draft) =>
      draft.action === 'import'
        ? {
            sheet: draft.sheet,
            action: 'import',
            resourceType: draft.resourceType,
            keyColumns: [naturalKeyField(draft.resourceType)],
            columnMapping: draft.columnMapping,
          }
        : { sheet: draft.sheet, action: 'skip' },
    ),
  };
}

interface SheetError {
  sheet: string;
  message: string;
}

function collectErrors(drafts: SheetDraft[]): SheetError[] {
  const errors: SheetError[] = [];
  for (const draft of drafts) {
    if (draft.action !== 'import') continue;
    for (const spec of requiredFields(draft.resourceType)) {
      if (draft.columnMapping[spec.field] === undefined) {
        errors.push({
          sheet: draft.sheet,
          message: format(IMP_ERR_REQUIRED_UNMAPPED, {
            sheet: draft.sheet,
            field: fieldLabel(spec.field),
          }),
        });
      }
    }
  }
  return errors;
}

// ---------------------------------------------------------------------------
// 行状态徽章
// ---------------------------------------------------------------------------

const ROW_STATUS_VARIANTS: Record<RowStatus, string> = {
  imported: 'chip--success',
  skipped: 'chip--muted',
  failed: 'chip--error',
};

function RowStatusChip({ status }: { status: RowStatus }) {
  return (
    <span className={`chip ${ROW_STATUS_VARIANTS[status]}`}>
      <span className="chip-dot" aria-hidden="true" />
      {ROW_STATUS_LABELS[status]}
    </span>
  );
}

// ---------------------------------------------------------------------------
// 第②步：工作表映射卡片
// ---------------------------------------------------------------------------

interface SheetCardProps {
  draft: SheetDraft;
  sheet: SheetAnalysis;
  errors: SheetError[];
  onToggle: (action: MappingAction) => void;
  onTypeChange: (type: ResourceType) => void;
  onTargetChange: (header: string, field: string) => void;
}

function fieldOptionLabel(spec: FieldSpec): string {
  const tags: string[] = [];
  if (spec.required) tags.push(IMP_REQUIRED_TAG);
  if (spec.naturalKey) tags.push(IMP_NATURAL_KEY_TAG);
  const tagText = tags.length > 0 ? `（${tags.join(' · ')}）` : '';
  return `${fieldLabel(spec.field)}（${spec.field} · ${FIELD_KIND_LABELS[spec.kind]}）${tagText}`;
}

function confidenceChip(confidence: number): ReactElement | null {
  if (confidence >= 0.75) {
    return <span className="chip chip--success">{IMP_CONFIDENCE_HIGH}</span>;
  }
  if (confidence > 0) {
    return <span className="chip chip--warning">{IMP_CONFIDENCE_MEDIUM}</span>;
  }
  return null;
}

function SheetCard({ draft, sheet, errors, onToggle, onTypeChange, onTargetChange }: SheetCardProps) {
  const enabled = draft.action === 'import';
  const mappedCount = Object.keys(draft.columnMapping).length;

  const columns: TableColumn<ColumnAnalysis>[] = [
    {
      key: 'header',
      header: IMP_COL_SOURCE,
      render: (column) => (
        <div className="col-header-cell">
          <span className="cell-strong" title={column.samples.join(' · ')}>
            {column.header}
          </span>
          {column.samples.length > 0 && (
            <span className="col-samples" title={column.samples.join(' · ')}>
              {column.samples.join(' · ')}
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'suggestion',
      header: IMP_COL_SUGGEST,
      render: (column) =>
        column.suggestedField !== '' && column.confidence > 0 ? (
          <span className="suggest-cell">
            {fieldLabel(column.suggestedField)}
            {confidenceChip(column.confidence)}
          </span>
        ) : (
          NO_VALUE
        ),
    },
    {
      key: 'target',
      header: IMP_COL_TARGET,
      render: (column) => {
        const mappedField =
          Object.entries(draft.columnMapping).find(([, header]) => header === column.header)?.[0] ??
          '';
        return (
          <select
            className="select select--full"
            value={mappedField}
            disabled={!enabled}
            onChange={(event) => onTargetChange(column.header, event.target.value)}
          >
            <option value="">{IMP_OPT_UNMAPPED}</option>
            {fieldsForType(draft.resourceType).map((spec) => (
              <option key={spec.field} value={spec.field}>
                {fieldOptionLabel(spec)}
              </option>
            ))}
          </select>
        );
      },
    },
  ];

  return (
    <div className={`card sheet-card${enabled ? '' : ' sheet-card--disabled'}`}>
      <div className="sheet-card-head">
        <div className="sheet-card-title">
          <span className="cell-strong">{draft.sheet}</span>
          <span className="muted-note">
            {format(IMP_ROWS_INFO, {
              rows: sheet.rowCount,
              empty: sheet.skippedEmptyRows,
            })}
          </span>
        </div>
        <label className="switch">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(event) => onToggle(event.target.checked ? 'import' : 'skip')}
          />
          <span>{enabled ? IMP_TOGGLE_IMPORT : IMP_TOGGLE_SKIP}</span>
        </label>
      </div>
      {enabled && (
        <>
          <label className="field">
            <span className="field-label">{IMP_RESOURCE_TYPE}</span>
            <select
              className="select"
              value={draft.resourceType}
              onChange={(event) => onTypeChange(event.target.value as ResourceType)}
            >
              {(Object.keys(RESOURCE_TYPE_LABELS) as ResourceType[]).map((type) => (
                <option key={type} value={type}>
                  {RESOURCE_TYPE_LABELS[type]}
                </option>
              ))}
            </select>
          </label>
          <div className="table-wrap">
            <Table
              columns={columns}
              rows={sheet.columns}
              rowKey={(column) => column.header}
              ariaLabel={draft.sheet}
            />
          </div>
          <div className="sheet-card-foot">
            <span className="muted-note">
              {format(IMP_MAPPED_COUNT, { mapped: mappedCount, total: sheet.columns.length })}
            </span>
            <span className="muted-note">
              {format(IMP_NATURAL_KEY_HINT, {
                field: fieldLabel(naturalKeyField(draft.resourceType)),
              })}
            </span>
          </div>
          {errors.length > 0 && (
            <ul className="form-error-list" role="alert">
              {errors.map((error) => (
                <li key={error.message}>{error.message}</li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 页面
// ---------------------------------------------------------------------------

export function ImportWizardPage() {
  const navigate = useNavigate();
  const toast = useToast();
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const [step, setStep] = useState(0);
  const [file, setFile] = useState<File | null>(null);
  const [analysis, setAnalysis] = useState<ImportAnalysisResponse | null>(null);
  const [drafts, setDrafts] = useState<SheetDraft[]>([]);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);

  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<ImportResultResponse | null>(null);

  const [rowFilter, setRowFilter] = useState<RowStatus | ''>('');
  const [rowsPage, setRowsPage] = useState(1);
  const [rowsData, setRowsData] = useState<Paged<BatchRow> | null>(null);
  const [rowsLoading, setRowsLoading] = useState(false);
  const [rowsError, setRowsError] = useState<string | null>(null);

  const sheetErrors = useMemo(() => collectErrors(drafts), [drafts]);
  const canSubmit = sheetErrors.length === 0 && !submitting && analysis !== null;

  const startUpload = useCallback(
    (candidate: File): void => {
      const invalid = validateFile(candidate);
      if (invalid !== null) {
        setFile(null);
        setUploadError(invalid);
        return;
      }
      setFile(candidate);
      setUploadError(null);
      setUploading(true);
      uploadWorkbook(candidate)
        .then((response) => {
          setAnalysis(response);
          setDrafts(buildDrafts(response));
          setStep(1);
          setUploading(false);
        })
        .catch((err: unknown) => {
          setUploading(false);
          const message = err instanceof ApiError ? formatApiErrorText(err) : ERR_NETWORK;
          setUploadError(message);
          toast.show(message, 'error');
        });
    },
    [toast],
  );

  function handleDrop(event: DragEvent<HTMLDivElement>): void {
    event.preventDefault();
    setDragging(false);
    const dropped = event.dataTransfer.files.item(0);
    if (dropped !== null) {
      startUpload(dropped);
    }
  }

  function handlePick(event: ChangeEvent<HTMLInputElement>): void {
    const picked = event.target.files?.item(0) ?? null;
    event.target.value = '';
    if (picked !== null) {
      startUpload(picked);
    }
  }

  function updateDraft(sheetName: string, patch: Partial<SheetDraft>): void {
    setDrafts((prev) =>
      prev.map((draft) => (draft.sheet === sheetName ? { ...draft, ...patch } : draft)),
    );
  }

  function setColumnTarget(sheetName: string, header: string, field: string): void {
    setDrafts((prev) =>
      prev.map((draft) => {
        if (draft.sheet !== sheetName) return draft;
        const mapping: Record<string, string> = { ...draft.columnMapping };
        for (const [target, source] of Object.entries(mapping)) {
          if (source === header) {
            delete mapping[target];
          }
        }
        if (field !== '') {
          mapping[field] = header;
        }
        return { ...draft, columnMapping: mapping };
      }),
    );
  }

  function changeType(sheetName: string, type: ResourceType): void {
    setDrafts((prev) =>
      prev.map((draft) => {
        if (draft.sheet !== sheetName) return draft;
        const sheetInfo = analysis?.sheets.find((s) => s.sheet === sheetName);
        const columnMapping =
          sheetInfo === undefined ? {} : prefillFromColumns(sheetInfo, type);
        return { ...draft, resourceType: type, columnMapping };
      }),
    );
  }

  function submit(): void {
    if (analysis === null || !canSubmit) return;
    setSubmitting(true);
    submitMapping(analysis.batchId, buildMapping(drafts))
      .then((response) => {
        setResult(response);
        setRowFilter('');
        setRowsPage(1);
        setStep(2);
        setSubmitting(false);
      })
      .catch((err: unknown) => {
        setSubmitting(false);
        const message = err instanceof ApiError ? formatApiErrorText(err) : ERR_NETWORK;
        toast.show(message, 'error');
      });
  }

  const loadRows = useCallback(
    (batchId: string, status: RowStatus | '', page: number): void => {
      setRowsLoading(true);
      setRowsError(null);
      listBatchRows(batchId, status === '' ? undefined : status, page, ROWS_PAGE_SIZE)
        .then((paged) => {
          setRowsData(paged);
          setRowsLoading(false);
        })
        .catch((err: unknown) => {
          setRowsError(err instanceof ApiError ? formatApiErrorText(err) : null);
          setRowsLoading(false);
        });
    },
    [],
  );

  useEffect(() => {
    if (step === 2 && result !== null) {
      loadRows(result.batchId, rowFilter, rowsPage);
    }
  }, [step, result, rowFilter, rowsPage, loadRows]);

  function resetWizard(): void {
    setStep(0);
    setFile(null);
    setAnalysis(null);
    setDrafts([]);
    setResult(null);
    setUploadError(null);
    setUploading(false);
    setRowFilter('');
    setRowsPage(1);
    setRowsData(null);
    setRowsError(null);
  }

  function changeRowFilter(status: RowStatus | ''): void {
    setRowFilter(status);
    setRowsPage(1);
  }

  // -- 渲染 --------------------------------------------------------------

  const resultCounts = result === null ? null : filledIssueCounts(result.issues);
  const tabs: Array<{ key: RowStatus | ''; label: string; count: number | null }> = [
    { key: '', label: IMP_TAB_ALL, count: rowsData?.total ?? null },
    { key: 'imported', label: ROW_STATUS_LABELS.imported, count: null },
    { key: 'skipped', label: ROW_STATUS_LABELS.skipped, count: null },
    { key: 'failed', label: ROW_STATUS_LABELS.failed, count: null },
  ];

  const rowColumns: TableColumn<BatchRow>[] = [
    {
      key: 'source',
      header: IMP_COL_SOURCE2,
      render: (row) => format(IMP_ROW_SOURCE, { sheet: row.sheet, row: row.rowNumber }),
    },
    { key: 'status', header: IMP_COL_ROW_STATUS, render: (row) => <RowStatusChip status={row.status} /> },
    {
      key: 'rule',
      header: IMP_COL_RULE,
      render: (row) =>
        row.ruleCode !== undefined && row.ruleCode !== '' ? (
          <RuleChip code={row.ruleCode} severity={severityForRule(row.ruleCode)} />
        ) : (
          NO_VALUE
        ),
    },
    { key: 'message', header: IMP_COL_MESSAGE, render: (row) => row.message ?? NO_VALUE },
  ];

  return (
    <div className="wizard">
      <Stepper steps={WIZARD_STEPS} current={step} />

      {step === 0 && (
        <div className="card">
          <div
            className={`dropzone${dragging ? ' dropzone--over' : ''}`}
            role="button"
            tabIndex={0}
            aria-label={IMP_DROPZONE_TITLE}
            onClick={() => fileInputRef.current?.click()}
            onKeyDown={(event: ReactKeyboardEvent<HTMLDivElement>) => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                fileInputRef.current?.click();
              }
            }}
            onDragOver={(event) => {
              event.preventDefault();
              setDragging(true);
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={handleDrop}
          >
            <IconUpload size={36} />
            <div className="dropzone-title">{IMP_DROPZONE_TITLE}</div>
            <div className="dropzone-desc">{IMP_DROPZONE_DESC}</div>
          </div>
          <input
            ref={fileInputRef}
            type="file"
            accept=".xlsx"
            className="visually-hidden"
            onChange={handlePick}
          />
          {file !== null && (
            <div className="muted-note file-note">
              {format(IMP_SELECTED_FILE, {
                name: file.name,
                size: formatFileSize(file.size),
              })}
            </div>
          )}
          {uploading && (
            <div className="loading-panel">
              <Spinner />
              <span className="loading-text">{IMP_UPLOADING}</span>
            </div>
          )}
          {uploadError !== null && !uploading && (
            <div className="error-block" role="alert">
              <span className="error-block-message">
                {uploadError}
                {file !== null ? ` · ${IMP_ERR_RETRY_HINT}` : ''}
              </span>
              {file !== null && (
                <button
                  type="button"
                  className="btn btn--secondary btn--sm"
                  onClick={() => file !== null && startUpload(file)}
                >
                  {IMP_DROPZONE_RETRY}
                </button>
              )}
            </div>
          )}
        </div>
      )}

      {step === 1 && analysis !== null && (
        <>
          <div className="card wizard-info">{format(IMP_UPLOADED_INFO, {
            workbook: analysis.workbookName,
            sheets: analysis.sheets.length,
            batchId: analysis.batchId,
          })}</div>
          {drafts.map((draft) => {
            const sheetInfo = analysis.sheets.find((s) => s.sheet === draft.sheet);
            if (sheetInfo === undefined) return null;
            return (
              <SheetCard
                key={draft.sheet}
                draft={draft}
                sheet={sheetInfo}
                errors={sheetErrors.filter((error) => error.sheet === draft.sheet)}
                onToggle={(action) => updateDraft(draft.sheet, { action })}
                onTypeChange={(type) => changeType(draft.sheet, type)}
                onTargetChange={(header, field) => setColumnTarget(draft.sheet, header, field)}
              />
            );
          })}
          <div className="wizard-actions">
            <button type="button" className="btn btn--secondary" onClick={() => setStep(0)}>
              {BTN_PREV_STEP}
            </button>
            <button
              type="button"
              className="btn btn--primary"
              disabled={!canSubmit}
              onClick={submit}
            >
              {BTN_NEXT_STEP}
            </button>
          </div>
          {sheetErrors.length > 0 && (
            <div className="form-error-summary" role="alert">
              <div className="form-error-summary-title">{IMP_VALIDATION_TITLE}</div>
              <ul>
                {sheetErrors.map((error) => (
                  <li key={error.message}>{error.message}</li>
                ))}
              </ul>
            </div>
          )}
        </>
      )}

      {step === 2 && result !== null && resultCounts !== null && (
        <>
          <div className="card wizard-info">
            <span className="cell-strong">{IMP_RESULT_TITLE}</span>
            <span className="muted-note">
              {format(IMP_RESULT_SUB, {
                batchId: result.batchId,
                status: BATCH_STATUS_LABELS[result.status] ?? result.status,
              })}
            </span>
          </div>
          <section className="stat-grid">
            <div className="card stat-card">
              <div className="stat-label">{IMP_ROWS_IMPORTED}</div>
              <div className="stat-value stat-value--success">{result.summary.rowsImported}</div>
              <div className="stat-sub">
                {IMP_ROWS_TOTAL} {result.summary.rowsTotal}
              </div>
            </div>
            <div className="card stat-card">
              <div className="stat-label">{IMP_ROWS_SKIPPED}</div>
              <div className="stat-value">{result.summary.rowsSkipped}</div>
              <div className="stat-sub">
                {IMP_ROWS_TOTAL} {result.summary.rowsTotal}
              </div>
            </div>
            <div className="card stat-card">
              <div className="stat-label">{IMP_ROWS_FAILED}</div>
              <div className="stat-value stat-value--error">{result.summary.rowsFailed}</div>
              <div className="stat-sub">
                {IMP_ROWS_TOTAL} {result.summary.rowsTotal}
              </div>
            </div>
            <div className="card stat-card">
              <div className="stat-label">{IMP_ISSUE_STATS}</div>
              <div className="stat-value">{resultCounts.error + resultCounts.warning + resultCounts.info}</div>
              <div className="stat-sub">
                {SEVERITY_LABELS.error} {resultCounts.error} · {SEVERITY_LABELS.warning}{' '}
                {resultCounts.warning} · {SEVERITY_LABELS.info} {resultCounts.info}
              </div>
            </div>
          </section>

          <div className="card">
            <div className="tabs">
              {tabs.map((tab) => (
                <button
                  key={tab.key}
                  type="button"
                  className={`tab${rowFilter === tab.key ? ' tab--active' : ''}`}
                  onClick={() => changeRowFilter(tab.key)}
                >
                  {tab.label}
                  {tab.count !== null ? ` (${tab.count})` : ''}
                </button>
              ))}
            </div>
            {rowsError !== null ? (
              <ErrorBlock
                message={rowsError}
                onRetry={() => loadRows(result.batchId, rowFilter, rowsPage)}
              />
            ) : (
              <>
                <Table
                  columns={rowColumns}
                  rows={rowsData?.items ?? []}
                  rowKey={(row) => `${row.sheet}-${row.rowNumber}`}
                  loading={rowsLoading}
                  emptyText={IMP_NO_ROWS}
                  ariaLabel={IMP_RESULT_TITLE}
                />
                {rowsData !== null && rowsData.total > 0 && (
                  <Pagination
                    page={rowsData.page}
                    pageSize={rowsData.pageSize}
                    total={rowsData.total}
                    onChange={setRowsPage}
                  />
                )}
              </>
            )}
          </div>

          <div className="wizard-actions">
            <button
              type="button"
              className="btn btn--secondary"
              onClick={() => navigate(`/issues?batchId=${encodeURIComponent(result.batchId)}`)}
            >
              {BTN_VIEW_ISSUES}
            </button>
            <button
              type="button"
              className="btn btn--secondary"
              onClick={() => navigate(`/report?batchId=${encodeURIComponent(result.batchId)}`)}
            >
              {BTN_VIEW_REPORT}
            </button>
            <button type="button" className="btn btn--primary" onClick={resetWizard}>
              {BTN_IMPORT_ANOTHER}
            </button>
          </div>
        </>
      )}
    </div>
  );
}
