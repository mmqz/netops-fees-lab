/**
 * NetOps Lab 前端文案（i18n）。
 *
 * 约定：所有面向用户的中文文案一律以命名常量形式集中在本文件，
 * 页面与组件只引用常量，禁止内联中文字符串（标点、数字除外）。
 * 含变量的文案使用 {占位符} 模板 + format() 填充。
 */

import type {
  AlertComparator,
  AlertState,
  BatchStatus,
  ChangeSetStatus,
  ChangeSetTrigger,
  CheckTrigger,
  RelationType,
  ResourceType,
  RuleCode,
  RowStatus,
  Severity,
  SimCheckCode,
  SimEntityStatus,
} from './api/types';

// ---------------------------------------------------------------------------
// 应用与导航
// ---------------------------------------------------------------------------

export const APP_NAME = 'NetOps Lab';
export const APP_SUBTITLE = '云网资源治理';
/** 本地回退版本号：侧栏优先展示 /api/health 上报的 domain.AppVersion（事实源），
 *  请求失败时才回退到本常量（R1-D1 根治前端手抄漂移）。 */
export const APP_VERSION = 'v0.14.0';

export const NAV_OVERVIEW = '概览';
export const NAV_IMPORT = '数据导入';
export const NAV_RESOURCES = '资源浏览';
export const NAV_ISSUES = '问题清单';
export const NAV_REPORT = '质量报告';

/** 仿真工作台导航分组（icon + 两个入口）。 */
export const NAV_LAB_GROUP = '仿真工作台';
export const NAV_LAB_WORKSPACES = '工作台';
export const NAV_LAB_TOPOLOGY = '拓扑工作台';

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
export const BTN_EXPORT_RESOURCES = '导出资源';

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

// 4xx 友好中文主题行（FX2-4）：后端错误码 → 中文引导，原文技术细节作为次行保留
export const ERR_API_INVALID_REQUEST = '请求参数无效，操作被拒绝';
export const ERR_API_OP_FAILED = '操作执行失败';
export const ERR_API_NOT_FOUND = '请求的对象不存在';
export const ERR_API_NOT_LATEST = '目标已不是最新状态，操作被拒绝';
export const ERR_API_CONFLICT = '状态冲突，操作被拒绝';
export const ERR_API_CHECKPOINT_MISSING_FILE = '检查点快照文件缺失，无法恢复';

// ---------------------------------------------------------------------------
// 业务标签映射（枚举值 → 中文）
// ---------------------------------------------------------------------------

export const RESOURCE_TYPE_LABELS: Record<ResourceType, string> = {
  device: '网元设备',
  ip_pool: 'IP地址池',
  circuit: '传输电路',
  port: '设备端口',
  base_station: '基站',
  site: '局站',
  machine_room: '机房',
  ptp_port: 'PTP端口',
  // W21 扩容（33 类，data-model v1.0 §3.2 中文 Label 同词形）
  rack: '机架',
  duct: '管道',
  manhole: '人井',
  pole: '杆路',
  board: '板卡',
  optical_cable: '光缆',
  cable_segment: '光缆段',
  fiber: '纤芯',
  odf: '光配线架',
  odf_terminal: 'ODF端子',
  splice_box: '接头盒',
  otn_och: 'OTN波道',
  sdh_timeslot: 'SDH时隙',
  protection_group: '保护组',
  ring: '环网',
  igp_domain: 'IGP域',
  vpn_instance: 'VPN实例',
  customer: '客户',
  service: '业务实例',
  leased_line: '专线',
  bbu: 'BBU',
  rru: 'RRU',
  olt: 'OLT',
  onu: 'ONU',
  pon_port: 'PON口',
};

/**
 * 资源类型标签查找：键来自后端动态 byType map（未知类型回退原始键名），
 * 避免前端另抄一份类型清单造成漂移（R1-D4）。
 */
export function resourceTypeLabel(key: string): string {
  const labels: Record<string, string | undefined> = RESOURCE_TYPE_LABELS;
  return labels[key] ?? key;
}

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
  W004: '身份键退化',
  W005: '属性变更待确认',
  W006: '疑似改名待确认',
  I001: '未关联设备',
};

export const RELATION_LABELS: Record<RelationType, string> = {
  assigned_to: '归属',
  a_end: 'A端',
  z_end: 'Z端',
  belongs_to: '所属',
  // W21 扩容（29 种，data-model v1.0 §4.2；方向 from=成员/依赖方 → to=容器/被依赖方）
  located_in: '所在机房',
  housed_in: '所在机房',
  installed_in: '所在设备',
  mounted_on: '所在板卡',
  cable_a_end: '光缆A端',
  cable_z_end: '光缆Z端',
  fiber_of: '属于光缆',
  fiber_terminates: '成端端子',
  segment_of: '所属光缆',
  connects_device: '连接设备',
  carried_by: '承载于',
  routed_over: '路由经过',
  uses_fiber: '占用纤芯',
  member_of_ring: '环成员',
  protects: '保护',
  in_domain: '所属IGP域',
  in_vpn: '所属VPN',
  serves: '服务客户',
  subscribed_by: '订阅客户',
  provided_by: '提供电路',
  depends_on: '依赖',
  uplink_to: '上联',
  home_bbu: '归属BBU',
  covers: '覆盖基站',
  connects_onu: '接入PON口',
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
  device_ip: '设备IP',
  port_name: '端口名称',
  port_desc: '端口描述',
  port_type: '端口类型',
  admin_status: '管理状态',
  oper_status: '运行状态',
  device_type: '设备类型',
  district: '区县',
  record_date: '日期',
  geo_lat: '纬度',
  geo_lng: '经度',
  geo: '经纬度',
  base_station_type: '基站类型',
  base_station_name: '无线基站名称',
  base_station_code: '基站编码',
  sharing: '电联共享',
  uplink_a_ip: '归属A IP',
  ring: '环',
  chain: '链',
  uplink_a_name: '归属A名称',
  uplink_a_port: '归属A端口',
  gateway_ip: '网关IP',
  base_station_ip: '基站IP',
  description: '描述',
  work_order: '工单号',
  remark: '备注',
  site_id: '局站ID',
  site_name: '局站名称',
  machine_room_id: '机房ID',
  machine_room_name: '机房名称',
  ip_address: 'IP地址',
  // ptp_port（v0.5 W8-D）：PTP 配置脚本导入的模型级端口模板字段
  device_model: '设备型号',
  annotation: '注释',
  portnum: '端口号',
  port_state: '端口状态',
  free_mode: '自由模式',
  pdelay_req_interval: '延迟请求间隔',
  unsymmetrical_offset: '非对称偏移',
  delay_mechanism: '延迟机制',
  domain_num: '域号',
  clock_mode: '时钟模式',
  state_algorithm: '状态算法',
  priority1: '优先级1',
  priority2: '优先级2',
  local_priority: '本地优先级',
  clock_class: '时钟等级',
  clock_accuracy: '时钟精度',
  announce_interval: '通告间隔',
  announce_timeout: '通告超时',
  utc_trace: 'UTC追踪',
  ssm_mode: 'SSM模式',
  ssm_sec_quality: 'SSM质量等级',
  ssm_wtr_time: 'SSM等待恢复时间',

  // ---- W21 扩容字段（data-model v1.0 §3.2；与后端 mapping Specs 的 Label 同词形，
  // 冲突名以主要类型语义为准：machine_room_id=机房ID、device_code=设备编码、
  // a_end_site/z_end_site=光缆端局站、ring_code=环编码） ----
  rack_code: '机架编码',
  column_no: '列号',
  row_no: '行号',
  duct_code: '管道编码',
  a_end_site: 'A端局站',
  z_end_site: 'Z端局站',
  length_km: '长度',
  hole_count: '孔数',
  manhole_code: '人井编码',
  pole_code: '杆路编码',
  a_end: '起点',
  z_end: '终点',
  pole_no: '杆号',
  board_id: '板卡ID',
  slot_no: '槽位号',
  board_model: '板卡型号',
  board_type: '板卡类型',
  cable_code: '光缆编码',
  cable_name: '光缆名称',
  core_count: '芯数',
  connect_device: '连接设备',
  segment_code: '光缆段编码',
  segment_index: '段序',
  a_end_box: 'A端接头盒',
  z_end_box: 'Z端接头盒',
  fiber_no: '纤芯号',
  terminate_odf: '成端ODF端子',
  fiber_count: '纤芯总数',
  usage: '使用情况',
  usage_direction: '使用局向',
  tx_rx: '收发关系',
  odf_code: 'ODF编码',
  odf_name: 'ODF名称',
  capacity: '容量',
  terminal_no: '端子号',
  fiber_code: '对应纤芯',
  splice_box_code: '接头盒编码',
  used_cores: '已用芯数',
  och_code: '波道编码',
  wave_no: '波道号',
  wavelength: '波长',
  rate: '速率',
  route_cable: '路由光缆',
  use_fiber: '占用纤芯',
  timeslot_code: '时隙编码',
  vc_level: 'VC级别',
  timeslot_no: '时隙号',
  capacity_mbps: '容量',
  pg_code: '保护组编码',
  protect_type: '保护类型',
  protected_circuit: '保护电路',
  work_path: '工作路径',
  protect_path: '保护路径',
  ring_code: '环编码',
  ring_name: '环名称',
  ring_type: '环类型',
  node_count: '节点数',
  domain_code: '域编码',
  domain_name: '域名称',
  process_no: '进程号',
  area_no: '区域号',
  protocol: '协议',
  vpn_code: 'VPN编码',
  vpn_name: 'VPN名称',
  rd: 'RD',
  rt: 'RT',
  customer: '客户',
  customer_code: '客户编码',
  customer_name: '客户名称',
  customer_type: '客户类型',
  contact: '联系人',
  service_code: '业务编码',
  service_type: '业务类型',
  provide_circuit: '提供电路',
  depend_device: '依赖设备',
  depend_circuit: '依赖电路',
  line_code: '专线编码',
  line_name: '专线名称',
  stn_no: '集团STN编号',
  a_end_address: 'A端装机地址',
  z_end_address: 'Z端装机地址',
  bbu_code: 'BBU编码',
  rru_code: 'RRU编码',
  home_bbu: '归属BBU',
  cover_station: '覆盖基站',
  olt_code: 'OLT编码',
  pon_port_count: 'PON口数',
  onu_code: 'ONU编码',
  connect_port: '接入PON口',
  user: '用户',
  port_no: '端口号',
  split_ratio: '分光比',
  // 既有类型新增的可选 ref 列（W21 §4 契约）：标签已在上方各字段中给出
};

