/**
 * geoBasemap —— 底图多模式注册表 + 离线叠加图层 + 自然地表覆盖 + GCJ-02 坐标转换。
 *
 * v0.6.4（#15）：
 * - offline 底图升级为**离线简洁底图**（浅色，类标准地图观感）：画布底色 +
 *   森林（绿）/草地农田/湿地 + 河流溪流湖泊（蓝）+ 地名 + 交通区划叠加，
 *   无任何商铺/POI 标注（用户明确要求"干净的 2D"）；设为默认底图——
 *   内网/离线环境开箱即用，不再依赖高德瓦片可达性；
 * - 自然要素/地名来自 scripts/gen_natural.py（PBF 提取 + RDP 简化 +
 *   坐标量化），双坐标变体（WGS84 / GCJ-02 预投影）→ 任意底图可用，
 *   高德底图上不偏移（与等高线同策略）；
 * - 高德街道/卫星、ESRI 极简保留为可选底图（高德瓦片实测可达，
 *   REST key 有效；但栅格自带大量 POI 标注，不再作为默认）。
 *
 * 历史（W16/#11）：底图从硬编码单一 ESRI 极简改为可切换模式注册表。
 * 离线交通/区划叠加（行政区划/铁路/公路）只在 WGS84 底图（esri|offline）
 * 下可用：高德为 GCJ-02，叠加 WGS84 数据会偏移 ~500m，且其栅格已含同类
 * 信息；自然要素与地名不受此限（GCJ 变体预投影）。
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
// 如地理编码/逆地理编码 restapi.amap.com——实测该 key 为有效 Web 服务 key）。
// 子域 01-04 负载均衡。栅格自带 POI/商铺标注（无法去除），故不作默认底图。
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

// —— ESRI（保留为「极简」模式，深色主题）——
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

// —— 自然地表覆盖 + 地名（v0.6.4/#15，scripts/gen_natural.py 产出）——
// 29,449 条要素（森林 12,955 / 水域面 4,961 / 河流 3,591 / 溪流 2,309 /
// 渠 1,113 / 草地农田 4,557）+ 17,615 个地名点；双坐标变体按底图选源。
const MAPDATA_NATURAL = '/mapdata/hlj-natural.geojson.gz';
const MAPDATA_NATURAL_GCJ = '/mapdata/hlj-natural-gcj.geojson.gz';
const MAPDATA_PLACES = '/mapdata/hlj-places.geojson.gz';
const MAPDATA_PLACES_GCJ = '/mapdata/hlj-places-gcj.geojson.gz';

// —— 真实网络数据（v0.8.0，scripts/realdata/extract.py 产出）——
// 光缆干线走廊：OSM 道路/铁路真实几何按线路名选取（绥满高速/滨洲线/嫩林线/
// 京漠线等，7 条干线，RDP 简化 ~150m）；属性 {code, n(名称), km(锚点链里程),
// k(road|rail)}。无线基站：OpenCelliD 2026-10-06 快照黑龙江 1,198 站
// (1,851 小区，~110m 网格聚类)；属性 {o(运营商), r(制式), c(小区数), s(样本)}。
const MAPDATA_CABLES = '/mapdata/hlj-cables.geojson.gz';
const MAPDATA_CABLES_GCJ = '/mapdata/hlj-cables-gcj.geojson.gz';
const MAPDATA_BS = '/mapdata/hlj-basestations.geojson.gz';
const MAPDATA_BS_GCJ = '/mapdata/hlj-basestations-gcj.geojson.gz';
const OCID_ATTRIBUTION =
  '<a href="https://opencellid.org" target="_blank" rel="noreferrer">基站: OpenCelliD (CC BY-SA 4.0)</a>';
/** 基站运营商配色（移动蓝/联通红/电信绿/其它紫，图例见图层标题提示）。 */
export const BS_OPERATOR_COLORS: Record<string, string> = {
  '中国移动': '#3d7ee0',
  '中国联通': '#e05252',
  '中国电信': '#1fa06b',
  '中国广电': '#a376f0',
};

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

/** 画布底色（加载期不闪白；深色主题底图即最终底色）。 */
export const GEO_BG = '#0e1626';
/** 浅色主题底色（离线简洁底图，类标准地图的暖白）。 */
export const GEO_BG_LIGHT = '#f5f3f0';

// —— 底图注册表 ——

