/**
 * 类型化 HTTP 客户端。
 *
 * - 统一封装 fetch：JSON 编解码、错误信封 {"error":{"code","message"}} → ApiError；
 * - 所有端点函数与 §9 契约一一对应，返回值类型见 ./types；
 * - 基地址为同源 /api（开发环境由 vite 代理到 http://localhost:8080）；
 * - 提供 debounce 工具（搜索防抖 300ms）。
 */

import type {
  BatchDetail,
  BatchListItem,
  BatchRow,
  HealthResponse,
  ImportAnalysisResponse,
  ImportResultResponse,
  IssueCounts,
  IssueItem,
  MappingModel,
  Paged,
  PartialIssueCounts,
  RelationGraph,
  ResourceDetail,
  ResourceItem,
  ResourceType,
  RollbackResponse,
  RowStatus,
  RuleCode,
  Severity,
  StatsResponse,
  TopologyCheckResponse,
  ValidateResponse,
} from './types';
import { ERR_BAD_RESPONSE, ERR_HTTP_FALLBACK, ERR_NETWORK, format } from '../i18n';

const BASE = '/api';

// ---------------------------------------------------------------------------
// 错误类型
// ---------------------------------------------------------------------------

/** 后端错误信封抛出的统一异常（网络失败时 status=0）。 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
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
  try {
    const body: unknown = await res.json();
    const root = asRecord(body);
    const envelope = root === null ? null : asRecord(root['error']);
    if (envelope !== null) {
      const parsedCode = strOf(envelope, 'code');
      const parsedMessage = strOf(envelope, 'message');
      if (parsedCode !== undefined && parsedCode !== '') code = parsedCode;
      if (parsedMessage !== undefined && parsedMessage !== '') message = parsedMessage;
    }
  } catch {
    // 响应体不是 JSON：保留回退文案
  }
  return new ApiError(res.status, code, message);
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
