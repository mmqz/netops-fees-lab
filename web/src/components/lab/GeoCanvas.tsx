/**
 * GeoCanvas —— 画布地理视图（GEO-2）：真实经纬度 2D/3D 地图（maplibre-gl，懒加载）。
 *
 * 在 TopologyCanvas 的视图维度（拓扑图 | 2D 地图 | 3D 地图）中承载后两种：
 * - 设备按 geoLat/geoLng 落位为 HTML Marker（状态色圆点 + 编码牌，
 *   键盘可聚焦、Enter/Space 触发选择，与右侧表格联动语义同拓扑图 tap）；
 *   GCJ-02 底图（高德）渲染前经 projLngLat 投影，WGS84 底图（ESRI/离线）原样；
 * - 链路为 GeoJSON LineString 图层：up #5b8fd9 2px 实线 / down #ff5252 3px
 *   虚线（rAF 驱动 marching-ants 动画，prefers-reduced-motion 关闭）/
 *   选中 #2f9dff 4px；端点缺坐标的链路跳过（右下角计数 chip 提示）；
 * - 3D 示意柱（fill-extrusion，仅 3D 视图可见）：屏幕高 18 + 连接数×10 px
 *   折算米高并钳制 [TOWER_MIN_M, TOWER_MAX_M]（#9：杜绝数十公里光柱；
 *   footprint 恒定 7px 屏宽——metersPerPixel 按 MapLibre 512px 世界坐标
 *   修正，zoom 期间 rAF 节流同帧重建高度与底面）；
 *   2D↔3D 切换 easeTo pitch 0|58（3D 为倾斜相机示意，非地形高程，见
 *   LAB_VIEW_GEO3D_TITLE 与 README「3D 视图定位」）；
 * - 底图四模式可切换（#11，geoBasemap.ts 注册表）：高德街道 / 高德卫星 /
 *   ESRI 极简 / 离线极简；离线叠加图层（行政区划/铁路/公路，黑龙江本地
 *   OSM 数据 © OpenStreetMap contributors，go:embed 随二进制分发）仅在
 *   WGS84 底图下可用；铁路默认关闭（用户要求：不得干扰主观看，可一键开）；
 * - 图层面板（W16「画布决定显示什么」）：底图模式 + 叠加图层 + 业务图层，
 *   偏好 localStorage 持久化（隐私模式静默降级）；
 * - 瓦片连续 ≥3 次失败 → 离线底图降级（重建无栅格样式，叠加图层保留
 *   WGS84 语义 +「离线底图模式」chip）；切换底图即重置失败计数再试；
 * - 全部设备无坐标 → 居中引导 +「返回拓扑图」；
 * - 悬停地标复用画布左上角信息条（onHoverInfo 回调）；
 * - 自带 ResizeObserver → map.resize()；卸载 map.remove() + 取消 rAF，
 *   反复切换视图不泄漏 WebGL 上下文。
 * maplibre-gl 经 `await import(...)` 懒加载（独立异步 chunk，主包不增重）。
 * 调试探针：实例挂 window.__geomap（getPitch/getBearing 等），底图切换/
 * 降级链路挂 window.__geotrace（create/rebuild/map-err/switch-eff 时间线，
 * e2e 验证降级假阳性修复用）。
 */

import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import type { Ref } from 'react';
import type {
  ErrorEvent as MLErrorEvent,
  Map as MLMap,
  MapSourceDataEvent,
  MapStyleDataEvent,
  Marker as MLMarker,
} from 'maplibre-gl';
import type { Feature, FeatureCollection, LineString, Point, Polygon, Position } from 'geojson';
// maplibre 全家（库本体 / 样式表 / worker URL）均不静态 import（W5-D 分包）：
// 地理视图挂载时同点动态 import。
import type { SimEntityStatus, SimTopology } from '../../api/types';
import type { SelectedEntity } from './selection';
import {
  BASEMAP_REF_SOURCE_ID,
  BASEMAP_SOURCE_ID,
  LINK_LAYERS,
  OVERLAY_LAYERS,
  TOWER_MAX_M,
  TOWER_MIN_M,
  basemapIsGCJ,
  buildGeoStyle,
  loadGeoLayerPrefs,
  overlaysAvailable,
  projLngLat,
  saveGeoLayerPrefs,
} from './geoBasemap';
import type { BasemapId, GeoLayerPrefs, OverlayId, StyleOptions } from './geoBasemap';
import {
  LAB_CANVAS_LEGEND_LABEL,
  LAB_GEO_BIZ_DEVICES,
  LAB_GEO_BIZ_DEVICES_TITLE,
  LAB_GEO_BIZ_LINKS,
  LAB_GEO_BIZ_LINKS_TITLE,
  LAB_GEO_BM_AMAP,
  LAB_GEO_BM_AMAP_SAT,
  LAB_GEO_BM_AMAP_SAT_TITLE,
  LAB_GEO_BM_AMAP_TITLE,
  LAB_GEO_BM_ESRI,
  LAB_GEO_BM_ESRI_TITLE,
  LAB_GEO_BM_OFFLINE,
  LAB_GEO_BM_OFFLINE_TITLE,
  LAB_GEO_LEGEND_TOWER,
  LAB_GEO_LAYERS_BASEMAP,
  LAB_GEO_LAYERS_BUSINESS,
  LAB_GEO_LAYERS_LABEL,
  LAB_GEO_LAYERS_OVERLAY,
  LAB_GEO_LAYERS_TOGGLE,
  LAB_GEO_LAYERS_TOGGLE_TITLE,
  LAB_GEO_MAP_LABEL,
  LAB_GEO_MARKER_LABEL,
  LAB_GEO_NOGEO,
  LAB_GEO_NOGEO_BACK,
  LAB_GEO_OFFLINE,
  LAB_GEO_OFFLINE_TITLE,
  LAB_GEO_OV_ADMIN,
  LAB_GEO_OV_ADMIN_TITLE,
  LAB_GEO_OV_RAIL,
  LAB_GEO_OV_RAIL_TITLE,
  LAB_GEO_OV_ROAD,
  LAB_GEO_OV_ROAD_TITLE,
  LAB_GEO_OV_UNAVAILABLE,
  LAB_GEO_PARTIAL,
  LAB_GEO_PARTIAL_TITLE,
  LAB_LEGEND_DEVICE_DOWN,
  LAB_LEGEND_DEVICE_UP,
  LAB_LEGEND_LINK_DOWN,
  LAB_LEGEND_LINK_UP,
  LOADING_TEXT,
  SIM_STATUS_LABELS,
  format,
} from '../../i18n';

// —— 常量（与规格及 global.css 同步） ——