export type BasemapId = 'offline' | 'amap' | 'amap-satellite' | 'esri';

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
  /** 配色主题：决定叠加图层/自然要素/等高线/山体阴影用浅色还是深色系。 */
  theme: 'light' | 'dark';
}

export const BASEMAPS: Record<BasemapId, BasemapDef> = {
  // v0.6.4：离线简洁底图为默认（#15：用户要求 2D 干净、无 POI、内网可用）。
  offline: {
    tiles: null,
    attribution: OSM_ATTRIBUTION,
    isGCJ: false,
    overlayTiles: null,
    overlayOpacity: 1,
    maxzoom: 16,
    theme: 'light',
  },
  amap: {
    tiles: amapStreetTiles(),
    attribution: AMAP_STREET_ATTRIBUTION,
    isGCJ: true,
    overlayTiles: null,
    overlayOpacity: 1,
    maxzoom: 18,
    theme: 'light',
  },
  'amap-satellite': {
    tiles: amapSatelliteTiles(),
    attribution: AMAP_STREET_ATTRIBUTION,
    isGCJ: true,
    overlayTiles: amapSatOverlayTiles(),
    overlayOpacity: 1,
    maxzoom: 18,
    theme: 'dark',
  },
  esri: {
    tiles: [ESRI_DARK_BASE_TILES],
    attribution: ESRI_ATTRIBUTION,
    isGCJ: false,
    overlayTiles: [ESRI_DARK_REF_TILES],
    overlayOpacity: 0.85,
    maxzoom: 16,
    theme: 'dark',
  },
};

export const BASEMAP_SOURCE_ID = 'basemap';
export const BASEMAP_REF_SOURCE_ID = 'basemap-ref';
/** 底图瓦片连续失败切换离线时，仍保留叠加图层的坐标系。 */
export const basemapIsGCJ = (id: BasemapId): boolean => BASEMAPS[id].isGCJ;
/** 离线交通/区划叠加图层仅在 WGS84 底图下可用（GCJ 底图上叠加会偏移且信息重复）。 */
export const overlaysAvailable = (id: BasemapId): boolean => !BASEMAPS[id].isGCJ;
/** 底图浅色/深色主题。 */
export const basemapIsLight = (id: BasemapId): boolean => BASEMAPS[id].theme === 'light';
/** 底图底色（GeoCanvas 加载期防闪白用）。 */
export const basemapBg = (id: BasemapId): string => (BASEMAPS[id].theme === 'light' ? GEO_BG_LIGHT : GEO_BG);

// —— 图层偏好（localStorage 持久化，隐私模式 try/catch 静默降级）——
// v0.6.4：存储键升级 v2——新增自然要素/地名开关且默认底图换为离线简洁，
// 旧 v1 偏好（basemap=amap、road 默认关）不再沿用，保证升级即见新观感。

export type OverlayId = 'admin' | 'railway' | 'road' | 'forest' | 'water' | 'green' | 'places' | 'cable' | 'basestation';

/** 仅 WGS84 底图可用的叠加（区划/交通：GCJ 底图栅格已含且会偏移）。 */
export const WGS84_ONLY_OVERLAYS: OverlayId[] = ['admin', 'railway', 'road'];
/** 任意底图可用的叠加（双坐标变体：自然要素/地名）。 */
export const ANY_BASEMAP_OVERLAYS: OverlayId[] = ['forest', 'water', 'green', 'places', 'cable', 'basestation'];

export interface GeoLayerPrefs {
  basemap: BasemapId;
  /** 离线叠加：地名/区划/森林/水系/草地农田默认开（#15「干净 2D」的地表
   *  感）；公路默认开（用户要"河流交通这些"）；铁路默认关（用户要求铁路
   *  不得干扰主观看，可一键开）。 */
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
  basemap: 'offline',
  overlays: {
    admin: true,
    railway: false,
    road: true,
    forest: true,
    water: true,
    green: true,
    places: true,
    // v0.8.0 真实网络数据：光缆干线默认开（7 条走廊是主叙事）；基站默认关
    // （1,198 站点密集，一键开——图层标题带运营商图例说明）。
    cable: true,
    basestation: false,
  },
  links: true,
  devices: true,
  terrain: true,
  contours: true,
};

const PREFS_KEY = 'netops-lab.geo.layers.v2';

const overlayBool = (
  ovRaw: Record<string, unknown>,
  key: OverlayId,
  fallback: boolean,
): boolean => (typeof ovRaw[key] === 'boolean' ? (ovRaw[key] as boolean) : fallback);

