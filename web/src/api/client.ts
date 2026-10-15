/**
 * 类型化 HTTP 客户端。
 *
 * - 统一封装 fetch：JSON 编解码、错误信封 {"error":{"code","message"}} → ApiError；
 * - 所有端点函数与 §9 契约一一对应，返回值类型见 ./types；
 * - 基地址为同源 /api（开发环境由 vite 代理到 http://localhost:8080）；
 * - 提供 debounce 工具（搜索防抖 300ms）。
 */

import type {
  AlertAckResponse,
  AlertClearResponse,
  AlertItem,
  AlertsQuery,
  DetectionRuleCreateRequest,
  DetectionRuleDeleteResponse,
  DetectionRuleItem,
  DetectionRulePatchRequest,
  DetectionRulesListResponse,
  DetectionRunResponse,
  ImpactDocument,
  RunStateHistoryResponse,
  MetricKeyItem,
  MetricSeriesResult,
  ResourceMetricsResponse,
  TraverseDirection,
  TraverseDocument,
  ApplyResult,
  AttributeChangeItem,
  AttributeChangeList,
  BatchDetail,
  BatchListItem,
  BatchRow,
  ChangeSetItem,
  CheckpointList,
  CheckpointResult,
  CheckRunDetail,
  CheckRunItem,
  DeleteWorkspaceResult,
  DiffResult,
  GovernanceSummary,
  HealthResponse,
  ImportAnalysisResponse,
  ImportResultResponse,
  IssueCounts,
  IssueItem,
  MappingModel,
  MaterializeResult,
  OpRequest,
  Paged,
  PartialIssueCounts,
  RelationGraph,
  RestoreResult,
  ResourceDetail,
  ResourceItem,
  ResourceType,
  RollbackResponse,
  RowStatus,
  RuleCode,
  RunResult,
  Severity,
  SimPathResult,
  SimTopology,
  StatsResponse,
  TopologyCheckResponse,
  UndoResult,
  ValidateResponse,
  WorkspaceCreateRequest,
  WorkspaceDetail,
  WorkspaceSummary,
  RenameList,
  RenameProposalItem,
} from './types';
import { ERR_API_CHECKPOINT_MISSING_FILE, ERR_API_CONFLICT, ERR_API_INVALID_REQUEST, ERR_API_NOT_FOUND, ERR_API_NOT_LATEST, ERR_API_OP_FAILED, ERR_BAD_RESPONSE, ERR_HTTP_FALLBACK, ERR_NETWORK, format } from '../i18n';

const BASE = '/api';

// ---------------------------------------------------------------------------
// 错误类型
// ---------------------------------------------------------------------------

/** 后端错误信封抛出的统一异常（网络失败时 status=0）。 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** 后端原始 message（可能为空串——AUD2-3/FX2-4：apply 校验 400 常空消息）。 */
  readonly backendMessage: string;

  constructor(status: number, code: string, message: string, backendMessage?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.backendMessage = backendMessage ?? message;
  }
}

// ---------------------------------------------------------------------------
// 错误友好展示（FX2-4）
// ---------------------------------------------------------------------------

/** 错误码 → 中文主题行；英文技术原文作为次行（detail）保留。 */
export interface ApiErrorDisplay {
  title: string;
  detail: string | null;
}

const API_ERROR_TITLES: Record<string, string> = {
  invalid_request: ERR_API_INVALID_REQUEST,
  op_failed: ERR_API_OP_FAILED,
  not_found: ERR_API_NOT_FOUND,
  not_latest: ERR_API_NOT_LATEST,
  conflict: ERR_API_CONFLICT,
  checkpoint_missing_file: ERR_API_CHECKPOINT_MISSING_FILE,
};

/** 消息是否为英文技术文案（后端 §7 约定英文；出现 CJK 视为已本地化）。 */
function isTechnicalEnglish(message: string): boolean {
  return !/[\u4e00-\u9fff]/.test(message);
}

/**
 * 所有错误展示面的统一入口（FX2-4）：
 * - 空消息/英文技术消息 → 中文主题行 + 原文作次行；
 * - 中文消息 → 原样作为主题行；
 * - 非 ApiError → String 兜底。
 * 兼容 FX-1 修复前后（后端带不带 message 均可）。
 */
