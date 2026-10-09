/**
 * NetOps Lab 前端 API 类型定义。
 *
 * 与 PROJECT_BRIEF §9 HTTP 契约逐字段对齐（字段名、大小写、嵌套结构），
 * 不得擅自增删字段。可选字段（?）仅用于后端确实可能省略或置空的位置
 * （Go omitempty / 空值 / 自由文本状态），前端读取时需做空值兜底。
 */

// ---------------------------------------------------------------------------
// 枚举（取值与 internal/domain 常量一致）
// ---------------------------------------------------------------------------

export type ResourceType = 'device' | 'ip_pool' | 'circuit';

export type Severity = 'error' | 'warning' | 'info';

export type RowStatus = 'imported' | 'skipped' | 'failed';

export type BatchStatus =
  | 'analyzing'
  | 'awaiting_mapping'
  | 'importing'
  | 'completed'
  | 'failed'
  | 'rolled_back';

export type RuleCode =
  | 'E001'
  | 'E002'
  | 'E003'
  | 'E004'
  | 'E005'
  | 'E006'
  | 'W001'
  | 'W002'
  | 'W003'
  | 'I001';

export type RelationType = 'assigned_to' | 'a_end' | 'z_end';

export type MappingAction = 'import' | 'skip';

export type ReportFormat = 'html' | 'json';

/** 资源状态枚举值（与后端存储一致：取值本身即中文标签）。 */
export type ResourceStatusValue = '在网' | '停用' | '在建' | '退网';

// ---------------------------------------------------------------------------
// 通用结构
// ---------------------------------------------------------------------------

export interface IssueCounts {
  error: number;
  warning: number;
  info: number;
}

/** Go map 编码可能省略计数为 0 的键，读取时需兜底为 0。 */
export type PartialIssueCounts = Partial<IssueCounts>;

export interface Paged<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
}

// ---------------------------------------------------------------------------
// 1. GET /api/health
// ---------------------------------------------------------------------------

export interface HealthResponse {
  status: string;
  version: string;
  ruleVersion: string;
}

// ---------------------------------------------------------------------------
// 2. POST /api/imports（multipart 字段 file）
// ---------------------------------------------------------------------------

export interface ColumnAnalysis {
  header: string;
  /** §9 契约含 index；后端当前实现省略，前端不依赖该字段。 */
  index?: number;
  samples: string[];
  suggestedField: string;
  confidence: number;
}

export interface SheetAnalysis {
  sheet: string;
  rowCount: number;
  skippedEmptyRows: number;
  columns: ColumnAnalysis[];
  /** omitempty：无法识别类型的工作表省略该字段。 */
  suggestedType?: ResourceType;
}

export interface SheetMapping {
  sheet: string;
  action: MappingAction;
  /** 仅 action=import 时有效（omitempty）。 */
  resourceType?: ResourceType;
  /** 仅 action=import 时有效（omitempty），等于该类型的自然键字段。 */
  keyColumns?: string[];
  /** 目标字段 ← 源表头（仅 import 时有效，omitempty）。 */
  columnMapping?: Record<string, string>;
}

export interface MappingModel {
  sheets: SheetMapping[];
}

export interface ImportAnalysisResponse {
  batchId: string;
  workbookName: string;
  sha256: string;
  sheets: SheetAnalysis[];
  suggestedMapping: MappingModel;
}

// ---------------------------------------------------------------------------
// 3. POST /api/imports/{id}/mapping
// ---------------------------------------------------------------------------

export interface ImportSummary {
  rowsTotal: number;
  rowsImported: number;
  rowsSkipped: number;
  rowsFailed: number;
}

export interface ImportResultResponse {
  batchId: string;
  status: BatchStatus;
  summary: ImportSummary;
  issues: PartialIssueCounts;
}

// ---------------------------------------------------------------------------
// 4. GET /api/batches
// ---------------------------------------------------------------------------

export interface BatchListItem {
  id: string;
  workbookName: string;
  status: BatchStatus;
  rowsTotal: number;
  rowsImported: number;
  rowsSkipped: number;
  rowsFailed: number;
  startedAt: string;
  finishedAt?: string | null;
}