export function loadGeoLayerPrefs(): GeoLayerPrefs {
  const fresh = (): GeoLayerPrefs => ({ ...DEFAULT_PREFS, overlays: { ...DEFAULT_PREFS.overlays } });
  if (typeof localStorage === 'undefined') return fresh();
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    if (raw === null) return fresh();
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null) return fresh();
    const p = parsed as Record<string, unknown>;
    const base: BasemapId =
      typeof p['basemap'] === 'string' && p['basemap'] in BASEMAPS ? (p['basemap'] as BasemapId) : DEFAULT_PREFS.basemap;
    const ovRaw = typeof p['overlays'] === 'object' && p['overlays'] !== null ? (p['overlays'] as Record<string, unknown>) : {};
    return {
      basemap: base,
      overlays: {
        admin: overlayBool(ovRaw, 'admin', DEFAULT_PREFS.overlays.admin),
        railway: overlayBool(ovRaw, 'railway', DEFAULT_PREFS.overlays.railway),
        road: overlayBool(ovRaw, 'road', DEFAULT_PREFS.overlays.road),
        forest: overlayBool(ovRaw, 'forest', DEFAULT_PREFS.overlays.forest),
        water: overlayBool(ovRaw, 'water', DEFAULT_PREFS.overlays.water),
        green: overlayBool(ovRaw, 'green', DEFAULT_PREFS.overlays.green),
        places: overlayBool(ovRaw, 'places', DEFAULT_PREFS.overlays.places),
        cable: overlayBool(ovRaw, 'cable', DEFAULT_PREFS.overlays.cable),
        basestation: overlayBool(ovRaw, 'basestation', DEFAULT_PREFS.overlays.basestation),
      },
      links: typeof p['links'] === 'boolean' ? p['links'] : DEFAULT_PREFS.links,
      devices: typeof p['devices'] === 'boolean' ? p['devices'] : DEFAULT_PREFS.devices,
      terrain: typeof p['terrain'] === 'boolean' ? p['terrain'] : DEFAULT_PREFS.terrain,
      contours: typeof p['contours'] === 'boolean' ? p['contours'] : DEFAULT_PREFS.contours,
    };
  } catch {
    return fresh();
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
  road: [
    'offline-roads-main-casing',
    'offline-roads-main-line',
    'offline-roads-dxa-casing',
    'offline-roads-dxa-line',
    'offline-roads-label',
  ],
  forest: ['natural-forest-fill'],
  water: [
    'natural-water-fill',
    'natural-river-line',
    'natural-stream-line',
    'natural-water-label',
    'natural-river-label',
  ],
  green: ['natural-green-fill'],
  places: ['places-city', 'places-county', 'places-town', 'places-village'],
  cable: ['cable-casing', 'cable-line', 'cable-line-rail', 'cable-label'],
  basestation: ['basestation-pts'],
};

/** 业务链路开关控制的图层 id（links-up/down/selected 同进退）。 */
export const LINK_LAYERS = ['links-up', 'links-down', 'links-selected'];

const statusMatch = (down: string, up: string): ExpressionSpecification =>
  ['match', ['get', 'status'], 'down', down, up] as unknown as ExpressionSpecification;

/** 可见性字面量（MapLibre layout.visibility）。 */
const visOf = (on: boolean): 'visible' | 'none' => (on ? 'visible' : 'none');

/** 类别多选 filter：match 多标签形式（MapLibre in 不支持多候选变参）。 */
const inClasses = (...classes: string[]): ExpressionSpecification =>
  ['match', ['get', 'c'], classes.length === 1 ? classes[0] : classes, true, false] as unknown as ExpressionSpecification;

// —— 主题化配色（浅色 = 离线简洁底图/高德街道；深色 = ESRI/卫星影像）——

interface ThemePalette {
  /** 行政边界线色（按 level：4=省界 / 5=地市 / 6=区县）。 */
  adminLine: ExpressionSpecification;
  adminFill: string;
  adminFillOpacity: number;
  adminLabel: string;
  adminHalo: string;
  rail: string;
  railLabel: string;
  /** 公路主线色（浅色按等级：高速橙 → 干道黄 → 次要白；深色纯色）。 */
  roadMain: ExpressionSpecification | string;
  roadDxa: ExpressionSpecification | string;
  roadCasing: string | null;
  roadLabel: string;
  /** 自然要素。 */
  forest: string;
  forestOpacity: number;
  grass: string;
  farm: string;
  wetland: string;
  scrub: string;
  greenOpacity: number;
  waterFill: string;
  waterOpacity: number;
  river: string;
  stream: string;
  waterLabel: string;
  /** 地名。 */
  placeCity: string;
  placeCounty: string;
  placeTown: string;
  placeVillage: string;
  placeHalo: string;
  /** 等高线（浅色 = 地形图棕系；深色 = 原蓝系）。 */
  contourMajor: string;
  contourMinor: string;
  contourLabel: string;
  contourHalo: string;
  /** 光缆干线走廊（v0.8.0）：琥珀主线 + 描边 + 标注。 */
  cableLine: string;
  cableCasing: string;
  cableLabel: string;
}

