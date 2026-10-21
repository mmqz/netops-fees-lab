/**
 * GeoCanvas —— 画布地理视图（GEO-2）：真实经纬度 2D/3D 地图（maplibre-gl，懒加载）。
 *
 * 在 TopologyCanvas 的视图维度（拓扑图 | 2D 地图 | 3D 地图）中承载后两种：
 * - 设备按 geoLat/geoLng 落位为 HTML Marker（状态色圆点 + 编码牌，
 *   键盘可聚焦、Enter/Space 触发选择，与右侧表格联动语义同拓扑图 tap）；
 *   GCJ-02 底图（高德）渲染前经 projLngLat 投影，WGS84 底图（ESRI/离线）原样；
 * - 链路为 GeoJSON LineString 图层：up #5b8fd9 2px 实线 / down #ff5252 3px
 *   虚线（rAF 驱动 marching-ants 动画，prefers-reduced-motion 关闭）；
 *   选中高亮 #2f9dff 4px 走独立单要素源 links-selected（#43：选中变化仅
 *   对 0/1 要素的小源 setData，主 links/devices 源不再随点击全量重下）；
 *   端点缺坐标的链路跳过（右下角计数 chip 提示）；
 * - 3D 示意柱（fill-extrusion，仅 3D 视图可见）：屏幕高 18 + 连接数×10 px
 *   折算米高并钳制 [TOWER_MIN_M, TOWER_MAX_M]（#9：杜绝数十公里光柱；
 *   footprint 恒定 7px 屏宽——metersPerPixel 按 MapLibre 512px 世界坐标
 *   修正。#43：缩放期间仅 3D 视图逐帧跟手重建（2D 塔层不可见，逐帧
 *   setData 纯浪费），zoomend 恒补一次保持数据新鲜，进 3D 强制刷新）；
 *   2D↔3D 切换各自独立相机（#12 解锁：离开某视图存相机，进入时恢复；
 *   首次进入按该视图 fitBounds；经拓扑图中转 = 重新适配，可预期）；
 * - 地理高程支撑（#13）：离线 DEM 三重呈现——3D setTerrain 起伏（夸张
 *   随 zoom 分档自适应：远观强到可见、近观回真实）、2D hillshade 山体
 *   阴影、等高线图层（200m/500m，GCJ 变体预投影任意底图可用）+ 光标处
 *   实时海拔读数（terrarium 瓦片客户端解码采样）；
 * - 底图四模式可切换（#11，geoBasemap.ts 注册表）：离线简洁（默认，v0.6.4
 *   浅色无 POI）/ 高德街道 / 高德卫星 / ESRI 极简；离线叠加图层（行政区划/
 *   铁路/公路 + 自然地表覆盖/地名，黑龙江本地 OSM 数据
 *   © OpenStreetMap contributors，go:embed 随二进制分发）；区划/交通仅在
 *   WGS84 底图下可用，自然要素/地名双坐标变体任意底图可用；铁路默认关闭
 *   （用户要求：不得干扰主观看，可一键开）；
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
import { useNavigate } from 'react-router-dom';
import type { Ref } from 'react';
import type {
  ErrorEvent as MLErrorEvent,
  Map as MLMap,
  MapLayerMouseEvent,
  MapMouseEvent,
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
  ANY_BASEMAP_OVERLAYS,
  BASEMAP_REF_SOURCE_ID,
  BASEMAP_SOURCE_ID,
  CONTOUR_LAYERS,
  CONTOURS_SOURCE_ID,
  DEM_SOURCE_ID,
  HILLSHADE_LAYER_ID,
  LINK_LAYERS,
  OVERLAY_LAYERS,
  OVERLAY_SOURCE_IDS,
  TOWER_MAX_M,
  TOWER_MIN_M,
  WGS84_ONLY_OVERLAYS,
  basemapBg,
  basemapIsGCJ,
  buildGeoStyle,
  loadGeoLayerPrefs,
  overlaySourceUrl,
  overlaysAvailable,
  projLngLat,
  saveGeoLayerPrefs,
  terrainExaggerationFor,
} from './geoBasemap';
import type { BasemapId, GeoLayerPrefs, OverlayId, StyleOptions } from './geoBasemap';
import { clusterBounds, clusterDevices } from './geoCluster';
import type { ClusterPoint, DeviceCluster } from './geoCluster';
import { clearElevationCache, sampleElevation } from './geoElevation';
import { listResources } from '../../api/client';
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
  LAB_GEO_ELEV_OUT,
  LAB_GEO_ELEV_TITLE,
  LAB_GEO_ELEV_VALUE,
  LAB_GEO_LEGEND_TOWER,
  LAB_GEO_LAYERS_BASEMAP,
  LAB_GEO_LAYERS_BUSINESS,
  LAB_GEO_LAYERS_LABEL,
  LAB_GEO_LAYERS_NATURAL,
  LAB_GEO_LAYERS_OVERLAY,
  LAB_GEO_LAYERS_REALDATA,
  LAB_GEO_OV_CABLE,
  LAB_GEO_OV_CABLE_TITLE,
  LAB_GEO_RESTORE_FULL,
  LAB_GEO_RESTORE_FULL_TITLE,
  LAB_GEO_OV_BASESTATION,
  LAB_GEO_OV_BASESTATION_TITLE,
  LAB_GEO_LAYERS_TOGGLE,
  LAB_GEO_LAYERS_TOGGLE_TITLE,
  LAB_GEO_MAP_LABEL,
  LAB_GEO_MARKER_LABEL,
  LAB_GEO_NOGEO,
  LAB_GEO_NOGEO_BACK,
  LAB_GEO_OFFLINE,
  LAB_GEO_OFFLINE_TITLE,
  LAB_GEO_CLUSTER_ARIA,
  LAB_GEO_CLUSTER_HOVER,
  LAB_GEO_CLUSTER_TITLE,
  LAB_GEO_OV_ADMIN,
  LAB_GEO_OV_ADMIN_TITLE,
  LAB_GEO_OV_CONTOUR,
  LAB_GEO_OV_CONTOUR_TITLE,
  LAB_GEO_OV_FOREST,
  LAB_GEO_OV_FOREST_TITLE,
  LAB_GEO_OV_GREEN,
  LAB_GEO_OV_GREEN_TITLE,
  LAB_GEO_OV_PLACES,
  LAB_GEO_OV_PLACES_TITLE,
  LAB_GEO_OV_RAIL,
  LAB_GEO_OV_RAIL_TITLE,
  LAB_GEO_OV_ROAD,
  LAB_GEO_OV_ROAD_TITLE,
  LAB_GEO_OV_TERRAIN,
  LAB_GEO_OV_TERRAIN_TITLE,
  LAB_GEO_OV_UNAVAILABLE,
  LAB_GEO_OV_WATER,
  LAB_GEO_OV_WATER_TITLE,
  LAB_GEO_PARTIAL,
  LAB_GEO_PARTIAL_TITLE,
  LAB_LEGEND_DEVICE_DOWN,
  LAB_LEGEND_DEVICE_UP,
  LAB_LEGEND_LINK_DOWN,
  LAB_LEGEND_LINK_UP,
  LOADING_TEXT,
  SIM_STATUS_LABELS,
  format,
  LAB_GEO_BS_NOT_FOUND,
  LAB_GEO_BS_RESOLVE_FAIL,
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

/** fitBounds 留白：随容器尺寸自适应（短边 8%，夹在 [24, 80]px；#12——
 *  小画布（分栏/半屏）上固定 80px 会吞掉 1/3 视野，默认比例显小）。 */
