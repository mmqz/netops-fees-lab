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

export type ResourceType = 'device' | 'ip_pool' | 'circuit' | 'port' | 'base_station';

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
  | 'W004'
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
  /** 1-based Excel 列位置（A=1；后端 analyze 始终发出该字段）。 */
  index: number;
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
  /** 工作簿内容摘要（后端始终发出）。 */
  sha256: string;
  status: BatchStatus;
  rowsTotal: number;
  rowsImported: number;
  rowsSkipped: number;
  rowsFailed: number;
  /** 失败原因（无失败时空串，后端始终发出）。 */
  error: string;
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
  /**
   * 动态键 map（后端 Go map[string]int）：键集由服务端决定——
   * device/ip_pool/circuit 恒出现（计 0 亦发），port 等新增类型仅在有数据时出现。
   * 渲染必须按实际键遍历（Object.entries），不得在前端硬编码类型清单（R1-D4）。
   */
  byType: Record<string, number>;
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

// ---------------------------------------------------------------------------
// 仿真工作台（Phase 2 · PROJECT_BRIEF_P2 §7）
// ---------------------------------------------------------------------------

/** 仿真实体状态（设备/接口/链路共用）。 */
export type SimEntityStatus = 'up' | 'down';

/** 工作台状态。 */
export type WorkspaceStatus = 'active' | 'archived';

/** §4 前向操作编码（外部边界闭集；restore_* 仅引擎内部，不出现在此）。 */
export type OpCode =
  | 'create_device'
  | 'set_device_status'
  | 'delete_device'
  | 'create_interface'
  | 'set_interface_status'
  | 'config_interface_ipv4'
  | 'delete_interface'
  | 'create_link'
  | 'set_link_status'
  | 'delete_link';

/** §4 类型化操作（以 op 判别的联合类型；字段名与契约逐字对齐）。 */
export type SimOp =
  | { op: 'create_device'; code: string; name: string }
  | { op: 'set_device_status'; deviceId: string; status: SimEntityStatus }
  | { op: 'delete_device'; deviceId: string }
  | {
      op: 'create_interface';
      deviceId: string;
      name: string;
      status?: SimEntityStatus;
      ipv4Addr?: string | null;
      ipv4Prefix?: number | null;
    }
  | { op: 'set_interface_status'; interfaceId: string; status: SimEntityStatus }
  | {
      op: 'config_interface_ipv4';
      interfaceId: string;
      ipv4Addr: string | null;
      ipv4Prefix: number | null;
    }
  | { op: 'delete_interface'; interfaceId: string }
  | {
      op: 'create_link';
      aInterfaceId: string;
      zInterfaceId: string;
      status?: SimEntityStatus;
      label?: string;
    }
  | { op: 'set_link_status'; linkId: string; status: SimEntityStatus }
  | { op: 'delete_link'; linkId: string };

/** 变更集触发方式。 */
export type ChangeSetTrigger = 'user' | 'undo' | 'checkpoint_restore';

/** 变更集状态。 */
export type ChangeSetStatus = 'applied' | 'reverted' | 'failed';

/** 校验运行触发方式。 */
export type CheckTrigger = 'manual' | 'api' | 'mcp' | 'auto_after_ops';

/** 检查项编码（CHK-001…CHK-005）。 */
export type SimCheckCode = 'CHK-001' | 'CHK-002' | 'CHK-003' | 'CHK-004' | 'CHK-005';

/**
 * 严重级别计数。后端由 map 编码时可能省略为 0 的键，
 * 读取时用 filledSeverityCounts 兜底（与 Phase 1 同一问题）。
 */
export type PartialSeverityCounts = Partial<IssueCounts>;

/** 工作台 POST 创建请求体。 */
export interface WorkspaceCreateRequest {
  name: string;
  description?: string;
  sourceBatchIds?: string[];
}

