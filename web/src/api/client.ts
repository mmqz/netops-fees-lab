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
  FeeBatchItem,
  FeeBatchesList,
  FeeImportSummary,
  FeeSheetsImportSummary,
  FeeLinkItem,
  FeeLinkScanResult,
  FeeLinksList,
  HealthResponse,
  ImportAnalysisResponse,
  ImportResultResponse,
  IssueCounts,
  IssueItem,
  MappingModel,
  MetricKeyItem,
  MetricSeriesResult,
  OpenApiSpec,
  Paged,
  PartialIssueCounts,
  ResourceDetail,
  ResourceItem,
  ResourceMetricsResponse,
  ResourceType,
  ClueDecisionItem,
  FeeDuplicatesResult,
  FeeAliasProposal,
  FeeCrossReconcileResult,
  FeeDetailInput,
  FeeDetailItem,
  FeeDetailsList,
  FeeDetailReconcile,
  FeeSiteCodeAlias,
  FeeSiteCodeAliasesList,
  FeeMonthlyResult,
  FeeTotalsResult,
  FeePeriodProbeResult,
  FeeRiskRule,
  FeeRuleAuditList,
  FeeRulesList,
  RiskResult,
  RiskMapReport,
  RollbackResponse,
  RowStatus,
  RuleCode,
  Severity,
  StatsResponse,
  ValidateResponse,
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
    res = await fetch(`${BASE}${path}`, withAuthHeaders(init));
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
    res = await fetch(`${BASE}${path}`, withAuthHeaders(init));
  } catch {
    throw new ApiError(0, 'network_error', ERR_NETWORK);
  }
  if (!res.ok) {
    throw await parseApiError(res);
  }
  return await res.text();
}

// ---------------------------------------------------------------------------
// 访问令牌（v0.11-R13）：serve --auth-token 的写操作网关配套。
// 令牌存 localStorage（浏览器本地，随每个请求以 Bearer 附着）；
// 未设置时不发头——未启用网关的服务器不受影响。
// ---------------------------------------------------------------------------

const AUTH_STORAGE_KEY = 'netops_auth_token';

/** 读取已保存的访问令牌（'' = 未设置）。 */
export function getStoredAuthToken(): string {
  try {
    return window.localStorage.getItem(AUTH_STORAGE_KEY) ?? '';
  } catch {
    return '';
  }
}

/** 保存 / 清除访问令牌（'' = 清除）。 */
export function setStoredAuthToken(token: string): void {
  try {
    if (token === '') window.localStorage.removeItem(AUTH_STORAGE_KEY);
    else window.localStorage.setItem(AUTH_STORAGE_KEY, token);
  } catch {
    /* localStorage 不可用（隐私模式等）：静默降级为不携带 */
  }
}

/** 为请求注入 Authorization: Bearer（已存在 Authorization 头时不覆盖）；
 *  multipart 上传（无 headers 的 init）同样附着。 */
