/**
 * NetOps Lab 前端文案（i18n）。
 *
 * 约定：所有面向用户的中文文案一律以命名常量形式集中在本文件，
 * 页面与组件只引用常量，禁止内联中文字符串（标点、数字除外）。
 * 含变量的文案使用 {占位符} 模板 + format() 填充。
 */

import type {
  BatchStatus,
  RelationType,
  ResourceType,
  RuleCode,
  RowStatus,
  Severity,
} from './api/types';

// ---------------------------------------------------------------------------
// 应用与导航
// ---------------------------------------------------------------------------

export const APP_NAME = 'NetOps Lab';
export const APP_SUBTITLE = '云网资源治理';
export const APP_VERSION = 'v0.1.0';

export const NAV_OVERVIEW = '概览';
export const NAV_IMPORT = '数据导入';
export const NAV_RESOURCES = '资源浏览';
export const NAV_ISSUES = '问题清单';
export const NAV_REPORT = '质量报告';

export const BREADCRUMB_HOME = '首页';
export const BREADCRUMB_LABEL = '面包屑';
export const NAV_MAIN_LABEL = '主导航';

export const NOT_FOUND_TITLE = '页面不存在';
export const NOT_FOUND_DESC = '您访问的页面不存在，请从左侧导航重新进入。';
export const BTN_BACK_HOME = '返回概览';

// ---------------------------------------------------------------------------
// 通用操作与状态
// ---------------------------------------------------------------------------

export const BTN_IMPORT_DATA = '导入数据';
export const BTN_REFRESH = '刷新';
export const BTN_RETRY = '重试';
export const BTN_CANCEL = '取消';
export const BTN_CONFIRM = '确认';
export const BTN_CLOSE = '关闭';
export const BTN_NEXT_STEP = '下一步';
export const BTN_PREV_STEP = '上一步';
export const BTN_ROLLBACK = '回滚';
export const BTN_VIEW_ISSUES = '查看问题清单';
export const BTN_VIEW_REPORT = '查看质量报告';
export const BTN_IMPORT_ANOTHER = '再导入一份';
export const BTN_DOWNLOAD_HTML = '下载 HTML';
export const BTN_DOWNLOAD_JSON = '下载 JSON';
export const BTN_REVALIDATE = '重新校验';
export const BTN_EXPAND_2HOP = '展开二跳';
export const BTN_COLLAPSE_2HOP = '收起二跳';
export const BTN_PROCESSING = '处理中…';

export const LOADING_TEXT = '加载中…';
export const EMPTY_TEXT = '暂无数据';
export const LOAD_FAILED = '数据加载失败';
export const NO_VALUE = '—';
export const FILTER_ALL = '全部';

export const PAG_TOTAL_PREFIX = '共';
export const PAG_TOTAL_SUFFIX = '条';
export const PAG_PAGE_OF = '第 {page} / {pageCount} 页';
export const PAG_PREV = '上一页';
export const PAG_NEXT = '下一页';

export const ERR_NETWORK = '网络请求失败，请检查服务是否可用';
export const ERR_HTTP_FALLBACK = '请求失败（HTTP {status}）';
export const ERR_BAD_RESPONSE = '服务返回数据格式异常';
export const ROOT_MOUNT_ERROR = '未找到应用挂载节点 #root';

// ---------------------------------------------------------------------------
// 业务标签映射（枚举值 → 中文）
// ---------------------------------------------------------------------------

export const RESOURCE_TYPE_LABELS: Record<ResourceType, string> = {
  device: '网元设备',
  ip_pool: 'IP地址池',
  circuit: '传输电路',
};

export const RESOURCE_STATUS_LABELS: Record<string, string> = {
  在网: '在网',
  停用: '停用',
  在建: '在建',
  退网: '退网',
};

export const STATUS_UNKNOWN = '未知';

export const SEVERITY_LABELS: Record<Severity, string> = {
  error: '错误',
  warning: '警告',
  info: '信息',
};

