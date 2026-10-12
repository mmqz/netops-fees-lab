/**
 * geoBasemap —— 底图多模式注册表 + 离线叠加图层 + GCJ-02 坐标转换（W16）。
 *
 * issue #11：底图从硬编码单一 ESRI 极简改为四种可切换模式：
 * - amap        高德街道图（栅格，中文标注/行政区划/交通，GCJ-02 坐标系）
 * - amap-satellite 高德卫星影像（style=6 + style=8 路网标注透明叠加）
 * - esri        ESRI World_Dark_Gray 极简（WGS84，原 v0.6.0 底图，保留）
 * - offline     离线极简（无外网瓦片：画布底色 + 黑龙江本地 GeoJSON 叠加，
 *               数据源 © OpenStreetMap contributors，Release mapdata-hlj）
 *
 * 离线叠加图层（行政区划/铁路/公路）只在 WGS84 底图（esri|offline）下可用：
 * 高德为 GCJ-02，叠加 WGS84 数据会偏移 ~500m，且其栅格已含同类信息。
 *
 * 坐标系契约：业务数据（设备/链路）按 WGS84 存储；高德底图渲染前逐点
 * WGS84→GCJ-02（wgs84ToGcj02，国界外原样返回）。fitBounds/地标/塔体全走
 * 同一投影入口 projLngLat，避免「设备漂移到河里」。
 *
 * 字体：离线图层的中文标注依赖 style.glyphs（OpenFreeMap Noto Sans，免密钥
 * CJK 覆盖）；完全离线时标注静默降级为无文字（线面几何不受影响）。
 */

import type { LayerSpecification, ExpressionSpecification } from '@maplibre/maplibre-gl-style-spec';
import type { StyleSpecification } from 'maplibre-gl';

// —— 高德（AMap）——
// 栅格瓦片端点免鉴权；key 按维护者要求嵌入（预留 Web 服务 REST API 扩展，
// 如地理编码/逆地理编码 restapi.amap.com）。子域 01-04 负载均衡。
export const AMAP_KEY = 'd10b7a30c80f410b171e140aee4bf676';
const AMAP_SUBDOMAINS = ['01', '02', '03', '04'];
const AMAP_STREET_ATTRIBUTION = '<a href="https://www.amap.com/" target="_blank" rel="noreferrer">© 高德地图</a>';

function amapStreetTiles(): string[] {
  return AMAP_SUBDOMAINS.map(
    (s) =>
      `https://webrd0${s}.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=8&key=${AMAP_KEY}&x={x}&y={y}&z={z}`,
  );
}

function amapSatelliteTiles(): string[] {
  return AMAP_SUBDOMAINS.map((s) => `https://webst0${s}.is.autonavi.com/appmaptile?style=6&x={x}&y={y}&z={z}`);
}

/** 卫星模式的路网+中文标注透明叠加（style=8，独立栅格层）。 */
function amapSatOverlayTiles(): string[] {
  return AMAP_SUBDOMAINS.map((s) => `https://webst0${s}.is.autonavi.com/appmaptile?style=8&x={x}&y={y}&z={z}`);
}

// —— ESRI（原 v0.6.0 底图，保留为「极简」模式）——
const ESRI_DARK_BASE_TILES =
  'https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Base/MapServer/tile/{z}/{y}/{x}';
const ESRI_DARK_REF_TILES =
  'https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Reference/MapServer/tile/{z}/{y}/{x}';
const ESRI_ATTRIBUTION = 'Esri, HERE, Garmin, FAO, NOAA, USGS';

// —— 离线叠加数据（web/public/mapdata → go:embed 进二进制，随包分发）——
// 数据 © OpenStreetMap contributors（Release mapdata-hlj-2026-10-12）。
const OSM_ATTRIBUTION = '<a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">© OpenStreetMap contributors</a>';
const MAPDATA_ADMIN = '/mapdata/hlj-admin-areas.geojson.gz';
const MAPDATA_RAIL = '/mapdata/hlj-railways.geojson.gz';
const MAPDATA_ROADS_MAIN = '/mapdata/hlj-roads-main.geojson.gz';
const MAPDATA_ROADS_DXA = '/mapdata/hlj-roads-daxinganling-full.geojson.gz';