export function formatApiError(err: unknown): ApiErrorDisplay {
  if (!(err instanceof ApiError)) {
    return { title: err instanceof Error ? err.message : String(err), detail: null };
  }
  if (err.code === 'network_error') return { title: ERR_NETWORK, detail: null };
  if (err.code === 'bad_response') return { title: ERR_BAD_RESPONSE, detail: null };
  const raw = err.backendMessage;
  if (raw === '' || raw === ERR_NETWORK || raw === ERR_BAD_RESPONSE) {
    return { title: API_ERROR_TITLES[err.code] ?? err.message, detail: null };
  }
  if (isTechnicalEnglish(raw)) {
    return { title: API_ERROR_TITLES[err.code] ?? err.message, detail: raw };
  }
  return { title: raw, detail: null };
}

/** 纯文本场景（form-error 等）：主题行 + 原文拼成单行。 */
export function formatApiErrorText(err: unknown): string {
  const display = formatApiError(err);
  return display.detail === null ? display.title : `${display.title}（${display.detail}）`;
}

// ---------------------------------------------------------------------------
// 内部工具（unknown → 类型的安全收窄）
// ---------------------------------------------------------------------------

function asRecord(value: unknown): Record<string, unknown> | null {
  if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return null;
}

function strOf(rec: Record<string, unknown>, key: string): string | undefined {
  const value: unknown = rec[key];
  return typeof value === 'string' ? value : undefined;
}

function numOf(rec: Record<string, unknown>, key: string): number | undefined {
  const value: unknown = rec[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

/** 解析错误信封 {"error":{"code","message"}}，失败时回退到 HTTP 状态文案。 */
async function parseApiError(res: Response): Promise<ApiError> {
  let code = `http_${res.status}`;
  let message = format(ERR_HTTP_FALLBACK, { status: res.status });
  let backendMessage = '';
  try {
    const body: unknown = await res.json();
    const root = asRecord(body);
    const envelope = root === null ? null : asRecord(root['error']);
    if (envelope !== null) {
      const parsedCode = strOf(envelope, 'code');
      const parsedMessage = strOf(envelope, 'message');
      if (parsedCode !== undefined && parsedCode !== '') code = parsedCode;
      if (parsedMessage !== undefined && parsedMessage !== '') {
        message = parsedMessage;
        backendMessage = parsedMessage;
      }
    }
  } catch {
    // 响应体不是 JSON：保留回退文案
  }
  return new ApiError(res.status, code, message, backendMessage);
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, init);
  } catch {
    throw new ApiError(0, 'network_error', ERR_NETWORK);
  }
  if (!res.ok) {
    throw await parseApiError(res);
  }
  try {
    return (await res.json()) as T;
  } catch {
    throw new ApiError(res.status, 'bad_response', ERR_BAD_RESPONSE);
  }
}

async function requestText(path: string, init?: RequestInit): Promise<string> {
  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, init);
  } catch {
    throw new ApiError(0, 'network_error', ERR_NETWORK);
  }
  if (!res.ok) {
    throw await parseApiError(res);
  }
  return await res.text();
}

function buildQuery(params: Record<string, string | number | undefined>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') {
      search.set(key, String(value));
    }
  }
  const encoded = search.toString();
  return encoded === '' ? '' : `?${encoded}`;
}

// ---------------------------------------------------------------------------
// 响应归一化（对已知的历史形态做兼容，字段名仍以 §9 契约为准）
// ---------------------------------------------------------------------------

function countsFrom(rec: Record<string, unknown>): IssueCounts {
  const error = numOf(rec, 'error') ?? 0;
  const warning = numOf(rec, 'warning') ?? 0;
  const info = numOf(rec, 'info') ?? 0;
  return { error, warning, info };
}

/** 兼容空计数键缺失的 Go map 编码。 */
export function filledIssueCounts(counts: PartialIssueCounts | undefined): IssueCounts {
  return {
    error: counts?.error ?? 0,
    warning: counts?.warning ?? 0,
    info: counts?.info ?? 0,
  };
}

/**
 * 归一化 POST /api/imports/{id}/mapping 响应。
 * 契约形态：{"batchId","status","summary":{…},"issues":{…}}；
 * 兼容形态：internal/importer.Summary 直接序列化时的扁平结构
 * （rows* 平铺 + issuesBySeverity）。两种形态字段含义一致。
 */