/** 3D 示意柱参数：footprint 恒定屏幕宽 7px（直径语义；buildTowerCollection
 *  按 MapLibre 512px 世界坐标的 metersPerPixel 换算半宽，zoom 期间 rAF 重建，
 *  #9 修复：v0.6.0 用 256px 基准公式导致宽度系统性放大 ~2× 且随缩放漂移）。 */
const TOWER_FOOTPRINT_PX = 7;
const TOWER_PX_BASE = 18;
const TOWER_PX_PER_DEGREE = 10;

/** 2D/3D 相机：俯仰角与 3D 斜视方位角；过渡时长（reduced-motion 时为 0）。 */
const PITCH_2D = 0;
const PITCH_3D = 58;
const BEARING_3D = -30;
const CAMERA_TRANSITION_MS = 700;

/** fitBounds 留白与缩放上限（首个数据批次与「适配视图」共用）。 */
const FIT_PADDING_PX = 80;
const FIT_MAX_ZOOM = 9;
/** 3D 斜视的 zoom 上限（单点场景 / 相机解的近端夹紧）。 */
const FIT3D_MAX_ZOOM = 7.2;
/** 3D 斜视补偿级数：俯视解 zoom − 1.3 = 斜视等效覆盖（58° 倾角垂直压缩 +
 *  底部地面余量）；也用于 2D↔3D 切换时的 zoom 补偿。 */
const ZOOM_3D_STEP_BACK = 1.3;

/** 初始相机（数据到达前）：中国中心，等待 fitBounds。 */
const DEFAULT_CENTER: [number, number] = [105, 36];
const DEFAULT_ZOOM = 3.2;

/** 底图瓦片连续失败阈值 → 离线降级看门狗触发条件之一。 */
const TILE_FAIL_LIMIT = 3;
/** 降级看门狗窗口（毫秒）：距「最近一次底图成功数据 or 样式重建」≥ 此值
 *  且连续失败 ≥ TILE_FAIL_LIMIT 才降级。双保险：
 *  - 相机飞动会取消在途瓦片，MapLibre 对取消也发 AJAXError: Failed to fetch (0)
 *    （与真实断网无法从消息区分）——飞行后的新瓦片 ~1s 内成功 → 计数清零；
 *  - 错误波可能停在宽限期内不再重发（实测：初始 6 错误后无新错误）——
 *    看门狗周期复查，覆盖「错误已停但底图从未成功」的停滞态。 */
const TILE_FAIL_WINDOW_MS = 4000;
/** 看门狗复查间隔。 */
const TILE_WATCHDOG_MS = 1000;

/** 故障链路虚线参数（marching-ants 帧间隔与帧序列）。 */
const DASH_ON = 2;
const DASH_OFF = 1.5;
const DASH_FRAME_MS = 80;

/**
 * marching-ants 帧序列：每帧以 [0, shift, ON, OFF, ON, P-shift] 构成等长模式，
 * shift 递增使虚线整体向线起点方向滑动（循环周期 = ON+OFF）。
 * 规避 0/0 与负值条目（maplibre dasharray 校验）。
 */
const DASH_FRAMES: number[][] = (() => {
  const period = DASH_ON + DASH_OFF;
  const steps = 12;
  const frames: number[][] = [];
  for (let i = 0; i < steps; i++) {
    const shift = (period * (i + 0.5)) / steps;
    frames.push([0, shift, DASH_ON, DASH_OFF, DASH_ON, period - shift]);
  }
  return frames;
})();

/** prefers-reduced-motion（与 TopologyCanvas 同一判定，模块内各自持有）。 */
function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

// —— GeoJSON 数据构建（纯函数，选中态直接写入 feature properties → setData 更新） ——

interface GeoDeviceProps {
  id: string;
  code: string;
  status: SimEntityStatus;
  selected: boolean;
}

interface GeoLinkProps {
  id: string;
  label: string;
  status: SimEntityStatus;
  selected: boolean;
}

interface GeoTowerProps {
  id: string;
  status: SimEntityStatus;
  /** 3D 柱目标屏幕像素高：18 + 连接数 × 10。 */
  towerPx: number;
  /** 柱高（米）：towerPx × 每像素米数，钳制 [TOWER_MIN_M, TOWER_MAX_M]。
   *  每次塔重建（zoom 变化，rAF 节流）时按当前 zoom 重算。 */
  heightM: number;
}

export interface GeoData {
  devices: FeatureCollection<Point, GeoDeviceProps>;
  links: FeatureCollection<LineString, GeoLinkProps>;
  towers: FeatureCollection<Polygon, GeoTowerProps>;
  /** 有坐标的设备数。 */
  deviceCount: number;
  /** 无坐标的设备数（部分缺坐标 chip）。 */
  missingCount: number;
  /** 端点缺坐标而被跳过的链路数。 */
  skippedLinks: number;
  /** down 链路数（虚线动画仅在 >0 时运行）。 */
  downLinks: number;
  /** 设备包围盒（fitBounds 用，已按当前底图坐标系投影）；无设备为 null。 */
  bbox: [[number, number], [number, number]] | null;
}

/** 地图地标（HTML Marker）持有项。 */
interface MarkerEntry {
  marker: MLMarker;
  btn: HTMLButtonElement;
}

/** 地图懒加载后的模块命名空间。 */
type MapLibreModule = typeof import('maplibre-gl');

/** geojson source 的最小结构（maplibre 未导出 GeoJSONSource 类型）。 */
interface GeoJSONSourceLike {
  setData(data: FeatureCollection): unknown;
}

/** hover 信息条载荷（与拓扑画布 HoverInfo 结构一致）。 */
export interface GeoHoverInfo {
  code: string;
  name: string;
  status: SimEntityStatus;
}

function towerPolygon(lng: number, lat: number, halfLng: number, halfLat: number): Position[][] {
  return [
    [
      [lng - halfLng, lat - halfLat],
      [lng + halfLng, lat - halfLat],
      [lng + halfLng, lat + halfLat],
      [lng - halfLng, lat + halfLat],
      [lng - halfLng, lat - halfLat],
    ],
  ];
}

/**
 * zoom 下某纬度每屏幕像素的米数（Web 墨卡托，#9 修正）。
 * MapLibre 世界坐标 z0 世界宽 512px（transform.worldSize = 512·2^zoom），
 * 每像素米数 = 赤道周长 × cos(lat) / (512·2^z) = 78271.517·cos(lat)/2^z。
 * v0.6.0 用 156543.03（256px 瓦片基准）→ 系统性偏大约 2×，footprint
 * 随缩放漂移最多 11 倍（issue #9 实测 19→217px）。
 */
export function metersPerPixel(lat: number, zoom: number): number {
  return (78271.51696 * Math.cos((lat * Math.PI) / 180)) / Math.pow(2, zoom);
}