const LIGHT: ThemePalette = {
  adminLine: [
    'match',
    ['get', 'level'],
    '4',
    '#a996b3',
    '5',
    '#bcaec4',
    '#ccc0d2',
  ] as unknown as ExpressionSpecification,
  adminFill: '#99a8c4',
  adminFillOpacity: 0.05,
  adminLabel: '#6b6b80',
  adminHalo: '#ffffff',
  rail: '#b4adb3',
  railLabel: '#7a7480',
  roadMain: [
    'match',
    ['get', 'highway'],
    'motorway',
    '#f7bc6f',
    'trunk',
    '#f9d297',
    'primary',
    '#fceece',
    'secondary',
    '#ffffff',
    '#ffffff',
  ] as unknown as ExpressionSpecification,
  roadDxa: '#ffffff',
  roadCasing: '#e3dcd0',
  roadLabel: '#8a8578',
  forest: '#c6e2be',
  forestOpacity: 0.9,
  grass: '#dceed3',
  farm: '#efe9cd',
  wetland: '#c6e1dc',
  scrub: '#d2e7cf',
  greenOpacity: 0.85,
  waterFill: '#a9cff0',
  waterOpacity: 0.95,
  river: '#8fb8e8',
  stream: '#b0d4f0',
  waterLabel: '#5f88b5',
  placeCity: '#2e3038',
  placeCounty: '#484b55',
  placeTown: '#5a5d66',
  placeVillage: '#767982',
  placeHalo: '#ffffff',
  contourMajor: '#b28a62',
  contourMinor: '#c9ac8c',
  contourLabel: '#a67c52',
  contourHalo: '#ffffff',
  cableLine: '#d97706',
  cableCasing: '#7c4a03',
  cableLabel: '#7c4a03',
};


const DARK: ThemePalette = {
  adminLine: [
    'match',
    ['get', 'level'],
    '4',
    '#7e9bd0',
    '5',
    '#55688f',
    '#3d4c6b',
  ] as unknown as ExpressionSpecification,
  adminFill: '#4a6699',
  adminFillOpacity: 0.1,
  adminLabel: '#9fb4d8',
  adminHalo: '#0e1626',
  rail: '#9b8ac2',
  railLabel: '#a99bc7',
  roadMain: '#46587a',
  roadDxa: '#35415c',
  roadCasing: null,
  roadLabel: '#7487a8',
  forest: '#2e5a3f',
  forestOpacity: 0.55,
  grass: '#3c5c47',
  farm: '#5c5a40',
  wetland: '#2f5d5c',
  scrub: '#3a5442',
  greenOpacity: 0.5,
  waterFill: '#1e4470',
  waterOpacity: 0.75,
  river: '#4a7ec2',
  stream: '#3c6aa8',
  waterLabel: '#8fa9d0',
  placeCity: '#e6ecf5',
  placeCounty: '#c6d2e6',
  placeTown: '#aebcd6',
  placeVillage: '#93a3c2',
  placeHalo: '#0e1626',
  contourMajor: '#7fa3d4',
  contourMinor: '#4d6390',
  contourLabel: '#8fa9d0',
  contourHalo: '#0e1626',
  cableLine: '#f6b83c',
  cableCasing: '#3a2b0a',
  cableLabel: '#f6c96b',
};


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

/** 大兴安岭地方道路宽（等级更细）。 */
const ROAD_DXA_WIDTH: ExpressionSpecification = [
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
] as unknown as ExpressionSpecification;

/** 线宽 + 外扩（casing 用；表达式加法）。 */
const widthPlus = (w: ExpressionSpecification, extra: number): ExpressionSpecification =>
  ['+', w, extra] as unknown as ExpressionSpecification;