export function normalizeImportResult(raw: unknown): ImportResultResponse | null {
  const root = asRecord(raw);
  if (root === null) return null;
  const batchId = strOf(root, 'batchId') ?? '';
  const status = strOf(root, 'status') ?? 'completed';
  const summaryRec = asRecord(root['summary']);
  const source = summaryRec ?? root;
  const summary = {
    rowsTotal: numOf(source, 'rowsTotal') ?? 0,
    rowsImported: numOf(source, 'rowsImported') ?? 0,
    rowsSkipped: numOf(source, 'rowsSkipped') ?? 0,
    rowsFailed: numOf(source, 'rowsFailed') ?? 0,
  };
  const countsRec = asRecord(root['issues']) ?? asRecord(root['issuesBySeverity']);
  const issues = countsRec === null ? {} : countsFrom(countsRec);
  return { batchId, status: status as ImportResultResponse['status'], summary, issues };
}

// ---------------------------------------------------------------------------
// 端点函数（§9 契约顺序）
// ---------------------------------------------------------------------------

/** GET /api/health */
export function getHealth(): Promise<HealthResponse> {
  return requestJson<HealthResponse>('/health');
}

/** POST /api/imports（multipart 字段 file）→ 202 工作簿解析结果 */
export async function uploadWorkbook(file: File): Promise<ImportAnalysisResponse> {
  const form = new FormData();
  form.append('file', file);
  return requestJson<ImportAnalysisResponse>('/imports', {
    method: 'POST',
    body: form,
  });
}

/** POST /api/imports/{id}/mapping → 202 执行结果 */
export async function submitMapping(
  batchId: string,
  mapping: MappingModel,
): Promise<ImportResultResponse> {
  const raw: unknown = await requestJson<unknown>(
    `/imports/${encodeURIComponent(batchId)}/mapping`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(mapping),
    },
  );
  const normalized = normalizeImportResult(raw);
  if (normalized === null) {
    throw new ApiError(500, 'bad_response', ERR_BAD_RESPONSE);
  }
  return normalized;
}

/** GET /api/batches（按开始时间倒序） */
export function listBatches(page: number, pageSize: number): Promise<Paged<BatchListItem>> {
  return requestJson<Paged<BatchListItem>>(
    `/batches${buildQuery({ page, pageSize })}`,
  );
}

/** GET /api/batches/{id}（详情 + 分工作表汇总） */
export function getBatch(batchId: string): Promise<BatchDetail> {
  return requestJson<BatchDetail>(`/batches/${encodeURIComponent(batchId)}`);
}

/** GET /api/batches/{id}/rows?status=&page=&pageSize= */
export function listBatchRows(
  batchId: string,
  status?: RowStatus,
  page?: number,
  pageSize?: number,
): Promise<Paged<BatchRow>> {
  return requestJson<Paged<BatchRow>>(
    `/batches/${encodeURIComponent(batchId)}/rows${buildQuery({ status, page, pageSize })}`,
  );
}

/**
 * DELETE /api/batches/{id}（回滚；被引用时 409 batch_in_use）。
 * 兼容 204 无响应体与 200 + 回滚结果体两种形态，前端不依赖具体字段。
 */
export async function deleteBatch(batchId: string): Promise<RollbackResponse> {
  let res: Response;
  try {
    res = await fetch(`${BASE}/batches/${encodeURIComponent(batchId)}`, {
      method: 'DELETE',
    });
  } catch {
    throw new ApiError(0, 'network_error', ERR_NETWORK);
  }
  if (!res.ok) {
    throw await parseApiError(res);
  }
  if (res.status === 204) {
    return {};
  }
  try {
    const raw: unknown = await res.json();
    const rec = asRecord(raw);
    return rec === null ? {} : (rec as RollbackResponse);
  } catch {
    return {};
  }
}

/** GET /api/resources?type=&q=&status=&page=&pageSize= */
export function listResources(query: ResourcesQuery): Promise<Paged<ResourceItem>> {
  return requestJson<Paged<ResourceItem>>(
    `/resources${buildQuery({
      type: query.type,
      q: query.q,
      status: query.status,
      page: query.page,
      pageSize: query.pageSize,
    })}`,
  );
}