// ---------------------------------------------------------------------------
// 5. GET /api/batches/{id}（详情 + 分工作表汇总）
// ---------------------------------------------------------------------------

export interface BatchSheetSummary {
  sheet: string;
  rowsTotal: number;
  rowsImported: number;
  rowsSkipped: number;
  rowsFailed: number;
}

export interface BatchDetail extends BatchListItem {
  sheets?: BatchSheetSummary[];
}

// ---------------------------------------------------------------------------
// 6. GET /api/batches/{id}/rows?status=&page=&pageSize=
// ---------------------------------------------------------------------------

export interface BatchRow {
  sheet: string;
  rowNumber: number;
  status: RowStatus;
  entityId?: string;
  ruleCode?: string;
  message?: string;
}

// ---------------------------------------------------------------------------
// 7. DELETE /api/batches/{id}（回滚结果；冲突时 409 batch_in_use）
// ---------------------------------------------------------------------------

/**
 * 回滚结果体。§9 未钉死具体字段，前端仅依赖 HTTP 状态码判断成败，
 * 字段全部可选，避免对未冻结字段做假设。
 */
export interface RollbackResponse {
  batchId?: string;
  status?: string;
}

// ---------------------------------------------------------------------------
// 8. GET /api/resources?type=&q=&status=&page=&pageSize=
// ---------------------------------------------------------------------------

export interface ResourceItem {
  id: string;
  type: ResourceType;
  naturalKey: string;
  name: string;
  /** 自由文本状态；空字符串表示未知（导入时未提供或被规则丢弃）。 */
  status: string;
  attributes: Record<string, string>;
  firstBatchId: string;
  updatedAt: string;
}

// ---------------------------------------------------------------------------
// 9. GET /api/resources/{id}
// ---------------------------------------------------------------------------

export interface SourceRef {
  workbook: string;
  sheet: string;
  rowNumber: number;
}

export interface ResourceDetail extends ResourceItem {
  sourceRefs: SourceRef[];
  relationCount: number;
}

// ---------------------------------------------------------------------------
// 10. GET /api/resources/{id}/relations?depth=1|2
// ---------------------------------------------------------------------------

export interface GraphNode {
  id: string;
  type: ResourceType;
  naturalKey: string;
  name: string;
}

export interface GraphEdge {
  id: string;
  relationType: RelationType;
  fromId: string;
  toId: string;
}

export interface RelationGraph {
  center: ResourceItem;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

// ---------------------------------------------------------------------------
// 11. GET /api/stats
// ---------------------------------------------------------------------------

export interface StatsResponse {
  totalResources: number;
  byType: Partial<Record<ResourceType, number>>;
  byStatus: Record<string, number>;
  byRelationType: Partial<Record<RelationType, number>>;
  issuesBySeverity: PartialIssueCounts;
  latestBatchId?: string | null;
}

// ---------------------------------------------------------------------------
// 12. GET /api/issues?batchId=&severity=&ruleCode=&page=&pageSize=
// ---------------------------------------------------------------------------

export interface IssueSource {
  workbook: string;
  sheet: string;
  rowNumber: number;
}

export interface IssueItem {
  id: string;
  ruleCode: RuleCode;
  severity: Severity;
  message: string;
  entityKind?: string;
  entityId?: string;
  entityKey?: string;
  source?: IssueSource | null;
  applicationGenerated: boolean;
  evidence?: Record<string, string>;
}

// ---------------------------------------------------------------------------
// 13. POST /api/validate
// ---------------------------------------------------------------------------

export interface ValidateResponse {
  issues: PartialIssueCounts;
  score: number;
}

// ---------------------------------------------------------------------------
// 15. GET /api/topology/check?fromDevice=&toDevice=&maxHops=
// ---------------------------------------------------------------------------

export interface TopologyHop {
  deviceId: string;
  deviceCode: string;
}

export interface TopologyCheckResponse {
  reachable: boolean;
  hops: TopologyHop[];
  pathLength: number;
}