const FIT_PADDING_MIN_PX = 24;
const FIT_PADDING_MAX_PX = 80;
/** 2D 适配 zoom 上限（#12：9 → 12.5——城市尺度数据不再被压远，
 *  同城近距设备初始视野即可分开，而非全部并入一个聚簇）。 */
const FIT_MAX_ZOOM = 12.5;
/** 3D 斜视的 zoom 上限（单点场景 / 相机解的近端夹紧；7.2 → 8.6：
 *  城市尺度 3D 近观塔体与地形）。 */
const FIT3D_MAX_ZOOM = 8.6;

/** 初始相机（数据到达前）：中国中心，等待 fitBounds。 */
const DEFAULT_CENTER: [number, number] = [105, 36];
const DEFAULT_ZOOM = 3.2;

/** 地标聚簇阈值（W17）：可见项（簇心/单例）之间的最小屏幕间距。设备牌
 * 宽 ~85px、高 ~18px，56px 内必重叠；全省初始视野下同城设备 1–2px 间距
 * → 聚成计数簇（点「锁在一起」的直接修复），点击/放大后展开。 */
const CLUSTER_PX = 56;
/** 点击聚簇后的目标 zoom 增量（成员 bbox fit + 领头设备的适度近观）。 */
const CLUSTER_ZOOM_STEP = 2.2;

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

// —— GeoJSON 数据构建（纯函数。#43：选中态不再烘进 feature properties——
// 几何只随 topology/坐标系变化；选中高亮由独立单要素源承担，见 links-selected） ——

interface GeoDeviceProps {
  id: string;
  code: string;
  status: SimEntityStatus;
}