// —— 离线地形高程（W17：3D 视图的真实起伏支撑）——
// AWS Terrain Tiles（Mapzen 数据管线，SRTM/GMTED 融合，terrarium 编码，
// 公开免密钥），黑龙江 bbox z4–z8 共 97 瓦片 ≈6.4MB，随二进制内嵌离线可用。
// 高于 z8 由 MapLibre 对 z8 父级瓦片上采样（300m/px 原生分辨率对省级
// 地形展示已足够）。scripts/download_dem_tiles.py 可重跑/扩展。
export const DEM_SOURCE_ID = 'dem';
const DEM_TILES_URL = '/mapdata/dem/{z}/{x}/{y}.png';
const DEM_ATTRIBUTION =
  '<a href="https://registry.opendata.aws/terrain-tiles/" target="_blank" rel="noreferrer">Terrain: Mapzen · AWS Open Data</a>';
/** 地形夸张（zoom 分档自适应，#13：远观强到可见、近观回到真实比例）。 */
export function terrainExaggerationFor(zoom: number): number {
  if (zoom < 6.5) return 4;
  if (zoom < 8) return 2.5;
  return 1.3;
}
/** hillshade 图层 id（可见性随 terrain 偏好）。 */
export const HILLSHADE_LAYER_ID = 'hillshade';

// —— 等高线（W17/#13：离线 DEM 派生，任意底图可用）——
// scripts/gen_contours.py 从同一批 z8 terrarium 瓦片 marching squares 生成；
// 双变体：WGS84（esri/离线底图）与 GCJ-02 预投影（高德底图，逐顶点已偏移，
// 不受 ~500m 火星坐标偏移影响）。200m 首曲线 / 500m 计曲线 + 高程标注。
const CONTOURS_URL = '/mapdata/hlj-contours.geojson.gz';
const CONTOURS_GCJ_URL = '/mapdata/hlj-contours-gcj.geojson.gz';
/** 等高线图层 id（可见性随 contours 偏好，不随底图坐标系禁用）。 */
export const CONTOUR_LAYERS = ['contours-major', 'contours-minor', 'contours-label'];

/** 中文标注字形（免密钥；完全离线时静默降级——MapLibre 容忍字形拉取失败）。 */
const GLYPHS_URL = 'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf';
const FONT = ['Noto Sans Regular'];

// —— 业务图层配色（与 global.css .geo-legend 同步，原值不动）——
export const LINK_UP_COLOR = '#5b8fd9';
export const LINK_DOWN_COLOR = '#ff5252';
export const LINK_SELECTED_COLOR = '#2f9dff';
export const DEVICE_UP_FILL = '#dce6f7';
export const DEVICE_UP_STROKE = '#8fa6ce';
export const DEVICE_DOWN_FILL = '#f2554a';
export const DEVICE_DOWN_STROKE = '#ff9d95';

/** 画布底色（加载期不闪白；offline 模式即最终底色）。 */
export const GEO_BG = '#0e1626';

// —— 底图注册表 ——

export type BasemapId = 'amap' | 'amap-satellite' | 'esri' | 'offline';

export interface BasemapDef {
  /** 瓦片 URL 列表（null = 无栅格，纯离线）。 */
  tiles: string[] | null;
  attribution: string;
  /** GCJ-02 底图：业务坐标需经 wgs84ToGcj02 投影。 */
  isGCJ: boolean;
  /** 是否携带第二栅格层（ESRI Reference / 高德卫星路网标注）。 */
  overlayTiles: string[] | null;
  overlayOpacity: number;
  maxzoom: number;
}

export const BASEMAPS: Record<BasemapId, BasemapDef> = {
  amap: {
    tiles: amapStreetTiles(),
    attribution: AMAP_STREET_ATTRIBUTION,
    isGCJ: true,
    overlayTiles: null,
    overlayOpacity: 1,
    maxzoom: 18,
  },
  'amap-satellite': {
    tiles: amapSatelliteTiles(),
    attribution: AMAP_STREET_ATTRIBUTION,
    isGCJ: true,
    overlayTiles: amapSatOverlayTiles(),
    overlayOpacity: 1,
    maxzoom: 18,
  },
  esri: {
    tiles: [ESRI_DARK_BASE_TILES],
    attribution: ESRI_ATTRIBUTION,
    isGCJ: false,
    overlayTiles: [ESRI_DARK_REF_TILES],
    overlayOpacity: 0.85,
    maxzoom: 16,
  },
  offline: {
    tiles: null,
    attribution: OSM_ATTRIBUTION,
    isGCJ: false,
    overlayTiles: null,
    overlayOpacity: 1,
    maxzoom: 16,
  },
};

