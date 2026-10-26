/**
 * NetOps Lab 前端文案（i18n）。
 *
 * 约定：所有面向用户的中文文案一律以命名常量形式集中在本文件，
 * 页面与组件只引用常量，禁止内联中文字符串（标点、数字除外）。
 * 含变量的文案使用 {占位符} 模板 + format() 填充。
 */

import type {
  BatchStatus,
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
/** 本地回退版本号：侧栏优先展示 /api/health 上报的 domain.AppVersion（事实源），
 *  请求失败时才回退到本常量（R1-D1 根治前端手抄漂移）。 */
export const APP_VERSION = 'v0.15.2';

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
export const IMP_DROPZONE_DESC = '支持 .xlsx 工作簿与 PTP 配置脚本（.txt/.cfg），单个文件不超过 10MB';
export const IMP_DROPZONE_RETRY = '重试上传';
export const IMP_SELECTED_FILE = '已选择文件：{name}（{size}）';
export const IMP_ROW_SOURCE = '{sheet} · 行 {row}';
export const IMP_UPLOADING = '正在上传并解析…';
export const IMP_ERR_FILE_TYPE = '请选择 .xlsx 工作簿或 .txt/.cfg PTP 配置脚本文件';
export const IMP_ERR_TOO_LARGE = '文件大小超过 10MB 上限';
export const IMP_UPLOADED_INFO = '已上传：{workbook} · {sheets} 个工作表 · 批次 {batchId}';
export const IMP_ERR_RETRY_HINT = '上传失败，可更换文件后重试';

export const IMP_TOGGLE_IMPORT = '启用导入';
export const IMP_TOGGLE_SKIP = '跳过该工作表';
export const IMP_RESOURCE_TYPE = '资源类型';
export const IMP_COL_SOURCE = '源表头';
export const IMP_COL_SUGGEST = '建议';
export const IMP_COL_TARGET = '目标字段';
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

export const RES_COL_KEY = '编码';
export const RES_COL_NAME = '名称';
export const RES_COL_TYPE = '类型';
export const RES_COL_STATUS = '状态';
export const RES_COL_UPDATED = '更新时间';
export const RES_DRAWER_TAB_ATTRS = '属性信息';
export const RES_DRAWER_TAB_SOURCE = '来源信息';

export const RES_NO_SOURCE = '暂无来源记录';
export const RES_SOURCE_ITEM = '{workbook} · {sheet} · 行 {row}';

export const RES_ATTR_FIRST_BATCH = '首次导入批次';
export const RES_ATTR_UPDATED_AT = '更新时间';
export const RES_EMPTY_TITLE = '暂无资源';
export const RES_EMPTY_DESC = '还没有任何资源数据，导入工作簿后即可在此浏览与检索。';
export const RES_TABLE_LABEL = '资源列表';

// ---------------------------------------------------------------------------
// 资源抽屉 · 待确认属性变更面板（W4-D）
// ---------------------------------------------------------------------------

// W13-D：引用字段的行内轻提示（relationRetargeted 预览为真时渲染）。

// W13-D：apply 响应 relationRetargeted=true 时使用（派生关系已随确认改挂）。

// ---------------------------------------------------------------------------
// 资源抽屉 · 疑似改名面板（W6-D）
// ---------------------------------------------------------------------------

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
// API 参考页（v0.10-R9，/dev，GET /api/openapi.json 的目录视图）
// ---------------------------------------------------------------------------

export const NAV_API = 'API 参考';
export const NAV_FEES = '费用分析';

// 费用分析页（#58 阶段 5）
export const FEES_TITLE = '费用分析';
export const FEES_DESC = '费用数据的风险分析入口：导入费用文件（CSV/Excel）→ 分析层 Parquet 分区 → 风险十场景查询（九内置 + 可配置规则）。线索是待核实差异而非认定结论（账单存在 ≠ 重复付款认定），每条线索携带行级证据锚点。';
export const FEES_IMPORT_TITLE = '导入费用文件';
export const FEES_IMPORT_TYPE = '费用类型';
export const FEES_IMPORT_YEAR = '年份';
export const FEES_IMPORT_MONTH = '月份';
export const FEES_IMPORT_FILE = '文件（.csv / .xlsx）';
export const FEES_IMPORT_FILE_REQUIRED = '请先选择要探测/导入的文件';
export const FEES_IMPORT_SUBMIT = '导入';
export const FEES_IMPORT_OK = (a: string, b: number) => `导入完成：${b} 行入库（批次 ${a}）`;
export const FEES_IMPORT_DUP = (a: string) => `同一文件已于批次 ${a} 完成导入（幂等跳过，零新数据）`;
// #125 多 sheet 台账导入：模式开关 + 各模式文案
export const FEES_IMPORT_MODE = '导入模式';
export const FEES_IMPORT_MODE_SINGLE = '单类型（CSV / 单 sheet）';
export const FEES_IMPORT_MODE_SHEETS = '多 sheet 台账（每 sheet 自动判型）';
export const FEES_IMPORT_SHEETS_OK = (n: number, detail: string) => `整本导入完成：${n} 个批次（${detail}）`;
export const FEES_IMPORT_SHEETS_DUP = '该台账已于既有批次完成导入（逐 sheet 幂等跳过，零新数据）';
/** #111-D：Fees 页原硬编码中文收编（表头/确认文案，~20 处）。 */
export const FEES_IMPORT_PERIOD_INVALID = '账期必须是 2000–2100 年的 1–12 月';
export const FEES_RISKS_YEAR_INVALID = '年份必须是 2000–2100';
export const FEES_RISKS_MONTH_ALL = '整年';
export const FEES_RISKS_CLUE_COUNT = (a: number) => `${a} 条线索`;
export const FEES_RISKS_COL_SITE = '站址';
export const FEES_RISKS_COL_PERIOD = '账期';
export const FEES_RISKS_COL_METRIC = '触发指标';
export const FEES_RISKS_COL_THRESHOLD = '阈值';
export const FEES_RISKS_COL_EVIDENCE = '证据';
export const FEES_RISKS_PERIOD_ALL_YEAR = (a: number) => `${a} 全年`;
export const FEES_RISKS_EVIDENCE_ROWS = (a: number) => `${a} 行`;
export const FEES_RISKS_EVIDENCE_BATCH = (a: string) => `批次 ${a}`;
export const FEES_LINKS_SIMILARITY = (a: string) => `相似度 ${a}%`;
export const FEES_BATCHES_EMPTY = '暂无费用批次——先在上方导入费用文件。';
export const FEES_BATCHES_COL_FILE = '文件';
export const FEES_BATCHES_COL_TYPE = '类型';
export const FEES_BATCHES_COL_PERIOD = '账期';
export const FEES_BATCHES_COL_ROWS = '行数';
export const FEES_BATCHES_COL_STATUS = '状态';
export const FEES_BATCHES_FAILED_ROWS = (a: number) => `失败 ${a}`;
export const FEES_BATCHES_ROLLBACK_CONFIRM = (a: string, b: string) =>
  `回滚批次 ${a}（${b}）？分析行将删除，快照证据保留，同文件可再导入。`;
export const FEES_BATCHES_ROLLBACK_BUSY = '回滚中…';
export const FEES_RISKS_TITLE = '风险十场景';
export const FEES_RISKS_SCENARIO = '场景';
export const FEES_RISKS_RUN = '查询';
export const FEES_RISKS_LIMIT = '线索上限';
// #89: per-scenario threshold parameters (the HTTP surface has accepted
// these since #58; the web is the human channel for exactly this tuning).
export const FEES_RISKS_STATUS = '资源状态集';
export const FEES_RISKS_STATUS_HINT = '如 停用,退用';
export const FEES_RISKS_DIFF_CENTS = '差异下限(分)';
export const FEES_RISKS_RATIO = '环比阈值';
export const FEES_RISKS_Z = 'z 阈值';
export const FEES_RISKS_MIN_SITES = '最少样本';
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
export const RISK_SCENARIO_OUTLIER = '同类站月度金额离群';
export const RISK_SCENARIO_UNLINKED = '零资源计费';
export const RISK_SCENARIO_IDLE_BILLING = '有费无量';
export const RISK_SCENARIO_MISSING_BILLING = '有量无费';
export const RISK_SCENARIO_LEDGER_CONSISTENCY = '台账内部一致性';
export const RISK_SCENARIO_CUSTOM_RULES = '可配置规则';
export const FEES_RULES_TITLE = '可配置风险规则（#2：L1 六元组）';
export const FEES_RULES_DESC = '规则即数据——新判断不再需要发版。创建默认停用（观察态：不点火直到人类启用）；改阈值 = 停用旧规则 + 建新规则，全程审计可追溯。启用/停用需要管理级令牌。';
export const FEES_RULES_COL_NAME = '规则名';
export const FEES_RULES_COL_TYPE = '费用类型';
export const FEES_RULES_COL_TUPLE = '口径（度量/聚合/维度/算子/阈值/窗口）';
export const FEES_RULES_COL_SEVERITY = '严重度';
export const FEES_RULES_COL_STATE = '状态';
export const FEES_RULES_COL_ACTIONS = '操作';
export const FEES_RULES_STATE_ENABLED = '已启用';
export const FEES_RULES_STATE_DISABLED = '停用（观察态）';
export const FEES_RULES_ENABLE = '启用';
export const FEES_RULES_DISABLE = '停用';
export const FEES_RULES_AUDIT = '台账';
export const FEES_RULES_AUDIT_TITLE = (a: string) => `规则动作台账（${a}）`;
export const FEES_RULES_AUDIT_EMPTY = '暂无动作记录';
export const FEES_RULES_AUDIT_CLOSE = '关闭';
export const FEES_RULES_AUDIT_AT = '时间';
export const FEES_RULES_AUDIT_ACTOR = '操作者（令牌层级）';
export const FEES_RULES_AUDIT_ACTION = '动作';
export const FEES_RULES_AUDIT_CHANGES = '变更';
export const FEES_RULES_CREATE_TITLE = '创建规则';
export const FEES_RULES_CREATE_NAME = '规则名（唯一）';
export const FEES_RULES_CREATE_NAME_PH = '如：电费单月超阈值';
export const FEES_RULES_CREATE_METRIC = '度量';
export const FEES_RULES_CREATE_AGG = '聚合';
export const FEES_RULES_CREATE_COMPARATOR = '算子';
export const FEES_RULES_CREATE_THRESHOLD = '阈值';
export const FEES_RULES_CREATE_THRESHOLD_HINT = '金额类=分、行数类=行数、环比类=涨幅比（0.30 表示 +30%）';
export const FEES_RULES_CREATE_WINDOW = '基期回看月数（仅环比）';
export const FEES_RULES_CREATE_SEVERITY = '严重度';
export const FEES_RULES_CREATE_DESC = '口径描述（可选）';
export const FEES_RULES_CREATE_DESC_PH = '如：单站单月电费合计超过 5000 元（500000 分）';
export const FEES_RULES_CREATE_SUBMIT = '创建（停用观察态）';
export const FEES_RULES_CREATED = (a: string) => `规则 ${a} 已创建——默认停用观察态，启用后参与 custom-rules 场景分析`;
export const FEES_RULES_ENABLED_OK = (a: string) => `规则 ${a} 已启用——custom-rules 场景的下一次运行将执行它`;
export const FEES_RULES_DISABLED_OK = (a: string) => `规则 ${a} 已停用——不再参与分析（已在案线索的裁定不受影响）`;
export const FEES_RULES_CONFIRM_ENABLE = (a: string) => `启用规则「${a}」？启用后 custom-rules 场景将逐条执行该规则并产出风险线索。`;
export const FEES_RULES_CONFIRM_DISABLE = (a: string) => `停用规则「${a}」？改阈值的正确姿势：停用旧规则 → 建新规则 → 启用新规则（三步各自留痕）。`;
export const FEES_RULES_EMPTY = '暂无规则——创建第一条：选度量/聚合/算子，填阈值，创建后启用。';
export const FEES_RULES_ENABLE_403 = '启用/停用是管理级操作——请在顶栏配置管理令牌（--admin-token）';
export const FEES_RULES_TOGGLING = '执行中…';
export const FEES_PROBE_TITLE = '导入前账期探测（#15：内容识别，只读不落库）';
export const FEES_PROBE_SUBMIT = '探测所选文件';
export const FEES_PROBE_PROBING = '探测中…';
export const FEES_PROBE_KIND = '表型';
export const FEES_PROBE_PERIOD = '识别账期';
export const FEES_PROBE_CONFIDENCE = '依据强度';
export const FEES_PROBE_BASIS = '识别依据';
export const FEES_PROBE_CONF_STRONG = '强（专用账期字段）';
export const FEES_PROBE_CONF_WEAK = '弱（列名携月）';
export const FEES_PROBE_CONF_NONE = '无证据（人工指定）';
export const FEES_PROBE_MATCH = (y: string) => `识别账期 ${y} 与申报一致——可放心导入`;
export const FEES_PROBE_MISMATCH = (p: string, d: string) => `识别账期 ${p} 与申报 ${d} 不一致——导入将默认拒绝；确认以申报为准请勾选「采用申报账期」并留痕导入`;
export const FEES_PROBE_UNKNOWN = '表型未识别（诚实降级）——申报账期将按人工指定治理（manual）';
export const FEES_PROBE_TRUST = '采用申报账期（periodTrustDeclared：不一致时以申报为准，unverified 留痕导入）';
export const FEES_RISKS_DISMISSED_COUNT = (n: number) => `已人工排除 ${n} 条（默认隐藏，重跑不再出现）`;
export const FEES_RISKS_SHOW_DISMISSED = '显示已排除线索（审计视图）';
export const FEES_RISKS_DECISION_VERIFIED = '已核实属实';
export const FEES_RISKS_DECISION_DISMISSED = '已排除';
export const FEES_RISKS_DECIDE_VERIFY = '标记属实';
export const FEES_RISKS_DECIDE_DISMISS = '排除线索';
export const FEES_RISKS_DECIDE_HINT = '人工核实通道：裁定落库后重跑生效；已排除的线索默认隐藏并在结果行计数。';
export const FEES_QUERY_TITLE = '查询预设（汇总 / 重复候选）';
export const FEES_QUERY_DESC = '站址账期合计与跨批次重复结算候选——候选供人工对账核对，账单存在 ≠ 重复付款认定。';
export const FEES_QUERY_PRESET = '预设';
export const FEES_QUERY_PRESET_TOTALS = '站址账期汇总';
export const FEES_QUERY_PRESET_DUPLICATES = '跨批次重复候选';
export const FEES_QUERY_RUN = '运行查询';
export const FEES_QUERY_COL_SITE = '站址编码';
export const FEES_QUERY_COL_ROWS = '行数';
export const FEES_QUERY_COL_TOTAL = '合计金额';
export const FEES_QUERY_COL_PERIOD = '账期';
export const FEES_QUERY_COL_BATCHES = '批次数';
export const FEES_QUERY_EMPTY = '该口径下无记录';
export const FEES_QUERY_COUNT = (n: number) => `共 ${n} 条`;
export const API_REF_HINT =
  '本页目录由 /api/openapi.json 实时渲染（后端路由表的投影，与实现零漂移）。AI 集成方直接读取该 JSON 端点即可发现全部接口。';
export const API_REF_ENDPOINTS = '个端点';
export const API_REF_TAG_GROUPS = '接口分组';
export const API_REF_EMPTY = '暂无接口文档（/api/openapi.json 不可达）';
export const API_TAG_LABELS: Record<string, string> = {
  system: '系统',
  imports: '导入与批次',
  resources: '资源',
  metrics: '指标时序',
  governance: '治理与校验',
  fees: '费用',
};

// ---------------------------------------------------------------------------
// 资源详情 · 性能指标页签（v0.9-R2）
// ---------------------------------------------------------------------------

export const RES_DRAWER_TAB_METRICS = '性能指标';
export const METRIC_EMPTY = '该资源没有指标序列（检测告警与劣化曲线在此展示）';
export const METRIC_LOAD_FAILED = '指标加载失败';
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
export const METRICS_EXPORT_WORKING = '导出中…';
export const METRICS_EXPORT_FILENAME = 'netops-metrics';

// ---------------------------------------------------------------------------
// 资源详情 · 告警页签（v0.11-R12：故障定位动线闭环——资源视角的告警史）
// ---------------------------------------------------------------------------

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
// 质量报告页
// ---------------------------------------------------------------------------

export const RPT_SCOPE = '报告范围';
export const RPT_SCOPE_ALL = '全部批次';
// issue #57②：来源中立的免责声明——数据库里可能是用户的真实台账、
// 真实公开数据或合成演示数据，页面无法区分，因此不再断言「合成测试数据」。
export const RPT_DISCLAIMER = '本报告基于当前已导入的数据生成，请以实际网络情况为准。';
export const RPT_PREVIEW_TITLE = '报告预览';
export const RPT_LOADING = '正在生成报告…';
export const RPT_FILENAME_HTML = 'netops-quality-report';
export const RPT_FILENAME_JSON = 'netops-quality-report';
export const RPT_FILE_EXT_HTML = '.html';
export const RPT_FILE_EXT_JSON = '.json';

export const TOAST_COPIED = '已复制到剪贴板';

export const LAB_CAPPED_HINT = '仅显示最近 {n} 条';

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

/** 各层开关文案（L1/L2 是演练实体本体，L0 空间锚点默认关）。 */

/** 图层无数据时开关的禁用提示。 */

/** 聚簇节点显示名（×N 为成员数）。 */

/** 大图布局保护：可见节点超阈值自动改网格布局的提示。 */

/** 影响分析：右键菜单项。 */

/** 影响分析进行中/失败提示。 */

/** 影响高亮面板标题与摘要。 */

/** 图层节点 hover/详情文案。 */

/** 边语义图例（四色语义：物理实线灰蓝 / 逻辑虚线琥珀 / 业务点线绿 / 上联细灰）。 */

/** 簇下钻面板。 */

/** LayerInfoTab 信息面板（#113-A 收编：旧工作台空态 + 点选引导空态）。 */

/** LayerInfoTab 节点详情 dt 标签。 */

/** LayerInfoTab 边详情 dt 标签。 */

/** LayerInfoTab 只读说明（覆盖层边不进操作对话框）。 */

/** 画布右键菜单回退：kind/sem 未命中标签表时的兜底标签（#113-A 收编）。 */