/** GET /api/workspaces 列表行。 */
export interface WorkspaceSummary {
  id: string;
  name: string;
  description: string;
  deviceCount: number;
  interfaceCount: number;
  linkCount: number;
  upDeviceCount: number;
  changesetCount: number;
  checkpointCount: number;
  lastRunId?: string | null;
  lastRunScore?: number | null;
  createdAt: string;
}

/** POST /api/workspaces → 201 物化结果。 */
export interface MaterializeResult {
  workspaceId: string;
  name: string;
  sourceBatchIds: string[];
  counts: {
    devices: number;
    interfaces: number;
    links: number;
  };
  workspaceHash: string;
}

/** GET /api/workspaces/{id} 详情计数。 */
export interface WorkspaceCounts {
  devices: number;
  upDevices: number;
  downDevices: number;
  interfaces: number;
  upInterfaces: number;
  links: number;
  upLinks: number;
  changesets: number;
  checkpoints: number;
}

/** GET /api/workspaces/{id} → 工作台详情。 */
export interface WorkspaceDetail {
  id: string;
  name: string;
  description: string;
  status: WorkspaceStatus;
  /** 无来源批次时后端发 null（nil 切片直出）。 */
  sourceBatchIds: string[] | null;
  counts: WorkspaceCounts;
  workspaceHash: string;
  latestChangeset: {
    id: string;
    description: string;
    trigger: ChangeSetTrigger;
    status: ChangeSetStatus;
    opsApplied: number;
    createdAt: string;
  } | null;
  latestRun: {
    runId: string;
    score: number;
    counts: PartialSeverityCounts;
  } | null;
  createdAt: string;
  updatedAt: string;
}

/** DELETE /api/workspaces/{id} → 级联删除计数。 */
export interface DeleteWorkspaceResult {
  deleted: boolean;
  removed: {
    devices: number;
    interfaces: number;
    links: number;
    changesets: number;
    checkRuns: number;
    checkpoints: number;
  };
}

/** 拓扑节点（设备，含接口列表；按 code 排序）。 */
export interface TopologyNode {
  id: string;
  code: string;
  name: string;
  status: SimEntityStatus;
  /**
   * 地理坐标（GEO v6）：可选展示元数据，无位置节点 omitempty 不带键。
   * 前端读取时需判 undefined；绝不允许进入状态语义。
   */
  geoLat?: number;
  geoLng?: number;
  interfaces: Array<{
    id: string;
    name: string;
    status: SimEntityStatus;
    /** 未配置 = 空串（线上始终是 string，非 null）。 */
    ipv4Addr: string;
    /** 未配置 = -1（线上始终是 number，非 null）。 */
    ipv4Prefix: number;
  }>;
}

/** 拓扑边（链路，按 id 排序）。 */
export interface TopologyEdge {
  id: string;
  aInterfaceId: string;
  aDeviceId: string;
  aDeviceCode: string;
  zInterfaceId: string;
  zDeviceId: string;
  zDeviceCode: string;
  status: SimEntityStatus;
  label: string;
}