export const BASEMAP_SOURCE_ID = 'basemap';
export const BASEMAP_REF_SOURCE_ID = 'basemap-ref';
/** 底图瓦片连续失败切换离线时，仍保留叠加图层的坐标系。 */
export const basemapIsGCJ = (id: BasemapId): boolean => BASEMAPS[id].isGCJ;
/** 离线叠加图层仅在 WGS84 底图下可用（GCJ 底图上叠加会偏移且信息重复）。 */
export const overlaysAvailable = (id: BasemapId): boolean => !BASEMAPS[id].isGCJ;

// —— 图层偏好（localStorage 持久化，隐私模式 try/catch 静默降级）——

export type OverlayId = 'admin' | 'railway' | 'road';

export interface GeoLayerPrefs {
  basemap: BasemapId;
  /** 离线叠加：行政区划默认开（运维需要知道设备属于哪个区县）；铁路默认关
   *  （用户要求铁路不得干扰主观看，可一键开）；公路默认关。 */
  overlays: Record<OverlayId, boolean>;
  /** 业务链路图层可见性。 */
  links: boolean;
  /** 设备地标（HTML Marker）可见性。 */
  devices: boolean;
  /** 地形高程（W17）：3D 视图起伏 + 2D 山体阴影（离线 DEM，默认开）。 */
  terrain: boolean;
  /** 等高线（W17/#13）：任意底图可用（GCJ 变体预投影），默认开。 */
  contours: boolean;
}

export const DEFAULT_PREFS: GeoLayerPrefs = {
  basemap: 'amap',
  overlays: { admin: true, railway: false, road: false },
  links: true,
  devices: true,
  terrain: true,
  contours: true,
};

const PREFS_KEY = 'netops-lab.geo.layers';

export function loadGeoLayerPrefs(): GeoLayerPrefs {
  if (typeof localStorage === 'undefined') return { ...DEFAULT_PREFS, overlays: { ...DEFAULT_PREFS.overlays } };
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    if (raw === null) return { ...DEFAULT_PREFS, overlays: { ...DEFAULT_PREFS.overlays } };
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null) return { ...DEFAULT_PREFS, overlays: { ...DEFAULT_PREFS.overlays } };
    const p = parsed as Record<string, unknown>;
    const base: BasemapId =
      typeof p['basemap'] === 'string' && p['basemap'] in BASEMAPS ? (p['basemap'] as BasemapId) : DEFAULT_PREFS.basemap;
    const ovRaw = typeof p['overlays'] === 'object' && p['overlays'] !== null ? (p['overlays'] as Record<string, unknown>) : {};
    return {
      basemap: base,
      overlays: {
        admin: typeof ovRaw['admin'] === 'boolean' ? ovRaw['admin'] : DEFAULT_PREFS.overlays.admin,
        railway: typeof ovRaw['railway'] === 'boolean' ? ovRaw['railway'] : DEFAULT_PREFS.overlays.railway,
        road: typeof ovRaw['road'] === 'boolean' ? ovRaw['road'] : DEFAULT_PREFS.overlays.road,
      },
      links: typeof p['links'] === 'boolean' ? p['links'] : DEFAULT_PREFS.links,
      devices: typeof p['devices'] === 'boolean' ? p['devices'] : DEFAULT_PREFS.devices,
      terrain: typeof p['terrain'] === 'boolean' ? p['terrain'] : DEFAULT_PREFS.terrain,
      contours: typeof p['contours'] === 'boolean' ? p['contours'] : DEFAULT_PREFS.contours,
    };
  } catch {
    return { ...DEFAULT_PREFS, overlays: { ...DEFAULT_PREFS.overlays } };
  }
}

export function saveGeoLayerPrefs(prefs: GeoLayerPrefs): void {
  if (typeof localStorage === 'undefined') return;
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
  } catch {
    // 写失败不影响功能（隐私模式）
  }
}

// —— GCJ-02 转换（火星坐标；标准算法，国界外不偏移）——

const GCJ_A = 6378245.0;
const GCJ_EE = 0.00669342162296594323;