export const RULE_LABELS: Record<RuleCode, string> = {
  E001: '重复键',
  E002: '必填缺失',
  E003: '无效引用',
  E004: '格式错误',
  E005: '网段重叠',
  E006: '地址重复',
  W001: '孤儿记录',
  W002: '未知状态值',
  W003: '电路自环',
  I001: '未关联设备',
};

export const RELATION_LABELS: Record<RelationType, string> = {
  assigned_to: '归属',
  a_end: 'A端',
  z_end: 'Z端',
};

export const BATCH_STATUS_LABELS: Record<BatchStatus, string> = {
  analyzing: '解析中',
  awaiting_mapping: '待映射',
  importing: '导入中',
  completed: '已完成',
  failed: '失败',
  rolled_back: '已回滚',
};

export const ROW_STATUS_LABELS: Record<RowStatus, string> = {
  imported: '已导入',
  skipped: '已跳过',
  failed: '失败',
};

/** §6 资源字段 → 中文标签（导入映射与属性展示共用）。 */
export const FIELD_LABELS: Record<string, string> = {
  device_code: '设备编码',
  device_name: '设备名称',
  city: '城市',
  vendor: '厂商',
  model: '型号',
  mgmt_ip: '管理IP',
  status: '状态',
  pool_name: '地址池名称',
  cidr: '网段(CIDR)',
  gateway: '网关',
  vlan_id: 'VLAN',
  assigned_device: '归属设备',
  circuit_code: '电路编号',
  a_end_device: 'A端设备',
  z_end_device: 'Z端设备',
  bandwidth_mbps: '带宽(Mbps)',
};

export const FIELD_KIND_LABELS: Record<string, string> = {
  string: '文本',
  ipv4: 'IPv4地址',
  cidr: 'CIDR网段',
  int: '整数',
  enum: '枚举',
  ref: '引用',
};

export const RESOURCE_STATUS_VALUES: string[] = ['在网', '停用', '在建', '退网'];

export const APP_GENERATED_LABEL = '系统生成';

// ---------------------------------------------------------------------------
// 概览页
// ---------------------------------------------------------------------------

export const OV_STAT_TOTAL_RESOURCES = '资源总数';
export const OV_STAT_TOTAL_ISSUES = '问题总数';
export const OV_STAT_QUALITY_SCORE = '质量评分';
export const OV_STAT_LATEST_BATCH = '最近批次';
export const OV_STAT_SCORE_FULL = '满分 100 分';
export const OV_STAT_UNIT = '条';
export const OV_BY_TYPE_TITLE = '资源类型分布';
export const OV_RECENT_BATCHES = '最近批次';
export const OV_RECENT_BATCHES_MORE = '仅显示最近 5 个批次';
export const OV_QUICK_ACTIONS = '快捷操作';
export const OV_EMPTY_TITLE = '暂无数据';
export const OV_EMPTY_DESC = '尚未导入任何工作簿。导入一份 Excel 数据，即可体验资源治理、质量校验与报告全流程。';
export const OV_NO_BATCHES = '暂无导入批次';
export const OV_COL_WORKBOOK = '工作簿';
export const OV_COL_STATUS = '状态';
export const OV_COL_ROWS = '总行数';
export const OV_COL_IMPORTED = '已导入';
export const OV_COL_SKIPPED = '已跳过';
export const OV_COL_FAILED = '失败';
export const OV_COL_STARTED_AT = '开始时间';
export const OV_COL_ACTIONS = '操作';
export const OV_ISSUE_ERR = '错误';
export const OV_ISSUE_WARN = '警告';
export const OV_ISSUE_INFO = '信息';
export const OV_ROLLBACK_TITLE = '确认回滚批次';
export const OV_ROLLBACK_MESSAGE =
  '回滚将删除批次「{workbook}」导入的全部资源、关系、来源与问题记录，操作不可恢复。是否继续？';
