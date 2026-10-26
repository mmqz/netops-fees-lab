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

export type ResourceType =
  | 'device'
  | 'ip_pool'
  | 'circuit'
  | 'port'
  | 'base_station'
  | 'site'
  | 'machine_room'
  | 'ptp_port'
  // W21 扩容（data-model v1.0 §3，migration v15）——L0 空间层
  | 'rack'
  | 'duct'
  | 'manhole'
  | 'pole'
  // L1 物理层（设备）
  | 'board'
  // L2 物理层（光缆）
  | 'optical_cable'
  | 'cable_segment'
  | 'fiber'
  | 'odf'
  | 'odf_terminal'
  | 'splice_box'
  // L3 传输层
  | 'otn_och'
  | 'sdh_timeslot'
  | 'protection_group'
  // L5 逻辑层
  | 'ring'
  | 'igp_domain'
  | 'vpn_instance'
  // L6 业务层
  | 'customer'
  | 'service'
  | 'leased_line'
  // L4 接入层
  | 'bbu'
  | 'rru'
  | 'olt'
  | 'onu'
  | 'pon_port';

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
  | 'W005'
  | 'W006'
  | 'I001';

export type RelationType =
  | 'assigned_to'
  | 'a_end'
  | 'z_end'
  | 'belongs_to'
  // W21 扩容（data-model v1.0 §4.2，migration v16）——空间归属
  | 'located_in'
  | 'housed_in'
  | 'installed_in'
  | 'mounted_on'
  // 物理连接（fiber_of 为 §4.2 carries_fiber 的 W21 方向勘误：成员→容器）
  | 'cable_a_end'
  | 'cable_z_end'
  | 'fiber_of'
  | 'fiber_terminates'
  | 'segment_of'
  | 'connects_device'
  // 传输承载
  | 'carried_by'
  | 'routed_over'
  | 'uses_fiber'
  // 逻辑归属
  | 'member_of_ring'
  | 'protects'
  | 'in_domain'
  | 'in_vpn'
  // 业务服务
  | 'serves'
  | 'subscribed_by'
  | 'provided_by'
  | 'depends_on'
  // 接入关联
  | 'uplink_to'
  | 'home_bbu'
  | 'covers'
  | 'connects_onu';

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
  dbPath?: string;
  /** v0.11-R11：定时巡检状态块（未启用时 enabled=false，键恒在）。 */
  scheduler?: SchedulerStatus;
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

/** W24 无人值守导入：单个工作表的表头指纹命中已存映射模板时的信息块
 *  （后端 importer.TemplateMatch 镜像；纯展示，向导仍走人工确认）。 */
export interface TemplateMatchInfo {
  templateId: string;
  resourceType: ResourceType;
  /** RFC3339 确认时间。 */
  confirmedAt: string;
}