export function buildGeoData(
  topology: SimTopology | null,
  selected: SelectedEntity | null,
  toGCJ: boolean,
): GeoData {
  const deviceFeatures: Array<Feature<Point, GeoDeviceProps>> = [];
  const linkFeatures: Array<Feature<LineString, GeoLinkProps>> = [];
  const coords = new Map<string, [number, number]>();
  let missingCount = 0;
  let downLinks = 0;

  if (topology !== null) {
    for (const node of topology.nodes) {
      const lat = node.geoLat;
      const lng = node.geoLng;
      if (lat === undefined || lng === undefined || !Number.isFinite(lat) || !Number.isFinite(lng)) {
        missingCount += 1;
        continue;
      }
      const pos: [number, number] = projLngLat(lng, lat, toGCJ);
      coords.set(node.id, pos);
      deviceFeatures.push({
        type: 'Feature',
        id: node.id,
        geometry: { type: 'Point', coordinates: pos },
        properties: {
          id: node.id,
          code: node.code,
          status: node.status,
          selected: selected?.kind === 'device' && selected.id === node.id,
        },
      });
    }
    for (const edge of topology.edges) {
      const a = coords.get(edge.aDeviceId);
      const z = coords.get(edge.zDeviceId);
      if (a === undefined || z === undefined) continue; // 端点缺坐标：跳过（skippedLinks 计数）
      if (edge.status === 'down') downLinks += 1;
      linkFeatures.push({
        type: 'Feature',
        id: edge.id,
        geometry: { type: 'LineString', coordinates: [a, z] },
        properties: {
          id: edge.id,
          label: edge.label,
          status: edge.status,
          selected: selected?.kind === 'link' && selected.id === edge.id,
        },
      });
    }
  }

  let skippedLinks = 0;
  if (topology !== null) {
    for (const edge of topology.edges) {
      if (!coords.has(edge.aDeviceId) || !coords.has(edge.zDeviceId)) skippedLinks += 1;
    }
  }

  let bbox: GeoData['bbox'] = null;
  if (coords.size > 0) {
    let minLng = Number.POSITIVE_INFINITY;
    let minLat = Number.POSITIVE_INFINITY;
    let maxLng = Number.NEGATIVE_INFINITY;
    let maxLat = Number.NEGATIVE_INFINITY;
    for (const [lng, lat] of coords.values()) {
      minLng = Math.min(minLng, lng);
      minLat = Math.min(minLat, lat);
      maxLng = Math.max(maxLng, lng);
      maxLat = Math.max(maxLat, lat);
    }
    bbox = [
      [minLng, minLat],
      [maxLng, maxLat],
    ];
  }

  return {
    devices: { type: 'FeatureCollection', features: deviceFeatures },
    links: { type: 'FeatureCollection', features: linkFeatures },
    // 塔不在此构建：footprint/heightM 需按当前 zoom 换算，由
    // buildTowerCollection + zoom rAF 重建路径单独供给（首次渲染前为空集）。
    towers: emptyFC() as FeatureCollection<Polygon, GeoTowerProps>,
    deviceCount: deviceFeatures.length,
    missingCount,
    skippedLinks,
    downLinks,
    bbox,
  };
}

/**
 * 塔集合（纯函数）：塔与设备一一对应。footprint 按当前 zoom 恒定 7px 屏宽
 * （直径语义，512px 世界坐标 mpp），屏幕高 18 + 连接数×10 px 折算米高并
 * 钳制 [TOWER_MIN_M, TOWER_MAX_M]（#9：缩小时不再出现数十公里光柱）。
 * zoom 变化（含动画期间，rAF 节流）后重建。
 */
export function buildTowerCollection(
  topology: SimTopology | null,
  zoom: number,
  toGCJ: boolean,
): FeatureCollection<Polygon, GeoTowerProps> {
  const features: Array<Feature<Polygon, GeoTowerProps>> = [];
  if (topology === null) return { type: 'FeatureCollection', features };
  const degree = new Map<string, number>();
  for (const edge of topology.edges) {
    degree.set(edge.aDeviceId, (degree.get(edge.aDeviceId) ?? 0) + 1);
    degree.set(edge.zDeviceId, (degree.get(edge.zDeviceId) ?? 0) + 1);
  }
  for (const node of topology.nodes) {
    const lat = node.geoLat;
    const lng = node.geoLng;
    if (lat === undefined || lng === undefined || !Number.isFinite(lat) || !Number.isFinite(lng)) {
      continue;
    }
    const cosLat = Math.max(0.2, Math.cos((lat * Math.PI) / 180));
    const mpp = metersPerPixel(lat, zoom);
    const halfLat = ((TOWER_FOOTPRINT_PX / 2) * mpp) / 111320;
    const halfLng = ((TOWER_FOOTPRINT_PX / 2) * mpp) / (111320 * cosLat);
    const towerPx = TOWER_PX_BASE + (degree.get(node.id) ?? 0) * TOWER_PX_PER_DEGREE;
    const heightM = Math.min(TOWER_MAX_M, Math.max(TOWER_MIN_M, towerPx * mpp));
    const [plng, plat] = projLngLat(lng, lat, toGCJ);
    features.push({
      type: 'Feature',
      id: node.id,
      geometry: { type: 'Polygon', coordinates: towerPolygon(plng, plat, halfLng, halfLat) },
      properties: { id: node.id, status: node.status, towerPx, heightM },
    });
  }
  return { type: 'FeatureCollection', features };
}

// —— 小工具 ——

function emptyFC(): FeatureCollection {
  return { type: 'FeatureCollection', features: [] };
}

function applyDataToMap(map: MLMap, data: GeoData): void {
  // 塔不在此处供给（geoData.towers 恒为空集占位）：footprint/heightM 依赖
  // 当前 zoom，由数据 effect 与 zoom rAF 重建路径单独 setData，否则会被清空。
  const entries: Array<[string, FeatureCollection]> = [
    ['devices', data.devices],
    ['links', data.links],
  ];
  for (const [sourceId, fc] of entries) {
    const src = map.getSource(sourceId) as GeoJSONSourceLike | undefined;
    if (src !== undefined) src.setData(fc);
  }
}

function applyTowerVisibility(map: MLMap, view: 'geo2d' | 'geo3d'): void {
  if (map.getLayer('towers') !== undefined) {
    map.setLayoutProperty('towers', 'visibility', view === 'geo3d' ? 'visible' : 'none');
  }
}