export const FIELD_KIND_LABELS: Record<string, string> = {
  string: '文本',
  ipv4: 'IPv4地址',
  cidr: 'CIDR网段',
  int: '整数',
  enum: '枚举',
  ref: '引用',
  lat: '纬度',
  lng: '经度',
  geo: '经纬度',
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
export const OV_GOVERNANCE_TITLE = '治理总览';
export const OV_GOVERNANCE_HINT = '待确认队列、决策审计与数据健康的一屏入口（数据来自 /api/governance/summary）';
export const OV_GOVERNANCE_PENDING_CHANGES = '待确认属性变更';
export const OV_GOVERNANCE_PENDING_RENAMES = '疑似改名待确认';
export const OV_GOVERNANCE_GO_CONFIRM = '去确认';
export const OV_GOVERNANCE_NO_PENDING = '暂无待确认事项';
export const OV_GOVERNANCE_ISSUES_TITLE = '问题分布';
export const OV_GOVERNANCE_TOP_RULES = '按规则 Top 5';
export const OV_GOVERNANCE_TOP_RULES_EMPTY = '暂无问题记录';
export const OV_GOVERNANCE_RESOURCES_TITLE = '资源形态';
export const OV_GOVERNANCE_RELATIONS_TITLE = '关系形态';
export const OV_GOVERNANCE_TOTAL = '合计';
export const OV_GOVERNANCE_BATCHES_TITLE = '批次状态';
export const OV_GOVERNANCE_BATCHES_EMPTY = '暂无导入批次';
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
export const IMP_DROPZONE_DESC = '支持 .xlsx 工作簿与 PTP 配置脚本（.txt/.cfg），单个文件不超过 10MB';
export const IMP_DROPZONE_RETRY = '重试上传';
export const IMP_SELECTED_FILE = '已选择文件：{name}（{size}）';
export const IMP_ROW_SOURCE = '{sheet} · 行 {row}';
export const IMP_UPLOADING = '正在上传并解析…';
export const IMP_ERR_FILE_TYPE = '请选择 .xlsx 工作簿或 .txt/.cfg PTP 配置脚本文件';
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

// R4 短期项 2：映射确认页自然键稳定性提示（仅展示，不改 keyColumns 逻辑）
/** IP 类自然键的短标签（行内展示）。 */
export const IMP_NATURAL_KEY_IP_SHORT = 'IP 作键不稳定';
/** IP 类自然键的完整告警文案（tooltip / 无障碍名称；migration v8 后当前四类
 *  均无 IP 自然键，属防御性提示，供未来类型复用）。 */
export const IMP_NATURAL_KEY_IP_WARNING =
  'IP 属可变属性：设备重划 IP 后，同键资源将分裂为两条（应改用稳定标识作键）';
/** 名称类自然键的作用域轻提示（info 级；单键名称类型如 ip_pool）。 */
export const IMP_NATURAL_KEY_NAME_SCOPE =
  '名称键需保证唯一作用域：跨地市同名会冲突，建议（地市 + 名称）复合作键';
/** port 复合键专属正向提示（migration v8 起）：键已锚定设备名称，IP 重划不再分裂。 */
export const IMP_NATURAL_KEY_PORT_COMPOSITE =
  '端口键 = 设备名称|端口名称：设备重划 IP 不再影响资源身份（设备IP 为普通值列）';
/** ptp_port 复合键专属提示（v0.5 W8-D，PTP 配置脚本通道）：模型级模板键。 */
export const IMP_NATURAL_KEY_PTP_COMPOSITE =
  'PTP端口键 = 设备型号|接口名称：配置脚本为模型级模板（无设备实例身份，不派生关系）；接口在脚本修订中改名时走 W006 疑似改名确认流';
/** 条件自然键提示（base_station，v0.4）：编码缺失时键退化为兜底字段并记 W004。 */
export const IMP_NATURAL_KEY_FALLBACK =
  '基站键 = 基站编码（全局唯一）；编码为空时退化为 地市|无线基站名称 兜底键（缺地市列则为纯名称），并记录 W004 治理问题（行照常导入）';
/** 引用字段列短标签（行内展示；v0.4 W3-D 机房局站导入故事）。 */
export const IMP_REF_FIELD_SHORT = '引用列 · 需先导入目标类型';
/** 引用字段列的完整说明（tooltip / 无障碍名称）：E003 依赖导入顺序的正向说明。 */
export const IMP_REF_FIELD_NOTE =
  '引用字段按被引用类型的自然键解析（机房表的 局站ID → 局站资源）：请先导入被引用类型（先局站后机房），未解析的引用将使该行失败（E003）；该列可不映射（不建立关系，行照常导入）';
/** W24 无人值守导入：表头指纹命中已确认映射模板的信息横幅（向导第②步，
 *  纯展示——向导仍是人工确认通道；无人值守走 CLI/HTTP auto=1）。 */
export const IMP_TEMPLATE_MATCHED =
  '已匹配映射模板（确认于 {date}）——向导预填的映射即模板内容';
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
/** 导出按钮 title：说明导出范围与回源能力（W12-D）。 */
export const RES_EXPORT_HINT = '把当前资源清单导出为 xlsx（当前类型筛选生效，搜索关键词不参与）；导出文件可再导入回库';
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
// 资源抽屉 · 待确认属性变更面板（W4-D）
// ---------------------------------------------------------------------------

export const RES_CHANGES_TITLE = '待确认属性变更';
export const RES_CHANGES_HINT = '同键再导入检测到属性差异，行已跳过；人工确认后变更才会生效。';
export const RES_CHANGES_BATCH_ROW = '批次 {batch} · 行 {row}';
// W13-D：引用字段的行内轻提示（relationRetargeted 预览为真时渲染）。
export const RES_CHANGES_REF_FIELD_HINT = '引用字段 · 确认后将同步改挂关系（需目标已存在）';
export const RES_CHANGE_APPLY = '应用';
export const RES_CHANGE_DISMISS = '忽略';
export const RES_CHANGE_APPLYING = '处理中…';
export const RES_CHANGES_APPLIED_TOAST = '已应用变更：{field} 已更新';
// W13-D：apply 响应 relationRetargeted=true 时使用（派生关系已随确认改挂）。
export const RES_CHANGES_APPLIED_RETARGETED_TOAST = '已应用变更：{field} 已更新，关系已改挂';
export const RES_CHANGES_DISMISSED_TOAST = '已忽略变更：{field} 维持原值';
export const RES_CHANGES_ACTION_FAILED = '操作失败：{message}';

// ---------------------------------------------------------------------------
// 资源抽屉 · 疑似改名面板（W6-D）
// ---------------------------------------------------------------------------

export const RES_RENAMES_TITLE = '疑似改名待确认';
export const RES_RENAMES_HINT =
  '新键行与库内旧键资源属性一致，疑似同一资产改名。确认后合并身份（保留本资源的 ID 与关系并改用新键），拒绝则维持两个独立资源。';
export const RES_RENAME_KEYS = '{old} → {new}';
export const RES_RENAMES_MATCHED = '{count} 个属性一致';
export const RES_RENAME_CONFIRM = '确认改名';
export const RES_RENAME_REJECT = '拒绝';
export const RES_RENAME_WORKING = '处理中…';
export const RES_RENAMES_APPLIED_TOAST = '已确认改名：{old} → {new}';
export const RES_RENAMES_REJECTED_TOAST = '已拒绝改名提案，资源 {old} 保持独立';
export const RES_RENAMES_ACTION_FAILED = '操作失败：{message}';

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
// 告警中心页（v0.9 /alerts）
// ---------------------------------------------------------------------------

export const NAV_ALERTS = '告警中心';

/** 告警状态（firing|acked|cleared）。 */
export const ALERT_STATE_LABELS: Record<AlertState, string> = {
  firing: '触发中',
  acked: '已确认',
  cleared: '已清除',
};

/** 阈值比较器 → 中文短语（用于「最新值 vs 阈值」行内描述）。 */
export const ALERT_COMPARATOR_LABELS: Record<AlertComparator, string> = {
  gt: '高于',
  gte: '不低于',
  lt: '低于',
  lte: '不高于',
};

export const ALR_FILTER_STATE = '状态';
export const ALR_FILTER_SEVERITY = '级别';
export const ALR_FILTER_ROLE = '关联';
export const ALR_COL_CORRELATION = '关联';
export const ALR_CORR_PRIMARY = '根因嫌疑';
export const ALR_CORR_SECONDARY = '疑似次生';
export const ALR_CORR_PRIMARY_HINT = '下游存在疑似次生告警——优先处置这条';
export const ALR_CORR_SECONDARY_HINT = '上游存在告警中的资源——疑似被波及，先处置根因';
export const ALR_CORR_PRIMARY_LINE = '下游存在疑似次生告警，本告警优先处置';
export const ALR_CORR_CAUSE_LINE = '疑似次生：上游资源 {resource} 存在告警中的告警 {alertId}——先处置该根因';
export const ALR_CORR_DETAIL_LABEL = '根因/次生';
export const ALR_CORR_NONE_LINE = '未关联（独立告警或同层告警）';
/** 关联角色 chip/选项文案（闭集）。 */
export const CORRELATION_ROLE_LABELS: Record<string, string> = {
  primary: ALR_CORR_PRIMARY,
  secondary: ALR_CORR_SECONDARY,
};
export const ALR_COL_STATE = '状态';
export const ALR_COL_SEVERITY = '级别';
export const ALR_COL_RULE = '规则';
export const ALR_COL_RESOURCE = '资源';
export const ALR_COL_FIRED = '触发时间';
export const ALR_COL_VALUE = '最新值 / 阈值';
export const ALR_COL_ACTIONS = '处置';
export const ALR_VALUE_TEXT = '{value}（{comparator}阈值 {threshold}）';
export const ALR_BTN_ACK = '确认';
export const ALR_BTN_CLEAR = '清除';
export const ALR_BTN_CLEAR_WORKING = '清除中…';
export const ALR_HINT = '触发中的告警需先确认（记录处置人）再清除；清除即归档，告警记录保留为审计线索';
export const ALR_ACK_TITLE = '确认告警';
export const ALR_ACK_LABEL = '确认人';
export const ALR_ACK_PLACEHOLDER = '例如：张工';
export const ALR_ACK_HINT = '确认人将写入告警审计字段（触发中 → 已确认）';
export const ALR_ACK_WORKING = '确认中…';
export const ALR_CLEAR_TITLE = '清除告警';
export const ALR_CLEAR_MESSAGE = '清除后该告警转入「已清除」归档态，记录保留可查。确定清除吗？';
export const ALR_ACKED_TOAST = '告警已确认（处置人：{who}）';
export const ALR_CLEARED_TOAST = '告警已清除并归档';
export const ALR_ACTION_FAILED = '处置失败：{message}';
export const ALR_DETAIL_TITLE = '告警详情';
export const ALR_DETAIL_RULE_ID = '规则 ID';
export const ALR_DETAIL_SERIES = '指标序列';
export const ALR_DETAIL_METRIC = '指标';
export const ALR_DETAIL_WINDOW = '窗口（点数）';
export const ALR_DETAIL_FIRST_SEEN = '首次出现';
export const ALR_DETAIL_LAST_FIRED = '本次触发';
export const ALR_DETAIL_ACK_LINE = '已由 {who} 确认（{at}）';
export const ALR_DETAIL_CLEARED_LINE = '已清除（{at}）';
export const ALR_DETAIL_LIFECYCLE = '处置通知';
export const LIFECYCLE_DELIVERED_TITLE = '{event} 通知已送达 · 尝试 {n} 次';
export const LIFECYCLE_FAILED_TITLE = '{event} 通知投递失败 · 尝试 {n} 次';
export const LIFECYCLE_NONE_TITLE = '该告警的处置动作未通知（未被 ack/clear 过，或当时未配置 webhook 通道）';
export const ALR_RESOURCE_LINK_TITLE = '在资源浏览中查看';
export const ALR_EMPTY = '当前筛选条件下没有告警';

// ---------------------------------------------------------------------------
// 检测规则管理页（v0.10-R6，/rules）
// ---------------------------------------------------------------------------

export const NAV_RULES = '检测规则';
export const RUL_HINT =
  '阈值规则的唯一权威来源：检测运行只评估启用中的规则；删除被告警引用的规则会被拒绝（告警是审计线索）。';
export const RUL_EMPTY = '没有规则（首次访问时会播种默认规则集）';
export const RUL_FILTER_ENABLED = '仅看启用中';
export const RUL_FILTER_METRIC = '指标';
export const RUL_COL_ENABLED = '状态';
export const RUL_COL_SEVERITY = '级别';
export const RUL_COL_LABEL = '规则标签';
export const RUL_COL_METRIC = '指标';
export const RUL_COL_CONDITION = '触发条件';
export const RUL_COL_WINDOW = '窗口';
export const RUL_COL_UPDATED = '更新时间';
export const RUL_COL_ACTIONS = '操作';
export const RUL_BTN_NEW = '新建规则';
export const RUL_BTN_ENABLE = '启用';
export const RUL_BTN_DISABLE = '停用';
export const RUL_BTN_EDIT = '编辑';
export const RUL_BTN_DELETE = '删除';
export const RUL_BTN_SAVE = '保存';
export const RUL_ENABLED_LABEL = '启用中';
export const RUL_DISABLED_LABEL = '已停用';
export const RUL_WINDOW_TEXT = '连续 {n} 点';
export const RUL_RULE_META = '规则 ID {id} · 创建于 {at}';
export const RUL_RULE_META_ID = '规则 ID';
export const RUL_RULE_META_LABEL = '规则标签';
export const RUL_RULE_CREATED_AT = '创建时间';
export const RUL_RULE_UPDATED_AT = '更新时间';
export const RUL_RULE_THRESHOLD_LINE = '完整条件';
export const RUL_METRIC_UNIT_HINT = '单位：{unit} · 合法区间 {min} ~ {max}';
export const RUL_METRIC_UNIT_NO_RANGE = '单位：{unit}';
export const RUL_CREATE_TITLE = '新建检测规则';
export const RUL_EDIT_TITLE = '编辑检测规则';
export const RUL_EDIT_IDENTITY_HINT = '指标是规则的身份字段不可修改；换指标请新建规则并停用本条。';
export const RUL_FIELD_METRIC = '指标（不可改）';
export const RUL_FIELD_METRIC_NEW = '指标';
export const RUL_FIELD_COMPARATOR = '比较器';
export const RUL_FIELD_THRESHOLD = '阈值';
export const RUL_FIELD_WINDOW = '窗口（连续越限点数）';
export const RUL_FIELD_SEVERITY = '级别';
export const RUL_FIELD_LABEL = '规则标签';
export const RUL_FIELD_LABEL_PLACEHOLDER = '例如：光功率劣化告警';
export const RUL_FIELD_ENABLED = '启用这条规则';
export const RUL_THRESHOLD_INVALID = '阈值必须是有限数值';
export const RUL_WINDOW_INVALID = '窗口必须是 1 ~ 365 的整数';
export const RUL_METRIC_REQUIRED = '请选择指标';
export const RUL_DELETE_TITLE = '删除检测规则';
export const RUL_DELETE_MESSAGE = '将删除规则 {label}（{ruleId}）。已产生告警的规则无法删除，请改用停用。';
export const RUL_CREATED_TOAST = '规则已创建（{id}）';
export const RUL_UPDATED_TOAST = '规则已更新（{id}）';
export const RUL_DELETED_TOAST = '规则已删除（{id}）';
export const RUL_TOGGLED_ON_TOAST = '规则已启用（{id}）';
export const RUL_TOGGLED_OFF_TOAST = '规则已停用（{id}）';
export const RUL_ACTION_FAILED = '操作失败：{message}';

// ---------------------------------------------------------------------------
// 立即检测（v0.10-R7，POST /api/detection/run 的 UI 入口，告警页/规则页共用）
// ---------------------------------------------------------------------------

export const RUN_BTN_LABEL = '立即检测';
export const RUN_BTN_WORKING = '检测中…';
export const RUN_CONFIRM_TITLE = '运行检测';
export const RUN_CONFIRM_MESSAGE =
  '对全库指标序列执行一次阈值检测扫描（评估所有启用规则）。运行完成后自动刷新告警列表。';
export const RUN_RESULT_TITLE = '检测运行回执';
export const RUN_FINDINGS = '发现越限 {n}';
export const RUN_CREATED = '新建告警 {n}';
export const RUN_CLEARED = '恢复清除 {n}';
export const RUN_RUNSTATES = '写入运行状态 {n}';
export const RUN_SUPPRESSED = '维护抑制 {n}';
export const RUN_FLAP_HELD = '抖动抑制 {n}';
export const RUN_EVALUATED_AT = '数据截止 {at}';
export const RUN_EVALUATED_NONE = '数据截止：本次窗口无数据';
export const RUN_MUTATIONS_TITLE = '告警变更明细';
export const RUN_MUTATIONS_EMPTY = '本次运行无告警变更（数据与规则未变时为预期行为）';
export const RUN_ACTION_CREATED = '新建';
export const RUN_ACTION_CLEARED = '清除';
export const RUN_ACTION_SUPPRESSED = '抑制';
export const RUN_ACTION_FLAP_HELD = '抖动抑制';
export const RUN_DONE = '完成';
export const RUN_FAILED = '检测运行失败：{message}';
export const RUN_TOAST_DONE = '检测完成：新建 {created} · 清除 {cleared}';

// ---------------------------------------------------------------------------
// 维护窗口页（v0.12-R15，/maintenance）
// ---------------------------------------------------------------------------

export const NAV_MAINTENANCE = '维护窗口';
export const MNT_HINT =
  '计划内维护期间抑制新告警：窗口时间区间内的新触发不产生告警（已触发的告警生命周期不受影响，恢复清除照常）。';
export const MNT_TIME_BASIS =
  '时间基准（双时基判定）：证据时刻（指标数据时间线，可重放）或运行时刻（扫描执行的现实时刻）任一落在窗口内即抑制新告警。当前数据时刻：{dataTime}——若按墙钟排班（如「今晚 22:00」）请按现实时刻填写；若要对历史回放按数据时刻对齐，请让窗口覆盖数据时刻。';
export const MNT_EMPTY = '没有维护窗口';
export const MNT_BTN_NEW = '新建窗口';
export const MNT_BTN_DELETE = '删除';
export const MNT_COL_LABEL = '事由';
export const MNT_COL_RANGE = '时间区间';
export const MNT_COL_SCOPE = '范围';
export const MNT_COL_STATUS = '状态';
export const MNT_COL_ACTIONS = '操作';
export const MNT_SCOPE_GLOBAL = '全部资源';
export const MNT_SCOPE_RESOURCES = '{n} 个资源';
export const MNT_STATUS_PLANNED = '计划中';
export const MNT_STATUS_ACTIVE = '进行中';
export const MNT_STATUS_ENDED = '已结束';
export const MNT_STATUS_ACTIVE_HINT = '当前时刻落在窗口内：新告警触发将被抑制';
export const MNT_CREATE_TITLE = '新建维护窗口';
export const MNT_FIELD_LABEL = '事由';
export const MNT_FIELD_LABEL_PLACEHOLDER = '例如：哈大齐干线割接';
export const MNT_FIELD_START = '开始时间';
export const MNT_FIELD_END = '结束时间（不含）';
export const MNT_FIELD_SCOPE = '抑制范围';
export const MNT_SCOPE_OPTION_GLOBAL = '全部资源（全局）';
export const MNT_SCOPE_OPTION_SCOPED = '指定资源';
export const MNT_SCOPE_SEARCH_PLACEHOLDER = '搜索资源（名称 / 自然键）添加到范围';
export const MNT_SCOPE_EMPTY_HINT = '未指定资源时抑制全部资源的新告警';
export const MNT_FIELD_NOTE = '备注（可选）';
export const MNT_FIELD_NOTE_PLACEHOLDER = '例如：计划内光缆割接，预期 2 小时';
export const MNT_LABEL_REQUIRED = '请填写事由';
export const MNT_TIMES_REQUIRED = '请填写开始与结束时间';
export const MNT_TIMES_INVALID = '时间格式不合法（需要日期时间）';
export const MNT_TIMES_ORDER = '结束时间必须晚于开始时间';
export const MNT_CREATE_TOAST = '窗口已创建（{id}）';
export const MNT_DELETE_TITLE = '删除维护窗口';
export const MNT_DELETE_MESSAGE = '将删除窗口「{label}」。历史运行回执不受影响；下一次检测将恢复新告警触发。';
export const MNT_DELETED_TOAST = '窗口已删除（{id}）';
export const MNT_ACTION_FAILED = '操作失败：{message}';
export const MNT_LOAD_FAILED = '窗口加载失败：{message}';

// ---------------------------------------------------------------------------
// 告警导出（v0.10-R7，/alerts 当前筛选 → CSV 下载）
// ---------------------------------------------------------------------------

export const ALR_BTN_EXPORT = '导出 CSV';
export const ALR_EXPORT_WORKING = '导出中…';
export const ALR_EXPORT_EMPTY = '当前筛选条件下没有告警，无法导出';
export const ALR_EXPORT_FAILED = '导出失败：{message}';
export const ALR_EXPORT_TOAST = '已导出 {n} 条告警';
export const ALR_EXPORT_FILENAME = 'netops-alerts';

// ---------------------------------------------------------------------------
// API 参考页（v0.10-R9，/dev，GET /api/openapi.json 的目录视图）
// ---------------------------------------------------------------------------

export const NAV_API = 'API 参考';
export const NAV_FEES = '费用分析';

// 费用分析页（#58 阶段 5）
export const FEES_TITLE = '费用分析';
export const FEES_DESC = '费用数据的风险分析入口：导入费用文件（CSV/Excel）→ 分析层 Parquet 分区 → 风险六场景查询。线索是待核实差异而非认定结论（账单存在 ≠ 重复付款认定），每条线索携带行级证据锚点。';
export const FEES_IMPORT_TITLE = '导入费用文件';
export const FEES_IMPORT_TYPE = '费用类型';
export const FEES_IMPORT_YEAR = '年份';
export const FEES_IMPORT_MONTH = '月份';
export const FEES_IMPORT_FILE = '文件（.csv / .xlsx / .xls）';
export const FEES_IMPORT_SUBMIT = '导入';
export const FEES_IMPORT_OK = (a: string, b: number) => `导入完成：${b} 行入库（批次 ${a}）`;
export const FEES_IMPORT_DUP = (a: string) => `同一文件已于批次 ${a} 完成导入（幂等跳过，零新数据）`;
export const FEES_RISKS_TITLE = '风险六场景';
export const FEES_RISKS_SCENARIO = '场景';
export const FEES_RISKS_RUN = '查询';
export const FEES_RISKS_LIMIT = '线索上限';
export const FEES_RISKS_EMPTY = '该场景在选定范围内无风险线索。';
export const FEES_RISKS_EXCLUDED = (a: number) => `已排除 ${a} 个跨批次 (站址, 账期)（属于重复结算/金额差异场景的线索域）`;
export const FEES_RISKS_EVIDENCE = '证据锚点';
export const FEES_RISKS_EVIDENCE_MORE = (a: number) => `…另有 ${a} 行（快照层可回读原文）`;
export const FEES_BATCHES_TITLE = '费用批次';
export const FEES_BATCHES_ROLLBACK = '回滚';
export const FEES_BATCHES_ROLLED_BACK = (a: string) => `批次 ${a} 已回滚（分析行删除，快照证据保留）`;
export const FEES_501_HINT = '当前构建不含分析查询引擎（501 engine_not_built）——导入/快照/Parquet 写入不受影响；查询需 DuckDB 构建（Docker 镜像默认含引擎，或 WSL2 内 make build-duckdb，见 docs/fees-layer.md §5）。';
export const FEE_TYPE_ELECTRICITY = '电费';
export const FEE_TYPE_TOWER_LEASE = '铁塔租费';
export const FEE_TYPE_REPAIR = '修理费';
export const FEE_TYPE_MAINTENANCE = '综维费';
// 费用-资源关联（#58 阶段 3 落地 HTTP/CLI，阶段 5 补前端工作台入口）
export const FEES_LINKS_TITLE = '费用-资源关联';
export const FEES_LINKS_DESC = '站址编码 → 资源的分级关联：精确（字节相等）与规则（常见前后缀变换）命中自动确认；模糊命中出待确认提案，人工确认/驳回后不再自动复提。退租后计费、零资源计费等场景依赖本关联。';
export const FEES_LINKS_SCAN = '重新扫描';
export const FEES_LINKS_DISMISSING = '驳回中…';
export const FEES_LINKS_SCAN_RESULT = (a: number, b: number, c: number, d: number, e: number, f: number) => `扫描 ${a} 个站址编码：精确命中 ${b} · 规则命中 ${c} · 模糊提案 ${d} · 未关联 ${e} · 人工决策保留 ${f}`;
export const FEES_LINKS_STATUS = '状态';
export const FEES_LINKS_GRADE = '级别';
export const FEES_LINKS_ALL = '全部';
export const FEES_LINKS_CODE = '站址编码';
export const FEES_LINKS_RESOURCE = '资源';
export const FEES_LINKS_SOURCE = '来源';
export const FEES_LINKS_ACTIONS = '操作';
export const FEES_LINKS_EMPTY = '暂无关联记录——导入费用文件并确保资源已入库后，点「重新扫描」。';
export const FEES_LINKS_CONFIRM = '确认';
export const FEES_LINKS_DISMISS = '驳回';
export const FEES_LINKS_CONFIRMED = (a: string) => `关联提案 ${a} 已确认（人工决策，优先于后续自动扫描）`;
export const FEES_LINKS_DISMISSED = (a: string) => `关联提案 ${a} 已驳回（审计保留，该编码与资源的配对不再自动复提）`;
export const FEES_LINKS_DISMISS_CONFIRM = (a: string, b: string) => `驳回模糊关联提案？\n站址编码 ${a} → 资源 ${b}\n驳回后该配对不再自动复提（审计记录保留）。`;
export const LINKS_STATUS_PENDING = '待确认';
export const LINKS_STATUS_CONFIRMED = '已确认';
export const LINKS_STATUS_DISMISSED = '已驳回';
export const LINKS_GRADE_EXACT = '精确';
export const LINKS_GRADE_RULE = '规则';
export const LINKS_GRADE_FUZZY = '模糊';
export const LINKS_SOURCE_AUTO = '自动';
export const LINKS_SOURCE_HUMAN = '人工';
export const RISK_SCENARIO_RETIRED_BILLING = '退租后仍在计费';
export const RISK_SCENARIO_DUPLICATE_SETTLEMENT = '跨批次重复结算';
export const RISK_SCENARIO_LEDGER_MISMATCH = '合同账单金额差异';
export const RISK_SCENARIO_SPIKE = '电费环比异常上涨';
export const RISK_SCENARIO_OUTLIER = '同类站单位成本离群';
export const RISK_SCENARIO_UNLINKED = '零资源计费';
export const API_REF_HINT =
  '本页目录由 /api/openapi.json 实时渲染（后端路由表的投影，与实现零漂移）。AI 集成方直接读取该 JSON 端点即可发现全部接口。';
export const API_REF_ENDPOINTS = '个端点';
export const API_REF_TAG_GROUPS = '接口分组';
export const API_REF_EMPTY = '暂无接口文档（/api/openapi.json 不可达）';
export const API_TAG_LABELS: Record<string, string> = {
  system: '系统',
  imports: '导入与批次',
  resources: '资源',
  graph: '图查询',
  metrics: '指标时序',
  detection: '检测',
  alerts: '告警',
  governance: '治理',
  lab: '仿真工作台',
};

// ---------------------------------------------------------------------------
// 资源详情 · 性能指标页签（v0.9-R2）
// ---------------------------------------------------------------------------

export const RES_DRAWER_TAB_METRICS = '性能指标';
export const METRIC_EMPTY = '该资源没有指标序列（检测告警与劣化曲线在此展示）';
export const METRIC_LOAD_FAILED = '指标加载失败';
export const METRIC_LOADING = '指标加载中…';
export const METRIC_CHART_POINTS = '{n} 点';
export const METRIC_CHART_LAST = '最新 {v}';
export const METRIC_CHART_SUSPECT = '疑似异常点';
export const METRIC_CHART_MISSING = '缺失点';

// ---------------------------------------------------------------------------
// 资源详情 · 指标 CSV 导出（v0.11-R12：与告警 CSV 导出对称）
// ---------------------------------------------------------------------------

export const METRICS_EXPORT_BTN = '导出指标 CSV';
export const METRICS_EXPORT_EMPTY = '无指标数据点可导出';
export const METRICS_EXPORT_TOAST = '已导出 {n} 个数据点（{s} 条序列）';
export const METRICS_EXPORT_FAILED = '导出失败：{message}';
export const METRICS_EXPORT_WORKING = '导出中…';
export const METRICS_EXPORT_FILENAME = 'netops-metrics';

// ---------------------------------------------------------------------------
// 资源详情 · 告警页签（v0.11-R12：故障定位动线闭环——资源视角的告警史）
// ---------------------------------------------------------------------------

export const RES_DRAWER_TAB_ALERTS = '告警';
export const RES_ALERTS_TAB_HINT = '该资源的告警记录（全部生命周期状态）；确认与清除在告警中心操作。';
export const RES_ALERTS_EMPTY = '该资源无告警记录';
export const RES_ALERTS_LOAD_FAILED = '告警加载失败';
export const RES_ALERTS_LINK = '去告警中心处置';
export const RES_ALERTS_MORE = '仅显示最近 {n} 条（共 {total} 条），其余在告警中心查看';

// ---------------------------------------------------------------------------
// 访问令牌设置（v0.11-R13：serve --auth-token 写网关的浏览器侧配套）
// ---------------------------------------------------------------------------

export const AUTH_TOKEN_BTN = '访问令牌';
export const AUTH_TOKEN_TITLE = '访问令牌';
export const AUTH_TOKEN_HINT =
  '服务器启用 --auth-token 时，写操作（确认、清除、导入、检测运行等）需要此令牌；只读浏览不受影响。令牌保存在本浏览器。';
export const AUTH_TOKEN_LABEL = '令牌';
export const AUTH_TOKEN_PLACEHOLDER = '粘贴 serve --auth-token 的值';
export const AUTH_TOKEN_SAVE = '保存';
export const AUTH_TOKEN_CLEAR = '清除';
export const AUTH_TOKEN_SAVED_TOAST = '访问令牌已保存（本浏览器）';
export const AUTH_TOKEN_CLEARED_TOAST = '访问令牌已清除';
export const AUTH_TOKEN_BADGE = '已设置';

// ---------------------------------------------------------------------------
// 资源详情 · 影响分析页签（v0.9-R3，图查询 traverse/impact）
// ---------------------------------------------------------------------------

export const RES_DRAWER_TAB_GRAPH = '影响分析';
export const GRAPH_MODE_RCA = '根因分析';
export const GRAPH_MODE_IMPACT = '影响面';
export const GRAPH_MODE_TRAVERSE_DOWN = '向下穿透';
export const GRAPH_MODE_TRAVERSE_UP = '向上汇总';
export const GRAPH_CANDIDATES_TITLE = '根因候选';
export const GRAPH_CANDIDATES_EMPTY = '无根因候选（本资源周边无异常节点）';
export const GRAPH_IMPACTED_TITLE = '受影响面';
export const GRAPH_IMPACTED_EMPTY = '无受影响资源';
export const GRAPH_TRAVERSE_TITLE = '穿透结果';
export const GRAPH_TRAVERSE_EMPTY = '该方向无可达资源';
export const GRAPH_NODES_UNIT = '节点 {n}';
export const GRAPH_EDGES_UNIT = '关系 {n}';
export const GRAPH_CANDIDATES_COUNT = '候选 {n}';
export const GRAPH_CROSSLAYER_UNIT = '跨层 {n}';
export const GRAPH_DEPTH_UNIT = '距离 {n}';
export const GRAPH_TRUNCATED_HINT = '结果已按预算截断（分类基于不完整子图，结论供参考）';
export const GRAPH_RUNSTATE_ABNORMAL = '异常';
export const GRAPH_RUNSTATE_NORMAL = '正常';
export const GRAPH_NODE_LINK_TITLE = '在资源浏览中查看该资源';
export const GRAPH_ROOT_LABEL = '分析起点';
export const GRAPH_REASON_LABEL = '判定理由';

// ---------------------------------------------------------------------------
// 资源详情 · 运行状态行（v0.9-R5）
// ---------------------------------------------------------------------------

export const RES_RUNSTATE_LABEL = '运行状态';
export const RES_RUNSTATE_NO_DATA = '无观测';
export const RES_RUNSTATE_HISTORY_TITLE = '状态历史';
export const RES_RUNSTATE_HISTORY_EMPTY = '暂无状态观测记录';

// ---------------------------------------------------------------------------
// 概览页 · 告警概览卡（v0.9-R5）
// ---------------------------------------------------------------------------

export const OV_STAT_FIRING_ALERTS = '触发中告警';
export const OV_ALERTS_SUB = '告警中心待处置队列';
export const OV_ALERTS_NONE_SUB = '无触发中告警';
/** v0.11-R11：告警卡副行——待处置级别矩阵 + 已确认数（stats 端点一次取齐）。 */
export const OV_ALERTS_SUB_V11 = '待处置：错误 {error} · 警告 {warning} · 已确认 {acked}';
export const BTN_VIEW_ALERTS = '告警中心';
export const RUN_STATE_LABELS: Record<string, string> = {
  up: '运行',
  down: '停机',
  degraded: '劣化',
  maintenance: '维护',
  unknown: '未知',
};

// ---------------------------------------------------------------------------
// 质量报告页
// ---------------------------------------------------------------------------

export const RPT_SCOPE = '报告范围';
export const RPT_SCOPE_ALL = '全部批次';
// issue #57②：来源中立的免责声明——数据库里可能是用户的真实台账、
// 真实公开数据或合成演示数据，页面无法区分，因此不再断言「合成测试数据」。
export const RPT_DISCLAIMER = '本报告基于当前已导入的数据生成，请以实际网络情况为准。';
export const RPT_PREVIEW_TITLE = '报告预览';
export const RPT_LOADING = '正在生成报告…';
export const RPT_EMPTY = '暂无报告内容';
export const RPT_FILENAME_HTML = 'netops-quality-report';
export const RPT_FILENAME_JSON = 'netops-quality-report';
export const RPT_FILE_EXT_HTML = '.html';
export const RPT_FILE_EXT_JSON = '.json';

// ---------------------------------------------------------------------------
// 仿真工作台（Phase 2 · /lab）
// ---------------------------------------------------------------------------

/** 仿真实体状态：up=运行中，down=已停机。 */
export const SIM_STATUS_LABELS: Record<SimEntityStatus, string> = {
  up: '运行中',
  down: '已停机',
};

/** 变更集状态中文标签。 */
export const CS_STATUS_LABELS: Record<ChangeSetStatus, string> = {
  applied: '已应用',
  reverted: '已撤销',
  failed: '失败',
};

/** 变更集触发方式中文标签。 */
export const CS_TRIGGER_LABELS: Record<ChangeSetTrigger, string> = {
  user: '用户操作',
  undo: '撤销',
  checkpoint_restore: '检查点恢复',
};

/** 校验运行触发方式中文标签。 */
export const CHECK_TRIGGER_LABELS: Record<CheckTrigger, string> = {
  manual: '手动',
  api: 'API',
  mcp: 'MCP',
  auto_after_ops: '操作后自动',
};

/** 仿真检查项编码 → 中文标签（RuleChip 同风格）。 */
export const SIM_CHECK_LABELS: Record<SimCheckCode, string> = {
  'CHK-001': '主机地址重复',
  'CHK-002': '网段重叠',
  'CHK-003': '设备孤岛',
  'CHK-004': '部分分区',
  'CHK-005': '接口未配置地址',
};

/** 检查项中文解释（checkCatalog：展示在编码 chip 的 title 提示上）。 */
export const SIM_CHECK_HINTS: Record<SimCheckCode, string> = {
  'CHK-001': '同一 IPv4 地址被多个处于运行状态的接口同时配置（错误）',
  'CHK-002': '两个已配置接口的子网相互重叠，分为完全相同 / 包含 / 部分重叠（错误）',
  'CHK-003': '处于运行状态的设备没有任何可用链路，成为孤岛（警告）',
  'CHK-004': '按可用链路划分连通分量，存在与最大分区失联的设备分区（提示）',
  'CHK-005': '处于运行状态的接口尚未配置 IPv4 地址（提示）',
};

/** CHK-002 重叠类型 → 中文（引擎 kind 字段）。 */
export const SIM_OVERLAP_KIND_LABELS: Record<string, string> = {
  identical: '完全相同',
  contains: '包含',
  partial: '部分重叠',
};

/** 检查消息中文模板（checkCatalog 按引擎消息正则解析后回填；未命中回退英文原文）。 */
export const SIM_MSG_DUP_ADDR = 'IPv4 地址 {addr} 被 {n} 个接口同时配置';
export const SIM_MSG_OVERLAP = '网段 {a} 与 {b} 重叠（{kind}）';
export const SIM_MSG_ISOLATED = '设备 {code} 为孤岛（没有任何可用链路）';
export const SIM_MSG_PARTITION = '{n} 台设备组成的分区与主分区失联';
export const SIM_MSG_UNCONFIGURED = '接口 {device}/{name} 尚未配置 IPv4 地址';

// —— 工作台列表页（/lab） ——

export const LAB_COL_NAME = '名称';
export const LAB_COL_DESC = '描述';
export const LAB_COL_DEVICES = '设备';
export const LAB_COL_INTERFACES = '接口';
export const LAB_COL_LINKS = '物理链路';
export const LAB_COL_LAYERS = '图层（节点/边）';
export const LAB_COL_LAYERS_TITLE =
  '图层覆盖层（#51）节点/语义边计数。v0.11 前创建的旧工作台无覆盖层（显示 —）；带图层的列值可用于区分新旧工作台';
export const LAB_COL_SCORE = '评分';
export const LAB_COL_LAST_RUN = '最近检查';
export const LAB_COL_CREATED_AT = '创建时间';
export const LAB_COL_ACTIONS = '操作';
export const LAB_TABLE_LABEL = '仿真工作台列表';
export const LAB_SCORE_VALUE = '{score} 分';

export const BTN_CREATE_WORKSPACE = '新建工作台';
export const BTN_DELETE_WORKSPACE = '删除';
export const BTN_CREATE = '创建';
export const BTN_GO_IMPORT = '去数据导入';

export const LAB_EMPTY_TITLE = '暂无工作台';
export const LAB_EMPTY_DESC =
  '尚未创建任何仿真工作台。请先在「数据导入」导入工作簿，再从已完成批次物化仿真拓扑。';

export const LAB_CREATE_TITLE = '新建工作台';
export const LAB_F_NAME = '名称';
export const LAB_F_NAME_REQUIRED = '请输入工作台名称';
export const LAB_F_DESC = '描述（可选）';
export const LAB_F_BATCHES = '数据来源批次';
export const LAB_F_BATCHES_HINT = '仅显示已完成批次，默认全选';
export const LAB_BATCH_SELECT_ALL = '全选';
export const LAB_BATCH_SELECT_NONE = '全不选';
export const LAB_NO_COMPLETED_BATCHES = '没有已完成的导入批次，请先在「数据导入」上传工作簿';
export const LAB_BATCH_OPTION = '{workbook} · 导入 {imported} / 共 {total} 行';

export const TOAST_WORKSPACE_CREATED =
  '工作台已创建：{name}（设备 {devices} · 接口 {interfaces} · 物理链路 {links} · 图层边 {layerLinks}）';
export const TOAST_WORKSPACE_DELETED = '工作台已删除';

export const LAB_DELETE_TITLE = '确认删除工作台';
export const LAB_DELETE_MESSAGE =
  '删除工作台「{name}」将移除其全部设备、接口、链路、变更集、检查运行与检查点，操作不可恢复。是否继续？';

// —— 拓扑工作台页（/lab/:id）通用 ——

export const LABWS_NOT_FOUND = '工作台不存在或已被删除';
export const LABWS_BACK_LIST = '返回工作台列表';
export const LABWS_HASH_LABEL = '状态哈希';
export const LABWS_CHIP_DEVICES = '设备';
export const LABWS_CHIP_INTERFACES = '接口';
// #53 附带（口径拆分）：头部/列表的「链路」此前只读 virtual_links 物化表（物理
// 链路 23），而画布实际渲染 1,140 条边（23 物理 + 1,073 图层语义边）——用户
// 看到“链路 23”会误以为与拓扑边数不符。拆分口径：物理链路 = virtual_links
//（设备间物理连接）；图层节点/图层边 = L0-L6 覆盖层只读投影（#51）。
export const LABWS_CHIP_LINKS = '物理链路';
export const LABWS_UP_TITLE = '运行中 {n}';
export const LABWS_DOWN_TITLE = '已停机 {n}';
export const LABWS_CHIP_LAYER_NODES = '图层节点';
export const LABWS_CHIP_LAYER_NODES_TITLE =
  '图层覆盖层（#51）：L0 空间锚点 / L4 接入 / L5 分组 / L6 业务等覆盖层节点（只读投影，不计入物理链路）';
export const LABWS_CHIP_LAYER_EDGES = '图层边';
export const LABWS_CHIP_LAYER_EDGES_TITLE =
  '图层覆盖层（#51）：L3 电路/波道、L4 接入上联、L5 分组归属、L6 业务关联等语义边（只读投影，不计入物理链路）';
export const LABWS_CHIP_LINKS_TITLE =
  '物理链路：virtual_links 物化表（设备间物理连接）；覆盖层语义边另计为「图层边」';

export const LAB_BTN_RUN_CHECKS = '运行校验';
export const LAB_BTN_APPLY_OPS = '执行操作';
export const LAB_BTN_UNDO = '撤销最近变更';
export const LAB_BTN_CREATE_CHECKPOINT = '创建检查点';
export const LAB_BTN_DIFF = '前后对比';
export const LAB_BTN_EXPORT_REPORT = '导出报告';

export const TOAST_CHECKS_DONE = '校验完成：错误 {error} · 警告 {warning} · 提示 {info} · 评分 {score} 分';
export const TOAST_OPS_DONE = '操作已执行：变更集 {id}（{n} 个操作）';
export const TOAST_UNDO_DONE = '已撤销变更集 {id}，撤销操作记录为新变更集 {newId}';
export const TOAST_CHECKPOINT_CREATED = '检查点已创建：{label}';
export const TOAST_RESTORE_DONE = '已恢复到检查点「{label}」（变更集 {id}）';
export const TOAST_RESTORE_NOOP = '已处于该检查点状态，未产生新变更集';

/** 成功 toast 的动作链接（W9）：跳转变更集标签页。 */
export const TOAST_VIEW_CHANGESETS = '查看变更集';
/** 点击复制成功后的轻提示（P2：批次 uuid 短码复制）。 */
export const TOAST_COPIED = '已复制到剪贴板';

export const LAB_UNDO_TITLE = '确认撤销最近变更';
export const LAB_UNDO_MESSAGE =
  '将撤销最近的已应用变更集，其反向操作会作为一个新的变更集执行。是否继续？';

// —— 右侧标签页 ——

export const LAB_TAB_DEVICES = '设备';
export const LAB_TAB_LAYERS = '图层';
export const LAB_TAB_INTERFACES = '接口';
export const LAB_TAB_LINKS = '链路';
export const LAB_TAB_CHECKS = '检查结果';
export const LAB_TAB_CHANGESETS = '变更集';
export const LAB_TAB_CHECKPOINTS = '检查点';

/** 实体表格客户端筛选（P2：40 接口列表检索）。 */
export const LAB_FILTER_DEVICES = '筛选设备（编码 / 名称）';
export const LAB_FILTER_INTERFACES = '筛选接口（设备 / 名称 / 地址）';
export const LAB_FILTER_LINKS = '筛选链路（标签 / 端点）';
export const LAB_FILTER_NO_MATCH = '没有符合筛选的条目';

// —— 画布 ——

export const LAB_CANVAS_LABEL = '拓扑画布';

/** #47：工具带末组——工作台级动作按钮分组（aria-label）。 */
export const LAB_ACTIONS_GROUP = '工作台动作';

/** #47 诉求 4：/lab 画布概览模式。 */
export const LAB_VIEW_CANVAS = '画布视图';
export const LAB_VIEW_TABLE = '列表模式';
export const LAB_VIEW_TOGGLE_LABEL = '工作台视图切换';
export const LAB_TILE_ENTER = '进入工作台';
export const LAB_TILE_PREVIEW_LOADING = '加载拓扑…';
export const LAB_TILE_PREVIEW_EMPTY = '空工作台';
export const LAB_TILE_PREVIEW_ERROR = '拓扑加载失败';
export const LAB_TILE_SCORE = '评分 {score}';
export const LAB_TILE_CREATED = '创建于 {at}';
export const LAB_LAYOUT = '布局';
export const LAB_LAYOUT_FORCE = '力导';
export const LAB_LAYOUT_HIER = '层级';
export const LAB_LAYOUT_CONCENTRIC = '同心圆';
export const LAB_LAYOUT_GRID = '网格';
export const LAB_LAYOUT_CIRCLE = '环形';
export const LAB_LAYOUT_FREEZE = '锁定';
export const LAB_MODE = '模式';
export const LAB_MODE_SELECT = '选择';
export const LAB_MODE_TRACE = '路径';
export const LAB_MODE_FOCUS = '聚焦';
export const LAB_CANVAS_LABELS = '链路标签（快捷键 L）';
export const LAB_CANVAS_HINT = '点击节点或连线，可在右侧定位对应条目';
export const LAB_CANVAS_EMPTY = '暂无拓扑数据';

/** 交互模式提示（工具带右侧 hint 随模式切换）。 */
export const LAB_MODE_TRACE_HINT = '依次点击两台设备，高亮最短路径；Esc 或点击空白清除';
export const LAB_MODE_FOCUS_HINT = '点击设备聚焦其邻域；Esc 或点击空白取消';
export const LAB_TRACE_PICK_A = '路径模式：点击起点设备';
export const LAB_TRACE_PICK_B = '已选起点 {a}，点击终点设备';
export const LAB_TRACE_DONE = '{a} → {b} · {hops} 跳 · 经 {via}';
export const LAB_TRACE_UNREACHABLE = '{a} → {b} · 不可达（无可用链路）';
export const LAB_TRACE_CLEAR = '清除路径';
export const LAB_TRACE_EXIT_MODE = '退出路径模式';
export const LAB_MINIMAP_LABEL = '缩略导航图（点击/拖动定位视图）';

/** 画布缩放 / 视图控制（W2：图标按钮 + title 提示；FX-6 标注键盘快捷键）。 */
export const LAB_CANVAS_ZOOM_IN = '放大';
export const LAB_CANVAS_ZOOM_OUT = '缩小';
export const LAB_CANVAS_FIT = '适配视图（快捷键 F）';
export const LAB_CANVAS_RESET = '重置视图（1:1 · 清除手动排布）';
export const LAB_CANVAS_ZOOM_HINT = '滚轮缩放 · 拖拽平移 · 框选';

/** FX-6：PNG 快照导出（工具带动作按钮）。 */
export const LAB_CANVAS_EXPORT = '导出 PNG 快照（完整拓扑 · 2 倍分辨率 · 深色底）';

/** FX-6：缩放百分比指示 chip。 */
export const LAB_CANVAS_ZOOM_CURRENT = '当前缩放比例';

/** FX-6：右键上下文菜单（节点 / 边 / 空白画布三套，纯自实现无插件）。 */
export const LAB_CANVAS_MENU_NODE = '设备操作菜单';
export const LAB_CANVAS_MENU_EDGE = '链路操作菜单';
export const LAB_CANVAS_MENU_CANVAS = '画布视图菜单';
export const LAB_CANVAS_CTX_FOCUS = '邻域聚焦';
export const LAB_CANVAS_CTX_FOCUS_TITLE = '聚焦该设备的一跳邻域（其余元素压暗，Esc 取消）';
export const LAB_CANVAS_CTX_TRACE = '设为路径追踪起点';
export const LAB_CANVAS_CTX_TRACE_TITLE = '切换到路径模式并以此设备为起点，再点击终点即高亮最短路径';
export const LAB_CANVAS_CTX_LOCATE = '在表格中定位';
export const LAB_CANVAS_CTX_LOCATE_TITLE = '选中该设备，右侧表格定位对应行';
export const LAB_CANVAS_CTX_COPY_ID = '复制设备 ID';
export const LAB_CANVAS_CTX_COPY_ID_TITLE = '复制设备编码（如 DEV-001）到剪贴板';
export const LAB_CANVAS_CTX_LOCATE_LINK = '在表格中定位';
export const LAB_CANVAS_CTX_LOCATE_LINK_TITLE = '选中该链路，右侧表格定位对应行';
export const LAB_CANVAS_CTX_COPY_LINK_ID = '复制链路 ID';
export const LAB_CANVAS_CTX_COPY_LINK_ID_TITLE = '复制链路标签（如 CKT-001）到剪贴板';
export const LAB_CANVAS_CTX_FIT = '适配视图';
export const LAB_CANVAS_CTX_FIT_TITLE = '缩放至完整拓扑可见（快捷键 F）';
export const LAB_CANVAS_CTX_RESET = '重置视图';
export const LAB_CANVAS_CTX_RESET_TITLE = '恢复 1:1 缩放并居中；若存在已保存的手动排布则清除并按当前布局重排';

// —— FX-5：zen 全屏工作模式 + 可拖拽分栏 ——

/** 工具带最右「全屏」按钮（进入 zen）。 */
export const LABWS_ZEN_ENTER = '全屏工作模式（Esc 退出）';
/** zen 中工具带「退出全屏」按钮的 title/aria-label。 */
export const LABWS_ZEN_EXIT = '退出全屏工作模式（Esc）';
/** 「退出全屏」按钮可见文案（zen 中带文字，明显可退）。 */
export const LABWS_ZEN_EXIT_LABEL = '退出全屏';
/** zen 左上浮动选中详情卡 aria-label。 */
export const LABWS_ZEN_INFO_LABEL = '选中详情';
/** 设备详情：接口 up/down 计数行。 */
export const LABWS_ZEN_INFO_IFACES = '接口 {total}（↑{up} · ↓{down}）';
/** 设备详情：IPv4 地址段标题。 */
export const LABWS_ZEN_INFO_IPV4 = 'IPv4 地址';
/** 设备无任何已配置 IPv4 时。 */
export const LABWS_ZEN_INFO_IPV4_NONE = '未配置 IPv4';
/** 已配置地址超出展示上限（超过 4 条折叠）。 */
export const LABWS_ZEN_INFO_IPV4_MORE = '等 {n} 个';
/** 链路详情：两端标题。 */
export const LABWS_ZEN_INFO_LINK_ENDS = '两端';

/** 分栏把手 aria-label。 */
export const LAB_SPLIT_HANDLE_LABEL = '画布与面板分栏';
/** 分栏把手 title（含当前比例与键盘微调提示）。 */
export const LAB_SPLIT_HANDLE_TITLE = '拖拽调整画布与面板比例；← → 方向键微调（当前画布 {pct}%）';
/** 分栏把手 aria-valuetext（读屏播报比例）。 */
export const LAB_SPLIT_PCT_VALUE = '画布 {pct}% / 面板 {rest}%';
/** 折叠右侧面板按钮（页头动作行）。 */
export const LAB_SPLIT_COLLAPSE = '折叠面板';
export const LAB_SPLIT_COLLAPSE_TITLE = '折叠右侧数据面板，画布占满工作区宽度';
/** 折叠后的展开按钮。 */
export const LAB_SPLIT_EXPAND = '展开面板';
export const LAB_SPLIT_EXPAND_TITLE = '展开右侧数据面板';

/** 画布图例（W2）。 */
export const LAB_CANVAS_LEGEND_LABEL = '图例';
export const LAB_LEGEND_DEVICE_UP = '运行设备';
export const LAB_LEGEND_DEVICE_DOWN = '停机设备';
export const LAB_LEGEND_IFACE_FAULT = '接口故障';
export const LAB_LEGEND_LINK_UP = '正常链路';
export const LAB_LEGEND_LINK_DOWN = '中断链路';

// —— GEO-2：地理 2D/3D 地图视图（视图维度 + 地图专属控件与状态） ——

/** 工具带视图分段（拓扑图 | 2D 地图 | 3D 地图）标签。 */
export const LAB_VIEW = '视图';
export const LAB_VIEW_TOPO = '拓扑图';
export const LAB_VIEW_GEO2D = '2D 地图';
export const LAB_VIEW_GEO3D = '3D 地图';
export const LAB_VIEW_TOPO_TITLE = '拓扑图视图（关系拓扑 · 布局 / 路径 / 聚焦 / PNG 导出）';
export const LAB_VIEW_GEO2D_TITLE = '2D 地图（俯视 · 设备按真实经纬度落位）';
export const LAB_VIEW_GEO3D_TITLE = '3D 地图（斜视 58° · 离线 DEM 地形起伏 + 柱体高度示意连接数 · 右键拖拽旋转）';

/** 地图视图：适配视图（快捷键 F，与拓扑视图一致）。 */
export const LAB_GEO_FIT = '适配视图（快捷键 F · 缩放至全部设备可见）';
/** 地图视图：指北复位（方位角归零）。 */
export const LAB_GEO_NORTH = '指北复位（方位角归零，保留当前俯仰角）';
/** 地图视图：设备标签开关（快捷键 L，镜像拓扑视图链路标签语义）。 */
export const LAB_GEO_LABELS = '设备标签（快捷键 L）';
/** 拓扑专属工具在地图视图中禁用时的说明 tooltip。 */
export const LAB_GEO_DISABLED_TITLE = '仅在拓扑图视图可用（切回拓扑图后生效）';
/** PNG 导出在地图视图中的禁用说明。 */
export const LAB_GEO_EXPORT_DISABLED = 'PNG 导出仅在拓扑图视图可用';
/** 地图视图工具带右侧 hint。 */
export const LAB_GEO_HINT = '点击地标定位右侧设备行 · 左键拖拽平移 · 右键拖拽旋转';

/** 底图瓦片连续加载失败 → 本地数据渲染模式 chip。 */
export const LAB_GEO_OFFLINE = '离线底图模式';
export const LAB_GEO_OFFLINE_TITLE = '底图瓦片加载失败，已切换为纯本地数据渲染（设备与链路不受影响）';

/** 全部设备无坐标 → 居中提示 + 返回拓扑图。 */
export const LAB_GEO_NOGEO = '当前工作区设备没有地理坐标——导入含经度/纬度（或经纬度）列的设备表后自动出现';
export const LAB_GEO_NOGEO_BACK = '返回拓扑图';

/** 部分设备缺坐标 → 右下角计数 chip。 */
export const LAB_GEO_PARTIAL = '{n} 台设备未显示（无坐标）';
export const LAB_GEO_PARTIAL_TITLE = '这些设备未携带地理坐标，仅在拓扑图视图中显示';

/** 地图地标（HTML Marker）的 aria-label：设备 {code} · {name} · {status}。 */
export const LAB_GEO_MARKER_LABEL = '设备 {code} · {name} · {status}';

/** 地图图例（3D 塔 = 连接数条目，仅 3D 视图显示）。 */
export const LAB_GEO_LEGEND_TOWER = '塔高 = 连接数';

/** 地图容器 aria-label。 */
export const LAB_GEO_MAP_LABEL = '地理地图';

// —— W16：底图多模式与图层控制（画布决定显示什么） ——

/** 图层面板（开关抽屉）aria-label 与触发按钮。 */
export const LAB_GEO_LAYERS_LABEL = '图层控制（底图模式与叠加图层）';
export const LAB_GEO_LAYERS_TOGGLE = '图层';
export const LAB_GEO_LAYERS_TOGGLE_TITLE = '选择底图模式与叠加图层（自然要素 / 交通区划 / 业务图层）';
/** 图层面板分区标题。 */
export const LAB_GEO_LAYERS_BASEMAP = '底图';
export const LAB_GEO_LAYERS_NATURAL = '自然要素';
export const LAB_GEO_LAYERS_OVERLAY = '交通与区划';
export const LAB_GEO_LAYERS_REALDATA = '真实网络数据（v0.8）';
export const LAB_GEO_OV_CABLE = '光缆干线';
export const LAB_GEO_OV_CABLE_TITLE =
  '七条干线走廊：哈大齐/哈牡/哈佳/佳牡/齐加/加漠/哈伊——OSM 公路/铁路真实几何（绥满高速·滨洲线·嫩林线·京漠线等），标注锚点链里程 · © OpenStreetMap contributors';
export const LAB_GEO_OV_BASESTATION = '无线基站';
export const LAB_GEO_OV_BASESTATION_TITLE =
  'OpenCelliD 真实小区 1,198 站（1,851 小区）：蓝=移动 红=联通 绿=电信 紫=广电，点径随小区数 · 数据 © OpenCelliD（CC BY-SA 4.0）· 点击基站跳转其资源详情（台账：状态/上联/服务客户/穿透分析）';
/** W31：基站点击跳转的资源解析失败提示（非基站台账库或网络错误时）。 */
export const LAB_GEO_BS_NOT_FOUND = '未在资源库中找到该基站（先导入基站台账）';
export const LAB_GEO_BS_RESOLVE_FAIL = '基站资源解析失败，请稍后重试';
export const LAB_GEO_LAYERS_BUSINESS = '业务图层';
/** 底图模式名（与 geoBasemap.ts BASEMAPS 一一对应）。 */
export const LAB_GEO_BM_AMAP = '高德街道';
export const LAB_GEO_BM_AMAP_TITLE = '高德街道图（中文标注 · 行政区划 · 交通路网 · 需外网 · 含商铺 POI 标注）';
export const LAB_GEO_BM_AMAP_SAT = '高德卫星';
export const LAB_GEO_BM_AMAP_SAT_TITLE = '高德卫星影像 + 路网中文标注叠加（需外网）';
export const LAB_GEO_BM_ESRI = '极简';
export const LAB_GEO_BM_ESRI_TITLE = 'ESRI 深色极简画布（低信息量，突出设备分布）';
export const LAB_GEO_BM_OFFLINE = '简洁（离线）';
export const LAB_GEO_BM_OFFLINE_TITLE =
  '离线简洁底图（内网可用：森林绿地 + 河流水系 + 地名 + 交通区划，类标准地图但无商铺标注 · © OpenStreetMap contributors）';
/** 自然要素图层名（v0.6.4/#15：任意底图可用，双坐标变体）。 */
export const LAB_GEO_OV_PLACES = '地名';
export const LAB_GEO_OV_PLACES_TITLE = '城市/区县/乡镇/村庄地名标注（黑龙江本地数据 · © OpenStreetMap contributors）';
export const LAB_GEO_OV_FOREST = '森林';
export const LAB_GEO_OV_FOREST_TITLE = '林地覆盖（大兴安岭连片林区为绿色底面 · © OpenStreetMap contributors）';
export const LAB_GEO_OV_WATER = '水系';
export const LAB_GEO_OV_WATER_TITLE =
  '河流/溪流线划 + 湖泊水库水面（树枝状河网含名称标注 · © OpenStreetMap contributors）';
export const LAB_GEO_OV_GREEN = '草地农田';
export const LAB_GEO_OV_GREEN_TITLE = '草地/湿地/灌木/农田（浅绿浅黄底面 · © OpenStreetMap contributors）';
/** 交通/区划图层名（仅 WGS84 底图可用）。 */
export const LAB_GEO_OV_ADMIN = '行政区划';
export const LAB_GEO_OV_ADMIN_TITLE = '省/市/县边界与地名（黑龙江本地数据，© OpenStreetMap contributors）';
export const LAB_GEO_OV_RAIL = '铁路';
export const LAB_GEO_OV_RAIL_TITLE = '铁路线（滨洲线等，含名称标注 · 默认关闭避免干扰主观看）';
export const LAB_GEO_OV_ROAD = '公路';
export const LAB_GEO_OV_ROAD_TITLE = '干线公路与国道编号（含大兴安岭地方道路 · 按需开启，勾选后加载 ~4.3MB）';
/** 恢复完整底图（#50：重图层默认降级为懒加载，一键找回 v0.6 观感）。 */
export const LAB_GEO_RESTORE_FULL = '恢复完整底图';
export const LAB_GEO_RESTORE_FULL_TITLE =
  '一键开启全部离线叠加与地形/等高线（自然要素 16MB + 等高线 7.3MB 按需加载，首次勾选后解析期间地图可能短暂繁忙）';
/** 地形高程开关（W17：离线 DEM，任何底图下可用）。 */
export const LAB_GEO_OV_TERRAIN = '地形高程';
export const LAB_GEO_OV_TERRAIN_TITLE =
  '离线高程数据（Mapzen · AWS Open Data，SRTM/GMTED）：3D 视图真实地形起伏，2D 视图山体阴影';
/** 等高线开关（W17/#13：离线 DEM 派生，任意底图可用）。 */
export const LAB_GEO_OV_CONTOUR = '等高线';
export const LAB_GEO_OV_CONTOUR_TITLE =
  '等高线（离线 DEM 派生：200m 首曲线 / 500m 计曲线 + 高程标注，任意底图可用）';
/** 光标海拔读数（W17/#13）。 */
export const LAB_GEO_ELEV_VALUE = '海拔 {n} m';
export const LAB_GEO_ELEV_OUT = '海拔 —（超出高程覆盖）';
export const LAB_GEO_ELEV_TITLE = '光标处实时海拔（离线 DEM 采样，覆盖黑龙江省）';
/** 聚簇气泡（W17：屏幕碰撞聚合，点击放大展开）。 */
export const LAB_GEO_CLUSTER_ARIA = '{n} 台设备聚簇（含 {down} 台故障），点击放大查看';
export const LAB_GEO_CLUSTER_TITLE = '{n} 台设备（{down} 台故障）：{codes} —— 点击放大展开';
/** GCJ 底图下叠加不可用提示（仅限交通/区划；自然要素任意底图可用）。 */
export const LAB_GEO_OV_UNAVAILABLE = '交通/区划本地叠加仅在简洁（离线）/极简底图下可用；森林/水系/地名任意底图可用';
/** 业务图层名。 */
export const LAB_GEO_BIZ_LINKS = '链路';
export const LAB_GEO_BIZ_LINKS_TITLE = '设备间链路（正常/故障/选中）';
export const LAB_GEO_BIZ_DEVICES = '设备';
export const LAB_GEO_BIZ_DEVICES_TITLE = '设备地标（状态色圆点 + 编码牌）';

// —— 设备标签页 ——

export const LAB_D_COL_CODE = '编码';
export const LAB_D_COL_NAME = '名称';
export const LAB_D_COL_STATUS = '状态';
export const LAB_D_COL_IFACES = '接口数';
export const LAB_D_COL_IPV4 = 'IPv4 数';
export const LAB_D_DOWN_TITLE = '确认停用设备';
export const LAB_D_DOWN_MESSAGE = '停机将中断所有经过该设备的连通性。确定停用设备 {code}？';
export const LAB_D_TABLE_LABEL = '设备列表';

// —— 接口标签页 ——

export const LAB_I_TABLE_LABEL = '接口列表';
export const LAB_I_COL_DEVICE = '设备';
export const LAB_I_COL_NAME = '名称';
export const LAB_I_COL_STATUS = '状态';
export const LAB_I_COL_IPV4 = 'IPv4';
export const LAB_I_COL_LINKS = '链路数';
export const LAB_I_IPV4_UNSET = '未配置';
export const LAB_I_IPV4_VALUE = '{addr}/{prefix}';
export const LAB_I_LABEL = '{device}:{name}';

export const BTN_ENABLE = '启用';
export const BTN_DISABLE = '停用';
export const BTN_CONFIG_ADDR = '配置地址';
export const BTN_DELETE = '删除';

export const LAB_I_CONFIG_TITLE = '配置接口地址';
export const LAB_I_CONFIG_DESC = '配置 {iface} IPv4 地址';
export const LAB_I_CONFIG_TARGET = '接口';
export const LAB_I_F_ADDR = 'IPv4 地址';
export const LAB_I_F_PREFIX = '前缀长度（0–32）';
export const LAB_I_ADDR_PLACEHOLDER = '例如 10.0.0.1';
export const LAB_I_PREFIX_PLACEHOLDER = '例如 24';
export const LAB_I_ERR_ADDR = 'IPv4 地址格式不正确（应为点分十进制，如 10.0.0.1）';
export const LAB_I_ERR_PREFIX = '前缀长度应为 0–32 的整数';
export const LAB_I_ERR_BOTH = '地址与前缀需同时填写';
export const BTN_CLEAR_ADDR = '清除地址';
export const LAB_I_CLEAR_HINT = '地址与前缀均留空提交即清除该接口地址';

export const LAB_I_DELETE_TITLE = '确认删除接口';
export const LAB_I_DELETE_MESSAGE = '删除接口将同时删除其上的链路。确定删除 {iface}？';

// —— 变更说明模板（写入变更集描述，展示于变更集标签页） ——

export const LAB_OP_DESC_ENABLE = '启用 {target}';
export const LAB_OP_DESC_DISABLE = '停用 {target}';
export const LAB_OP_DESC_DELETE_IFACE = '删除接口 {target}';
export const LAB_OP_DESC_DELETE_LINK = '删除链路 {target}';
export const LAB_OP_DESC_CONFIG_IPV4 = '配置 {target} IPv4 地址';

// —— 链路标签页 ——

export const LAB_L_TABLE_LABEL = '链路列表';
export const LAB_L_COL_LABEL = '标签';
export const LAB_L_COL_A = 'A 端';
export const LAB_L_COL_Z = 'Z 端';
export const LAB_L_COL_STATUS = '状态';
export const LAB_L_ENDPOINT = '{device}:{iface}';
export const LAB_L_UNNAMED = '未命名';
export const LAB_L_DELETE_TITLE = '确认删除链路';
export const LAB_L_DELETE_MESSAGE = '确定删除链路 {label}（{a} ↔ {z}）？删除后可通过撤销恢复。';

// —— 执行操作对话框 ——

export const LAB_OPS_TITLE = '执行操作';
export const LAB_OPS_TYPE = '操作类型';
export const LAB_OPS_F_DEVICE = '设备';
export const LAB_OPS_F_INTERFACE = '接口';
export const LAB_OPS_F_LINK = '链路';
export const LAB_OPS_F_STATUS = '目标状态';
export const LAB_OPS_F_CODE = '设备编码';
export const LAB_OPS_F_NAME = '设备名称';
export const LAB_OPS_F_IF_NAME = '接口名称（如 eth0）';
export const LAB_OPS_F_A_IFACE = 'A 端接口';
export const LAB_OPS_F_Z_IFACE = 'Z 端接口';
export const LAB_OPS_F_LINK_LABEL = '链路标签（可选）';
export const LAB_OPS_F_INIT_STATUS = '初始状态（默认运行中）';
export const LAB_OPS_F_DESC = '变更说明（可选）';
export const LAB_OPS_RUN_CHECKS = '执行后自动运行校验';
export const LAB_OPS_ERR_REQUIRED = '请完整填写必填项';
export const LAB_OPS_ERR_SAME_IFACE = '新建链路的两个端点必须是不同设备上的接口';

export const LAB_OPS_RAW_TOGGLE = 'JSON 模式';
export const LAB_OPS_RAW_PLACEHOLDER =
  '[{"op":"set_link_status","linkId":"ln-1","status":"down"}]';
export const LAB_OPS_RAW_PARSE_ERROR = 'JSON 解析失败：{message}';
export const LAB_OPS_RAW_NOT_ARRAY = 'JSON 顶层必须是操作数组或 {ops:[…]} 对象';
export const LAB_OPS_RAW_NO_OPS = '操作数组不能为空';
export const LAB_OPS_RAW_HINT = '高级模式：直接提交操作 JSON（数组或 {ops,description,runChecks} 对象）';

export const OP_LABEL_CREATE_DEVICE = '新建设备';
export const OP_LABEL_SET_DEVICE_STATUS = '启停设备';
export const OP_LABEL_DELETE_DEVICE = '删除设备';
export const OP_LABEL_CREATE_INTERFACE = '新建接口';
export const OP_LABEL_DELETE_INTERFACE = '删除接口';
export const OP_LABEL_SET_INTERFACE_STATUS = '接口启停';
export const OP_LABEL_CONFIG_IPV4 = '配置接口地址';
export const OP_LABEL_CLEAR_IPV4 = '清除接口地址';
export const OP_LABEL_CREATE_LINK = '新建链路';
export const OP_LABEL_DELETE_LINK = '删除链路';
export const OP_LABEL_SET_LINK_STATUS = '链路启停';

// —— 检查结果标签页 ——

export const LAB_C_TABLE_LABEL = '检查结果列表';
export const LAB_C_COL_SEV = '级别';
export const LAB_C_COL_CHECK = '检查项';
export const LAB_C_COL_MESSAGE = '消息';
export const LAB_C_COL_ENTITY = '对象';
export const LAB_C_TRIGGER = '触发方式';
export const LAB_C_STARTED_AT = '开始时间';
export const LAB_C_HISTORY = '历史运行';
export const LAB_C_RUN_OPTION = '{runId} · {time} · {score} 分';
/** 下拉取数命中条数上限时的提示（AUD2-15）。 */
export const LAB_CAPPED_HINT = '仅显示最近 {n} 条';
export const LAB_C_ONLY_FINDINGS = '仅显示问题';
export const LAB_C_NO_RUNS = '尚未运行过校验，点击「运行校验」生成第一份结果';
export const LAB_C_NO_FINDINGS = '该次运行没有发现问题';
export const LAB_C_PASSED = '通过';
export const LAB_C_EMPTY_DETAIL = '无详情';
export const LAB_C_DETAIL = '详情';

// —— 前后对比对话框 ——

export const LAB_DIFF_TITLE = '前后对比';
export const LAB_DIFF_BTN_RUN = '生成对比';
export const LAB_DIFF_FROM = '基准运行（前）';
export const LAB_DIFF_TO = '对比运行（后）';
export const LAB_DIFF_RUN_OPTION = '{runId} · {time} · {score} 分';
export const LAB_DIFF_SCORE = '评分变化';
export const LAB_DIFF_ADDED = '新增问题';
export const LAB_DIFF_REMOVED = '消失问题';
export const LAB_DIFF_CONN_LOST = '连通性丢失';
export const LAB_DIFF_CONN_GAINED = '连通性恢复';
export const LAB_DIFF_PAIR = '{from} ↔ {to}';
export const LAB_DIFF_EMPTY_ADDED = '无新增问题';
export const LAB_DIFF_EMPTY_REMOVED = '无消失问题';
export const LAB_DIFF_EMPTY_LOST = '无连通性丢失';
export const LAB_DIFF_EMPTY_GAINED = '无连通性恢复';
export const LAB_DIFF_NOT_ENOUGH = '运行历史不足两次，请先运行校验';
export const LAB_DIFF_SAME_RUN = '请选择两个不同的运行';
export const LAB_DIFF_COL_CHECK = '检查项';
export const LAB_DIFF_COL_SEV = '级别';
export const LAB_DIFF_COL_MESSAGE = '消息';
export const LAB_DIFF_ERR_REQUIRED = '请选择两个不同的校验运行';

/** 连通性对列表默认收起（W8）：「共 N 对，点击展开」/「收起列表」。 */
export const LAB_DIFF_PAIRS_EXPAND = '共 {n} 对，点击展开';
export const LAB_DIFF_PAIRS_COLLAPSE = '收起列表';

// —— 变更集标签页 ——

export const LAB_CS_TABLE_LABEL = '变更集列表';
export const LAB_CS_COL_ID = 'ID';
export const LAB_CS_COL_DESC = '描述';
export const LAB_CS_COL_TRIGGER = '触发';
export const LAB_CS_COL_STATUS = '状态';
export const LAB_CS_COL_OPS = '操作数';
export const LAB_CS_COL_CREATED = '创建时间';
export const LAB_CS_COL_REVERTED = '撤销时间';
export const LAB_CS_BTN_UNDO = '撤销';
export const LAB_CS_ONLY_LATEST_HINT = '仅可撤销最新已应用变更集';
export const LAB_CS_EMPTY = '暂无变更集';
export const LAB_CS_UNDO_TITLE = '确认撤销变更集';
export const LAB_CS_UNDO_MESSAGE =
  '将撤销变更集 {id} 的全部操作，其反向操作作为新变更集执行。是否继续？';

// —— 检查点标签页 ——

export const LAB_CP_TABLE_LABEL = '检查点列表';
export const LAB_CP_LABEL = '标签';
export const LAB_CP_LABEL_PLACEHOLDER = '例如：故障注入前基线';
export const LAB_CP_COL_ID = 'ID';
export const LAB_CP_COL_LABEL = '标签';
export const LAB_CP_COL_HASH = '状态哈希';
export const LAB_CP_COL_CREATED = '创建时间';
export const LAB_CP_BTN_RESTORE = '恢复';
export const LAB_CP_EMPTY = '暂无检查点';
export const LAB_CP_ERR_LABEL = '请输入检查点标签';
export const LAB_CP_RESTORE_TITLE = '确认恢复检查点';
export const LAB_CP_RESTORE_MESSAGE =
  '恢复将把工作台状态回退到该检查点（作为新变更集执行）。确定恢复「{label}」？';

// ---------------------------------------------------------------------------
// 检测运行历史与定时巡检（v0.11-R11：/alerts 巡检状态条 + 运行历史对话框）
// ---------------------------------------------------------------------------

/** 运行来源徽章文案（闭集）。 */
export const RUN_SOURCE_LABELS: Record<'api' | 'cli' | 'scheduler', string> = {
  api: '手动触发',
  cli: '命令行',
  scheduler: '定时调度',
};

export const PATROL_LAST_LABEL = '上次巡检';
export const PATROL_LAST_NONE = '尚未巡检';
export const PATROL_LAST_LINE =
  '{source} · {time} · 耗时 {duration} · 新增 {created} / 清除 {cleared} / 抑制 {suppressed} / 抖动 {flapHeld}';
export const PATROL_LAST_FAILED = '失败';
export const PATROL_NEXT_LABEL = '下次自动巡检';
export const PATROL_NEXT_LINE = '{time}（每 {interval} 一轮）';
export const PATROL_OFF = '未启用定时巡检（仅手动触发）';
export const PATROL_BTN_HISTORY = '运行历史';
export const PATROL_RUNS_TITLE = '检测运行历史';
export const PATROL_RUNS_HINT =
  '每次检测扫描（手动 / 命令行 / 定时调度）都会落库一条回执：来源、窗口、计数、耗时与失败原因。';
export const PATROL_COL_SOURCE = '来源';
export const PATROL_COL_STARTED = '开始时间';
export const PATROL_COL_DURATION = '耗时';
export const PATROL_COL_COUNTS = '发现 / 新增 / 清除 / 抑制 / 抖动';
export const PATROL_COL_STATUS = '状态';
export const PATROL_COL_WINDOW = '评估窗口';
export const PATROL_COL_DATA_TS = '数据截止';
export const PATROL_COL_WEBHOOK = '通知';
export const WEBHOOK_DELIVERED_LABEL = '已送达';
export const WEBHOOK_FAILED_LABEL = '投递失败';
export const WEBHOOK_OFF_LABEL = '—';
export const LIFECYCLE_EVENT_ACK_LABEL = '确认';
export const LIFECYCLE_EVENT_CLEAR_LABEL = '清除';
export const WEBHOOK_DELIVERED_TITLE = '已送达';
export const WEBHOOK_FAILED_ATTEMPTS_TITLE = '投递失败 · 尝试 {n} 次 · {error}';
export const PATROL_STATUS_OK = '成功';
export const PATROL_STATUS_FAILED = '失败';
export const PATROL_WINDOW_ALL = '全库';
export const PATROL_WINDOW_RANGE = '{from} ~ {to}';
export const PATROL_DURATION_TEXT = '{n} ms';
export const PATROL_RUNS_EMPTY = '暂无检测运行记录';
export const PATROL_RUNS_LOAD_FAILED = '运行历史加载失败';

/** 巡检条 CSS：一行的信息密度（只用于 /alerts 顶部条）。 */
export const PATROL_INTERVAL_SECONDS_TEXT = '{n} 秒';
export const PATROL_INTERVAL_MINUTES_TEXT = '{n} 分钟';
export const PATROL_INTERVAL_HOURS_TEXT = '{n} 小时';
export const PATROL_INTERVAL_COMPOUND_TEXT = '{n} 秒';

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

/** 相对时间文案（P2：<24h 显示「x 分钟前」，完整时间放 title）。 */
export const TIME_JUST_NOW = '刚刚';
export const TIME_MINUTES_AGO = '{n} 分钟前';
export const TIME_HOURS_AGO = '{n} 小时前';
export const TIME_DAYS_AGO = '{n} 天前';

/**
 * 相对时间：小于 24h → 「x 分钟/小时前」，7 天内 → 「x 天前」，
 * 更早回退绝对时间。配合 title=formatDateTime(iso) 使用。
 */
export function formatRelativeTime(iso?: string | null): string {
  if (!iso) return NO_VALUE;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const diffMs = Date.now() - date.getTime();
  if (diffMs < 60_000) return TIME_JUST_NOW;
  const minutes = Math.floor(diffMs / 60_000);
  if (minutes < 60) return format(TIME_MINUTES_AGO, { n: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return format(TIME_HOURS_AGO, { n: hours });
  const days = Math.floor(hours / 24);
  if (days <= 7) return format(TIME_DAYS_AGO, { n: days });
  return formatDateTime(iso);
}

// ---------------------------------------------------------------------------
// 拓扑画布图层覆盖层（#51：七层平台呈现 + 影响分析高亮 + 簇下钻）
// ---------------------------------------------------------------------------

/** 图层开关行 aria 标签。 */
export const LAB_LAYERS_GROUP = '图层显示（#51 七层平台）';

/** 各层开关文案（L1/L2 是演练实体本体，L0 空间锚点默认关）。 */
export const LAYER_TOGGLE_LABELS: Record<string, string> = {
  L0: 'L0 空间锚点',
  L1: 'L1 设备',
  L2: 'L2 光缆',
  L3: 'L3 传输承载',
  L4: 'L4 接入层',
  L5: 'L5 逻辑分组',
  L6: 'L6 业务层',
};

/** 图层无数据时开关的禁用提示。 */
export const LAYER_TOGGLE_EMPTY = '该层在本工作台无数据';

/** 聚簇节点显示名（×N 为成员数）。 */
export const LAYER_CLUSTER_LABEL = '接入 ×{n}';

/** L4 层开关打开时聚簇摘要（tooltip/面板）。 */
export const LAYER_CLUSTER_SUMMARY = '{clusters} 个接入簇 · 共 {stations} 个基站（聚合显示，点击下钻）';

/** 大图布局保护：可见节点超阈值自动改网格布局的提示。 */
export const LAYER_LAYOUT_GUARD = '可见节点 {n} 超过 {max}，已自动切换网格布局（性能保护）';

/** 影响分析：右键菜单项。 */
export const LAB_CANVAS_CTX_IMPACT = '影响分析';
export const LAB_CANVAS_CTX_IMPACT_TITLE = '从本资源向业务层收集影响面（#51 故障故事）';
export const LAB_CANVAS_CTX_IMPACT_ROOT = '向下找根因';
export const LAB_CANVAS_CTX_IMPACT_ROOT_TITLE = '从表象向异常根因追查（§10.5③）';

/** 影响分析进行中/失败提示。 */
export const LAB_IMPACT_LOADING = '正在收集影响面…';
export const LAB_IMPACT_EMPTY = '影响面为空（该资源向上未触达业务层，或均为已停用节点）';
export const LAB_IMPACT_ERROR = '影响分析失败';

/** 影响高亮面板标题与摘要。 */
export const LAB_IMPACT_PANEL_TITLE = '影响分析结果';
export const LAB_IMPACT_PANEL_ROOT = '分析起点';
export const LAB_IMPACT_PANEL_IMPACTED = '受影响面（向上 {n}）';
export const LAB_IMPACT_PANEL_CANDIDATES = '根因候选（{n}）';
export const LAB_IMPACT_PANEL_CLEAR = '清除高亮（Esc）';
export const LAB_IMPACT_DIMMED_HINT = '未受影响元素已压暗；受影响节点/边为琥珀色';

/** 图层节点 hover/详情文案。 */
export const LAYER_KIND_LABELS: Record<string, string> = {
  cluster: '接入簇',
  base_station: '基站',
  olt: 'OLT',
  onu: 'ONU',
  bbu: 'BBU',
  rru: 'RRU',
  ring: '环网',
  igp_domain: 'IGP 域',
  vpn_instance: 'VPN 实例',
  protection_group: '保护组',
  ip_pool: '地址池',
  customer: '客户',
  service: '业务',
  leased_line: '专线',
  manhole: '人井',
  pole: '杆路',
};

/** 边语义图例（四色语义：物理实线灰蓝 / 逻辑虚线琥珀 / 业务点线绿 / 上联细灰）。 */
export const LAYER_SEM_LABELS: Record<string, string> = {
  physical: '物理链路（光缆走廊）',
  logical: '逻辑承载（电路/波道）',
  service: '业务关联（客户/业务）',
  access: '接入上联',
  group: '分组归属',
};

/** 簇下钻面板。 */
export const LAYER_MEMBERS_TITLE = '接入簇成员（{total}）';
export const LAYER_MEMBERS_CODE = '基站编码';
export const LAYER_MEMBERS_NAME = '名称';
export const LAYER_MEMBERS_STATUS = '状态';
export const LAYER_MEMBERS_LOAD_ERR = '成员加载失败';
export const LAYER_PON_PORTS = 'PON 口 {n} 个';
export const LAYER_STATION_SERVES = '服务基站 {n} 个';

/** 图层选中卡（zen 模式之外，画布下方/侧栏的简要信息）。 */
export const LAYER_NODE_INFO = '{kind} · {code}';