function gcjTransformLat(x: number, y: number): number {
  let ret = -100.0 + 2.0 * x + 3.0 * y + 0.2 * y * y + 0.1 * x * y + 0.2 * Math.sqrt(Math.abs(x));
  ret += ((20.0 * Math.sin(6.0 * x * Math.PI) + 20.0 * Math.sin(2.0 * x * Math.PI)) * 2.0) / 3.0;
  ret += ((20.0 * Math.sin(y * Math.PI) + 40.0 * Math.sin((y / 3.0) * Math.PI)) * 2.0) / 3.0;
  ret += ((160.0 * Math.sin((y / 12.0) * Math.PI) + 320 * Math.sin((y * Math.PI) / 30.0)) * 2.0) / 3.0;
  return ret;
}

function gcjTransformLng(x: number, y: number): number {
  let ret = 300.0 + x + 2.0 * y + 0.1 * x * x + 0.1 * x * y + 0.1 * Math.sqrt(Math.abs(x));
  ret += ((20.0 * Math.sin(6.0 * x * Math.PI) + 20.0 * Math.sin(2.0 * x * Math.PI)) * 2.0) / 3.0;
  ret += ((20.0 * Math.sin(x * Math.PI) + 40.0 * Math.sin((x / 3.0) * Math.PI)) * 2.0) / 3.0;
  ret += ((150.0 * Math.sin((x / 12.0) * Math.PI) + 300.0 * Math.sin((x / 30.0) * Math.PI)) * 2.0) / 3.0;
  return ret;
}

const outOfChina = (lng: number, lat: number): boolean =>
  lng < 72.004 || lng > 137.8347 || lat < 0.8293 || lat > 55.8271;

/** WGS84 → GCJ-02（高德/火星坐标）。国界外原样返回（无偏移区）。 */
export function wgs84ToGcj02(lng: number, lat: number): [number, number] {
  if (outOfChina(lng, lat)) return [lng, lat];
  let dLat = gcjTransformLat(lng - 105.0, lat - 35.0);
  let dLng = gcjTransformLng(lng - 105.0, lat - 35.0);
  const radLat = (lat / 180.0) * Math.PI;
  let magic = Math.sin(radLat);
  magic = 1 - GCJ_EE * magic * magic;
  const sqrtMagic = Math.sqrt(magic);
  dLat = (dLat * 180.0) / (((GCJ_A * (1 - GCJ_EE)) / (magic * sqrtMagic)) * Math.PI);
  dLng = (dLng * 180.0) / ((GCJ_A / sqrtMagic) * Math.PI);
  return [lng + dLng, lat + dLat];
}

/** 业务坐标 → 当前底图坐标系（GCJ 底图时转换，否则原样）。 */
export function projLngLat(lng: number, lat: number, toGCJ: boolean): [number, number] {
  return toGCJ ? wgs84ToGcj02(lng, lat) : [lng, lat];
}

// —— 离线叠加图层定义（Geometry 线/面 + 中文标注 symbol）——

/** 每个叠加开关控制的图层 id 集合（visibility 批量切换）。 */
export const OVERLAY_LAYERS: Record<OverlayId, string[]> = {
  admin: [
    'offline-admin-fill',
    'offline-admin-line',
    'offline-admin-label-l4',
    'offline-admin-label-l5',
    'offline-admin-label-l6',
  ],
  railway: ['offline-rail-line', 'offline-rail-label'],
  road: ['offline-roads-main-line', 'offline-roads-dxa-line', 'offline-roads-label'],
};

/** 业务链路开关控制的图层 id（links-up/down/selected 同进退）。 */
export const LINK_LAYERS = ['links-up', 'links-down', 'links-selected'];

const statusMatch = (down: string, up: string): ExpressionSpecification =>
  ['match', ['get', 'status'], 'down', down, up] as unknown as ExpressionSpecification;

/** 行政边界线色（按 level：4=省界亮 / 5=地市 / 6=区县更淡）。 */
const ADMIN_LINE_COLOR: ExpressionSpecification = [
  'match',
  ['get', 'level'],
  '4',
  '#7e9bd0',
  '5',
  '#55688f',
  '#3d4c6b',
] as unknown as ExpressionSpecification;

const ADMIN_LINE_WIDTH: ExpressionSpecification = [
  'match',
  ['get', 'level'],
  '4',
  1.6,
  '5',
  1.1,
  0.7,
] as unknown as ExpressionSpecification;

/** 公路主线宽（按 highway 等级；link 类减半）。 */
const ROAD_WIDTH: ExpressionSpecification = [
  'match',
  ['get', 'highway'],
  'motorway',
  1.8,
  'trunk',
  1.5,
  'primary',
  1.2,
  'secondary',
  1.0,
  'motorway_link',
  0.7,
  'trunk_link',
  0.7,
  'primary_link',
  0.7,
  'secondary_link',
  0.7,
  0.6,
] as unknown as ExpressionSpecification;