/** 叠加/业务图层可见性批量应用（叠加仅在 WGS84 底图下放行）。 */
function applyLayerVisibilities(map: MLMap, prefs: GeoLayerPrefs, effective: BasemapId): void {
  const overlaysOn = overlaysAvailable(effective);
  for (const id of OVERLAY_LAYERS.admin) {
    if (map.getLayer(id) !== undefined) {
      map.setLayoutProperty(id, 'visibility', prefs.overlays.admin && overlaysOn ? 'visible' : 'none');
    }
  }
  for (const id of OVERLAY_LAYERS.railway) {
    if (map.getLayer(id) !== undefined) {
      map.setLayoutProperty(id, 'visibility', prefs.overlays.railway && overlaysOn ? 'visible' : 'none');
    }
  }
  for (const id of OVERLAY_LAYERS.road) {
    if (map.getLayer(id) !== undefined) {
      map.setLayoutProperty(id, 'visibility', prefs.overlays.road && overlaysOn ? 'visible' : 'none');
    }
  }
  const linkVis = prefs.links ? 'visible' : 'none';
  for (const id of LINK_LAYERS) {
    if (map.getLayer(id) !== undefined) map.setLayoutProperty(id, 'visibility', linkVis);
  }
}

/**
 * fitGeoBounds：按**目标视图**适配相机（v0.3.1）。
 *
 * 2D：沿用 v0.3.0 的 fitBounds（平视假设正确，行为已验收）。
 *
 * 3D：不用 MapLibre 的 fitBounds/cameraForBounds——它们的「恰好容纳」解在
 * 大倾角下过于保守，而 2D 假设的解在斜视下又怼脸。正解是墨卡托几何直解
 * （zoomForBBox3D）。
 */
function fitGeoBounds(map: MLMap, data: GeoData, view: 'geo2d' | 'geo3d'): void {
  if (data.bbox === null) return;
  const duration = prefersReducedMotion() ? 0 : 600;
  const center: [number, number] = [
    (data.bbox[0][0] + data.bbox[1][0]) / 2,
    (data.bbox[0][1] + data.bbox[1][1]) / 2,
  ];
  if (view === 'geo3d') {
    if (data.deviceCount === 1) {
      map.easeTo({ center, zoom: FIT3D_MAX_ZOOM, pitch: PITCH_3D, bearing: BEARING_3D, duration });
      return;
    }
    const rect = map.getContainer().getBoundingClientRect();
    const zoom = zoomForBBox3D(data.bbox, rect.width, rect.height);
    const latSpan = data.bbox[1][1] - data.bbox[0][1];
    map.easeTo({
      center: [center[0], center[1] + latSpan * 0.18],
      zoom, pitch: PITCH_3D, bearing: BEARING_3D, duration,
    });
    return;
  }
  if (data.deviceCount === 1) {
    map.easeTo({ center, zoom: FIT_MAX_ZOOM, duration });
    return;
  }
  map.fitBounds(data.bbox, { padding: FIT_PADDING_PX, maxZoom: FIT_MAX_ZOOM, duration });
}

const TOWER_FIT_W3D = 0.55;
const TOWER_FIT_H3D = 0.3;
function zoomForBBox3D(bbox: [[number, number], [number, number]], wPx: number, hPx: number): number {
  const rad = Math.PI / 180;
  const mercY = (lat: number): number => Math.log(Math.tan(Math.PI / 4 + (lat * rad) / 2));
  const bw = Math.max((bbox[1][0] - bbox[0][0]) * rad, 1e-4);
  const bh = Math.max(Math.abs(mercY(bbox[1][1]) - mercY(bbox[0][1])), 1e-4);
  const w = Math.max(wPx, 120);
  const h = Math.max(hPx, 90);
  const zw = Math.log2((w * 2 * Math.PI) / (256 * TOWER_FIT_W3D * bw));
  const zh = Math.log2((h * 2 * Math.PI) / (256 * TOWER_FIT_H3D * bh));
  return Math.min(FIT3D_MAX_ZOOM, Math.max(3, Math.min(zw, zh)));
}

/**
 * 底图瓦片错误判定：tile 失败事件运行时携带 tile 数据（当前样式瓦片源均
 * 为底图；本地 GeoJSON 不产瓦片错误），另兜底匹配 sourceId / 消息中的源名
 * 或瓦片域名（arcgisonline / autonavi）。
 */
function isBasemapError(ev: MLErrorEvent): boolean {
  const rec = ev as unknown as Record<string, unknown>;
  if (rec['sourceId'] === BASEMAP_SOURCE_ID || rec['source'] === BASEMAP_SOURCE_ID) return true;
  if (rec['tile'] !== undefined) return true;
  const data = rec['data'];
  if (typeof data === 'object' && data !== null && (data as Record<string, unknown>)['tile'] !== undefined) {
    return true;
  }
  const err = rec['error'];
  const msg =
    typeof err === 'object' && err !== null && 'message' in err
      ? String((err as Record<string, unknown>)['message'])
      : String(err ?? '');
  return msg.includes(BASEMAP_SOURCE_ID) || msg.includes('cartocdn') || msg.includes('arcgisonline') || msg.includes('autonavi');
}

// —— 组件 ——

/** TopologyCanvas 经 ref 持有的命令句柄（工具带「适配视图 / 指北复位」与 F 键）。 */
export interface GeoCanvasHandle {
  fitView: () => void;
  resetNorth: () => void;
}

export interface GeoCanvasProps {
  topology: SimTopology | null;
  selected: SelectedEntity | null;
  /** 表格↔画布双向选中联动（与拓扑视图 tap 同一回调）。 */
  onSelect: (entity: SelectedEntity) => void;
  /** 当前地图视图：geo2d（俯视）| geo3d（斜视 58°）。 */
  view: 'geo2d' | 'geo3d';
  /** 设备标签开关（共享画布 prefs.labels，L 键统一切换）。 */
  labels: boolean;
  /** zen 全屏镜像（尺寸变化由自身 ResizeObserver 感知，这里仅语义对齐父级状态）。 */
  zen?: boolean;
  /** 分栏拖拽进行中 → 挂起逐帧 resize（拖完由补偿 effect 补一次）。 */
  resizeSuspended?: boolean;
  /** hover 信息条回调（复用拓扑画布左上角悬浮信息条）。 */
  onHoverInfo?: (info: GeoHoverInfo | null) => void;
  /** 全部设备无坐标时的「返回拓扑图」。 */
  onBackToTopo?: () => void;
}