export const TOAST_ROLLBACK_DONE = '批次已回滚';
export const TOAST_BATCH_IN_USE = '该批次创建的资源仍被其他批次引用，无法回滚';
export const TOAST_VALIDATE_DONE = '校验完成，当前质量评分：{score} 分';
export const TOAST_DISMISS = '关闭提示';

// ---------------------------------------------------------------------------
// 数据导入页（三步向导）
// ---------------------------------------------------------------------------

export const IMP_STEP1 = '上传工作簿';
export const IMP_STEP2 = '映射确认';
export const IMP_STEP3 = '执行结果';
export const STEPPER_LABEL = '导入流程步骤';

export const IMP_DROPZONE_TITLE = '拖拽工作簿到此处，或点击选择文件';
export const IMP_DROPZONE_DESC = '支持 .xlsx 格式，单个文件不超过 10MB';
export const IMP_DROPZONE_RETRY = '重试上传';
export const IMP_SELECTED_FILE = '已选择文件：{name}（{size}）';
export const IMP_ROW_SOURCE = '{sheet} · 行 {row}';
export const IMP_UPLOADING = '正在上传并解析…';
export const IMP_ERR_NOT_XLSX = '请选择 .xlsx 格式的工作簿文件';
export const IMP_ERR_TOO_LARGE = '文件大小超过 10MB 上限';
export const IMP_FILE_SIZE = '文件大小';
export const IMP_UPLOADED_INFO = '已上传：{workbook} · {sheets} 个工作表 · 批次 {batchId}';
export const IMP_ERR_RETRY_HINT = '上传失败，可更换文件后重试';

export const IMP_TOGGLE_IMPORT = '启用导入';
export const IMP_TOGGLE_SKIP = '跳过该工作表';
export const IMP_RESOURCE_TYPE = '资源类型';
export const IMP_COLUMN_MAPPING = '列映射';
export const IMP_COL_SOURCE = '源表头';
export const IMP_COL_SUGGEST = '建议';
export const IMP_COL_TARGET = '目标字段';
export const IMP_COL_SAMPLES = '样本';
export const IMP_OPT_UNMAPPED = '不导入（丢弃该列）';
export const IMP_REQUIRED_TAG = '必填';
export const IMP_NATURAL_KEY_TAG = '自然键';
export const IMP_CONFIDENCE_HIGH = '高';
export const IMP_CONFIDENCE_MEDIUM = '中';
export const IMP_NATURAL_KEY_HINT = '自然键：{field}，自动作为唯一键，不可更改';
export const IMP_VALIDATION_TITLE = '请先完成以下必填映射：';
export const IMP_ERR_REQUIRED_UNMAPPED = '工作表「{sheet}」：必填字段「{field}」尚未映射';
export const IMP_ROWS_INFO = '数据行 {rows} 行，跳过空行 {empty} 行';
export const IMP_MAPPED_COUNT = '已映射 {mapped} / {total} 列';

export const IMP_RESULT_TITLE = '导入执行完成';
export const IMP_RESULT_SUB = '批次 {batchId} · 状态 {status}';
export const IMP_ROWS_IMPORTED = '已导入';
export const IMP_ROWS_SKIPPED = '已跳过';
export const IMP_ROWS_FAILED = '失败';
export const IMP_ROWS_TOTAL = '总行数';
export const IMP_ISSUE_STATS = '问题统计';
export const IMP_COL_SOURCE2 = '来源';
export const IMP_COL_ROW_STATUS = '状态';
export const IMP_COL_RULE = '规则';
export const IMP_COL_MESSAGE = '消息';
export const IMP_TAB_ALL = '全部';
export const IMP_NO_ROWS = '没有符合筛选条件的行';

// ---------------------------------------------------------------------------
// 资源浏览页
// ---------------------------------------------------------------------------