/** GET /api/workspaces/{id}/topology。 */
export interface SimTopology {
  workspaceId: string;
  workspaceHash: string;
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

/** POST /api/workspaces/{id}/topology/path 连通性查询结果。 */
export interface SimPathResult {
  reachable: boolean;
  hops: Array<{ deviceId: string; code: string }>;
  pathLength: number;
}

/** POST /api/workspaces/{id}/ops 请求体。 */
export interface OpRequest {
  ops: SimOp[];
  description?: string;
  runChecks?: boolean;
}

/** 校验摘要（applyOps / undo / restore 附带；runChecks 全量形态见 RunResult）。 */
export interface RunSummary {
  runId: string;
  counts: PartialSeverityCounts;
  score: number;
}

/** POST /api/workspaces/{id}/ops → 应用结果。 */
export interface ApplyResult {
  changesetId: string;
  trigger: 'user';
  opsApplied: number;
  workspaceHash: string;
  checks?: RunSummary | null;
}

/** POST /api/workspaces/{id}/checks/run → 运行结果（无逐条结果）。 */
export interface RunResult {
  runId: string;
  trigger: CheckTrigger;
  counts: PartialSeverityCounts;
  score: number;
  workspaceHash: string;
  durationMs: number;
}

/** GET /api/workspaces/{id}/checks 列表行。 */
export interface CheckRunItem {
  runId: string;
  changesetId?: string | null;
  trigger: CheckTrigger;
  startedAt: string;
  counts: PartialSeverityCounts;
  score: number;
}

/** GET /api/workspaces/{id}/checks/{runId} 逐条检查结果。 */
export interface CheckResultRow {
  seq: number;
  checkCode: SimCheckCode;
  severity: Severity;
  passed: boolean;
  entityKind?: string | null;
  entityId?: string | null;
  message: string;
  /** 详情为空时后端发 null（json.RawMessage 直出）。 */
  detail: Record<string, unknown> | null;
}

/** GET /api/workspaces/{id}/checks/{runId} 详情。 */
export interface CheckRunDetail {
  runId: string;
  workspaceId: string;
  changesetId?: string | null;
  trigger: CheckTrigger;
  ruleVersion: string;
  startedAt: string;
  durationMs: number;
  score: number;
  counts: PartialSeverityCounts;
  results: CheckResultRow[];
}

/** diff 中的差异问题条目。 */
export interface DiffFinding {
  checkCode: SimCheckCode;
  severity: Severity;
  message: string;
  /** 详情为空时后端发 null（json.RawMessage 直出）。 */
  detail: Record<string, unknown> | null;
}

/** diff 端点摘要（from/to）。 */
export interface DiffRunSummary {
  runId: string;
  startedAt: string;
  score: number;
  counts: PartialSeverityCounts;
}

/** 连通性丢失/恢复的一个设备对（设备编码）。 */
export interface DiffPair {
  fromDevice: string;
  toDevice: string;
}

/** GET …/checks/{runId}/diff?compareTo= → 前后对比。 */
export interface DiffResult {
  from: DiffRunSummary;
  to: DiffRunSummary;
  findingsAdded: DiffFinding[];
  findingsRemoved: DiffFinding[];
  connectivityLost: DiffPair[];
  connectivityGained: DiffPair[];
  scoreDelta: number;
}

/** GET /api/workspaces/{id}/changesets 列表行。 */
export interface ChangeSetItem {
  id: string;
  description: string;
  trigger: ChangeSetTrigger;
  status: ChangeSetStatus;
  opsApplied: number;
  createdAt: string;
  revertedAt?: string | null;
}

/** POST /api/workspaces/{id}/undo → 撤销结果。 */
export interface UndoResult {
  changesetId: string;
  trigger: 'undo';
  reverted: string;
  opsApplied: number;
  workspaceHash: string;
  checks?: RunSummary | null;
}

/** POST /api/workspaces/{id}/checkpoints → 201。 */
export interface CheckpointResult {
  checkpointId: string;
  label: string;
  workspaceHash: string;
  createdAt: string;
}

/** GET /api/workspaces/{id}/checkpoints 列表行。 */
export interface CheckpointItem {
  checkpointId: string;
  label: string;
  workspaceHash: string;
  createdAt: string;
}

/** 检查点列表信封（契约：只有 items + total，无分页字段）。 */
export interface CheckpointList {
  items: CheckpointItem[];
  total: number;
}

/**
 * POST /api/workspaces/{id}/checkpoints/{cpId}/restore。
 * no-op 恢复：changesetId 为空（null 或空串）且附 note。
 */
export interface RestoreResult {
  checkpointId: string;
  label: string;
  changesetId?: string | null;
  note?: string | null;
  opsApplied: number;
  restoredToHash: string;
  workspaceHash: string;
  checks?: RunSummary | null;
}