/** 可见性字面量（MapLibre layout.visibility）。 */
const visOf = (on: boolean): 'visible' | 'none' => (on ? 'visible' : 'none');

function adminLabelLayer(level: string, minzoom: number, size: number, visible: boolean): LayerSpecification {
  return {
    id: `offline-admin-label-l${level}`,
    type: 'symbol',
    source: 'offline-admin',
    minzoom,
    filter: ['==', ['get', 'level'], level],
    layout: {
      'text-field': ['get', 'name'],
      'text-font': FONT,
      'text-size': size,
      'text-letter-spacing': 0.08,
      'text-padding': 4,
      visibility: visOf(visible),
    },
    paint: {
      'text-color': '#9fb4d8',
      'text-halo-color': '#0e1626',
      'text-halo-width': 1.4,
    },
  } as unknown as LayerSpecification;
}

/**
 * 离线叠加图层（初始可见性由调用方烘焙；GeoCanvas 切换时 setLayoutProperty）。
 */
function overlayLayers(on: Record<OverlayId, boolean>): LayerSpecification[] {
  return [
    {
      id: 'offline-admin-fill',
      type: 'fill',
      source: 'offline-admin',
      layout: { visibility: visOf(on.admin) },
      paint: { 'fill-color': '#4a6699', 'fill-opacity': 0.1 },
    },
    {
      id: 'offline-admin-line',
      type: 'line',
      source: 'offline-admin',
      layout: { visibility: visOf(on.admin) },
      paint: { 'line-color': ADMIN_LINE_COLOR, 'line-width': ADMIN_LINE_WIDTH, 'line-opacity': 0.9 },
    },
    adminLabelLayer('4', 3, 12, on.admin),
    adminLabelLayer('5', 5.5, 11, on.admin),
    adminLabelLayer('6', 7, 10, on.admin),
    {
      id: 'offline-rail-line',
      type: 'line',
      source: 'offline-rail',
      layout: { visibility: visOf(on.railway), 'line-cap': 'round' },
      paint: {
        'line-color': '#9b8ac2',
        'line-width': ['match', ['get', 'railway'], 'rail', 1.3, 0.8] as unknown as ExpressionSpecification,
        'line-opacity': 0.85,
        'line-dasharray': [3, 2],
      },
    },
    {
      id: 'offline-rail-label',
      type: 'symbol',
      source: 'offline-rail',
      minzoom: 11,
      layout: {
        'text-field': ['coalesce', ['get', 'name'], ['get', 'ref']] as unknown as ExpressionSpecification,
        'text-font': FONT,
        'text-size': 10,
        'symbol-placement': 'line',
        'text-padding': 3,
        visibility: visOf(on.railway),
      },
      paint: { 'text-color': '#a99bc7', 'text-halo-color': '#0e1626', 'text-halo-width': 1.2 },
    },
    {
      id: 'offline-roads-main-line',
      type: 'line',
      source: 'offline-roads-main',
      layout: { visibility: visOf(on.road) },
      paint: { 'line-color': '#46587a', 'line-width': ROAD_WIDTH, 'line-opacity': 0.85 },
    },
    {
      id: 'offline-roads-dxa-line',
      type: 'line',
      source: 'offline-roads-dxa',
      layout: { visibility: visOf(on.road) },
      paint: {
        'line-color': '#35415c',
        'line-width': [
          'match',
          ['get', 'highway'],
          'trunk',
          1.5,
          'primary',
          1.2,
          'secondary',
          1.0,
          'tertiary',
          0.8,
          0.55,
        ] as unknown as ExpressionSpecification,
        'line-opacity': 0.8,
      },
    },
    {
      id: 'offline-roads-label',
      type: 'symbol',
      source: 'offline-roads-main',
      minzoom: 10.5,
      layout: {
        'text-field': ['coalesce', ['get', 'ref'], ['get', 'name']] as unknown as ExpressionSpecification,
        'text-font': FONT,
        'text-size': 10,
        'symbol-placement': 'line',
        'text-padding': 3,
        visibility: visOf(on.road),
      },
      paint: { 'text-color': '#7487a8', 'text-halo-color': '#0e1626', 'text-halo-width': 1.2 },
    },
  ];
}