export const RES_FILTER_TYPE = '类型';
export const RES_FILTER_STATUS = '状态';
export const RES_FILTER_SEARCH = '搜索';
export const RES_SEARCH_PLACEHOLDER = '搜索编码 / 名称 / 属性值';
export const RES_COL_KEY = '编码';
export const RES_COL_NAME = '名称';
export const RES_COL_TYPE = '类型';
export const RES_COL_STATUS = '状态';
export const RES_COL_UPDATED = '更新时间';
export const RES_DRAWER_TAB_ATTRS = '属性信息';
export const RES_DRAWER_TAB_SOURCE = '来源信息';
export const RES_DRAWER_TAB_RELATIONS = '关联信息';
export const RES_ATTR_FIELD = '字段';
export const RES_ATTR_VALUE = '值';
export const RES_NO_SOURCE = '暂无来源记录';
export const RES_SOURCE_ITEM = '{workbook} · {sheet} · 行 {row}';
export const RES_RELATION_COUNT = '关联数';
export const RES_NO_RELATIONS = '暂无关联';
export const RES_HOP2_TITLE = '二跳关联';
export const RES_HOP2_EMPTY = '该节点没有二跳关联';
export const RES_ATTR_FIRST_BATCH = '首次导入批次';
export const RES_ATTR_UPDATED_AT = '更新时间';
export const RES_ATTR_NATURAL_KEY = '自然键';
export const RES_EMPTY_TITLE = '暂无资源';
export const RES_EMPTY_DESC = '还没有任何资源数据，导入工作簿后即可在此浏览与检索。';
export const RES_TABLE_LABEL = '资源列表';

// ---------------------------------------------------------------------------
// 问题清单页
// ---------------------------------------------------------------------------

export const ISS_FILTER_SEVERITY = '严重级别';
export const ISS_FILTER_RULE = '规则';
export const ISS_FILTER_BATCH = '批次';
export const ISS_BATCH_OPTION = '{workbook}（{status}）';
export const ISS_COL_SEVERITY = '级别';
export const ISS_COL_RULE = '规则';
export const ISS_COL_MESSAGE = '消息';
export const ISS_COL_ENTITY = '实体';
export const ISS_COL_SOURCE = '来源';
export const ISS_SOURCE_TEXT = '{workbook} › {sheet} › 行 {row}';
export const ISS_EVIDENCE_TITLE = '证据明细';
export const ISS_EVIDENCE_KEY = '键';
export const ISS_EVIDENCE_VALUE = '值';
export const ISS_EVIDENCE_EMPTY = '该问题没有附加证据';
export const ISS_EXPAND_HINT = '点击行可展开证据明细';

// ---------------------------------------------------------------------------
// 质量报告页
// ---------------------------------------------------------------------------

export const RPT_SCOPE = '报告范围';
export const RPT_SCOPE_ALL = '全部批次';
export const RPT_DISCLAIMER =
  '本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。';
export const RPT_PREVIEW_TITLE = '报告预览';
export const RPT_LOADING = '正在生成报告…';
export const RPT_EMPTY = '暂无报告内容';
export const RPT_FILENAME_HTML = 'netops-quality-report';
export const RPT_FILENAME_JSON = 'netops-quality-report';
export const RPT_FILE_EXT_HTML = '.html';
export const RPT_FILE_EXT_JSON = '.json';

// ---------------------------------------------------------------------------
// 格式化工具
// ---------------------------------------------------------------------------

/**
 * 以 {name} 占位符模板填充文案。
 * 未提供的占位符原样保留，避免静默丢失信息。
 */
export function format(template: string, vars: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match: string, name: string): string => {
    const value: string | number | undefined = vars[name];
    return value === undefined ? match : String(value);
  });
}

/** RFC3339 → 本地时间「YYYY-MM-DD HH:mm」，空值显示占位符。 */
export function formatDateTime(iso?: string | null): string {
  if (!iso) return NO_VALUE;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const pad = (n: number): string => String(n).padStart(2, '0');
  const y = date.getFullYear();
  const m = pad(date.getMonth() + 1);
  const d = pad(date.getDate());
  const hh = pad(date.getHours());
  const mm = pad(date.getMinutes());
  return `${y}-${m}-${d} ${hh}:${mm}`;
}

/** 文件大小（字节）人性化显示。 */
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