function adminLabelLayer(
  level: string,
  minzoom: number,
  size: number,
  visible: boolean,
  t: ThemePalette,
): LayerSpecification {
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
      'text-color': t.adminLabel,
      'text-halo-color': t.adminHalo,
      'text-halo-width': 1.4,
    },
  } as unknown as LayerSpecification;
}

/**
 * 离线交通/区划叠加（初始可见性由调用方烘焙；GeoCanvas 切换时 setLayoutProperty）。
 * 浅色主题下公路加 casing（白路在暖白底上需要描边才可见）。
 */
function overlayLayers(on: Record<OverlayId, boolean>, t: ThemePalette): LayerSpecification[] {
  const roadVis = visOf(on.road);
  const layers: LayerSpecification[] = [
    {
      id: 'offline-admin-fill',
      type: 'fill',
      source: 'offline-admin',
      layout: { visibility: visOf(on.admin) },
      paint: { 'fill-color': t.adminFill, 'fill-opacity': t.adminFillOpacity },
    },
    {
      id: 'offline-admin-line',
      type: 'line',
      source: 'offline-admin',
      layout: { visibility: visOf(on.admin) },
      paint: { 'line-color': t.adminLine, 'line-width': ADMIN_LINE_WIDTH, 'line-opacity': 0.9 },
    },
    adminLabelLayer('4', 3, 12, on.admin, t),
    adminLabelLayer('5', 5.5, 11, on.admin, t),
    adminLabelLayer('6', 7, 10, on.admin, t),
    {
      id: 'offline-rail-line',
      type: 'line',
      source: 'offline-rail',
      layout: { visibility: visOf(on.railway), 'line-cap': 'round' },
      paint: {
        'line-color': t.rail,
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
      paint: { 'text-color': t.railLabel, 'text-halo-color': t.adminHalo, 'text-halo-width': 1.2 },
    },
  ];
  if (t.roadCasing !== null) {
    layers.push({
      id: 'offline-roads-main-casing',
      type: 'line',
      source: 'offline-roads-main',
      layout: { visibility: roadVis, 'line-cap': 'round' },
      paint: {
        'line-color': t.roadCasing,
        'line-width': widthPlus(ROAD_WIDTH, 1.6),
        'line-opacity': 0.9,
      },
    });
  }
  layers.push({
    id: 'offline-roads-main-line',
    type: 'line',
    source: 'offline-roads-main',
    layout: { visibility: roadVis, 'line-cap': 'round' },
    paint: { 'line-color': t.roadMain, 'line-width': ROAD_WIDTH, 'line-opacity': 0.95 },
  });
  if (t.roadCasing !== null) {
    layers.push({
      id: 'offline-roads-dxa-casing',
      type: 'line',
      source: 'offline-roads-dxa',
      layout: { visibility: roadVis, 'line-cap': 'round' },
      paint: {
        'line-color': t.roadCasing,
        'line-width': widthPlus(ROAD_DXA_WIDTH, 1.2),
        'line-opacity': 0.85,
      },
    });
  }
  layers.push({
    id: 'offline-roads-dxa-line',
    type: 'line',
    source: 'offline-roads-dxa',
    layout: { visibility: roadVis, 'line-cap': 'round' },
    paint: { 'line-color': t.roadDxa, 'line-width': ROAD_DXA_WIDTH, 'line-opacity': 0.9 },
  });
  layers.push({
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
      visibility: roadVis,
    },
    paint: { 'text-color': t.roadLabel, 'text-halo-color': t.adminHalo, 'text-halo-width': 1.2 },
  });
  return layers;
}

/** 行政边界线宽（按 level：4=省界 1.6 / 5=地市 1.1 / 6=区县 0.7）。 */
const ADMIN_LINE_WIDTH: ExpressionSpecification = [
  'match',
  ['get', 'level'],
  '4',
  1.6,
  '5',
  1.1,
  0.7,
] as unknown as ExpressionSpecification;

/**
 * 自然要素图层（v0.6.4/#15）：森林/绿地/水面（fill）+ 河流/溪流（line）+
 * 水域/河流名称标注。源为单一天然要素 FC（面 + 线混合，fill 只渲染面、
 * line 只渲染线，filter 按类别 c 分流）。双坐标变体 → 任意底图可用。
 */