export const GeoCanvas = forwardRef<GeoCanvasHandle, GeoCanvasProps>(function GeoCanvas(
  { topology, selected, onSelect, view, labels, zen = false, resizeSuspended = false, onHoverInfo, onBackToTopo },
  ref: Ref<GeoCanvasHandle>,
) {
  const mapDivRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<MLMap | null>(null);
  const libRef = useRef<MapLibreModule | null>(null);
  const markersRef = useRef<Map<string, MarkerEntry>>(new Map());
  const geoDataRef = useRef<GeoData | null>(null);
  const topoRef = useRef<SimTopology | null>(topology);
  const didFitRef = useRef(false);
  const onSelectRef = useRef(onSelect);
  const onHoverInfoRef = useRef(onHoverInfo);
  const selectedRef = useRef(selected);
  const labelsRef = useRef(labels);
  const viewRef = useRef(view);
  topoRef.current = topology;
  const resizeSuspendedRef = useRef(resizeSuspended);

  // —— W16：底图/图层偏好（localStorage 持久化）与瓦片降级态 ——
  const [prefs, setPrefs] = useState<GeoLayerPrefs>(() => loadGeoLayerPrefs());
  const prefsRef = useRef(prefs);
  prefsRef.current = prefs;
  /** 底图瓦片连续失败 → 强制离线降级（不改用户偏好，切底图即重置）。 */
  const [tileFallback, setTileFallback] = useState(false);
  const tileFallbackRef = useRef(false);
  /** 当前实际渲染的底图（降级时为 offline）。 */
  const effective: BasemapId = tileFallback ? 'offline' : prefs.basemap;
  const effectiveRef = useRef<BasemapId>(effective);
  effectiveRef.current = effective;
  const gcj = basemapIsGCJ(effective);
  const gcjRef = useRef(gcj);
  gcjRef.current = gcj;
  /** 图层面板开合（会话内状态，不持久化）。 */
  const [panelOpen, setPanelOpen] = useState(false);

  /** style 的 geojson source 是否就绪（初始加载 / setStyle 重建后置 true）。 */
  const [sourcesReady, setSourcesReady] = useState(false);

  useEffect(() => {
    saveGeoLayerPrefs(prefs);
  }, [prefs]);

  useEffect(() => {
    onSelectRef.current = onSelect;
  }, [onSelect]);

  // —— 实例生命周期：挂载即懒加载 maplibre → 建图 → 控件/事件；卸载全量回收 ——
  /** 样式重建（底图切换/离线降级）：setStyle + 一次性 styledata 回填。 */
  const rebuildStyleRef = useRef<((id: BasemapId) => void) | null>(null);
  /** 底图切换去重（初始样式在建图时已按当值构建）。 */
  const styleKeyRef = useRef<string | null>(null);
  /** 瓦片失败计数（挂载 effect 与切底图 handler 共享）。 */
  const tileFailStreakRef = useRef(0);

  useEffect(() => {
    onHoverInfoRef.current = onHoverInfo;
  }, [onHoverInfo]);

  useEffect(() => {
    selectedRef.current = selected;
  }, [selected]);

  useEffect(() => {
    labelsRef.current = labels;
  }, [labels]);

  useEffect(() => {
    viewRef.current = view;
  }, [view]);

  useEffect(() => {
    resizeSuspendedRef.current = resizeSuspended;
  }, [resizeSuspended]);

  const geoData = useMemo(() => buildGeoData(topology, selected, gcj), [topology, selected, gcj]);

  useEffect(() => {
    geoDataRef.current = geoData;
  }, [geoData]);

  // —— 塔重建（#9）：zoom 动画期间 rAF 节流同帧重建 footprint + heightM ——
  const scheduleTowerRebuildRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    const div = mapDivRef.current;
    if (div === null) return undefined;
    let disposed = false;
    let observer: ResizeObserver | null = null;
    let localMap: MLMap | null = null;
    const cleanups: Array<() => void> = [];

    void (async (): Promise<void> => {
      // 同点懒加载库 / 样式 / worker URL（W5-D 分包：maplibre 家族全部按需，
      // 入口对 vendor-maplibre 零静态依赖）。
      const [maplibregl, , workerUrlMod] = await Promise.all([
        import('maplibre-gl'),
        import('maplibre-gl/dist/maplibre-gl.css'),
        import('maplibre-gl/dist/maplibre-gl-worker.mjs?url'),
      ]);
      if (disposed || mapDivRef.current !== div) return;
      libRef.current = maplibregl;
      // 必须在建图之前：worker URL 默认按 import.meta.url 推导（打包后指向不存在的
      // 未哈希文件名 → Worker 加载失败）。?url 资源地址在构建期注入，恒有效。
      maplibregl.setWorkerUrl(workerUrlMod.default);
      const styleOpts = (id: BasemapId): StyleOptions => ({
        withRaster: true,
        overlayVisibility: {
          admin: prefsRef.current.overlays.admin && overlaysAvailable(id),
          railway: prefsRef.current.overlays.railway && overlaysAvailable(id),
          road: prefsRef.current.overlays.road && overlaysAvailable(id),
        },
        linksVisible: prefsRef.current.links,
      });
      const map = new maplibregl.Map({
        container: div,
        style: buildGeoStyle(effectiveRef.current, styleOpts(effectiveRef.current)),
        center: DEFAULT_CENTER,
        zoom: DEFAULT_ZOOM,
        // 初始 pitch/bearing 按目标视图设置（v0.3.1）：否则「挂载即 3D」时
        // view effect 会发 easeTo 纠正 pitch，打断首批 fitBounds 动画。
        pitch: viewRef.current === 'geo3d' ? PITCH_3D : PITCH_2D,
        bearing: viewRef.current === 'geo3d' ? BEARING_3D : 0,
        attributionControl: { compact: true },
      });
      localMap = map;
      mapRef.current = map;
      styleKeyRef.current = effectiveRef.current;
      // W16 调试探针：底图切换/降级全链路（浏览器 e2e 用，正式版移除）
      (window as unknown as Record<string, unknown>).__geotrace = [
        { t: 'create', id: effectiveRef.current, prefs: prefsRef.current.basemap },
      ];
      // e2e 调试探针（getPitch / getBearing / getLayoutProperty 断言用）
      (window as unknown as Record<string, unknown>).__geomap = map;

      map.addControl(new maplibregl.NavigationControl({ visualizePitch: true }), 'bottom-right');
      map.addControl(new maplibregl.ScaleControl({ unit: 'metric' }), 'bottom-left');

      // 样式重建公共路径：setStyle → 等业务 source 就绪 → 回填数据/塔/可见性
      const rebuildStyle = (id: BasemapId): void => {
        (window as unknown as { __geotrace?: Array<Record<string, unknown>> }).__geotrace?.push({ t: 'rebuild', id });
        try {
          map.setStyle(buildGeoStyle(id, styleOpts(id)));
        } catch {
          return; // setStyle 异常：保留当前样式（本地数据层未受影响）
        }
        // styleKey 事实源统一在此维护：离线降级与切换路径共用，保证
        // 「降级后切回同一底图」也能重建（否则 styleKey 恒为旧值 → 死锁）。
        styleKeyRef.current = id;
        styleSetAt = Date.now();
        tileFailStreakRef.current = 0;
        const reapply = (): void => {
          if (disposed || map.getSource('devices') === undefined) return;
          map.off('styledata', reapply);
          if (geoDataRef.current !== null) applyDataToMap(map, geoDataRef.current);
          const topo = topoRef.current;
          const tsrc = map.getSource('towers') as GeoJSONSourceLike | undefined;
          if (topo !== null && tsrc !== undefined) tsrc.setData(buildTowerCollection(topo, map.getZoom(), gcjRef.current));
          applyTowerVisibility(map, viewRef.current);
          applyLayerVisibilities(map, prefsRef.current, effectiveRef.current);
        };
        map.on('styledata', reapply);
        cleanups.push(() => map.off('styledata', reapply));
      };
      rebuildStyleRef.current = rebuildStyle;

      // style 的 geojson source 就绪门（初始与 setStyle 重建后都经此置位）
      const onStyleData = (): void => {
        if (map.getSource('devices') === undefined) return;
        map.off('styledata', onStyleData);
        if (!disposed) setSourcesReady(true);
      };
      map.on('styledata', onStyleData);
      cleanups.push(() => map.off('styledata', onStyleData));
      onStyleData();

      // 底图瓦片失败看门狗：连续 ≥3 失败 且 距最近成功/样式重建 ≥ 窗口 →
      // 离线底图降级（本地数据层恒可渲染，叠加图层保留 WGS84 语义——offline
      // 底图下区划/铁路/公路照常显示）。onMapError 只计数，判定统一在看门狗
      // 周期里做（见 TILE_FAIL_WINDOW_MS 注释的双保险 rationale）。
      let lastBasemapDataAt = 0;
      let styleSetAt = Date.now();
      const fallback = (): void => {
        if (disposed || tileFallbackRef.current) return;
        tileFallbackRef.current = true;
        setTileFallback(true);
        rebuildStyle('offline');
      };
      const onMapError = (ev: MLErrorEvent): void => {
        if (disposed || tileFallbackRef.current || !isBasemapError(ev)) return;
        tileFailStreakRef.current += 1;
      };
      const onMapData = (ev: MapSourceDataEvent | MapStyleDataEvent): void => {
        const sid = (ev as MapSourceDataEvent).sourceId;
        if (sid === BASEMAP_SOURCE_ID || sid === BASEMAP_REF_SOURCE_ID) {
          tileFailStreakRef.current = 0;
          lastBasemapDataAt = Date.now();
        }
      };
      const watchdog = window.setInterval((): void => {
        if (disposed || tileFallbackRef.current) return;
        if (tileFailStreakRef.current < TILE_FAIL_LIMIT) return;
        const since = Math.max(lastBasemapDataAt, styleSetAt);
        if (Date.now() - since < TILE_FAIL_WINDOW_MS) return;
        fallback();
      }, TILE_WATCHDOG_MS);
      map.on('error', onMapError);
      map.on('data', onMapData);
      cleanups.push(() => {
        map.off('error', onMapError);
        map.off('data', onMapData);
        window.clearInterval(watchdog);
      });

      // —— 塔重建（#9）：zoom 动画期间 rAF 节流（缩放中 footprint/heightM
      // 每帧跟手，恒定 7px 屏宽与钳制高度不再「动画结束才跳变」）——
      let towerRaf = 0;
      const rebuildTowersNow = (): void => {
        if (disposed) return;
        const topo = topoRef.current;
        const src = map.getSource('towers') as GeoJSONSourceLike | undefined;
        if (topo !== null && src !== undefined) src.setData(buildTowerCollection(topo, map.getZoom(), gcjRef.current));
      };
      const scheduleTowerRebuild = (): void => {
        if (disposed || towerRaf !== 0) return;
        towerRaf = requestAnimationFrame(() => {
          towerRaf = 0;
          rebuildTowersNow();
        });
      };
      scheduleTowerRebuildRef.current = scheduleTowerRebuild;
      map.on('zoom', scheduleTowerRebuild);
      map.on('zoomend', scheduleTowerRebuild);
      cleanups.push(() => {
        map.off('zoom', scheduleTowerRebuild);
        map.off('zoomend', scheduleTowerRebuild);
        if (towerRaf !== 0) cancelAnimationFrame(towerRaf);
        scheduleTowerRebuildRef.current = null;
      });

      // 容器尺寸变化 → map.resize（zen / 分栏 / 视口变化；拖拽中由父级挂起）
      if (typeof ResizeObserver !== 'undefined') {
        observer = new ResizeObserver(() => {
          if (resizeSuspendedRef.current || disposed) return;
          map.resize();
        });
        observer.observe(div);
      }
    })();

    return () => {
      disposed = true;
      observer?.disconnect();
      for (const fn of cleanups) fn();
      markersRef.current.forEach((entry) => {
        entry.marker.remove();
      });
      markersRef.current.clear();
      localMap?.remove();
      mapRef.current = null;
      libRef.current = null;
      rebuildStyleRef.current = null;
      styleKeyRef.current = null;
      tileFallbackRef.current = false;
      tileFailStreakRef.current = 0;
      didFitRef.current = false;
      if ((window as unknown as Record<string, unknown>).__geomap === localMap) {
        delete (window as unknown as Record<string, unknown>).__geomap;
      }
      // 卸载时收掉悬停信息条（避免信息条残留在拓扑视图上）
      onHoverInfoRef.current?.(null);
    };
  }, []);

  // —— 底图切换（#11）：setStyle 重建 + 相机保持（setStyle 不动 center/zoom/pitch）——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    (window as unknown as { __geotrace?: Array<Record<string, unknown>> }).__geotrace?.push({
      t: 'switch-eff',
      effective,
      styleKey: styleKeyRef.current,
    });
    if (styleKeyRef.current === effective) return;
    rebuildStyleRef.current?.(effective);
  }, [effective, sourcesReady]);

  // —— 偏好变化 → 叠加/业务图层可见性（setStyle 重建路径已按当值烘焙，此处覆盖
  //    prefs 微调（不换底图）的即时生效）——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    applyLayerVisibilities(map, prefs, effective);
  }, [prefs, effective, sourcesReady]);

  // —— 数据 → GeoJSON source（setData 原地更新，相机保持；首批数据 fitBounds）——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    geoDataRef.current = geoData;
    applyDataToMap(map, geoData);
    // 塔依赖当前 zoom：单独供给（zoom rAF 重建路径见挂载 effect）
    const tsrc = map.getSource('towers') as GeoJSONSourceLike | undefined;
    if (tsrc !== undefined) tsrc.setData(buildTowerCollection(topology, map.getZoom(), gcj));
    if (!didFitRef.current && geoData.deviceCount > 0) {
      didFitRef.current = true;
      fitGeoBounds(map, geoData, view);
    }
  }, [geoData, sourcesReady, topology, view, gcj]);

  // —— 底图坐标系切换（WGS84↔GCJ-02）→ 塔体同帧重投影 ——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    scheduleTowerRebuildRef.current?.();
  }, [gcj, sourcesReady]);

  // —— 地标（HTML Marker）：设备增删/状态/坐标系变化重建；选中与标签走类切换 ——
  const rebuildMarkers = useCallback((): void => {
    const map = mapRef.current;
    const lib = libRef.current;
    if (map === null || lib === null) return;
    markersRef.current.forEach((entry) => {
      entry.marker.remove();
    });
    markersRef.current.clear();
    if (topology === null) return;
    const selectedId = selectedRef.current?.kind === 'device' ? selectedRef.current.id : null;
    const showLabels = labelsRef.current;
    const devicesVisible = prefsRef.current.devices;
    const toGCJ = gcjRef.current;
    for (const node of topology.nodes) {
      const lat = node.geoLat;
      const lng = node.geoLng;
      if (lat === undefined || lng === undefined || !Number.isFinite(lat) || !Number.isFinite(lng)) continue;
      // 外层 div 由 maplibre 定位（anchor center，transform 由其管理）；
      // 内层 button 承载视觉与交互（原生 role=button，键盘 Enter/Space 触发选择）
      const outer = document.createElement('div');
      if (!devicesVisible) outer.style.display = 'none';
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.tabIndex = 0;
      btn.className = 'geo-marker';
      if (node.status === 'down') btn.classList.add('geo-marker--down');
      if (selectedId === node.id) btn.classList.add('geo-marker--selected');
      if (!showLabels) btn.classList.add('geo-marker--nolabel');
      const dot = document.createElement('span');
      dot.className = 'geo-marker-dot';
      dot.setAttribute('aria-hidden', 'true');
      const code = document.createElement('span');
      code.className = 'geo-marker-code';
      code.textContent = node.code;
      btn.appendChild(dot);
      btn.appendChild(code);
      btn.setAttribute(
        'aria-label',
        format(LAB_GEO_MARKER_LABEL, { code: node.code, name: node.name, status: SIM_STATUS_LABELS[node.status] }),
      );
      const hoverIn = (): void => {
        onHoverInfoRef.current?.({ code: node.code, name: node.name, status: node.status });
      };
      const hoverOut = (): void => {
        onHoverInfoRef.current?.(null);
      };
      btn.addEventListener('click', () => {
        onSelectRef.current({ kind: 'device', id: node.id });
      });
      btn.addEventListener('mouseenter', hoverIn);
      btn.addEventListener('mouseleave', hoverOut);
      btn.addEventListener('focus', hoverIn);
      btn.addEventListener('blur', hoverOut);
      outer.appendChild(btn);
      const marker = new lib.Marker({ element: outer, anchor: 'center' })
        .setLngLat(projLngLat(lng, lat, toGCJ))
        .addTo(map);
      markersRef.current.set(node.id, { marker, btn });
    }
  }, [topology, gcj]);

  useEffect(() => {
    if (!sourcesReady) return;
    rebuildMarkers();
  }, [topology, sourcesReady, rebuildMarkers, gcj]);

  // 选中 / 标签开关 / 设备可见性 → 地标类与显隐切换
  useEffect(() => {
    const selectedId = selected?.kind === 'device' ? selected.id : null;
    markersRef.current.forEach((entry, id) => {
      entry.btn.classList.toggle('geo-marker--selected', selectedId === id);
      entry.btn.classList.toggle('geo-marker--nolabel', !labels);
      const el = entry.marker.getElement();
      el.style.display = prefs.devices ? '' : 'none';
    });
  }, [selected, labels, prefs.devices, topology]);

  // —— 2D↔3D：pitch 0|58 + 3D 斜视 bearing -30（easeTo 700ms；reduced-motion 0）——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    applyTowerVisibility(map, view);
    const pitch = view === 'geo3d' ? PITCH_3D : PITCH_2D;
    const bearing = view === 'geo3d' ? BEARING_3D : 0;
    if (Math.abs(map.getPitch() - pitch) < 0.01 && Math.abs(map.getBearing() - bearing) < 0.01) return;
    // zoom 补偿（v0.3.1）：58° 斜视放大近景、等效覆盖缩小——进 3D 后退
    // ZOOM_3D_STEP_BACK 级、回 2D 还原，让切换前后「看到的范围」保持一致。
    const zoom = Math.min(15, Math.max(1, map.getZoom() + (view === 'geo3d' ? -ZOOM_3D_STEP_BACK : ZOOM_3D_STEP_BACK)));
    map.easeTo({ pitch, bearing, zoom, duration: prefersReducedMotion() ? 0 : CAMERA_TRANSITION_MS });
  }, [view, sourcesReady]);

  // —— 故障链路虚线动画（marching-ants）：仅存在 down 链路且未偏好减少动效时运行 ——
  useEffect(() => {
    if (!sourcesReady || geoData.downLinks === 0 || prefersReducedMotion()) return undefined;
    let raf = 0;
    let last = 0;
    let frame = 0;
    const step = (t: number): void => {
      raf = requestAnimationFrame(step);
      if (t - last < DASH_FRAME_MS) return;
      last = t;
      const map = mapRef.current;
      if (map !== null && map.getLayer('links-down') !== undefined) {
        map.setPaintProperty('links-down', 'line-dasharray', DASH_FRAMES[frame % DASH_FRAMES.length]);
      }
      frame += 1;
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [geoData.downLinks, sourcesReady, tileFallback]);

  // —— resize 补偿：分栏拖拽结束 / zen 切换后补一次 map.resize ——
  useEffect(() => {
    if (resizeSuspended) return undefined;
    const raf = requestAnimationFrame(() => {
      mapRef.current?.resize();
    });
    return () => cancelAnimationFrame(raf);
  }, [resizeSuspended, zen]);

  // —— 命令句柄（工具带「适配视图 F / 指北复位」经 TopologyCanvas 转发）——
  useImperativeHandle(
    ref,
    () => ({
      fitView: () => {
        const map = mapRef.current;
        if (map === null || geoDataRef.current === null) return;
        fitGeoBounds(map, geoDataRef.current, viewRef.current);
      },
      resetNorth: () => {
        const map = mapRef.current;
        if (map === null) return;
        map.easeTo({ bearing: 0, duration: prefersReducedMotion() ? 0 : CAMERA_TRANSITION_MS });
      },
    }),
    [],
  );

  // —— W16：底图/图层偏好 handlers（切底图重置瓦片降级，重新探测外网）——
  const chooseBasemap = useCallback(
    (id: BasemapId): void => {
      setPrefs((p) => (p.basemap === id ? p : { ...p, basemap: id }));
      if (tileFallbackRef.current) {
        tileFallbackRef.current = false;
        setTileFallback(false);
      }
      tileFailStreakRef.current = 0;
    },
    [],
  );
  const toggleOverlay = useCallback(
    (id: OverlayId): void => {
      setPrefs((p) => ({ ...p, overlays: { ...p.overlays, [id]: !p.overlays[id] } }));
    },
    [],
  );
  const toggleBusiness = useCallback(
    (id: 'links' | 'devices'): void => {
      setPrefs((p) => ({ ...p, [id]: !p[id] }));
    },
    [],
  );

  const overlaysUsable = overlaysAvailable(effective);
  const basemapChoices: Array<{ id: BasemapId; label: string; title: string }> = [
    { id: 'amap', label: LAB_GEO_BM_AMAP, title: LAB_GEO_BM_AMAP_TITLE },
    { id: 'amap-satellite', label: LAB_GEO_BM_AMAP_SAT, title: LAB_GEO_BM_AMAP_SAT_TITLE },
    { id: 'esri', label: LAB_GEO_BM_ESRI, title: LAB_GEO_BM_ESRI_TITLE },
    { id: 'offline', label: LAB_GEO_BM_OFFLINE, title: LAB_GEO_BM_OFFLINE_TITLE },
  ];
  const overlayChoices: Array<{ id: OverlayId; label: string; title: string }> = [
    { id: 'admin', label: LAB_GEO_OV_ADMIN, title: LAB_GEO_OV_ADMIN_TITLE },
    { id: 'railway', label: LAB_GEO_OV_RAIL, title: LAB_GEO_OV_RAIL_TITLE },
    { id: 'road', label: LAB_GEO_OV_ROAD, title: LAB_GEO_OV_ROAD_TITLE },
  ];

  return (
    <div className="geo-canvas" aria-label={LAB_GEO_MAP_LABEL}>
      <div ref={mapDivRef} className="geo-map" />
      {topology === null && <div className="geo-overlay geo-state">{LOADING_TEXT}</div>}
      {topology !== null && geoData.deviceCount === 0 && (
        <div className="geo-overlay geo-empty">
          <p className="geo-empty-text">{LAB_GEO_NOGEO}</p>
          {onBackToTopo !== undefined && (
            <button type="button" className="geo-empty-back" onClick={onBackToTopo}>
              {LAB_GEO_NOGEO_BACK}
            </button>
          )}
        </div>
      )}
      {tileFallback && (
        <div className="geo-overlay geo-chip geo-chip--offline" role="status" title={LAB_GEO_OFFLINE_TITLE}>
          {LAB_GEO_OFFLINE}
        </div>
      )}
      {geoData.missingCount > 0 && geoData.deviceCount > 0 && (
        <div className="geo-overlay geo-chip geo-chip--partial" role="status" title={LAB_GEO_PARTIAL_TITLE}>
          {format(LAB_GEO_PARTIAL, { n: geoData.missingCount })}
        </div>
      )}
      {/* W16 图层面板：底图模式 + 叠加图层 + 业务图层（画布决定显示什么）。
          右上角常驻折叠钮，展开为分区面板；叠加图层在 GCJ 底图下禁用并提示。 */}
      <div className="geo-layerpanel" role="group" aria-label={LAB_GEO_LAYERS_LABEL}>
        <button
          type="button"
          className="geo-layerpanel-toggle"
          aria-expanded={panelOpen}
          title={LAB_GEO_LAYERS_TOGGLE_TITLE}
          onClick={() => setPanelOpen((v) => !v)}
        >
          {LAB_GEO_LAYERS_TOGGLE}
        </button>
        {panelOpen && (
          <div className="geo-layerpanel-body">
            <div className="geo-layerpanel-section">{LAB_GEO_LAYERS_BASEMAP}</div>
            <div className="geo-layerpanel-options" role="radiogroup" aria-label={LAB_GEO_LAYERS_BASEMAP}>
              {basemapChoices.map((b) => (
                <label key={b.id} className="geo-layerpanel-opt" title={b.title}>
                  <input
                    type="radio"
                    name="geo-basemap"
                    /* 反映 effective（降级后显示离线选中）：瓦片失败降级时，
                     * 用户点回原底图按钮是真实变更（chooseBasemap 会重置降级
                     * 重试），若显示 prefs 值则点已选中项是 no-op，卡死降级态。 */
                    checked={effective === b.id}
                    onChange={() => chooseBasemap(b.id)}
                  />
                  {b.label}
                </label>
              ))}
            </div>
            <div className="geo-layerpanel-section">{LAB_GEO_LAYERS_OVERLAY}</div>
            <div className="geo-layerpanel-options">
              {overlayChoices.map((o) => (
                <label
                  key={o.id}
                  className={`geo-layerpanel-opt${overlaysUsable ? '' : ' geo-layerpanel-opt--disabled'}`}
                  title={overlaysUsable ? o.title : LAB_GEO_OV_UNAVAILABLE}
                >
                  <input
                    type="checkbox"
                    disabled={!overlaysUsable}
                    checked={prefs.overlays[o.id] && overlaysUsable}
                    onChange={() => toggleOverlay(o.id)}
                  />
                  {o.label}
                </label>
              ))}
              {!overlaysUsable && <p className="geo-layerpanel-hint">{LAB_GEO_OV_UNAVAILABLE}</p>}
            </div>
            <div className="geo-layerpanel-section">{LAB_GEO_LAYERS_BUSINESS}</div>
            <div className="geo-layerpanel-options">
              <label className="geo-layerpanel-opt" title={LAB_GEO_BIZ_LINKS_TITLE}>
                <input type="checkbox" checked={prefs.links} onChange={() => toggleBusiness('links')} />
                {LAB_GEO_BIZ_LINKS}
              </label>
              <label className="geo-layerpanel-opt" title={LAB_GEO_BIZ_DEVICES_TITLE}>
                <input type="checkbox" checked={prefs.devices} onChange={() => toggleBusiness('devices')} />
                {LAB_GEO_BIZ_DEVICES}
              </label>
            </div>
          </div>
        )}
      </div>
      {/* 地图图例：设备·运行/停机、链路·正常/故障（3D 视图附塔高条目）；左下角，
          抬升避开 ScaleControl；配色与地图图层同步（global.css .geo-legend 覆盖） */}
      <div className="lab-canvas-legend geo-legend" aria-label={LAB_CANVAS_LEGEND_LABEL}>
        <span className="legend-item">
          <span className="legend-dot legend-dot--up" aria-hidden="true" />
          {LAB_LEGEND_DEVICE_UP}
        </span>
        <span className="legend-item">
          <span className="legend-dot legend-dot--down" aria-hidden="true" />
          {LAB_LEGEND_DEVICE_DOWN}
        </span>
        <span className="legend-item">
          <span className="legend-line legend-line--up" aria-hidden="true" />
          {LAB_LEGEND_LINK_UP}
        </span>
        <span className="legend-item">
          <span className="legend-line legend-line--down" aria-hidden="true" />
          {LAB_LEGEND_LINK_DOWN}
        </span>
        {view === 'geo3d' && (
          <span className="legend-item">
            <span className="legend-tower" aria-hidden="true" />
            {LAB_GEO_LEGEND_TOWER}
          </span>
        )}
      </div>
    </div>
  );
});