function withAuthHeaders(init?: RequestInit): RequestInit | undefined {
  const token = getStoredAuthToken();
  if (token === '') return init;
  const headers = new Headers(init?.headers);
  if (headers.get('Authorization') !== null) return init;
  headers.set('Authorization', `Bearer ${token}`);
  return { ...(init ?? {}), headers };
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
    res = await fetch(`${BASE}/batches/${encodeURIComponent(batchId)}`, withAuthHeaders({
      method: 'DELETE',
    }) ?? { method: 'DELETE' });
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

/** GET /api/stats */
export function getStats(): Promise<StatsResponse> {
  return requestJson<StatsResponse>('/stats');
}

/**
 * GET /api/governance/summary（v0.6.0 W14-D 治理总览）：待确认队列 + 决策审计 +
 * 问题分布 + 资源/关系/批次形态的一屏聚合（只读、无参数、空库恒全零）。
 */
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

/** W6-D：疑似改名提案列表（GET /api/renames）。 */
export interface RenamesQuery {
  status?: string;
  resourceId?: string;
  resourceType?: string;
  batchId?: string;
  limit?: number;
  offset?: number;
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

/**
 * GET /api/workspaces/{id}/layer-members?cluster=&page=&pageSize=
 * （#51：基站聚簇成员下钻分页；cluster = 聚合节点的 anchorDeviceId）。
 */
// ---------------------------------------------------------------------------
// 告警中心（GET /api/alerts + ack/clear，v0.7 检测告警）
// ---------------------------------------------------------------------------

/**
 * POST /api/alerts/{id}/ack — firing→acked（人类确认边）。
 * ackedBy 必填（确认人，告警审计字段）。
 */
/** GET /api/openapi.json — 路由表投影的 OpenAPI 3 文档（发现性入口）。 */
export function getOpenApiSpec(): Promise<OpenApiSpec> {
  return requestJson<OpenApiSpec>('/openapi.json');
}

// ---------------------------------------------------------------------------
// 检测规则管理（GET/POST/PATCH/DELETE /api/detection/rules，v0.10-R6）
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 维护窗口（/api/maintenance/windows，v0.12-R15）
// ---------------------------------------------------------------------------

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

/**
 * GET /api/resources/{id}/impact?direction=up|down — 影响面 + 根因候选。
 * down = 根因追查（§10.5③：从疑点向下找异常根因）；up = 影响面收集
 * （§10.6②：从异常源向上汇总业务受损面——后端默认）。
 */
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

// ---------------------------------------------------------------------------
// 费用分析层（#58 阶段 5）：导入 / 批次 / 回滚 / 风险七场景
// ---------------------------------------------------------------------------

/** POST /api/fees/imports（multipart：file + feeType + year + month） */
export function uploadFeeFile(
  file: File,
  feeType: string,
  year: number,
  month: number,
  trustDeclaredPeriod = false,
): Promise<FeeImportSummary> {
  const form = new FormData();
  form.append('file', file);
  form.append('feeType', feeType);
  form.append('year', String(year));
  form.append('month', String(month));
  if (trustDeclaredPeriod) {
    // #15：探测面板显示识别与申报不一致后，操作者选择以申报为准——
    // 覆盖留痕导入（period_source=unverified），永不静默。
    form.append('periodTrustDeclared', '1');
  }
  return requestJson<FeeImportSummary>('/fees/imports', {
    method: 'POST',
    body: form,
  });
}

/** POST /api/fees/imports/sheets（#125 多 sheet 台账：无 feeType，每 sheet 自动判型） */
export function uploadFeeSheetsFile(
  file: File,
  year: number,
  month: number,
  trustDeclaredPeriod = false,
): Promise<FeeSheetsImportSummary> {
  const form = new FormData();
  form.append('file', file);
  form.append('year', String(year));
  form.append('month', String(month));
  if (trustDeclaredPeriod) {
    form.append('periodTrustDeclared', '1');
  }
  return requestJson<FeeSheetsImportSummary>('/fees/imports/sheets', {
    method: 'POST',
    body: form,
  });
}

/** GET /api/fees/batches?status=（最新在前） */
export function fetchFeeBatches(status?: string): Promise<FeeBatchesList> {
  return requestJson<FeeBatchesList>(`/fees/batches${buildQuery({ status })}`);
}

/** POST /api/fees/batches/{id}/rollback（删分析行、保快照证据） */
export function rollbackFeeBatch(batchId: string): Promise<FeeBatchItem> {
  return requestJson<FeeBatchItem>(
    `/fees/batches/${encodeURIComponent(batchId)}/rollback`,
    { method: 'POST' },
  );
}

/** GET /api/fees/risks/{scenario}（七场景之一；501 engine_not_built = 默认构建未编译引擎） */
export function fetchFeeRisks(
  scenario: string,
  params: {
    feeType: string;
    year: number;
    month?: number;
    limit?: number;
    diffCents?: number;
    ratio?: number;
    z?: number;
    minSites?: number;
    status?: string;
    /** #119 审计视图：含已排除（dismissed）线索 */
    includeDecided?: boolean;
  },
): Promise<RiskResult> {
  const { includeDecided, ...rest } = params;
  return requestJson<RiskResult>(
    `/fees/risks/${encodeURIComponent(scenario)}${buildQuery({
      ...(rest as Record<string, string | number | undefined>),
      includeDecided: includeDecided === true ? '1' : undefined,
    })}`,
  );
}

/** GET /api/fees/query/totals（站址账期合计；#121 浏览器通道补全） */
export function fetchFeeTotals(params: {
  feeType: string;
  year: number;
  month?: number;
  limit?: number;
}): Promise<FeeTotalsResult> {
  return requestJson<FeeTotalsResult>(
    `/fees/query/totals${buildQuery(params as Record<string, string | number | undefined>)}`,
  );
}

/** GET /api/fees/query/duplicates（跨批次重复结算候选；#121 浏览器通道补全） */
export function fetchFeeDuplicates(params: {
  feeType: string;
  year: number;
  month?: number;
  limit?: number;
}): Promise<FeeDuplicatesResult> {
  return requestJson<FeeDuplicatesResult>(
    `/fees/query/duplicates${buildQuery(params as Record<string, string | number | undefined>)}`,
  );
}

/** GET /api/fees/query/monthly（#3 月度趋势序列——全年逐月 rows/sites/totalCents） */
export function fetchFeeMonthly(params: {
  feeType: string;
  year: number;
}): Promise<FeeMonthlyResult> {
  return requestJson<FeeMonthlyResult>(
    `/fees/query/monthly${buildQuery(params as Record<string, string | number | undefined>)}`,
  );
}

/** GET /api/fees/details —— 明细台账（#16：过滤 linkKey/period/feeType/status/category） */
export function fetchFeeDetails(params: {
  linkKey?: string;
  period?: string;
  feeType?: string;
  status?: string;
  category?: string;
}): Promise<FeeDetailsList> {
  return requestJson<FeeDetailsList>(
    `/fees/details${buildQuery(params as Record<string, string | undefined>)}`,
  );
}

/** POST /api/fees/details —— 明细补录（申报级；三强制外全部可选） */
export function addFeeDetail(input: FeeDetailInput): Promise<FeeDetailItem> {
  return requestJson<FeeDetailItem>('/fees/details', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
}

/** POST /api/fees/details/{id}/status —— 明细裁定（管理级：confirmed/rejected/unmatched） */
export function setFeeDetailStatus(
  id: string,
  status: string,
  rejectReason?: string,
): Promise<FeeDetailItem> {
  return requestJson<FeeDetailItem>(`/fees/details/${encodeURIComponent(id)}/status`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ status, rejectReason: rejectReason ?? '' }),
  });
}