/** GET /api/resources/{id}（含 sourceRefs 与 relationCount） */
export function getResource(id: string): Promise<ResourceDetail> {
  return requestJson<ResourceDetail>(`/resources/${encodeURIComponent(id)}`);
}

/** GET /api/resources/{id}/relations?depth=1|2 */
export function getResourceRelations(id: string, depth: 1 | 2): Promise<RelationGraph> {
  return requestJson<RelationGraph>(
    `/resources/${encodeURIComponent(id)}/relations${buildQuery({ depth })}`,
  );
}

/** GET /api/stats */
export function getStats(): Promise<StatsResponse> {
  return requestJson<StatsResponse>('/stats');
}

/**
 * GET /api/governance/summary（v0.6.0 W14-D 治理总览）：待确认队列 + 决策审计 +
 * 问题分布 + 资源/关系/批次形态的一屏聚合（只读、无参数、空库恒全零）。
 */
export function getGovernanceSummary(): Promise<GovernanceSummary> {
  return requestJson<GovernanceSummary>('/governance/summary');
}

/** GET /api/issues?batchId=&severity=&ruleCode=&page=&pageSize= */
export function listIssues(query: IssuesQuery): Promise<Paged<IssueItem>> {
  return requestJson<Paged<IssueItem>>(
    `/issues${buildQuery({
      batchId: query.batchId,
      severity: query.severity,
      ruleCode: query.ruleCode,
      page: query.page,
      pageSize: query.pageSize,
    })}`,
  );
}

/** POST /api/validate → 重算集合级规则并返回评分 */
export function runValidate(): Promise<ValidateResponse> {
  return requestJson<ValidateResponse>('/validate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({}),
  });
}

/** GET /api/report?format=html → 独立 HTML 文档文本 */
export function getReportHtml(batchId?: string): Promise<string> {
  return requestText(`/report${buildQuery({ batchId, format: 'html' })}`);
}

/** GET /api/report?format=json → JSON 报告文本（供下载，不做结构解析） */
export function getReportJson(batchId?: string): Promise<string> {
  return requestText(`/report${buildQuery({ batchId, format: 'json' })}`);
}

/** GET /api/topology/check?fromDevice=&toDevice=&maxHops= */
export function checkTopology(
  fromDevice: string,
  toDevice: string,
  maxHops?: number,
): Promise<TopologyCheckResponse> {
  return requestJson<TopologyCheckResponse>(
    `/topology/check${buildQuery({ fromDevice, toDevice, maxHops })}`,
  );
}

// ---------------------------------------------------------------------------
// 属性变更确认（W4-D，migration v11）
// ---------------------------------------------------------------------------

export interface AttributeChangesQuery {
  status?: 'pending' | 'applied' | 'dismissed' | 'all';
  resourceId?: string;
  batchId?: string;
  limit?: number;
  offset?: number;
}

/** GET /api/attribute-changes?status=&resource_id=&batch_id=&limit=&offset= */
export function listAttributeChanges(query: AttributeChangesQuery): Promise<AttributeChangeList> {
  return requestJson<AttributeChangeList>(
    `/attribute-changes${buildQuery({
      status: query.status,
      resource_id: query.resourceId,
      batch_id: query.batchId,
      limit: query.limit,
      offset: query.offset,
    })}`,
  );
}

/** POST /api/attribute-changes/{id}/apply（非 pending 409；旧值漂移 409 attribute_conflict） */
export function applyAttributeChange(id: string): Promise<AttributeChangeItem> {
  return requestJson<AttributeChangeItem>(
    `/attribute-changes/${encodeURIComponent(id)}/apply`,
    { method: 'POST' },
  );
}

/** POST /api/attribute-changes/{id}/dismiss（非 pending 409） */
export function dismissAttributeChange(id: string): Promise<AttributeChangeItem> {
  return requestJson<AttributeChangeItem>(
    `/attribute-changes/${encodeURIComponent(id)}/dismiss`,
    { method: 'POST' },
  );
}