function naturalLayers(on: Record<OverlayId, boolean>, t: ThemePalette): LayerSpecification[] {
  return [
    {
      id: 'natural-forest-fill',
      type: 'fill',
      source: 'natural',
      minzoom: 4.5,
      filter: inClasses('wood', 'forest'),
      layout: { visibility: visOf(on.forest) },
      paint: { 'fill-color': t.forest, 'fill-opacity': t.forestOpacity },
    },
    {
      id: 'natural-green-fill',
      type: 'fill',
      source: 'natural',
      minzoom: 6,
      filter: inClasses('grass', 'scrub', 'farm', 'wetland'),
      layout: { visibility: visOf(on.green) },
      paint: {
        'fill-color': [
          'match',
          ['get', 'c'],
          'farm',
          t.farm,
          'wetland',
          t.wetland,
          'scrub',
          t.scrub,
          t.grass,
        ] as unknown as ExpressionSpecification,
        'fill-opacity': t.greenOpacity,
      },
    },
    {
      id: 'natural-water-fill',
      type: 'fill',
      source: 'natural',
      minzoom: 4,
      filter: inClasses('water', 'riverbank'),
      layout: { visibility: visOf(on.water) },
      paint: { 'fill-color': t.waterFill, 'fill-opacity': t.waterOpacity },
    },
    {
      id: 'natural-river-line',
      type: 'line',
      source: 'natural',
      minzoom: 5,
      filter: ['==', ['get', 'c'], 'river'],
      layout: { visibility: visOf(on.water), 'line-cap': 'round' },
      paint: {
        'line-color': t.river,
        'line-width': [
          'interpolate',
          ['linear'],
          ['zoom'],
          5,
          0.7,
          9,
          1.6,
          12.5,
          2.6,
        ] as unknown as ExpressionSpecification,
        'line-opacity': 0.9,
      },
    },
    {
      id: 'natural-stream-line',
      type: 'line',
      source: 'natural',
      minzoom: 7,
      filter: inClasses('stream', 'canal'),
      layout: { visibility: visOf(on.water), 'line-cap': 'round' },
      paint: {
        'line-color': t.stream,
        'line-width': [
          'interpolate',
          ['linear'],
          ['zoom'],
          7,
          0.5,
          12.5,
          1.6,
        ] as unknown as ExpressionSpecification,
        'line-opacity': 0.85,
      },
    },
    {
      id: 'natural-water-label',
      type: 'symbol',
      source: 'natural',
      minzoom: 8,
      filter: ['all', inClasses('water', 'riverbank'), ['has', 'n']],
      layout: {
        'text-field': ['get', 'n'],
        'text-font': FONT,
        'text-size': 9.5,
        'text-letter-spacing': 0.1,
        'text-padding': 6,
        visibility: visOf(on.water),
      },
      paint: { 'text-color': t.waterLabel, 'text-halo-color': t.placeHalo, 'text-halo-width': 1.3 },
    },
    {
      id: 'natural-river-label',
      type: 'symbol',
      source: 'natural',
      minzoom: 9.5,
      filter: ['all', inClasses('river', 'stream'), ['has', 'n']],
      layout: {
        'text-field': ['get', 'n'],
        'text-font': FONT,
        'text-size': 9,
        'symbol-placement': 'line',
        'symbol-spacing': 620,
        'text-padding': 3,
        visibility: visOf(on.water),
      },
      paint: { 'text-color': t.waterLabel, 'text-halo-color': t.placeHalo, 'text-halo-width': 1.2 },
    } as unknown as LayerSpecification,
  ];
}

/** 地名标注（v0.6.4/#15）：城市/区县/乡镇/村庄分层 minzoom。 */
/**
 * 真实网络数据图层（v0.8.0）：光缆干线走廊（琥珀色双描边 + 名称标注，
 * 铁路走廊细虚线）+ 无线基站（运营商配色圆点，半径随小区数）。
 * 数据源署名与许可见文件头部注释；任何底图可用（GCJ 变体预投影）。
 */