interface GeoLinkProps {
  id: string;
  label: string;
  status: SimEntityStatus;
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

/** geojson source 的最小结构（maplibre 未导出 GeoJSONSource 类型）。
 * setData 接受 URL 字符串（#50 懒加载补拉：服务器带 Content-Encoding: gzip，
 * 浏览器 fetch 透明解压）或 FeatureCollection。 */
interface GeoJSONSourceLike {
  setData(data: FeatureCollection | string): unknown;
}

/** hover 信息条载荷（与拓扑画布 HoverInfo 结构一致）。 */
export interface GeoHoverInfo {
  code: string;
  name: string;
  status: SimEntityStatus;
}

/** 视图相机快照（#12：2D/3D 各自独立，切换时存/恢复）。 */
interface ViewCamera {
  center: { lng: number; lat: number };
  zoom: number;
  bearing: number;
  pitch: number;
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

export function buildGeoData(topology: SimTopology | null, toGCJ: boolean): GeoData {
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

/**
 * #43 选中高亮：链路选中线画在独立单要素源 links-selected 上（样式层
 * links-selected 同名换源）。选中变化只对这个 0/1 要素的小源 setData——
 * O(1)，主 links 源的几何/属性不再因点击而全量重下（原实现把 selected 烘进
 * feature properties，每次点击 buildGeoData 全量重建 + 两个主源 setData）。
 * 几何直接复用 geoData.links 里已投影的 Feature（同坐标系同数据，无需重算）。
 */
function applyLinkHighlight(map: MLMap, selected: SelectedEntity | null, data: GeoData | null): void {
  const src = map.getSource('links-selected') as GeoJSONSourceLike | undefined;
  if (src === undefined) return;
  let feature: Feature<LineString, GeoLinkProps> | undefined;
  if (selected !== null && selected.kind === 'link' && data !== null) {
    feature = data.links.features.find((f) => f.properties.id === selected.id);
  }
  src.setData(
    feature === undefined
      ? (emptyFC() as FeatureCollection<LineString, GeoLinkProps>)
      : { type: 'FeatureCollection', features: [feature] },
  );
}

function applyTowerVisibility(map: MLMap, view: 'geo2d' | 'geo3d'): void {
  if (map.getLayer('towers') !== undefined) {
    map.setLayoutProperty('towers', 'visibility', view === 'geo3d' ? 'visible' : 'none');
  }
}

/** 地形（W17/#13）：3D 视图 + 偏好开 → setTerrain（离线 DEM 源，夸张随
 *  zoom 分档自适应——整省远观时 1500m 起伏占屏 <1%，恒定 1.2 视觉全平，
 *  远观加强到 4，近观回 1.3）；否则显式卸载（null）。try/catch：
 *  DEM 源尚未就绪等瞬时态不致命（下次 effect/zoomend 重试）。 */
function applyTerrainFor(map: MLMap, view: 'geo2d' | 'geo3d', terrain: boolean): void {
  const target =
    view === 'geo3d' && terrain
      ? { source: DEM_SOURCE_ID, exaggeration: terrainExaggerationFor(map.getZoom()) }
      : null;
  try {
    map.setTerrain(target);
  } catch {
    // 样式重建在途：忽略（reapply/effect 会重挂）
  }
}

/** 叠加/业务图层可见性批量应用。区划/交通叠加仅在 WGS84 底图下放行；
 * 自然要素/地名与等高线为双坐标变体，任意底图可用（terrain 同源 DEM）。 */
function applyLayerVisibilities(map: MLMap, prefs: GeoLayerPrefs, effective: BasemapId): void {
  const overlaysOn = overlaysAvailable(effective);
  for (const id of WGS84_ONLY_OVERLAYS) {
    const on = prefs.overlays[id] && overlaysOn;
    for (const layerId of OVERLAY_LAYERS[id]) {
      if (map.getLayer(layerId) !== undefined) {
        map.setLayoutProperty(layerId, 'visibility', on ? 'visible' : 'none');
      }
    }
  }
  for (const id of ANY_BASEMAP_OVERLAYS) {
    const on = prefs.overlays[id];
    for (const layerId of OVERLAY_LAYERS[id]) {
      if (map.getLayer(layerId) !== undefined) {
        map.setLayoutProperty(layerId, 'visibility', on ? 'visible' : 'none');
      }
    }
  }
  const contourVis = prefs.contours ? 'visible' : 'none';
  for (const id of CONTOUR_LAYERS) {
    if (map.getLayer(id) !== undefined) map.setLayoutProperty(id, 'visibility', contourVis);
  }
  const linkVis = prefs.links ? 'visible' : 'none';
  for (const id of LINK_LAYERS) {
    if (map.getLayer(id) !== undefined) map.setLayoutProperty(id, 'visibility', linkVis);
  }
  if (map.getLayer(HILLSHADE_LAYER_ID) !== undefined) {
    map.setLayoutProperty(HILLSHADE_LAYER_ID, 'visibility', prefs.terrain ? 'visible' : 'none');
  }
}

/** 懒加载源补拉（issue #50）：buildGeoStyle 只给可见叠加源填 URL，运行中
 * 勾选（不重建样式）时源里还是空 FeatureCollection——此函数把所有「已开
 * 而源仍为空集」的叠加源 setData(URL)（幂等：源 data 已是字符串 URL 即
 * 跳过；GCJ 底图选 GCJ 预投影变体）。natural 三态共享一源，任一开即拉。 */
function fillOverlaySources(map: MLMap, prefs: GeoLayerPrefs, effective: BasemapId): void {
  const isGCJ = basemapIsGCJ(effective);
  const overlaysOn = overlaysAvailable(effective);
  const want = new Set<string>();
  for (const id of WGS84_ONLY_OVERLAYS) {
    if (prefs.overlays[id] && overlaysOn) {
      for (const sid of OVERLAY_SOURCE_IDS[id]) want.add(sid);
    }
  }
  for (const id of ANY_BASEMAP_OVERLAYS) {
    if (prefs.overlays[id]) {
      for (const sid of OVERLAY_SOURCE_IDS[id]) want.add(sid);
    }
  }
  if (prefs.contours) want.add(CONTOURS_SOURCE_ID);
  for (const sid of want) {
    const src = map.getSource(sid) as GeoJSONSourceLike | undefined;
    if (src === undefined) continue;
    // 已是 URL（本次 style 烘焙时填过 / 已拉取过）则跳过：serialize() 的
    // data 在「空集」时是对象、URL 时是字符串。
    try {
      const data = (src as unknown as { serialize(): { data?: unknown } }).serialize().data;
      if (typeof data === 'string' && data !== '') continue;
    } catch {
      // serialize 失败（旧 maplibre 行为差异）：继续补拉，幂等代价 = 一次重复 setData
    }
    const url = overlaySourceUrl(sid, isGCJ);
    if (url !== '') {
      try {
        src.setData(url);
      } catch {
        // 源尚在样式重建途中：跳过（styledata 后的 reapply/effect 会重试）
      }
    }
  }
}

/**
 * fitGeoBounds：按**目标视图**适配相机（#12 重校：留白自适应 + 上限提升）。
 *
 * 2D：fitBounds（留白 = 容器短边 8% 夹 [24,80]px，maxZoom 12.5）。
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
  const rect = map.getContainer().getBoundingClientRect();
  const padding = Math.max(
    FIT_PADDING_MIN_PX,
    Math.min(FIT_PADDING_MAX_PX, Math.round(Math.min(rect.width, rect.height) * 0.08) || FIT_PADDING_MAX_PX),
  );
  if (view === 'geo3d') {
    if (data.deviceCount === 1) {
      map.easeTo({ center, zoom: FIT3D_MAX_ZOOM, pitch: PITCH_3D, bearing: BEARING_3D, duration });
      return;
    }
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
  map.fitBounds(data.bbox, { padding, maxZoom: FIT_MAX_ZOOM, duration });
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

/**
 * #43：复用簇 chip 的派生态刷新（创建路径也走它，保证新建/复用一致）。
 * 成员集合（key）不变时计数不变，但状态可能随 ops 变化——停机角标 /
 * --down/--selected 类 / title / aria-label 均从当前 members 重算；文本与
 * 属性先比对再写，避免逐帧 move/zoom 下无变化时的重排浪费。
 */
function refreshClusterChip(el: HTMLButtonElement, members: ClusterPoint[], selected: boolean): void {
  const n = members.length;
  const down = members.filter((m) => m.status === 'down').length;
  el.classList.toggle('geo-cluster--down', down > 0);
  el.classList.toggle('geo-cluster--selected', selected);
  const count = el.querySelector('.geo-cluster-count');
  if (count !== null && count.textContent !== String(n)) count.textContent = String(n);
  let badge = el.querySelector('.geo-cluster-down');
  if (down > 0) {
    if (badge === null) {
      const el2 = document.createElement('span');
      el2.className = 'geo-cluster-down';
      el2.textContent = String(down);
      el2.setAttribute('aria-hidden', 'true');
      el.appendChild(el2);
    } else if (badge.textContent !== String(down)) {
      badge.textContent = String(down);
    }
  } else if (badge !== null) {
    badge.remove();
  }
  const codes = members.map((m) => m.code).slice(0, 6).join('、');
  const title = format(LAB_GEO_CLUSTER_TITLE, { n, down, codes });
  if (el.title !== title) el.title = title;
  const aria = format(LAB_GEO_CLUSTER_ARIA, { n, down });
  if (el.getAttribute('aria-label') !== aria) el.setAttribute('aria-label', aria);
}

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
  // W31：基站图层点击 → 资源详情跳转（?focus= 打开详情抽屉）。
  const navigate = useNavigate();
  const navigateRef = useRef(navigate);
  useEffect(() => {
    navigateRef.current = navigate;
  }, [navigate]);
  const libRef = useRef<MapLibreModule | null>(null);
  const markersRef = useRef<Map<string, MarkerEntry>>(new Map());
  /** 聚簇 marker 持有项（W17）：key = 成员 id 升序 join（不含状态——状态变化
   *  走增量更新而非换 key）。#43：改为增量差分维护——复用未变 chip，只增删
   *  差集 + 刷新已复用 chip 的计数/状态/选中类，不再全拆全建。members 供
   *  选中 effect 与增量刷新取成员状态（id 相同但 status 可能随 ops 变化）。 */
  const clusterMarkersRef = useRef<Map<string, { marker: MLMarker; el: HTMLButtonElement; members: ClusterPoint[] }>>(
    new Map(),
  );
  const updateClustersRef = useRef<(() => void) | null>(null);
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
  /** #12：每视图相机快照（切换时存旧取新；卸载丢弃）。 */
  const viewCamerasRef = useRef<{ geo2d: ViewCamera | null; geo3d: ViewCamera | null }>({
    geo2d: null,
    geo3d: null,
  });
  /** #12：上一次生效的视图（相机存/取的分界；挂载值 = 初始视图）。 */
  const prevViewRef = useRef<'geo2d' | 'geo3d'>(view);
  /** 光标海拔读数（#13）：null = 光标不在地图上；value null = 超出覆盖。 */
  const [elevReadout, setElevReadout] = useState<{ value: number | null } | null>(null);
  const elevSeqRef = useRef(0);

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

  // #43：几何只随 topology/坐标系变化（选中态已解耦，见 applyLinkHighlight）。
  const geoData = useMemo(() => buildGeoData(topology, gcj), [topology, gcj]);

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
          // 区划/交通：仅 WGS84 底图（GCJ 会偏移且栅格已含）
          admin: prefsRef.current.overlays.admin && overlaysAvailable(id),
          railway: prefsRef.current.overlays.railway && overlaysAvailable(id),
          road: prefsRef.current.overlays.road && overlaysAvailable(id),
          // 自然要素/地名：双坐标变体，任意底图
          forest: prefsRef.current.overlays.forest,
          water: prefsRef.current.overlays.water,
          green: prefsRef.current.overlays.green,
          places: prefsRef.current.overlays.places,
          // 真实网络数据（v0.8.0）：双坐标变体，任意底图
          cable: prefsRef.current.overlays.cable,
          basestation: prefsRef.current.overlays.basestation,
        },
        linksVisible: prefsRef.current.links,
        terrainVisible: prefsRef.current.terrain,
        contoursVisible: prefsRef.current.contours,
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
          // 样式重建后选中高亮源也是空的，与主数据同路回填（否则切底图丢高亮）。
          applyLinkHighlight(map, selectedRef.current, geoDataRef.current);
          const topo = topoRef.current;
          const tsrc = map.getSource('towers') as GeoJSONSourceLike | undefined;
          if (topo !== null && tsrc !== undefined) tsrc.setData(buildTowerCollection(topo, map.getZoom(), gcjRef.current));
          applyTowerVisibility(map, viewRef.current);
          applyLayerVisibilities(map, prefsRef.current, effectiveRef.current);
          // 懒源补拉（#50）：样式重建后已可见的叠加源若未带 URL（GCJ 降级
          // 等边界），统一补齐。
          fillOverlaySources(map, prefsRef.current, effectiveRef.current);
          // setStyle 会清掉 terrain：按当前视图/偏好重挂（W17）。
          applyTerrainFor(map, viewRef.current, prefsRef.current.terrain);
          updateClustersRef.current?.();
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