/** GET /api/fees/details/reconcile —— 差额提示（明细侧 vs 系统表侧） */
export function reconcileFeeDetails(params: {
  linkKey: string;
  period: string;
  feeType: string;
}): Promise<FeeDetailReconcile> {
  return requestJson<FeeDetailReconcile>(
    `/fees/details/reconcile${buildQuery(params as Record<string, string | undefined>)}`,
  );
}

/** GET /api/fees/site-code-aliases —— 编码映射台账（#12） */
export function fetchFeeAliases(): Promise<FeeSiteCodeAliasesList> {
  return requestJson<FeeSiteCodeAliasesList>('/fees/site-code-aliases');
}

/** POST /api/fees/site-code-aliases —— 建立编码映射（#12 人工确认通道，留痕） */
export function addFeeAlias(input: {
  canonical: string;
  alias: string;
  source: string;
  confidence: string;
}): Promise<FeeSiteCodeAlias> {
  return requestJson<FeeSiteCodeAlias>('/fees/site-code-aliases', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
}

/** POST /api/fees/site-code-aliases/scan —— 扫描提议（只提议不落库） */
export function scanFeeAliases(): Promise<{ items: FeeAliasProposal[]; total: number; note: string }> {
  return requestJson<{ items: FeeAliasProposal[]; total: number; note: string }>(
    '/fees/site-code-aliases/scan', { method: 'POST' },
  );
}

/** DELETE /api/fees/site-code-aliases/{id} —— 移除映射（管理级治理） */
export function removeFeeAlias(id: string): Promise<{ removed: string }> {
  return requestJson<{ removed: string }>(`/fees/site-code-aliases/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

/** GET /api/fees/cross-reconcile?year=&month= —— 跨数据集对账表（#12） */
export function fetchFeeCrossReconcile(params: {
  year: number;
  month?: number;
}): Promise<FeeCrossReconcileResult> {
  return requestJson<FeeCrossReconcileResult>(
    `/fees/cross-reconcile${buildQuery(params as Record<string, number | undefined>)}`,
  );
}

/** POST /api/fees/links/scan — 分级扫描（精确/规则自动确认，模糊出提案） */
export function scanFeeLinks(): Promise<FeeLinkScanResult> {
  return requestJson<FeeLinkScanResult>('/fees/links/scan', { method: 'POST' });
}

/** GET /api/fees/links?status=&grade=&code=&resource_id=（最新在前，资源侧 join 展示） */
export function fetchFeeLinks(params: {
  status?: string;
  grade?: string;
  code?: string;
  resourceId?: string;
  page?: number;
  pageSize?: number;
}): Promise<FeeLinksList> {
  return requestJson<FeeLinksList>(
    `/fees/links${buildQuery({
      status: params.status,
      grade: params.grade,
      code: params.code,
      resource_id: params.resourceId,
      page: params.page,
      pageSize: params.pageSize,
    } as Record<string, string | number | undefined>)}`,
  );
}

/** POST /api/fees/links/{id}/confirm — 人工确认模糊提案（pending → confirmed） */
export function confirmFeeLink(id: string): Promise<FeeLinkItem> {
  return requestJson<FeeLinkItem>(
    `/fees/links/${encodeURIComponent(id)}/confirm`,
    { method: 'POST' },
  );
}

/** POST /api/fees/links/{id}/dismiss — 驳回模糊提案（审计保留，配对不再自动复提） */
export function dismissFeeLink(id: string): Promise<FeeLinkItem> {
  return requestJson<FeeLinkItem>(
    `/fees/links/${encodeURIComponent(id)}/dismiss`,
    { method: 'POST' },
  );
}

/** POST /api/fees/risk-clue-decisions — #119 风险线索人工裁定（UPSERT：可复议翻转） */
export function decideFeeRiskClue(body: {
  scenario: string;
  feeType: string;
  siteCode: string;
  year: number;
  month: number;
  status: 'verified' | 'dismissed';
  note?: string;
}): Promise<ClueDecisionItem> {
  return requestJson<ClueDecisionItem>('/fees/risk-clue-decisions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

/** POST /api/fees/period-probe（#15：multipart 只读探测——导入前确认面板的数据面；
 * 表型分类 + 三档账期识别 strong/weak/none，不落库） */
export function probeFeePeriod(file: File): Promise<FeePeriodProbeResult> {
  const form = new FormData();
  form.append('file', file);
  return requestJson<FeePeriodProbeResult>('/fees/period-probe', {
    method: 'POST',
    body: form,
  });
}

/** GET /api/fees/rules?enabled=（#2：规则列表——读开放，规则集本身是证据） */
export function fetchFeeRules(enabledOnly: boolean): Promise<FeeRulesList> {
  return requestJson<FeeRulesList>(`/fees/rules${enabledOnly ? '?enabled=1' : ''}`);
}

/** POST /api/fees/rules（#2：创建——申报级写通道；新规则默认停用观察态） */
export function createFeeRule(input: {
  name: string;
  feeType: string;
  metric: string;
  aggregation: string;
  dimension: string;
  comparator: string;
  threshold: number;
  windowMonths: number;
  severity: string;
  description: string;
}): Promise<FeeRiskRule> {
  return requestJson<FeeRiskRule>('/fees/rules', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
}

/** POST /api/fees/rules/{id}/enable|disable（#2：人类通道，管理级令牌） */
export function setFeeRuleEnabled(id: string, enabled: boolean): Promise<FeeRiskRule> {
  return requestJson<FeeRiskRule>(`/fees/rules/${encodeURIComponent(id)}/${enabled ? 'enable' : 'disable'}`, {
    method: 'POST',
  });
}

/** GET /api/fees/rules/{id}/audit（#2：动作台账 created/enabled/disabled + actor + 变更 JSON） */
export function fetchFeeRuleAudit(id: string): Promise<FeeRuleAuditList> {
  return requestJson<FeeRuleAuditList>(`/fees/rules/${encodeURIComponent(id)}/audit`);
}

/** GET /api/fees/risk-map（#18 站点风险地图数据面——云端只做渲染能力，
 * 坐标由本地资源表自带；无坐标时 points 的 lat/lng 为 null） */
export function fetchFeeRiskMap(params: {
  feeType: string;
  year: number;
  month?: number;
}): Promise<RiskMapReport> {
  return requestJson<RiskMapReport>(
    `/fees/risk-map${buildQuery(params as Record<string, string | number | undefined>)}`,
  );
}