function realNetworkLayers(on: Record<OverlayId, boolean>, t: ThemePalette): LayerSpecification[] {
  const cableVis = visOf(on.cable);
  const bsVis = visOf(on.basestation);
  const operatorColor = (fallback: string): ExpressionSpecification =>
    // match 表达式的 标签/值 必须平铺（label1,value1,label2,value2,...,fallback）——
    // 嵌套数组会被解析为子表达式而使整个样式校验失败（W28 实测教训）。
    (['match', ['get', 'o'], ...Object.entries(BS_OPERATOR_COLORS).flat(), fallback] as unknown as ExpressionSpecification);
  return [
    {
      id: 'cable-casing',
      type: 'line',
      source: 'cables',
      layout: { visibility: cableVis, 'line-cap': 'round' },
      paint: {
        'line-color': t.cableCasing,
        'line-width': ['match', ['get', 'k'], 'road', 4.2, 2.6] as unknown as ExpressionSpecification,
        'line-opacity': 0.55,
      },
    },
    {
      id: 'cable-line',
      type: 'line',
      source: 'cables',
      filter: ['==', ['get', 'k'], 'road'],
      layout: { visibility: cableVis, 'line-cap': 'round' },
      paint: {
        'line-color': t.cableLine,
        'line-width': 2.2,
        'line-opacity': 0.92,
      },
    },
    {
      // 铁路走廊：细虚线（line-dasharray 不支持数据驱动表达式 → filter 分层，
      // 静态 dasharray —— maplibre 6.x 样式校验安全）
      id: 'cable-line-rail',
      type: 'line',
      source: 'cables',
      filter: ['==', ['get', 'k'], 'rail'],
      layout: { visibility: cableVis, 'line-cap': 'round' },
      paint: {
        'line-color': t.cableLine,
        'line-width': 1.1,
        'line-opacity': 0.78,
        'line-dasharray': [4, 2],
      },
    },
    {
      id: 'cable-label',
      type: 'symbol',
      source: 'cables',
      minzoom: 7,
      layout: {
        'text-field': ['concat', ['get', 'n'], ' ', ['to-string', ['get', 'km']], 'km'] as unknown as ExpressionSpecification,
        'text-font': FONT,
        'text-size': 10.5,
        'symbol-placement': 'line',
        'symbol-spacing': 420,
        'text-padding': 4,
        visibility: cableVis,
      },
      paint: { 'text-color': t.cableLabel, 'text-halo-color': t.adminHalo, 'text-halo-width': 1.4 },
    },
    {
      id: 'basestation-pts',
      type: 'circle',
      source: 'basestations',
      minzoom: 5.5,
      layout: { visibility: bsVis },
      paint: {
        'circle-radius': ['match', ['get', 'c'], 3, 5, 2, 3.6, 2.6] as unknown as ExpressionSpecification,
        'circle-color': operatorColor('#8d8d8d'),
        'circle-opacity': 0.8,
        'circle-stroke-width': 0.8,
        'circle-stroke-color': t.adminHalo,
        'circle-stroke-opacity': 0.9,
      },
    },
  ];
}

function placesLayers(on: Record<OverlayId, boolean>, t: ThemePalette): LayerSpecification[] {
  const defs: Array<{ cls: string[]; id: string; minzoom: number; size: number; color: string }> = [
    { cls: ['province', 'city'], id: 'places-city', minzoom: 4.2, size: 12.5, color: t.placeCity },
    { cls: ['county', 'district'], id: 'places-county', minzoom: 6.5, size: 11, color: t.placeCounty },
    { cls: ['town'], id: 'places-town', minzoom: 8.8, size: 10.2, color: t.placeTown },
    { cls: ['village', 'hamlet'], id: 'places-village', minzoom: 11, size: 9.4, color: t.placeVillage },
  ];
  return defs.map(
    (d): LayerSpecification =>
      ({
        id: d.id,
        type: 'symbol',
        source: 'places',
        minzoom: d.minzoom,
        filter: inClasses(...d.cls),
        layout: {
          'text-field': ['get', 'n'],
          'text-font': FONT,
          'text-size': d.size,
          'text-padding': 4,
          visibility: visOf(on.places),
        },
        paint: { 'text-color': d.color, 'text-halo-color': t.placeHalo, 'text-halo-width': 1.8 },
      }) as unknown as LayerSpecification,
  );
}

// —— 样式构建（GeoCanvas 在底图切换/离线降级时重建整个 style）——
// 业务 GeoJSON source（devices/links/towers）与塔体 3D 塔参数见 GeoCanvas。

/** 3D 塔物理上限（米）：杜绝 issue #9 的数十公里光柱；下限避免贴地不可见。 */
export const TOWER_MIN_M = 60;
export const TOWER_MAX_M = 4000;