/** W6-D：疑似改名提案列表（GET /api/renames）。 */
export interface RenamesQuery {
  status?: string;
  resourceId?: string;
  resourceType?: string;
  batchId?: string;
  limit?: number;
  offset?: number;
}
export function listRenames(query: RenamesQuery): Promise<RenameList> {
  return requestJson<RenameList>(
    `/renames${buildQuery({
      status: query.status,
      resource_id: query.resourceId,
      resource_type: query.resourceType,
      batch_id: query.batchId,
      limit: query.limit,
      offset: query.offset,
    })}`,
  );
}

/** 确认改名（POST /api/renames/{id}/apply）：合并身份，旧资源保留 ID 并接管新键。 */
export function applyRename(id: string): Promise<RenameProposalItem> {
  return requestJson<RenameProposalItem>(
    `/renames/${encodeURIComponent(id)}/apply`,
    { method: 'POST' },
  );
}

/** 拒绝改名（POST /api/renames/{id}/reject）：两个资源维持独立。 */
export function rejectRename(id: string): Promise<RenameProposalItem> {
  return requestJson<RenameProposalItem>(
    `/renames/${encodeURIComponent(id)}/reject`,
    { method: 'POST' },
  );
}

// ---------------------------------------------------------------------------
// 仿真工作台端点（PROJECT_BRIEF_P2 §7）
// ---------------------------------------------------------------------------

/** 兼容空计数键缺失的 Go map 编码（仿真检查计数与 Phase 1 同一问题）。 */
export function filledSeverityCounts(counts: PartialIssueCounts | undefined | null): IssueCounts {
  return {
    error: counts?.error ?? 0,
    warning: counts?.warning ?? 0,
    info: counts?.info ?? 0,
  };
}