// —— 样式构建（GeoCanvas 在底图切换/离线降级时重建整个 style）——
// 业务 GeoJSON source（devices/links/towers）与塔体 3D 塔参数见 GeoCanvas。

/** 3D 塔物理上限（米）：杜绝 issue #9 的数十公里光柱；下限避免贴地不可见。 */
export const TOWER_MIN_M = 60;
export const TOWER_MAX_M = 4000;

export interface StyleOptions {
  /** 栅格是否纳入（false = 离线降级/离线模式：无外网瓦片）。 */
  withRaster: boolean;
  /** 叠加图层初始可见性（GCJ 底图恒 false，由调用方保证）。 */
  overlayVisibility: Record<OverlayId, boolean>;
  /** 业务链路可见性。 */
  linksVisible: boolean;
  /** 地形高程可见性（W17）：hillshade 图层烘焙进样式；terrain 3D 起伏由
   *  GeoCanvas 按视图模式命令式 setTerrain（2D 关、3D 开）。 */
  terrainVisible: boolean;
  /** 等高线可见性（W17/#13）：任意底图可用（GCJ 变体预投影）。 */
  contoursVisible: boolean;
}

export function buildGeoStyle(basemapId: BasemapId, opts: StyleOptions): StyleSpecification {
  const def = BASEMAPS[basemapId];
  const withRaster = opts.withRaster && def.tiles !== null;
  const contourData = def.isGCJ ? CONTOURS_GCJ_URL : CONTOURS_URL;
  const sources: StyleSpecification['sources'] = {
    devices: { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    links: { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    towers: { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    'offline-admin': { type: 'geojson', data: MAPDATA_ADMIN, attribution: OSM_ATTRIBUTION },
    'offline-rail': { type: 'geojson', data: MAPDATA_RAIL, attribution: OSM_ATTRIBUTION },
    'offline-roads-main': { type: 'geojson', data: MAPDATA_ROADS_MAIN, attribution: OSM_ATTRIBUTION },
    'offline-roads-dxa': { type: 'geojson', data: MAPDATA_ROADS_DXA, attribution: OSM_ATTRIBUTION },
    // 等高线（W17/#13）：按底图坐标系选变体，高程派生数据同 DEM 署名
    contours: { type: 'geojson', data: contourData, attribution: DEM_ATTRIBUTION },
    // 离线 DEM（恒在：本地瓦片不依赖外网；terrain 起伏与 hillshade 都从它取）
    [DEM_SOURCE_ID]: {
      type: 'raster-dem',
      tiles: [DEM_TILES_URL],
      encoding: 'terrarium',
      minzoom: 4,
      maxzoom: 8,
      tileSize: 256,
      attribution: DEM_ATTRIBUTION,
    } as StyleSpecification['sources'][string],
  };
  if (withRaster && def.tiles !== null) {
    sources[BASEMAP_SOURCE_ID] = {
      type: 'raster',
      tiles: def.tiles,
      maxzoom: def.maxzoom,
      tileSize: 256,
      attribution: def.attribution,
    };
    if (def.overlayTiles !== null) {
      sources[BASEMAP_REF_SOURCE_ID] = {
        type: 'raster',
        tiles: def.overlayTiles,
        maxzoom: def.maxzoom,
        tileSize: 256,
      };
    }
  }
  const layers: LayerSpecification[] = [{ id: 'geo-bg', type: 'background', paint: { 'background-color': GEO_BG } }];
  if (withRaster && def.tiles !== null) {
    layers.push({ id: BASEMAP_SOURCE_ID, type: 'raster', source: BASEMAP_SOURCE_ID, paint: {} });
    if (def.overlayTiles !== null) {
      layers.push({
        id: BASEMAP_REF_SOURCE_ID,
        type: 'raster',
        source: BASEMAP_REF_SOURCE_ID,
        paint: { 'raster-opacity': def.overlayOpacity },
      });
    }
  }

  // 山体阴影（W17，底图之上/业务图层之下）：terrain 偏好开时可见，
  // 与 3D 起伏共用同一离线 DEM 源。深色主题下的柔和参数。
  layers.push({
    id: HILLSHADE_LAYER_ID,
    type: 'hillshade',
    source: DEM_SOURCE_ID,
    layout: { visibility: visOf(opts.terrainVisible) },
    paint: {
      'hillshade-exaggeration': 0.45,
      'hillshade-shadow-color': '#0b101d',
      'hillshade-highlight-color': '#7d90b8',
      'hillshade-accent-color': '#243049',
    },
  } as unknown as LayerSpecification);

  // 等高线（W17/#13，hillshade 之上、本地叠加之下）：计曲线（500m 整倍）
  // 低 zoom 即可见、首曲线（200m）z≥9.5；高程标注仅计曲线、沿线稀疏。
  const contourVis = visOf(opts.contoursVisible);
  layers.push(
    {
      id: 'contours-major',
      type: 'line',
      source: 'contours',
      minzoom: 4.5,
      filter: ['==', ['get', 'major'], true],
      layout: { visibility: contourVis },
      paint: { 'line-color': '#7fa3d4', 'line-width': 1.1, 'line-opacity': 0.6 },
    },
    {
      id: 'contours-minor',
      type: 'line',
      source: 'contours',
      minzoom: 9.5,
      filter: ['==', ['get', 'major'], false],
      layout: { visibility: contourVis },
      paint: { 'line-color': '#4d6390', 'line-width': 0.7, 'line-opacity': 0.42 },
    },
    {
      id: 'contours-label',
      type: 'symbol',
      source: 'contours',
      minzoom: 8,
      filter: ['==', ['get', 'major'], true],
      layout: {
        'text-field': ['concat', ['to-string', ['get', 'level']], ' m'] as unknown as ExpressionSpecification,
        'text-font': FONT,
        'text-size': 9.5,
        'symbol-placement': 'line',
        'symbol-spacing': 380,
        'text-padding': 2,
        visibility: contourVis,
      },
      paint: { 'text-color': '#8fa9d0', 'text-halo-color': '#0e1626', 'text-halo-width': 1.3 },
    } as unknown as LayerSpecification,
  );

  // 离线叠加（在业务图层之下：运维主观看的是设备，地理要素做背景参照）
  layers.push(...overlayLayers(opts.overlayVisibility));

  const linkVis = visOf(opts.linksVisible);
  layers.push(
    {
      id: 'links-up',
      type: 'line',
      source: 'links',
      filter: ['==', ['get', 'status'], 'up'],
      layout: { visibility: linkVis },
      paint: { 'line-color': LINK_UP_COLOR, 'line-width': 2 },
    },
    {
      id: 'links-down',
      type: 'line',
      source: 'links',
      filter: ['==', ['get', 'status'], 'down'],
      layout: { visibility: linkVis },
      paint: { 'line-color': LINK_DOWN_COLOR, 'line-width': 3, 'line-dasharray': [2, 1.5] },
    },
    {
      id: 'links-selected',
      type: 'line',
      source: 'links',
      filter: ['==', ['get', 'selected'], true],
      layout: { visibility: linkVis },
      paint: { 'line-color': LINK_SELECTED_COLOR, 'line-width': 4 },
    },
    {
      id: 'towers',
      type: 'fill-extrusion',
      source: 'towers',
      layout: { visibility: 'none' },
      paint: {
        'fill-extrusion-color': statusMatch(LINK_DOWN_COLOR, LINK_UP_COLOR),
        // #9 修复：塔高（米）为数据驱动 heightM —— buildTowerCollection 在每次
        // zoom 重建（rAF 节流）时按 towerPx × metersPerPixel(lat, zoom) 计算
        // 并钳制到 [TOWER_MIN_M, TOWER_MAX_M]（杜绝数十公里光柱）。不用 zoom
        // 表达式：高度与 footprint 同帧重建，缩放动画期间一致变化，且规避
        // zoom 表达式嵌套 min/max 的样式校验限制。
        'fill-extrusion-height': ['get', 'heightM'] as unknown as ExpressionSpecification,
        'fill-extrusion-base': 0,
        'fill-extrusion-opacity': 0.85,
      },
    },
    {
      id: 'devices-ground',
      type: 'circle',
      source: 'devices',
      paint: {
        'circle-radius': 7,
        'circle-color': statusMatch(DEVICE_DOWN_FILL, DEVICE_UP_FILL),
        'circle-opacity': 0.28,
        'circle-stroke-width': 1.5,
        'circle-stroke-color': statusMatch(DEVICE_DOWN_STROKE, DEVICE_UP_STROKE),
        'circle-stroke-opacity': 0.7,
      },
    },
  );
  return { version: 8, glyphs: GLYPHS_URL, sources, layers };
}