export interface StyleOptions {
  /** 栅格是否纳入（false = 离线降级/离线模式：无外网瓦片）。 */
  withRaster: boolean;
  /** 叠加图层初始可见性（admin/railway/road 恒 false when GCJ，由调用方保证）。 */
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
  const t = def.theme === 'light' ? LIGHT : DARK;
  const contourData = def.isGCJ ? CONTOURS_GCJ_URL : CONTOURS_URL;
  const naturalData = def.isGCJ ? MAPDATA_NATURAL_GCJ : MAPDATA_NATURAL;
  const placesData = def.isGCJ ? MAPDATA_PLACES_GCJ : MAPDATA_PLACES;
  const sources: StyleSpecification['sources'] = {
    devices: { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    links: { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    towers: { type: 'geojson', data: { type: 'FeatureCollection', features: [] } },
    'offline-admin': { type: 'geojson', data: MAPDATA_ADMIN, attribution: OSM_ATTRIBUTION },
    'offline-rail': { type: 'geojson', data: MAPDATA_RAIL, attribution: OSM_ATTRIBUTION },
    'offline-roads-main': { type: 'geojson', data: MAPDATA_ROADS_MAIN, attribution: OSM_ATTRIBUTION },
    'offline-roads-dxa': { type: 'geojson', data: MAPDATA_ROADS_DXA, attribution: OSM_ATTRIBUTION },
    // 自然要素 + 地名（v0.6.4/#15）：双坐标变体按底图选源（高德 = GCJ 预投影）
    natural: { type: 'geojson', data: naturalData, attribution: OSM_ATTRIBUTION },
    places: { type: 'geojson', data: placesData, attribution: OSM_ATTRIBUTION },
    // 真实网络数据（v0.8.0）：光缆走廊（OSM 几何）+ 无线基站（OpenCelliD
    // CC BY-SA 4.0，署名单列）；双坐标变体按底图选源（高德 = GCJ 预投影）。
    cables: { type: 'geojson', data: def.isGCJ ? MAPDATA_CABLES_GCJ : MAPDATA_CABLES, attribution: OSM_ATTRIBUTION },
    basestations: { type: 'geojson', data: def.isGCJ ? MAPDATA_BS_GCJ : MAPDATA_BS, attribution: OCID_ATTRIBUTION },
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
  const layers: LayerSpecification[] = [
    { id: 'geo-bg', type: 'background', paint: { 'background-color': def.theme === 'light' ? GEO_BG_LIGHT : GEO_BG } },
  ];
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
  // 与 3D 起伏共用同一离线 DEM 源。浅色主题 = 柔和暖灰（纸质地感），
  // 深色主题 = 原夜蓝色系。
  layers.push({
    id: HILLSHADE_LAYER_ID,
    type: 'hillshade',
    source: DEM_SOURCE_ID,
    layout: { visibility: visOf(opts.terrainVisible) },
    paint: def.theme === 'light'
      ? {
          'hillshade-exaggeration': 0.5,
          'hillshade-shadow-color': '#dcd4c8',
          'hillshade-highlight-color': '#fffef9',
          'hillshade-accent-color': '#d0c7b8',
        }
      : {
          'hillshade-exaggeration': 0.45,
          'hillshade-shadow-color': '#0b101d',
          'hillshade-highlight-color': '#7d90b8',
          'hillshade-accent-color': '#243049',
        },
  } as unknown as LayerSpecification);

  // 自然要素面（v0.6.4/#15）：森林/绿地/水面铺在 hillshade 之上（地表
  // 覆盖给地形上色），交通线划/等高线在其上保持可读。
  layers.push(...naturalLayers(opts.overlayVisibility, t));

  // 等高线（W17/#13，自然面之上、交通线划之下）：计曲线（500m 整倍）
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
      paint: { 'line-color': t.contourMajor, 'line-width': 1.1, 'line-opacity': 0.55 },
    },
    {
      id: 'contours-minor',
      type: 'line',
      source: 'contours',
      minzoom: 9.5,
      filter: ['==', ['get', 'major'], false],
      layout: { visibility: contourVis },
      paint: { 'line-color': t.contourMinor, 'line-width': 0.7, 'line-opacity': 0.4 },
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
      paint: { 'text-color': t.contourLabel, 'text-halo-color': t.contourHalo, 'text-halo-width': 1.3 },
    } as unknown as LayerSpecification,
  );

  // 离线交通/区划叠加（自然面之上：路网河流与行政界压住地表色）
  layers.push(...overlayLayers(opts.overlayVisibility, t));

  // 地名标注（在交通线划之上、业务图层之下——干净的"标准地图"层级观感）
  layers.push(...placesLayers(opts.overlayVisibility, t));

  // 真实网络数据（v0.8.0）：光缆走廊 + 基站点（地名之上、业务图层之下）
  layers.push(...realNetworkLayers(opts.overlayVisibility, t));

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