      // —— 塔重建（#9/#43）：zoom 动画期间 rAF 节流同帧重建 footprint + heightM
      // （缩放中每帧跟手，恒定 7px 屏宽与钳制高度不再「动画结束才跳变」）。
      // #43 收敛：逐帧重建只在 3D 视图（塔层可见）做——2D 下塔层 visibility:none，
      // 逐帧 setData 是纯浪费（v0.8.0 真实数据反馈的卡顿源之一）；zoomend 恒补
      // 一次（含 2D，保持数据新鲜，切回 3D 不会用陈旧几何）。——
      let towerRaf = 0;
      const rebuildTowersNow = (force: boolean): void => {
        if (disposed) return;
        // 非 force（zoom 逐帧路径）仅 3D 执行；force（zoomend/进 3D）恒执行。
        if (!force && viewRef.current !== 'geo3d') return;
        const topo = topoRef.current;
        const src = map.getSource('towers') as GeoJSONSourceLike | undefined;
        if (topo !== null && src !== undefined) src.setData(buildTowerCollection(topo, map.getZoom(), gcjRef.current));
      };
      const scheduleTowerRebuild = (): void => {
        if (disposed || towerRaf !== 0) return;
        towerRaf = requestAnimationFrame(() => {
          towerRaf = 0;
          rebuildTowersNow(false);
        });
      };
      const rebuildTowersEnd = (): void => rebuildTowersNow(true);
      scheduleTowerRebuildRef.current = scheduleTowerRebuild;
      map.on('zoom', scheduleTowerRebuild);
      map.on('zoomend', rebuildTowersEnd);
      cleanups.push(() => {
        map.off('zoom', scheduleTowerRebuild);
        map.off('zoomend', rebuildTowersEnd);
        if (towerRaf !== 0) cancelAnimationFrame(towerRaf);
        scheduleTowerRebuildRef.current = null;
      });