export interface SheetAnalysis {
  sheet: string;
  rowCount: number;
  skippedEmptyRows: number;
  columns: ColumnAnalysis[];
  /** omitempty：无法识别类型的工作表省略该字段。 */
  suggestedType?: ResourceType;
  /** W24：表头指纹命中模板时非 null（信息性横幅的数据源；未命中为 null）。 */
  templateMatched?: TemplateMatchInfo | null;
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
  /** 运行状态快照（W22：up|down|degraded|maintenance|unknown；空串=无观测）。 */
  runState?: string;
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
// 11b. GET /api/governance/summary（v0.6.0 W14-D 治理总览）
// ---------------------------------------------------------------------------

/** 治理总览：待确认队列（属性变更 / 疑似改名）。 */
export interface GovernancePendingCounts {
  attributeChanges: number;
  renameProposals: number;
}

/** 治理总览：决策审计计数（两确认流的终态；后端恒发四个固定键）。 */
export interface GovernanceDecidedCounts {
  attributeChanges: Partial<Record<'applied' | 'dismissed', number>>;
  renameProposals: Partial<Record<'applied' | 'rejected', number>>;
}

/** 治理总览：问题按规则分布的一行（后端 count 降序 / 码升序，前 10）。 */
export interface GovernanceRuleCount {
  rule: RuleCode;
  count: number;
}

/** 治理总览聚合（除 generatedAt 外全字段确定性；空库恒全零）。 */
export interface GovernanceSummary {
  pending: GovernancePendingCounts;
  decided: GovernanceDecidedCounts;
  issues: {
    /** 后端恒发 error/warning/info 三个键（计 0 亦发）。 */
    bySeverity: PartialIssueCounts;
    byRule: GovernanceRuleCount[];
  };
  resources: {
    total: number;
    /** 动态键 map：键集 = 库内实际出现的类型字面量。 */
    byType: Record<string, number>;
  };
  relations: {
    total: number;
    /** 动态键 map：键集 = 库内实际出现的关系类型字面量。 */
    byType: Partial<Record<RelationType, number>>;
  };
  batches: {
    total: number;
    /** 动态键 map：键集 = 库内实际出现的批次状态字面量。 */
    byStatus: Partial<Record<BatchStatus, number>>;
  };
  generatedAt: string;
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
// 12b. 属性变更确认（W4-D，migration v11）
// ---------------------------------------------------------------------------

/** 属性变更提案状态（与 resource_attribute_changes CHECK 一致）。 */
export type AttributeChangeStatus = 'pending' | 'applied' | 'dismissed';

/** GET /api/attribute-changes 的单个变更提案（含资源侧 join 字段）。 */
export interface AttributeChangeItem {
  id: string;
  resourceId: string;
  /** 资源被回滚级联删除后为空串（join 不到资源）。 */
  resourceType: string;
  naturalKey: string;
  batchId: string;
  field: string;
  oldValue: string;
  newValue: string;
  status: AttributeChangeStatus;
  sheet: string;
  rowNumber: number;
  createdAt: string;
  appliedAt?: string | null;
  /** W13-D：apply 响应里是“确认时已同步改挂派生关系”的真值；待确认列表里是同一判定的预览（引用字段且已有派生关系 → 确认后会改挂，用于行内提示与 toast）。非引用字段恒 false。 */
  relationRetargeted: boolean;
}

/** 列表信封：{items, total}（limit/offset 分页）。 */
export interface AttributeChangeList {
  items: AttributeChangeItem[];
  total: number;
}

/** W6-D：疑似改名提案（GET /api/renames 的项）。resourceId=旧资源（身份保留方）。 */
export type RenameStatus = 'pending' | 'applied' | 'rejected';
export interface RenameProposalItem {
  id: string;
  resourceId: string;
  resourceType: string;
  naturalKey: string;
  batchId: string;
  newResourceId: string;
  oldKey: string;
  newKey: string;
  matchedFields: string[];
  status: RenameStatus;
  sheet: string;
  rowNumber: number;
  createdAt: string;
  decidedAt: string | null;
}
export interface RenameList {
  items: RenameProposalItem[];
  total: number;
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
  /** #51 图层覆盖层计数（v0.11 前创建的旧工作台为 0）。 */
  layerNodeCount: number;
  layerLinkCount: number;
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
    /** #51 图层覆盖层（只读投影）。 */
    layerNodes: number;
    layerLinks: number;
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
  /** #51 图层覆盖层（只读投影；v0.12 前的旧工作台可能缺键）。 */
  layerNodes?: number;
  layerLinks?: number;
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
  /** 设备血缘资源 id（#51：画布影响分析锚点）；人工创建设备无此键。 */
  resourceId?: string;
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
  /** 链路血缘资源 id（电路/光缆走廊；#51：画布影响分析锚点）；无此键=合成链路。 */
  resourceId?: string;
}

/** 图层节点种类（#51 层级覆盖层的闭合词汇）。 */
export type OverlayLayer = 'L0' | 'L3' | 'L4' | 'L5' | 'L6';

/** 图层边语义（闭合词汇，与后端 CHECK 同源）。 */
export type LayerLinkSem = 'logical' | 'service' | 'access' | 'group';

/** 图层节点载荷：L0 空间锚点 / L4 接入（基站聚合簇、olt/onu/bbu/rru）/
 *  L5 分组（环/IGP/VPN/保护组/地址池）/ L6 业务（客户/业务/专线）。 */
export interface LayerNodePayload {
  id: string;
  resourceId?: string;
  layer: OverlayLayer;
  kind: string;
  code: string;
  name: string;
  status?: string;
  /** 视觉锚定的虚拟设备 id（布局贴近其上联/依赖设备）。 */
  anchorDeviceId?: string;
  geoLat?: number;
  geoLng?: number;
  /** 聚合簇：成员基站数（aggregated=true 时存在）。 */
  memberCount?: number;
  /** 聚合簇：样例基站编码（前 5 个）。 */
  sample?: string[];
  aggregated?: boolean;
  /** 类型化附加信息：olt 的 PON 口清单 / 客户的基站服务数。 */
  detail?: { ponPorts?: string[]; stationServes?: number };
}

/** 图层边载荷：端点可为 device（虚拟设备）/ interface（接口）/ node（图层节点）。 */
export interface LayerEdgePayload {
  id: string;
  layer: OverlayLayer;
  sem: LayerLinkSem;
  label: string;
  resourceId?: string;
  aKind: 'device' | 'interface' | 'node';
  aRef: string;
  aCode?: string;
  zKind: 'device' | 'interface' | 'node';
  zRef: string;
  zCode?: string;
}

/** 图层每层计数（含 L4 聚簇形态）。 */
export interface LayerCount {
  nodes: number;
  edges: number;
  clusters: number;
  clusteredNodes: number;
}

/** 拓扑载荷的图层覆盖层（#51）：聚合后的节点 + 语义边 + 每层摘要。 */
export interface TopologyLayers {
  nodes: LayerNodePayload[];
  edges: LayerEdgePayload[];
  summary: { byLayer: Partial<Record<OverlayLayer, LayerCount>> };
}

/** GET /api/workspaces/{id}/topology。 */
export interface SimTopology {
  workspaceId: string;
  workspaceHash: string;
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  /** 图层覆盖层（#51，v0.12 前物化的工作台无此键）。 */
  layers?: TopologyLayers;
}

/** 图层聚簇成员（#51 基站簇下钻行）。 */
export interface LayerMember {
  id: string;
  resourceId?: string;
  kind: string;
  code: string;
  name: string;
  status?: string;
  geoLat?: number;
  geoLng?: number;
}

/** GET /api/workspaces/{id}/layer-members 分页信封。 */
export interface LayerMembersPage {
  items: LayerMember[];
  total: number;
  page: number;
  pageSize: number;
  cluster: string;
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

// ---------------------------------------------------------------------------
// 18. GET /api/alerts + POST /api/alerts/{id}/ack|clear（检测告警，v0.7）
// ---------------------------------------------------------------------------

/** 告警状态（与 alerts.state CHECK 一致：firing|acked|cleared）。 */
export type AlertState = 'firing' | 'acked' | 'cleared';

/** 阈值比较器（detection_rules.comparator）。 */
export type AlertComparator = 'gt' | 'lt' | 'gte' | 'lte';

/** GET /api/alerts 单条告警（含 rule + series → resource JOIN 字段）。 */
export interface AlertItem {
  alertId: string;
  ruleId: string;
  ruleLabel: string;
  metricKey: string;
  comparator: AlertComparator;
  threshold: number;
  window: number;
  severity: Severity;
  seriesId: string;
  state: AlertState;
  firedAt: string;
  firstSeen: string;
  clearedAt?: string | null;
  ackedAt?: string | null;
  ackedBy?: string | null;
  lastValue: number;
  /** v0.12-R16 根因/次生关联裁定：'' 未标记 | primary 根因嫌疑 | secondary 疑似次生。 */
  correlationRole: AlertCorrelationRole;
  /** secondary 专用：最近告警根因的告警 id / 资源 id。 */
  correlatedAlertId: string;
  correlatedResourceId: string;
  resource: {
    id: string;
    type: string;
    naturalKey: string;
    name: string;
  };
  /** v0.13-R19 生命周期通知裁决：本告警最近一次 ack/clear 通知的投递终局。 */
  lifecycleDelivery: AlertLifecycleDelivery;
}

/** 告警生命周期通知裁决（v0.13-R19，三值诚实口径；字段永不缺省）。 */
export interface AlertLifecycleDelivery {
  /** '' = 从未通知（未 ack/clear 过或当时通道未配置）| delivered | failed。 */
  status: '' | 'delivered' | 'failed';
  /** 通知描述的生命周期事件：''（伴随 status ''）| ack | clear。 */
  event: '' | 'ack' | 'clear';
  /** 实际 POST 次数（1..3）。 */
  attempts: number;
  /** 裁决行的 delivered_at（投递钟表审计字段）。 */
  ts: string;
}

/** 告警关联角色（v0.12-R16，闭集：primary 根因嫌疑 / secondary 疑似次生；'' = 未标记）。 */
export type AlertCorrelationRole = '' | 'primary' | 'secondary';

/** GET /api/alerts 查询参数（state/severity/resource_id/role 可选）。 */
export interface AlertsQuery {
  state?: AlertState;
  severity?: Severity;
  resourceId?: string;
  /** 只看根因（primary）或只看症状（secondary）；缺省 = 全部。 */
  role?: AlertCorrelationRole;
  page: number;
  pageSize: number;
}

/** POST /api/alerts/{id}/ack 响应（生命周期回执）。 */
export interface AlertAckResponse {
  alertId: string;
  state: AlertState;
  ackedAt: string;
  ackedBy: string;
}

/** POST /api/alerts/{id}/clear 响应。 */
export interface AlertClearResponse {
  alertId: string;
  state: AlertState;
  clearedAt: string;
}

// ---------------------------------------------------------------------------
// 18-B. 告警统计与检测运行历史（v0.11-R11）
// ---------------------------------------------------------------------------

/** 检测运行来源（闭集：api / cli / scheduler；MCP 只读红线永不在此）。 */
export type DetectionRunSource = 'api' | 'cli' | 'scheduler';

/** GET /api/detection/runs 行（每次扫描的落库回执）。 */
export interface DetectionRunItem {
  runId: string;
  source: DetectionRunSource;
  startedAt: string;
  finishedAt: string;
  durationMs: number;
  from: string;
  to: string;
  evaluatedAt: string;
  findings: number;
  alertsCreated: number;
  alertsCleared: number;
  runStatesWritten: number;
  /** 维护窗口抑制的新告警数（v0.12-R15；v24 前的回执回填为 0）。 */
  suppressed: number;
  /** 抖动抑制的重燃告警数（v0.12-R17；v26 前的回执回填为 0）。 */
  flapHeld: number;
  error: string;
  /**
   * v0.12-R18 通知投递裁决（LEFT JOIN webhook_deliveries）：
   * delivered = 已送达（含重试吸收的瞬时失败）；failed = 全部尝试失败；
   * '' = 该次运行未配置 webhook 通道（诚实三值，非伪造的 failed）。
   */
  webhookStatus: '' | 'delivered' | 'failed';
  /** 实际发出的 POST 次数（1..3，含重试）。 */
  webhookAttempts: number;
  /** failed 时的最后失败原因（delivered/通道关为空串）。 */
  webhookError: string;
}

/** GET /api/detection/runs 查询参数。 */
export interface DetectionRunsQuery {
  page: number;
  pageSize: number;
}

/** GET /api/alerts/stats 响应（闭集零填充：三态计数 + 待处置级别矩阵）。 */
export interface AlertStats {
  total: number;
  byState: Record<AlertState, number>;
  activeBySeverity: Record<Severity, number>;
}

/** GET /api/health 的 scheduler 块（nil 安全：未启用时全零/false）。 */
export interface SchedulerStatus {
  enabled: boolean;
  intervalSeconds: number;
  running: boolean;
  rounds: number;
  lastRunAt: string;
  nextRunAt: string;
}

// ---------------------------------------------------------------------------
// 19. 指标时序（GET /api/metrics/keys + GET /api/resources/{id}/metrics）
// ---------------------------------------------------------------------------

/** GET /api/metrics/keys 行（封闭指标目录，来自 metric_keys 表）。 */
export interface MetricKeyItem {
  metricKey: string;
  label: string;
  unit: string;
  valueKind: 'gauge' | 'counter' | 'ratio';
  minValue?: number | null;
  maxValue?: number | null;
  direction: string;
  createdAt: string;
}

/** 指标数据点（quality：ok|missing|suspect）。 */
export interface MetricPoint {
  ts: string;
  value: number;
  quality: 'ok' | 'missing' | 'suspect';
}

/** GET /api/resources/{id}/metrics 的单条序列。 */
export interface MetricSeriesResult {
  seriesId: string;
  metricKey: string;
  unit: string;
  points: MetricPoint[];
}

/** GET /api/resources/{id}/metrics 响应。 */
export interface ResourceMetricsResponse {
  resourceId: string;
  series: MetricSeriesResult[];
}

// ---------------------------------------------------------------------------
// 20. 图查询（GET /api/resources/{id}/traverse | impact | context）
// ---------------------------------------------------------------------------

/** traverse 文档的节点（depth = BFS 层深）。 */
export interface TraverseNode {
  id: string;
  type: string;
  name: string;
  layer: number;
  depth: number;
  status: string;
  runState: string;
}

export interface TraverseEdge {
  from: string;
  to: string;
  relation: string;
  crossLayer: boolean;
}

/** GET /api/resources/{id}/traverse 响应。 */
export interface TraverseDocument {
  root: { id: string; type: string; name: string; layer: number };
  nodes: TraverseNode[];
  edges: TraverseEdge[];
  paths?: string[][];
  truncated: boolean;
  stats: { visitedEdges: number; elapsedMs: number };
}

/** impact 文档的节点投影。 */
export interface ImpactNode {
  id: string;
  type: string;
  name: string;
  layer: number;
  runState: string;
}

/** GET /api/resources/{id}/impact 响应。 */
export interface ImpactDocument {
  root: ImpactNode;
  impacted: Array<{ node: ImpactNode; distance: number; viaPath: string[] }>;
  candidates: Array<{ node: ImpactNode; distance: number; reason: string }>;
  truncated: boolean;
}

/** traverse 方向（down = 向下寻根因，up = 向上看业务，both）。 */
export type TraverseDirection = 'down' | 'up' | 'both';

// ---------------------------------------------------------------------------
// 21. GET /api/resources/{id}/run-state/history（W22 状态时序）
// ---------------------------------------------------------------------------

export interface RunStateHistoryItem {
  ts: string;
  runState: string;
  source: string;
}

export interface RunStateHistoryResponse {
  items: RunStateHistoryItem[];
  total: number;
}

// ---------------------------------------------------------------------------
// 22. 检测规则 CRUD + 检测运行（v0.10-R6，GET/POST/PATCH/DELETE
// /api/detection/rules + POST /api/detection/run）
// ---------------------------------------------------------------------------

/** GET /api/detection/rules 单条规则（含启停与阈值参数）。 */
export interface DetectionRuleItem {
  ruleId: string;
  metricKey: string;
  comparator: AlertComparator;
  threshold: number;
  window: number;
  severity: Severity;
  enabled: boolean;
  label: string;
  createdAt: string;
  updatedAt: string;
}

/** GET /api/detection/rules 响应（rule_id 顺序，确定性）。 */
export interface DetectionRulesListResponse {
  items: DetectionRuleItem[];
  total: number;
}

/** POST /api/detection/rules 请求体（ruleId 可选 = 后端自动生成 dr_<uuid>）。 */
export interface DetectionRuleCreateRequest {
  ruleId?: string;
  metricKey: string;
  comparator: AlertComparator;
  threshold: number;
  window?: number;
  severity: Severity;
  label?: string;
  enabled?: boolean;
}

/**
 * PATCH /api/detection/rules/{id} 请求体：仅可变字段（部分更新语义）。
 * metricKey/ruleId 是身份字段不可改（后端 400）。
 */
export interface DetectionRulePatchRequest {
  comparator?: AlertComparator;
  threshold?: number;
  window?: number;
  severity?: Severity;
  enabled?: boolean;
  label?: string;
}

/** DELETE /api/detection/rules/{id} 响应（409 rule_has_alerts = 审计保护）。 */
export interface DetectionRuleDeleteResponse {
  ruleId: string;
  deleted: boolean;
}

/** POST /api/detection/run 单条告警变更行（created | cleared | suppressed | flap_held）。 */
export interface AlertMutationSummary {
  alertId: string;
  ruleId: string;
  seriesId: string;
  resourceId: string;
  metricKey: string;
  severity: Severity;
  /**
   * v0.12-R15：suppressed = 被维护窗口抑制的潜在新告警（无告警行，state 为空串）。
   * v0.12-R17：flap_held = 被抖动抑制门拦下的重燃告警（无告警行，state 为空串，
   * alertId 为确定性的「应燃 id」——序列稳定后落同一身份，可关联）。
   */
  action: 'created' | 'cleared' | 'suppressed' | 'flap_held';
  /** suppressed/flap_held 行无告警行 → 空串；created/cleared 行为变更后状态。 */
  state: AlertState | '';
  /** v0.12-R16：created 行带本次运行算出的根因/次生裁定；cleared/suppressed 行为空串。 */
  correlationRole?: AlertCorrelationRole;
  firstSeen: string;
  firedAt: string;
  clearedAt: string;
  lastValue: number;
}

/** POST /api/detection/run 响应（一次全库扫描的运行回执）。 */
export interface DetectionRunResponse {
  findings: number;
  alertsCreated: number;
  alertsCleared: number;
  runStatesWritten: number;
  /** v0.12-R15：被维护窗口抑制的新告警数（findings 的子集）。 */
  suppressed: number;
  /** v0.12-R17：被抖动抑制门拦下的重燃告警数（findings 的子集）。 */
  flapHeld: number;
  evaluatedAt: string;
  alerts: AlertMutationSummary[];
}

// ---------------------------------------------------------------------------
// 24. 维护窗口（/api/maintenance/windows，v0.12-R15）
// ---------------------------------------------------------------------------

/** GET /api/maintenance/windows 行（计划内抑制新告警的时间区间）。 */
export interface MaintenanceWindow {
  windowId: string;
  label: string;
  /** RFC3339 UTC，半开区间 [startsAt, endsAt)。 */
  startsAt: string;
  endsAt: string;
  /** 空 = 全局 scope（覆盖全部资源）。 */
  resourceIds: string[];
  note: string;
  createdAt: string;
}

/** POST /api/maintenance/windows 请求体。 */
export interface MaintenanceWindowRequest {
  label: string;
  startsAt: string;
  endsAt: string;
  resourceIds?: string[];
  note?: string;
}

/** POST /api/maintenance/windows 响应。 */
export interface MaintenanceWindowCreated {
  windowId: string;
  label: string;
  startsAt: string;
  endsAt: string;
  resourceIds: string[];
  note: string;
  createdAt: string;
}

/** DELETE /api/maintenance/windows/{id} 响应。 */
export interface MaintenanceWindowDeleted {
  windowId: string;
  deleted: boolean;
}

// ---------------------------------------------------------------------------
// 23. OpenAPI 文档（GET /api/openapi.json，v0.10-R9）
// ---------------------------------------------------------------------------

/** OpenAPI 文档的单个操作（方法级）。 */
export interface OpenApiOperation {
  tags: string[];
  summary: string;
}

/** OpenAPI 文档的单个路径（本 API 每路径恰一方法）。 */
export interface OpenApiPathItem {
  get?: OpenApiOperation;
  post?: OpenApiOperation;
  patch?: OpenApiOperation;
  delete?: OpenApiOperation;
}

/** GET /api/openapi.json 响应（路由表的忠实投影）。 */
export interface OpenApiSpec {
  openapi: string;
  info: {
    title: string;
    version: string;
    description: string;
  };
  tags: Array<{ name: string }>;
  paths: Record<string, OpenApiPathItem>;
}

// ---------------------------------------------------------------------------
// 费用分析层（#58 阶段 5，前端工作台入口）
// ---------------------------------------------------------------------------

/** GET /api/fees/batches 的批次项 */
export interface FeeBatchItem {
  id: string;
  workbookName: string;
  feeType: string;
  feeTypeLabel: string;
  period: string;
  status: string;
  rowsTotal: number;
  rowsImported: number;
  rowsFailed: number;
  snapshotPath: string;
  createdAt: string;
  completedAt: string | null;
}

/** GET /api/fees/batches 信封 */
export interface FeeBatchesList {
  items: FeeBatchItem[];
  total: number;
}

/** POST /api/fees/imports 结果（201 新导入 / 200 幂等重复） */
export interface FeeImportSummary {
  batchId: string;
  status: string;
  workbookName: string;
  sha256: string;
  feeType: string;
  feeYear: number;
  feeMonth: number;
  rowsTotal: number;
  rowsImported: number;
  rowsFailed: number;
  files: string[];
  errors?: string[];
  skippedDuplicate: boolean;
  existingBatch?: string;
}

/** POST /api/fees/imports/sheets 的单 sheet 批次结果 */
export interface FeeSheetBatchResult {
  sheet: string;
  feeType: string;
  batchId: string;
  status: string;
  rowsTotal: number;
  rowsImported: number;
  rowsFailed: number;
  files?: string[];
  errors?: string[];
  skippedDuplicate: boolean;
  existingBatch?: string;
}

/** POST /api/fees/imports/sheets 结果（201 整本 / 200 全幂等） */
export interface FeeSheetsImportSummary {
  workbookName: string;
  sha256: string;
  year: number;
  month: number;
  status: string;
  batches: FeeSheetBatchResult[];
  skippedSheets?: string[];
}

/** 风险线索的行级证据锚点（可回读快照原文） */
export interface RiskEvidenceAnchor {
  sourceFile: string;
  sourceRow: number;
  importBatch: string;
  amountCents: number;
}

/** 一条待核实的风险线索（账单存在 ≠ 认定结论） */
export interface RiskClue {
  siteCode: string;
  /** #4 记录键（订单号/电表编号/工单号）——行级业务身份；缺省 = 该批次无记录键列 */
  recordKey?: string;
  year: number;
  month: number;
  metricValue: number;
  metricLabel: string;
  threshold: number | null;
  thresholdLabel: string;
  evidence: RiskEvidenceAnchor[];
  evidenceTotal: number;
  note?: string;
  /** #119 人工裁定：verified（属实，保持可见）/ dismissed（已排除，仅审计视图出现）；缺省 = 待核实 */
  decision?: string;
  /** 裁定时间（RFC3339） */
  decidedAt?: string;
}

/** GET /api/fees/risks/{scenario} 信封 */
export interface RiskResult {
  scenario: string;
  scenarioCn: string;
  feeType: string;
  year: number;
  month: number;
  clues: RiskClue[];
  total: number;
  excluded: number;
  /** #119 本次被人工裁定排除而隐藏的线索数（绝不静默丢弃） */
  dismissed: number;
  note: string;
  /** #21 结构化非阻断告警（排除主导等「无法分析 ≠ 无异常」形态）——UI 渲染为告警条 */
  warnings?: string[];
}

/** 站址账期合计行（GET /api/fees/query/totals；整数分契约） */
export interface FeeTotalsRow {
  siteCode: string;
  rows: number;
  totalCents: number;
}

/** GET /api/fees/query/totals 信封 */
export interface FeeTotalsResult {
  preset: string;
  feeType: string;
  year: number;
  month: number;
  items: FeeTotalsRow[];
  total: number;
}

/** 月度趋势行（GET /api/fees/query/monthly；#3 展示层数据面；整数分契约） */
export interface FeeMonthlyRow {
  month: number;
  rows: number;
  sites: number;
  totalCents: number;
}

/** GET /api/fees/query/monthly 信封（有数据的月份才出现，无数据月份缺席） */
export interface FeeMonthlyResult {
  preset: string;
  feeType: string;
  year: number;
  items: FeeMonthlyRow[];
  total: number;
}

/** 跨批次重复结算候选行（GET /api/fees/query/duplicates；候选非认定） */
export interface FeeDuplicatesRow {
  siteCode: string;
  year: number;
  month: number;
  batches: number;
  rows: number;
  totalCents: number;
}

/** GET /api/fees/query/duplicates 信封 */
export interface FeeDuplicatesResult {
  preset: string;
  feeType: string;
  year: number;
  month: number;
  items: FeeDuplicatesRow[];
  total: number;
}

/** 明细层行（#16 完成线 6：人工补录佐证层——永不参与系统表金额） */
export interface FeeDetailItem {
  id: string;
  linkKey: string;
  period: string;
  feeType: string;
  category: string;
  amountCents: number;
  occurredOn?: string;
  itemName?: string;
  quantity?: number;
  quantityUnit?: string;
  unitPriceCents?: number;
  reason?: string;
  location?: string;
  workOrderNo?: string;
  invoiceNo?: string;
  counterparty?: string;
  sourceFile?: string;
  sourceRow?: number;
  remark?: string;
  status: string;
  createdBy: string;
  createdAt: string;
  verifiedBy?: string;
  verifiedAt?: string;
  rejectReason?: string;
}

/** GET /api/fees/details 信封 */
export interface FeeDetailsList {
  items: FeeDetailItem[];
  total: number;
}

/** 明细补录请求体（三强制：linkKey/period/feeType/category/amount） */
export interface FeeDetailInput {
  linkKey: string;
  period: string;
  feeType: string;
  category: string;
  amountYuan: number;
  itemName?: string;
  reason?: string;
  sourceFile?: string;
  sourceRow?: number;
  remark?: string;
}

/** GET /api/fees/details/reconcile —— 差额提示（只提示不阻断） */
export interface FeeDetailReconcile {
  linkKey: string;
  period: string;
  feeType: string;
  rows: number;
  detailSumCents: number;
  engineCentsAvailable: boolean;
  systemCents: number | null;
  deltaCents: number | null;
  note: string;
}

/** 站址编码映射行（#12：canonical↔alias 双系统编码桥） */
export interface FeeSiteCodeAlias {
  id: string;
  canonical: string;
  alias: string;
  source: string;
  confidence: string;
  createdBy: string;
  createdAt: string;
}

/** GET /api/fees/site-code-aliases 信封 */
export interface FeeSiteCodeAliasesList {
  items: FeeSiteCodeAlias[];
  total: number;
}

/** 扫描提议（只提议不落库——确认走建立通道） */
export interface FeeAliasProposal {
  canonical: string;
  alias: string;
  source: string;
  why: string;
}

/** 跨数据集对账行（#12 验收 2/3） */
export interface FeeCrossRow {
  canonical: string;
  class: string;
  electricityCents: number;
  towerCents: number;
  deltaCents: number;
  elecRows: number;
  towerRows: number;
  redirectedFrom?: string;
}

/** GET /api/fees/cross-reconcile 信封 */
export interface FeeCrossReconcileResult {
  year: number;
  month: number;
  rows: FeeCrossRow[];
  onlyElectricity: number;
  onlyTower: number;
  both: number;
  aliasesApplied: number;
  note: string;
}

/** #119 风险线索人工裁定（risk_clue_decisions 的 REST 形状） */
export interface ClueDecisionItem {
  id: string;
  scenario: string;
  scenarioCn: string;
  feeType: string;
  siteCode: string;
  year: number;
  month: number;
  /** verified | dismissed */
  status: string;
  note?: string;
  decidedAt: string;
}

/** 费用-资源关联（fee_site_links 的 REST 形状；join 展示字段可缺省——资源消失时记录仍自洽） */
export interface FeeLinkItem {
  id: string;
  feeSiteCode: string;
  resourceId: string;
  resourceType: string;
  resourceKey: string;
  /** 资源侧 join 展示名（资源已删除则缺省，resourceKey 仍完整叙事） */
  resourceName?: string;
  resourceStatus?: string;
  /** exact | rule | fuzzy */
  grade: string;
  /** 规则级命中的规则名（rule-<n>） */
  rule?: string;
  /** 模糊级相似度 0–1 */
  similarity?: number;
  /** pending | confirmed | dismissed */
  status: string;
  /** auto | human */
  source: string;
  createdAt: string;
  decidedAt: string | null;
}

/** GET /api/fees/links 信封（#90 分页化：total 为全量计数） */
export interface FeeLinksList {
  items: FeeLinkItem[];
  total: number;
  page: number;
  pageSize: number;
}

/** POST /api/fees/links/scan 计数回执 */
export interface FeeLinkScanResult {
  codes: number;
  exactLinks: number;
  ruleLinks: number;
  fuzzyProposed: number;
  unlinked: number;
  humanKept: number;
}

/** POST /api/fees/period-probe 结果（#15：表型分类 + 三档账期识别，只读不落库——
 * 导入前确认面板的数据面；kind=未识别时 year/month 为 0） */
export interface FeePeriodProbeResult {
  kind: string;
  /** 中文表型名（与 CLI/HTTP 文档同源） */
  kindLabel: string;
  year: number;
  month: number;
  /** strong | weak | none */
  confidence: string;
  basis: string;
  /** 采样行数（≤50） */
  rowsSampled: number;
}

/** GET/POST /api/fees/rules 的规则行（#2：L1 六元组，enabled=false 即观察态） */
export interface FeeRiskRule {
  id: string;
  name: string;
  feeType: string;
  metric: string;
  aggregation: string;
  dimension: string;
  comparator: string;
  threshold: number;
  windowMonths: number;
  severity: string;
  description?: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

/** GET /api/fees/rules 信封 */
export interface FeeRulesList {
  items: FeeRiskRule[];
  total: number;
  enabledOnly: boolean;
}

/** GET /api/fees/rules/{id}/audit 的动作台账行（#2：阈值变更留痕的可见面） */
export interface FeeRuleAuditEntry {
  id: number;
  ruleId: string;
  /** created | enabled | disabled */
  action: string;
  actor: string;
  changes?: string;
  at: string;
}

/** GET /api/fees/rules/{id}/audit 信封 */
export interface FeeRuleAuditList {
  ruleId: string;
  items: FeeRuleAuditEntry[];
  total: number;
}