function postJson<T>(path: string, body: unknown): Promise<T> {
  return requestJson<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

/** GET /api/workspaces?page=&pageSize= */
export function listWorkspaces(page = 1, pageSize = 20): Promise<Paged<WorkspaceSummary>> {
  return requestJson<Paged<WorkspaceSummary>>(
    `/workspaces${buildQuery({ page, pageSize })}`,
  );
}

/** POST /api/workspaces（物化默认取全部 completed 批次）→ 201 */
export function createWorkspace(req: WorkspaceCreateRequest): Promise<MaterializeResult> {
  return postJson<MaterializeResult>('/workspaces', req);
}

/** GET /api/workspaces/{id} */
export function getWorkspace(id: string): Promise<WorkspaceDetail> {
  return requestJson<WorkspaceDetail>(`/workspaces/${encodeURIComponent(id)}`);
}

/** DELETE /api/workspaces/{id} → 200 级联删除结果 */
export function deleteWorkspace(id: string): Promise<DeleteWorkspaceResult> {
  return requestJson<DeleteWorkspaceResult>(`/workspaces/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

/** GET /api/workspaces/{id}/topology */
export function getTopology(id: string): Promise<SimTopology> {
  return requestJson<SimTopology>(`/workspaces/${encodeURIComponent(id)}/topology`);
}

/** POST /api/workspaces/{id}/topology/path body {fromDevice,toDevice}（设备编码） */
export function topologyPath(id: string, fromDevice: string, toDevice: string): Promise<SimPathResult> {
  return postJson<SimPathResult>(
    `/workspaces/${encodeURIComponent(id)}/topology/path`,
    { fromDevice, toDevice },
  );
}

/** POST /api/workspaces/{id}/ops（语义失败 → 422 op_failed，状态不变） */
export function applyOps(id: string, req: OpRequest): Promise<ApplyResult> {
  return postJson<ApplyResult>(`/workspaces/${encodeURIComponent(id)}/ops`, req);
}

/** POST /api/workspaces/{id}/checks/run → 200 全量运行结果 */
export function runChecks(id: string): Promise<RunResult> {
  return postJson<RunResult>(`/workspaces/${encodeURIComponent(id)}/checks/run`, {});
}

/** GET /api/workspaces/{id}/checks?page=&pageSize=（按 run id 倒序） */
export function listChecks(id: string, page = 1, pageSize = 20): Promise<Paged<CheckRunItem>> {
  return requestJson<Paged<CheckRunItem>>(
    `/workspaces/${encodeURIComponent(id)}/checks${buildQuery({ page, pageSize })}`,
  );
}

/** GET /api/workspaces/{id}/checks/{runId}（findings 在前，基线在后） */
export function getCheckRun(id: string, runId: string): Promise<CheckRunDetail> {
  return requestJson<CheckRunDetail>(
    `/workspaces/${encodeURIComponent(id)}/checks/${encodeURIComponent(runId)}`,
  );
}

/** GET /api/workspaces/{id}/checks/{runId}/diff?compareTo=<runId2> */
export function diffRuns(id: string, runId: string, compareTo: string): Promise<DiffResult> {
  return requestJson<DiffResult>(
    `/workspaces/${encodeURIComponent(id)}/checks/${encodeURIComponent(runId)}/diff${buildQuery({ compareTo })}`,
  );
}

/** GET /api/workspaces/{id}/changesets?page=&pageSize=（倒序） */
export function listChangeSets(id: string, page = 1, pageSize = 20): Promise<Paged<ChangeSetItem>> {
  return requestJson<Paged<ChangeSetItem>>(
    `/workspaces/${encodeURIComponent(id)}/changesets${buildQuery({ page, pageSize })}`,
  );
}

/** POST /api/workspaces/{id}/undo body {changesetId?}（默认最新已应用） */
export function undo(id: string, changesetId?: string): Promise<UndoResult> {
  return postJson<UndoResult>(`/workspaces/${encodeURIComponent(id)}/undo`, { changesetId });
}

/** POST /api/workspaces/{id}/checkpoints body {label} → 201 */
export function createCheckpoint(id: string, label: string): Promise<CheckpointResult> {
  return postJson<CheckpointResult>(`/workspaces/${encodeURIComponent(id)}/checkpoints`, { label });
}

/** GET /api/workspaces/{id}/checkpoints（信封仅 items + total） */
export function listCheckpoints(id: string): Promise<CheckpointList> {
  return requestJson<CheckpointList>(`/workspaces/${encodeURIComponent(id)}/checkpoints`);
}

/** POST /api/workspaces/{id}/checkpoints/{cpId}/restore */
export function restoreCheckpoint(id: string, checkpointId: string): Promise<RestoreResult> {
  return postJson<RestoreResult>(
    `/workspaces/${encodeURIComponent(id)}/checkpoints/${encodeURIComponent(checkpointId)}/restore`,
    {},
  );
}

// ---------------------------------------------------------------------------
// 告警中心（GET /api/alerts + ack/clear，v0.7 检测告警）
// ---------------------------------------------------------------------------

/** GET /api/alerts?state=&severity=&resource_id=&page=&pageSize= */
export function listAlerts(query: AlertsQuery): Promise<Paged<AlertItem>> {
  return requestJson<Paged<AlertItem>>(
    `/alerts${buildQuery({
      state: query.state,
      severity: query.severity,
      resource_id: query.resourceId,
      page: query.page,
      pageSize: query.pageSize,
    })}`,
  );
}

/**
 * POST /api/alerts/{id}/ack — firing→acked（人类确认边）。
 * ackedBy 必填（确认人，告警审计字段）。
 */
export function ackAlert(id: string, ackedBy: string): Promise<AlertAckResponse> {
  return postJson<AlertAckResponse>(`/alerts/${encodeURIComponent(id)}/ack`, { ackedBy });
}

/** POST /api/alerts/{id}/clear — firing|acked→cleared（人类清除边，空体合法）。 */
export function clearAlert(id: string): Promise<AlertClearResponse> {
  return requestJson<AlertClearResponse>(`/alerts/${encodeURIComponent(id)}/clear`, {
    method: 'POST',
  });
}

// ---------------------------------------------------------------------------
// 检测规则管理（GET/POST/PATCH/DELETE /api/detection/rules，v0.10-R6）
// ---------------------------------------------------------------------------

/** GET /api/detection/rules — 全量规则（含禁用，rule_id 顺序，懒播种默认规则）。 */
export function listDetectionRules(): Promise<DetectionRulesListResponse> {
  return requestJson<DetectionRulesListResponse>('/detection/rules');
}

/** POST /api/detection/rules — 新建规则（201；重复 rule_id 409）。 */
export function createDetectionRule(
  req: DetectionRuleCreateRequest,
): Promise<DetectionRuleItem> {
  return postJson<DetectionRuleItem>('/detection/rules', req);
}

/** PATCH /api/detection/rules/{id} — 部分更新（metricKey 身份不可改，400）。 */
export function patchDetectionRule(
  id: string,
  req: DetectionRulePatchRequest,
): Promise<DetectionRuleItem> {
  return requestJson<DetectionRuleItem>(`/detection/rules/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
}

/** DELETE /api/detection/rules/{id} — 有告警的规则 409（审计保护）。 */
export function deleteDetectionRule(id: string): Promise<DetectionRuleDeleteResponse> {
  return requestJson<DetectionRuleDeleteResponse>(`/detection/rules/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

/** POST /api/detection/run — 一次全库扫描（from/to 可选 RFC3339 窗口）。 */
export function runDetection(from?: string, to?: string): Promise<DetectionRunResponse> {
  const body: Record<string, string> = {};
  if (from !== undefined && from !== '') body.from = from;
  if (to !== undefined && to !== '') body.to = to;
  return postJson<DetectionRunResponse>('/detection/run', body);
}

// ---------------------------------------------------------------------------
// 指标时序（GET /api/metrics/keys + GET /api/resources/{id}/metrics）
// ---------------------------------------------------------------------------

/** GET /api/metrics/keys — 封闭指标目录（key → 中文标签/单位映射源）。 */
export function listMetricKeys(): Promise<{ items: MetricKeyItem[]; total: number }> {
  return requestJson<{ items: MetricKeyItem[]; total: number }>('/metrics/keys');
}

/**
 * GET /api/resources/{id}/metrics — 该资源的全部指标序列（原始点，
 * 按 (metricKey, ts) 排序，确定性）。可选 keys/from/to/agg 过滤。
 */
export function getResourceMetrics(id: string, keys?: string[]): Promise<ResourceMetricsResponse> {
  return requestJson<ResourceMetricsResponse>(
    `/resources/${encodeURIComponent(id)}/metrics${buildQuery({ keys: keys?.join(',') })}`,
  );
}

export type { MetricSeriesResult };

// ---------------------------------------------------------------------------
// 图查询（v0.9-R3：traverse / impact；context 留作 R4+）
// ---------------------------------------------------------------------------

/** GET /api/resources/{id}/traverse?direction=down|up|both（默认 down）。 */
export function getTraverse(id: string, direction: TraverseDirection): Promise<TraverseDocument> {
  return requestJson<TraverseDocument>(
    `/resources/${encodeURIComponent(id)}/traverse${buildQuery({ direction })}`,
  );
}

/**
 * GET /api/resources/{id}/impact?direction=up|down — 影响面 + 根因候选。
 * down = 根因追查（§10.5③：从疑点向下找异常根因）；up = 影响面收集
 * （§10.6②：从异常源向上汇总业务受损面——后端默认）。
 */
export function getImpact(id: string, direction: 'up' | 'down'): Promise<ImpactDocument> {
  return requestJson<ImpactDocument>(
    `/resources/${encodeURIComponent(id)}/impact${buildQuery({ direction })}`,
  );
}

/** GET /api/resources/{id}/run-state/history?from=&to= — 状态时序（ts 升序）。 */
export function getRunStateHistory(id: string): Promise<RunStateHistoryResponse> {
  return requestJson<RunStateHistoryResponse>(`/resources/${encodeURIComponent(id)}/run-state/history`);
}

// ---------------------------------------------------------------------------
// 防抖工具
// ---------------------------------------------------------------------------

/** 通用防抖：延迟 ms 内的多次调用只保留最后一次（用于搜索框 300ms）。 */
export function debounce<A extends unknown[]>(
  fn: (...args: A) => void,
  ms: number,
): (...args: A) => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  return (...args: A): void => {
    if (timer !== undefined) {
      clearTimeout(timer);
    }
    timer = setTimeout(() => {
      timer = undefined;
      fn(...args);
    }, ms);
  };
}

// ---------------------------------------------------------------------------
// 查询参数接口
// ---------------------------------------------------------------------------

export interface ResourcesQuery {
  type?: ResourceType;
  q?: string;
  status?: string;
  page?: number;
  pageSize?: number;
}

export interface IssuesQuery {
  batchId?: string;
  severity?: Severity;
  ruleCode?: RuleCode;
  page?: number;
  pageSize?: number;
}