      // —— 聚簇重算（W17）：move/zoom 期间 rAF 节流（与塔同帧但独立计数）。
      // 平移也会改变屏幕间距，挂 move 而非仅 zoom。 ——
      let clusterRaf = 0;
      const scheduleClusterUpdate = (): void => {
        if (disposed || clusterRaf !== 0) return;
        clusterRaf = requestAnimationFrame(() => {
          clusterRaf = 0;
          updateClustersRef.current?.();
        });
      };
      map.on('move', scheduleClusterUpdate);
      map.on('moveend', scheduleClusterUpdate);
      cleanups.push(() => {
        map.off('move', scheduleClusterUpdate);
        map.off('moveend', scheduleClusterUpdate);
        if (clusterRaf !== 0) cancelAnimationFrame(clusterRaf);
      });

      // —— 光标海拔读数（W17/#13）：mousemove rAF 节流 → 采样离线 DEM ——
      // GCJ 底图下 lngLat 为火星坐标，采样前逆偏移（geoElevation 内处理）。
      let elevRaf = 0;
      const onElevMove = (ev: MapMouseEvent): void => {
        if (disposed || elevRaf !== 0) return;
        elevRaf = requestAnimationFrame(() => {
          elevRaf = 0;
          const seq = (elevSeqRef.current += 1);
          const { lng, lat } = ev.lngLat;
          void sampleElevation(lng, lat, gcjRef.current).then((value) => {
            if (seq === elevSeqRef.current && !disposed) setElevReadout({ value });
          });
        });
      };
      const onElevOut = (): void => {
        elevSeqRef.current += 1;
        setElevReadout(null);
      };
      map.on('mousemove', onElevMove);
      map.on('mouseout', onElevOut);
      cleanups.push(() => {
        map.off('mousemove', onElevMove);
        map.off('mouseout', onElevOut);
        if (elevRaf !== 0) cancelAnimationFrame(elevRaf);
      });

      // —— 地形夸张随 zoom 分档重标（#13）：zoom 结束后按当前档位重挂 ——
      const onTerrainZoomEnd = (): void => {
        if (disposed) return;
        applyTerrainFor(map, viewRef.current, prefsRef.current.terrain);
      };
      map.on('zoomend', onTerrainZoomEnd);
      cleanups.push(() => map.off('zoomend', onTerrainZoomEnd));

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
      clusterMarkersRef.current.forEach((entry) => {
        entry.marker.remove();
      });
      clusterMarkersRef.current.clear();
      updateClustersRef.current = null;
      localMap?.remove();
      mapRef.current = null;
      libRef.current = null;
      rebuildStyleRef.current = null;
      styleKeyRef.current = null;
      tileFallbackRef.current = false;
      tileFailStreakRef.current = 0;
      didFitRef.current = false;
      viewCamerasRef.current = { geo2d: null, geo3d: null };
      prevViewRef.current = viewRef.current;
      elevSeqRef.current += 1;
      setElevReadout(null);
      clearElevationCache();
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
    // #50 懒加载：勾选重图层时源仍是空集 → 此处补拉（首帧默认最小集，
    // 34MB 自然要素/等高线只在用户主动开启后才下载）。
    fillOverlaySources(map, prefs, effective);
  }, [prefs, effective, sourcesReady]);

  // —— 数据 → GeoJSON source（setData 原地更新，相机保持；首批数据 fitBounds）——
  // #43：依赖收敛为 [geoData, sourcesReady]——geoData 已只随 topology/gcj 变化
  //（选中/视图切换都不再触发本 effect，主源 setData 仅发生在拓扑/坐标系变化）；
  // view 出依赖（旧版切 2D↔3D 也会全量重下两个主源，几何并未变）；首批适配
  // 改读 viewRef（同步 effect 先于本 effect 执行，值已新鲜）。
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    geoDataRef.current = geoData;
    applyDataToMap(map, geoData);
    // #43：拓扑/坐标系变化后选中链路的几何可能已变，高亮源同帧重下（O(1)）。
    applyLinkHighlight(map, selectedRef.current, geoData);
    // 塔依赖当前 zoom：单独供给（zoom rAF 重建路径见挂载 effect）
    const tsrc = map.getSource('towers') as GeoJSONSourceLike | undefined;
    if (tsrc !== undefined) tsrc.setData(buildTowerCollection(topology, map.getZoom(), gcj));
    if (!didFitRef.current && geoData.deviceCount > 0) {
      didFitRef.current = true;
      fitGeoBounds(map, geoData, viewRef.current);
    }
  }, [geoData, sourcesReady]);

  // —— #43：选中 → 高亮源（0/1 要素）+ 地标/簇类切换，零重建零重算 ——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    applyLinkHighlight(map, selected, geoDataRef.current);
  }, [selected, sourcesReady]);

  // —— W31：基站图层点击 → 资源详情（台账联动）——
  // feature.properties.id = 站编码（主小区 CGI = base_station 自然键，
  // scripts/realdata/patch_station_ids.py 已补进两个坐标变体的图层）。
  // 解析经 GET /api/resources?type=base_station&q=<code>（exact 自然键命中
  // 恰一条），跳转 /resources?focus=<id>（详情抽屉：属性/关系/穿透分析）。
  // 找不到（未导入基站台账的库）→ hover 信息条提示，不跳转不报错弹窗。
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    let cancelled = false;
    const onBsClick = (ev: MapLayerMouseEvent): void => {
      const props = ev.features?.[0]?.properties as Record<string, unknown> | undefined;
      const code = props === undefined ? undefined : (props.id as string | undefined);
      if (code === undefined || code === '') return;
      listResources({ type: 'base_station', q: code, page: 1, pageSize: 5 })
        .then((paged) => {
          if (cancelled) return;
          const hit = paged.items.find((it) => it.naturalKey === code) ?? paged.items[0];
          if (hit === undefined) {
            onHoverInfoRef.current?.({ code, name: LAB_GEO_BS_NOT_FOUND, status: 'up' });
            return;
          }
          navigateRef.current(`/resources?focus=${encodeURIComponent(hit.id)}`);
        })
        .catch(() => {
          if (!cancelled) {
            onHoverInfoRef.current?.({ code, name: LAB_GEO_BS_RESOLVE_FAIL, status: 'up' });
          }
        });
    };
    const onBsEnter = (): void => {
      map.getCanvas().style.cursor = 'pointer';
    };
    const onBsLeave = (): void => {
      map.getCanvas().style.cursor = '';
    };
    map.on('click', 'basestation-pts', onBsClick);
    map.on('mouseenter', 'basestation-pts', onBsEnter);
    map.on('mouseleave', 'basestation-pts', onBsLeave);
    return () => {
      cancelled = true;
      map.off('click', 'basestation-pts', onBsClick);
      map.off('mouseenter', 'basestation-pts', onBsEnter);
      map.off('mouseleave', 'basestation-pts', onBsLeave);
    };
  }, [sourcesReady]);

  // —— 底图坐标系切换（WGS84↔GCJ-02）→ 塔体同帧重投影 ——
  // （几何重建主路径已由数据 effect 覆盖，这里 rAF 再补一帧确保相机就绪后
  //  按当前 zoom 取值；2D 下被 rebuildTowersNow 的视图守卫拦下，无浪费。）
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    scheduleTowerRebuildRef.current?.();
  }, [gcj, sourcesReady]);

  // —— 聚簇（W17/#43）：屏幕碰撞 → 计数簇 + 单例地标，点击簇放大展开 ——
  // 依赖全部走 ref（挂载 effect 的 move rAF 与各 effect 直接调度，无重建问题）。
  // #43 增量化：原版每次 move/zoom 帧「全拆再全建」——DOM 拆建 + Marker
  // 注册 + GC 是拖拽/缩放卡顿主源，且注释里的「≤几百设备」规模假设已被
  // v0.8.0 真实数据打破。现改为两段收敛：
  // - 视口裁剪（issue 要求）：只有视口 ± CLUSTER_PX 带内的投影点参与聚簇
  //   与建 chip。带宽 = 聚簇阈值：带外点不可能并入任何可见簇（成员距簇心
  //   必 < 阈值），数学上不改变可见结果；带外地标的 --clustered 类在平移
  //   进入视口后由下一帧 move rAF 纠正（无感延迟）。
  // - 差分维护：chip 以成员 id 集合为 key，未变者复用（簇心 = leader 的
  //   地理坐标，成员不变则位置也不变），只增删差集 + 刷新已复用 chip 的
  //   计数/停机/选中派生态（见 refreshClusterChip——key 不含状态，ops 后
  //   状态变化仍能刷新）。
  const updateClusters = useCallback((): void => {
    const map = mapRef.current;
    const lib = libRef.current;
    if (map === null || lib === null) return;
    const topo = topoRef.current;
    // 设备层关 / 无数据：期望集为空 → 增量清空（语义等价旧版「先全拆再判空」）。
    if (topo === null || !prefsRef.current.devices) {
      for (const entry of clusterMarkersRef.current.values()) entry.marker.remove();
      clusterMarkersRef.current.clear();
      return;
    }

    // 屏幕投影（含 GCJ 投影后的坐标——投影与 Marker.setLngLat 同源）。
    const pts: ClusterPoint[] = [];
    const vw = map.getContainer().clientWidth;
    const vh = map.getContainer().clientHeight;
    for (const node of topo.nodes) {
      const lat = node.geoLat;
      const lng = node.geoLng;
      if (lat === undefined || lng === undefined || !Number.isFinite(lat) || !Number.isFinite(lng)) continue;
      const [plng, plat] = projLngLat(lng, lat, gcjRef.current);
      const p = map.project([plng, plat]);
      if (p.x < -CLUSTER_PX || p.y < -CLUSTER_PX || p.x > vw + CLUSTER_PX || p.y > vh + CLUSTER_PX) continue;
      pts.push({ id: node.id, code: node.code, name: node.name, status: node.status, lng: plng, lat: plat, x: p.x, y: p.y });
    }
    if (pts.length === 0) {
      for (const entry of clusterMarkersRef.current.values()) entry.marker.remove();
      clusterMarkersRef.current.clear();
      return;
    }

    const { clusters, singletons } = clusterDevices(pts, CLUSTER_PX);

    // 单例 → 显示独立地标；被簇吞并 → 隐藏（CSS .geo-marker--clustered）。
    const singletonIds = new Set(singletons.map((p) => p.id));
    markersRef.current.forEach((entry, id) => {
      entry.btn.classList.toggle('geo-marker--clustered', !singletonIds.has(id));
    });

    const selectedId = selectedRef.current?.kind === 'device' ? selectedRef.current.id : null;
    // 期望集：视口带内的簇（成员均距簇心 < 阈值，leader 在带内即可）。
    const desired = new Map<string, DeviceCluster>();
    for (const c of clusters) {
      desired.set(c.members.map((m) => m.id).sort().join('|'), c);
    }

    // 差分删除：结果中已不存在的簇。
    for (const [key, entry] of clusterMarkersRef.current) {
      if (!desired.has(key)) {
        entry.marker.remove();
        clusterMarkersRef.current.delete(key);
      }
    }

    for (const [key, c] of desired) {
      // down 计数在 refreshClusterChip 内从 members 现算（新建/复用一致）。
      const selected = selectedId !== null && c.members.some((m) => m.id === selectedId);
      const existing = clusterMarkersRef.current.get(key);
      if (existing !== undefined) {
        // 复用（成员集合不变）：仅刷新派生态，无 DOM 增删、无 Marker 注册。
        existing.members = c.members;
        refreshClusterChip(existing.el, existing.members, selected);
        continue;
      }
      const el = document.createElement('button');
      el.type = 'button';
      el.tabIndex = 0;
      el.className = 'geo-cluster';
      const count = document.createElement('span');
      count.className = 'geo-cluster-count';
      count.textContent = String(c.members.length);
      el.appendChild(count);
      const marker = new lib.Marker({ element: el, anchor: 'center' })
        .setLngLat([c.center.lng, c.center.lat])
        .addTo(map);
      const entry: { marker: MLMarker; el: HTMLButtonElement; members: ClusterPoint[] } = {
        marker,
        el,
        members: c.members,
      };
      // hover/click 闭包一律读 entry.members（复用路径会整体替换该数组引用，
      // 保证状态变化后提示/展开用的是当前成员，而非创建时的旧快照）。
      const hoverIn = (): void => {
        const d = entry.members.filter((m) => m.status === 'down').length;
        const codes = entry.members.map((m) => m.code).slice(0, 6).join('、');
        onHoverInfoRef.current?.({ code: format(LAB_GEO_CLUSTER_HOVER, { n: entry.members.length }), name: codes, status: d > 0 ? 'down' : 'up' });
      };
      const hoverOut = (): void => {
        onHoverInfoRef.current?.(null);
      };
      el.addEventListener('click', () => {
        // 放大展开：目标 zoom = max(当前 + CLUSTER_ZOOM_STEP, 成员 bbox 的
        // cameraForBounds 解)——保证同城近距设备一次点击即拆簇（旧版仅
        // +2.2 级，哈尔滨 3 台在 z7.8 仍同簇，需点两次）。
        const bounds = clusterBounds(entry.members);
        let fitZoom = 0;
        try {
          const cam = map.cameraForBounds(bounds, { padding: 120 });
          const z = cam?.zoom;
          if (typeof z === 'number' && Number.isFinite(z)) fitZoom = z;
        } catch {
          // 容器尺寸异常时退回固定步进
        }
        map.fitBounds(bounds, {
          padding: 120,
          maxZoom: Math.max(map.getZoom() + CLUSTER_ZOOM_STEP, fitZoom),
          duration: prefersReducedMotion() ? 0 : CAMERA_TRANSITION_MS,
        });
      });
      el.addEventListener('mouseenter', hoverIn);
      el.addEventListener('mouseleave', hoverOut);
      el.addEventListener('focus', hoverIn);
      el.addEventListener('blur', hoverOut);
      refreshClusterChip(el, c.members, selected);
      clusterMarkersRef.current.set(key, entry);
    }
  }, []);
  updateClustersRef.current = updateClusters;

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
    // 重建后立即重算聚簇（设备增删/坐标系切换改变屏幕间距）。
    updateClustersRef.current?.();
  }, [topology, sourcesReady, rebuildMarkers, gcj]);

  // 选中 / 标签开关 / 设备可见性 → 地标类与显隐切换（#43：纯类切换）
  // 旧版在选中变化时重跑全量聚簇重建；相机未动则成员不可能变，改为对既有
  // chip 就地切换 --selected（members 已在 entry 上，无需重算屏幕投影）。
  useEffect(() => {
    const selectedId = selected?.kind === 'device' ? selected.id : null;
    markersRef.current.forEach((entry, id) => {
      entry.btn.classList.toggle('geo-marker--selected', selectedId === id);
      entry.btn.classList.toggle('geo-marker--nolabel', !labels);
      const el = entry.marker.getElement();
      el.style.display = prefs.devices ? '' : 'none';
    });
    clusterMarkersRef.current.forEach((entry) => {
      entry.el.classList.toggle(
        'geo-cluster--selected',
        selectedId !== null && entry.members.some((m) => m.id === selectedId),
      );
    });
  }, [selected, labels, prefs.devices]);

  // 设备层开关 → 簇 chip 增量增删（关 = 期望空集全拆；开 = 按当前相机重建。
  // #43：从选中 effect 拆出——选中不再触发射簇重算，开关本身才需要。）
  useEffect(() => {
    if (!sourcesReady) return;
    updateClustersRef.current?.();
  }, [prefs.devices, sourcesReady]);

  // —— 2D↔3D 独立相机（#12 解锁）——
  // 离开旧视图：存相机快照（center/zoom/bearing/pitch）；进入新视图：
  // 有快照 → 恢复；无（首次进入）→ 按该视图 fitBounds（3D 墨卡托直解）；
  // 数据未到 → 仅纠正 pitch/bearing。旧 ±1.3 zoom 补偿 hack 移除：
  // 两个视图各自记忆，不再共享相机（用户反馈「2D 图锁在一起」）。
  // 经拓扑图中转 = GeoCanvas 卸载重挂，快照丢弃 → 重新适配（可预期）。
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    applyTowerVisibility(map, view);
    const prev = prevViewRef.current;
    if (prev === view) return;
    prevViewRef.current = view;
    // #43：进 3D 强制刷新塔几何——2D 期间逐帧重建被视图守卫拦下，塔集合
    // 可能停在旧 zoom 值上（zoomend 兜底覆盖缩放手势，这里覆盖「2D 静态
    // 切换」；rAF 后 viewRef 已是 geo3d，不会被守卫拦截）。
    if (view === 'geo3d') scheduleTowerRebuildRef.current?.();
    const c = map.getCenter();
    viewCamerasRef.current[prev] = {
      center: { lng: c.lng, lat: c.lat },
      zoom: map.getZoom(),
      bearing: map.getBearing(),
      pitch: map.getPitch(),
    };
    const saved = viewCamerasRef.current[view];
    if (saved !== null) {
      map.easeTo({ ...saved, duration: prefersReducedMotion() ? 0 : CAMERA_TRANSITION_MS });
      return;
    }
    const data = geoDataRef.current;
    if (data !== null && data.deviceCount > 0) {
      fitGeoBounds(map, data, view);
      return;
    }
    map.easeTo({
      pitch: view === 'geo3d' ? PITCH_3D : PITCH_2D,
      bearing: view === 'geo3d' ? BEARING_3D : 0,
      duration: prefersReducedMotion() ? 0 : CAMERA_TRANSITION_MS,
    });
  }, [view, sourcesReady]);

  // —— 地形（W17）：3D 视图 + 偏好开 → 离线 DEM 起伏；回 2D / 关偏好卸载。
  // 2D 下地形无视觉贡献但占渲染开销，故按视图显式开关；hillshade（2D 也有
  //  山体阴影）走 applyLayerVisibilities。effective 入依赖：底图切换重建样式
  //  会清 terrain，此 effect 负责重挂。 ——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    applyTerrainFor(map, view, prefs.terrain);
  }, [view, prefs.terrain, sourcesReady, effective]);

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
  const toggleTerrain = useCallback((): void => {
    setPrefs((p) => ({ ...p, terrain: !p.terrain }));
  }, []);
  const toggleContours = useCallback((): void => {
    setPrefs((p) => ({ ...p, contours: !p.contours }));
  }, []);
  /** 一键恢复完整底图（#50）：重图层默认懒加载后的找回入口——全部离线
   *  叠加 + 地形 + 等高线一次开（懒源补拉由 prefs effect 统一执行）。 */
  const restoreFullBasemap = useCallback((): void => {
    setPrefs((p) => ({
      ...p,
      overlays: {
        admin: true,
        railway: true,
        road: true,
        forest: true,
        water: true,
        green: true,
        places: true,
        cable: true,
        basestation: true,
      },
      terrain: true,
      contours: true,
    }));
  }, []);

  const overlaysUsable = overlaysAvailable(effective);
  const basemapChoices: Array<{ id: BasemapId; label: string; title: string }> = [
    { id: 'offline', label: LAB_GEO_BM_OFFLINE, title: LAB_GEO_BM_OFFLINE_TITLE },
    { id: 'amap', label: LAB_GEO_BM_AMAP, title: LAB_GEO_BM_AMAP_TITLE },
    { id: 'amap-satellite', label: LAB_GEO_BM_AMAP_SAT, title: LAB_GEO_BM_AMAP_SAT_TITLE },
    { id: 'esri', label: LAB_GEO_BM_ESRI, title: LAB_GEO_BM_ESRI_TITLE },
  ];
  /** 自然要素/地名（双坐标变体，任意底图可用）。 */
  const naturalChoices: Array<{ id: OverlayId; label: string; title: string }> = [
    { id: 'places', label: LAB_GEO_OV_PLACES, title: LAB_GEO_OV_PLACES_TITLE },
    { id: 'forest', label: LAB_GEO_OV_FOREST, title: LAB_GEO_OV_FOREST_TITLE },
    { id: 'water', label: LAB_GEO_OV_WATER, title: LAB_GEO_OV_WATER_TITLE },
    { id: 'green', label: LAB_GEO_OV_GREEN, title: LAB_GEO_OV_GREEN_TITLE },
  ];
  /** 真实网络数据（v0.8.0：任意底图可用，双坐标变体）。 */
  const realdataChoices: Array<{ id: OverlayId; label: string; title: string }> = [
    { id: 'cable', label: LAB_GEO_OV_CABLE, title: LAB_GEO_OV_CABLE_TITLE },
    { id: 'basestation', label: LAB_GEO_OV_BASESTATION, title: LAB_GEO_OV_BASESTATION_TITLE },
  ];
  /** 交通/区划（仅 WGS84 底图可用）。 */
  const overlayChoices: Array<{ id: OverlayId; label: string; title: string }> = [
    { id: 'admin', label: LAB_GEO_OV_ADMIN, title: LAB_GEO_OV_ADMIN_TITLE },
    { id: 'road', label: LAB_GEO_OV_ROAD, title: LAB_GEO_OV_ROAD_TITLE },
    { id: 'railway', label: LAB_GEO_OV_RAIL, title: LAB_GEO_OV_RAIL_TITLE },
  ];

  return (
    <div className="geo-canvas" aria-label={LAB_GEO_MAP_LABEL} style={{ background: basemapBg(effective) }}>
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
      {/* 光标海拔读数（W17/#13）：左下角 ScaleControl 之上，连续跟随光标
          （aria-live off：防屏幕阅读器连读）。超出黑龙江覆盖显示 —。 */}
      {elevReadout !== null && (
        <div
          className="geo-overlay geo-chip geo-elev"
          role="status"
          aria-live="off"
          title={LAB_GEO_ELEV_TITLE}
        >
          {elevReadout.value !== null
            ? format(LAB_GEO_ELEV_VALUE, { n: Math.round(elevReadout.value) })
            : LAB_GEO_ELEV_OUT}
        </div>
      )}
      {/* W16 图层面板：底图模式 + 自然要素 + 交通区划 + 业务图层（画布决定
          显示什么）。v0.6.4：自然要素/地名任意底图可用；交通/区划在 GCJ 底图
          下禁用并提示。右上角常驻折叠钮，展开为分区面板。 */}
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
            <div className="geo-layerpanel-section">{LAB_GEO_LAYERS_NATURAL}</div>
            <div className="geo-layerpanel-options">
              {naturalChoices.map((o) => (
                <label key={o.id} className="geo-layerpanel-opt" title={o.title}>
                  <input type="checkbox" checked={prefs.overlays[o.id]} onChange={() => toggleOverlay(o.id)} />
                  {o.label}
                </label>
              ))}
            </div>
            <div className="geo-layerpanel-section">{LAB_GEO_LAYERS_REALDATA}</div>
            <div className="geo-layerpanel-options">
              {realdataChoices.map((o) => (
                <label key={o.id} className="geo-layerpanel-opt" title={o.title}>
                  <input type="checkbox" checked={prefs.overlays[o.id]} onChange={() => toggleOverlay(o.id)} />
                  {o.label}
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
              {/* 地形高程（W17）：叠加区末尾独立一行——不随 GCJ 底图禁用
                  （DEM 是本地栅格，与任何底图叠加都自洽）。 */}
              <label className="geo-layerpanel-opt" title={LAB_GEO_OV_TERRAIN_TITLE}>
                <input type="checkbox" checked={prefs.terrain} onChange={toggleTerrain} />
                {LAB_GEO_OV_TERRAIN}
              </label>
              {/* 等高线（W17/#13）：GCJ 变体预投影，同样不随底图禁用。 */}
              <label className="geo-layerpanel-opt" title={LAB_GEO_OV_CONTOUR_TITLE}>
                <input type="checkbox" checked={prefs.contours} onChange={toggleContours} />
                {LAB_GEO_OV_CONTOUR}
              </label>
              {/* 一键找回完整底图（#50：默认最小集后重图层全开的入口）。 */}
              <button
                type="button"
                className="geo-layerpanel-restore"
                title={LAB_GEO_RESTORE_FULL_TITLE}
                onClick={restoreFullBasemap}
              >
                {LAB_GEO_RESTORE_FULL}
              </button>
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
