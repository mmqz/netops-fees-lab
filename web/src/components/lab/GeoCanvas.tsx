/**
 * GeoCanvas —— 画布地理视图（GEO-2）：真实经纬度 2D/3D 地图（maplibre-gl，懒加载）。
 *
 * 在 TopologyCanvas 的视图维度（拓扑图 | 2D 地图 | 3D 地图）中承载后两种：
 * - 设备按 geoLat/geoLng 落位为 HTML Marker（状态色圆点 + 编码牌，
 *   键盘可聚焦、Enter/Space 触发选择，与右侧表格联动语义同拓扑图 tap）；
 * - 链路为 GeoJSON LineString 图层：up #5b8fd9 2px 实线 / down #ff5252 3px
 *   虚线（rAF 驱动 marching-ants 动画，prefers-reduced-motion 关闭）/
 *   选中 #2f9dff 4px；端点缺坐标的链路跳过（右下角计数 chip 提示）；
 * - 3D 塔（fill-extrusion）：像素高 16 + 连接数×9（按 zoom 反比换算米高，
 *   省级恒定可见），仅 3D 视图可见；
 *   2D↔3D 切换 easeTo pitch 0|58（3D 同时 bearing -30 斜视角）；
 * - 底图 ESRI World_Dark_Gray 栅格瓦片（Base + Reference 双层，免密钥；attribution
 *   法律要求保留）；瓦片连续 ≥3 次
 *   失败 → 重建无栅格样式（本地 GeoJSON 恒可渲染）+「离线底图模式」chip；
 * - 全部设备无坐标 → 居中引导 +「返回拓扑图」；
 * - 悬停地标复用画布左上角信息条（onHoverInfo 回调，文案同拓扑 hover）；
 * - 自带 ResizeObserver → map.resize()（zen 全屏 / 分栏拖拽由父级传
 *   resizeSuspended 挂起，拖完补偿一次）；卸载 map.remove() + 取消 rAF，
 *   反复切换视图不泄漏 WebGL 上下文。
 * maplibre-gl 经 `await import(...)` 懒加载（独立异步 chunk，主包不增重）。
 * 调试探针：实例挂 window.__geomap（供 e2e 验证 getPitch/getBearing 等）。
 */

import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import type { Ref } from 'react';
import type {
  ErrorEvent as MLErrorEvent,
  Map as MLMap,
  MapSourceDataEvent,
  MapStyleDataEvent,
  Marker as MLMarker,
  StyleSpecification,
} from 'maplibre-gl';
import type { LayerSpecification, FilterSpecification, ExpressionSpecification } from '@maplibre/maplibre-gl-style-spec';
import type { Feature, FeatureCollection, LineString, Point, Polygon, Position } from 'geojson';
// maplibre 全家（库本体 / 样式表 / worker URL）均不静态 import（W5-D 分包）：
// 静态 import 会把 vendor-maplibre 变成入口的静态依赖（modulepreload
// ~1.08 MB JS + 首屏 CSS），失去 ADR-0009 的懒加载语义；三者在首次
// 地理视图挂载时同点动态 import。
import type { SimEntityStatus, SimTopology } from '../../api/types';
import type { SelectedEntity } from './selection';
import {
  LAB_CANVAS_LEGEND_LABEL,
  LAB_GEO_LEGEND_TOWER,
  LAB_GEO_MAP_LABEL,
  LAB_GEO_MARKER_LABEL,
  LAB_GEO_NOGEO,
  LAB_GEO_NOGEO_BACK,
  LAB_GEO_OFFLINE,
  LAB_GEO_OFFLINE_TITLE,
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

/** ESRI 深色画布底图（Canvas/World_Dark_Gray，免密钥匿名可用；2026-10 实测
 *  CARTO dark_all 对匿名 IP 统一返回 2513B 的「API KEY REQUIRED」水印砖，已弃用）。
 *  Reference 层叠加地名/边界注记，提供地理参照。注意 ESRI 的 z/y/x 路径顺序。 */
const ESRI_DARK_BASE_TILES =
  'https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Base/MapServer/tile/{z}/{y}/{x}';
const ESRI_DARK_REF_TILES =
  'https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Reference/MapServer/tile/{z}/{y}/{x}';
const BASEMAP_SOURCE_ID = 'basemap';
const BASEMAP_REF_SOURCE_ID = 'basemap-ref';
const BASEMAP_ATTRIBUTION = 'Esri, HERE, Garmin, FAO, NOAA, USGS';

/** 画布底色（与 .lab-canvas-wrap--dark / style background 一致，加载期不闪白）。 */
const GEO_BG = '#0e1626';

/** 链路配色（规格钉死：up #5b8fd9 2 / down #ff5252 3 虚线 / 选中 #2f9dff 4）。 */
const LINK_UP_COLOR = '#5b8fd9';
const LINK_DOWN_COLOR = '#ff5252';
const LINK_SELECTED_COLOR = '#2f9dff';

/** 设备地面圆点（与拓扑节点徽章同系：up 浅蓝底 / down 停机红）。 */
const DEVICE_UP_FILL = '#dce6f7';
const DEVICE_UP_STROKE = '#8fa6ce';
const DEVICE_DOWN_FILL = '#f2554a';
const DEVICE_DOWN_STROKE = '#ff9d95';

/** 3D 塔参数：footprint 恒定像素宽（随 zoom 重建多边形，亚像素问题修复：
 *  固定 0.0016° 在省级缩放下仅 0.04px，光栅化不出面）与像素高 18 + 连接数 × 10
 *  （样式层按 zoom 几何插值换算米高，省级恒定可见）。 */
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

/** 底图瓦片连续失败阈值 → 切离线底图模式。 */
const TILE_FAIL_LIMIT = 3;

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
  /** 3D 塔目标像素高：16 + 连接数 × 9（样式层按 zoom 换算为米）。 */
  towerPx: number;
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
  /** 设备包围盒（fitBounds 用）；无设备为 null。 */
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

/** zoom 下某纬度每屏幕像素的米数（Web 墨卡托）。 */
function metersPerPixel(lat: number, zoom: number): number {
  return (156543.03392 * Math.cos((lat * Math.PI) / 180)) / Math.pow(2, zoom);
}

export function buildGeoData(topology: SimTopology | null, selected: SelectedEntity | null): GeoData {
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
      const pos: [number, number] = [lng, lat];
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
    // 塔不在此构建：footprint 需按当前 zoom 换算（恒定像素宽），由
    // buildTowerCollection + zoomend 重建路径单独供给（首次渲染前为空集）。
    towers: emptyFC() as FeatureCollection<Polygon, GeoTowerProps>,
    deviceCount: deviceFeatures.length,
    missingCount,
    skippedLinks,
    downLinks,
    bbox,
  };
}

/** 塔集合（纯函数）：塔与设备一一对应，footprint 按当前 zoom 恒定 7px 宽，
 *  像素高 18 + 连接数 × 10（高度换算见样式层注释）。zoom 变化后重建。 */
export function buildTowerCollection(
  topology: SimTopology | null,
  zoom: number
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
    const mpp = metersPerPixel(lat, zoom);
    const halfLat = (TOWER_FOOTPRINT_PX * mpp) / 111320;
    const cosLat = Math.max(0.2, Math.cos((lat * Math.PI) / 180));
    const halfLng = (TOWER_FOOTPRINT_PX * mpp) / (111320 * cosLat);
    features.push({
      type: 'Feature',
      id: node.id,
      geometry: { type: 'Polygon', coordinates: towerPolygon(lng, lat, halfLng, halfLat) },
      properties: {
        id: node.id,
        status: node.status,
        towerPx: TOWER_PX_BASE + (degree.get(node.id) ?? 0) * TOWER_PX_PER_DEGREE,
      },
    });
  }
  return { type: 'FeatureCollection', features };
}

// —— 样式构建（纯函数：瓦片失败后可按 withRaster=false 重建） ——

const statusMatch = (down: string, up: string): ExpressionSpecification =>
  ['match', ['get', 'status'], 'down', down, up] as unknown as ExpressionSpecification;

const eqFilter = (field: string, value: string): FilterSpecification =>
  ['==', ['get', field], value] as unknown as FilterSpecification;

const isTrueFilter = (field: string): FilterSpecification =>
  ['==', ['get', field], true] as unknown as FilterSpecification;

function emptyFC(): FeatureCollection {
  return { type: 'FeatureCollection', features: [] };
}

export function buildGeoStyle(withRaster: boolean): StyleSpecification {
  const sources: StyleSpecification['sources'] = {
    devices: { type: 'geojson', data: emptyFC() },
    links: { type: 'geojson', data: emptyFC() },
    towers: { type: 'geojson', data: emptyFC() },
  };
  if (withRaster) {
    sources[BASEMAP_SOURCE_ID] = {
      type: 'raster',
      tiles: [ESRI_DARK_BASE_TILES],
      maxzoom: 16,
      tileSize: 256,
      attribution: BASEMAP_ATTRIBUTION,
    };
    sources[BASEMAP_REF_SOURCE_ID] = {
      type: 'raster',
      tiles: [ESRI_DARK_REF_TILES],
      maxzoom: 16,
      tileSize: 256,
    };
  }
  const layers: LayerSpecification[] = [
    { id: 'geo-bg', type: 'background', paint: { 'background-color': GEO_BG } },
  ];
  if (withRaster) {
    layers.push({ id: BASEMAP_SOURCE_ID, type: 'raster', source: BASEMAP_SOURCE_ID, paint: {} });
    layers.push({
      id: BASEMAP_REF_SOURCE_ID,
      type: 'raster',
      source: BASEMAP_REF_SOURCE_ID,
      paint: { 'raster-opacity': 0.85 },
    });
  }
  layers.push(
    {
      id: 'links-up',
      type: 'line',
      source: 'links',
      filter: eqFilter('status', 'up'),
      paint: { 'line-color': LINK_UP_COLOR, 'line-width': 2 },
    },
    {
      id: 'links-down',
      type: 'line',
      source: 'links',
      filter: eqFilter('status', 'down'),
      paint: { 'line-color': LINK_DOWN_COLOR, 'line-width': 3, 'line-dasharray': [DASH_ON, DASH_OFF] },
    },
    {
      id: 'links-selected',
      type: 'line',
      source: 'links',
      filter: isTrueFilter('selected'),
      paint: { 'line-color': LINK_SELECTED_COLOR, 'line-width': 4 },
    },
    {
      id: 'towers',
      type: 'fill-extrusion',
      source: 'towers',
      layout: { visibility: 'none' },
      paint: {
        'fill-extrusion-color': statusMatch(LINK_DOWN_COLOR, LINK_UP_COLOR),
        // 塔高（米）= 设备像素高 × 该 zoom 每像素米数。MapLibre 表达式规范：paint
        // 属性里 ["zoom"] 只能作顶层 interpolate 的输入，故用 exponential-2 双停止点
        // z0→9e4、z16→9e4/2^16，几何插值恒等于 9e4×2^-zoom（已折入中纬余弦≈0.66）。
        // 省级 z≈3.2 时 16–51px 恒定可见；街道级 z≈12 时回到真实建筑尺度。
        'fill-extrusion-height': [
          'interpolate',
          ['exponential', 2],
          ['zoom'],
          0,
          ['*', ['get', 'towerPx'], 90000],
          16,
          ['*', ['get', 'towerPx'], 90000 / 65536],
        ] as unknown as ExpressionSpecification,
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
  return { version: 8, sources, layers };
}

// —— 小工具 ——

function applyDataToMap(map: MLMap, data: GeoData): void {
  // 塔不在此处供给（geoData.towers 恒为空集占位）：footprint 需按当前 zoom
  // 重建，由数据 effect 与 zoomend 防抖路径单独 setData，否则会被此处清空。
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

/**
 * fitGeoBounds：按**目标视图**适配相机（v0.3.1）。
 *
 * 2D：沿用 v0.3.0 的 fitBounds（平视假设正确，行为已验收）。
 *
 * 3D：不用 MapLibre 的 fitBounds/cameraForBounds——它们的「恰好容纳」解在
 * 大倾角下过于保守（塔群挤成一撮占屏宽 ~3%，实测 zoom 2.84 相当于全球
 * 视野），而 2D 假设的解在斜视下又怼脸。正解是墨卡托几何直解：给定容器
 * 像素与 bbox 弧度跨度，让塔群占屏宽 ~55%、等效高 ~30%（斜视垂直压缩），
 * clamp [3, 7.2]。一次 easeTo 把 pitch/bearing/zoom/center 全部到位。
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
    // center 北移 ~18% 的 bbox 高：pitch 58 的地面投影中心偏屏幕下方，
    // 北移让塔群落在屏幕中央条带。
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

/**
 * 3D 斜视的 bbox → zoom 直解（纯函数，可用手算复核）：
 * 世界像素宽 256·2^z 对应 2π 弧度。要求 bbox 弧度宽占容器宽 TOWER_FIT_W3D
 * （0.55），墨卡托弧度高占容器高 TOWER_FIT_H3D（0.30 —— 斜视把垂直跨度
 * 压缩到约一半再留地面余量）。zoom 取两个约束的较小者并夹紧。
 *
 * 手算复核（黑龙江孪生 bbox ≈ lng 122.5..130.4 / lat 43.5..53，容器
 * 564×359）：bw = 7.9°·rad = 0.138；mercY(53)−mercY(43.5) ≈ 0.248；
 * zw = log2(564·2π / (256·0.55·0.138)) ≈ 7.5；zh = log2(359·2π /
 * (256·0.30·0.248)) ≈ 6.9 → zoom 6.9（塔群占屏宽 ~55%）。
 */
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
 * 底图瓦片错误判定：tile 失败事件运行时携带 tile 数据（当前样式唯一瓦片源即
 * 底图；本地 GeoJSON 不产瓦片错误），另兜底匹配 sourceId / 消息中的源名或瓦片域名。
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
  return msg.includes(BASEMAP_SOURCE_ID) || msg.includes('cartocdn') || msg.includes('arcgisonline');
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
  const offlineRef = useRef(false);
  const didFitRef = useRef(false);
  const onSelectRef = useRef(onSelect);
  const onHoverInfoRef = useRef(onHoverInfo);
  const selectedRef = useRef(selected);
  const labelsRef = useRef(labels);
  const viewRef = useRef(view);
  topoRef.current = topology;
  const resizeSuspendedRef = useRef(resizeSuspended);
  /** style 的 geojson source 是否就绪（初始加载 / setStyle 重建后置 true）。 */
  const [sourcesReady, setSourcesReady] = useState(false);
  /** 底图瓦片连续失败 → 离线底图模式（重建无 raster 样式）。 */
  const [offline, setOffline] = useState(false);

  useEffect(() => {
    onSelectRef.current = onSelect;
  }, [onSelect]);

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

  const geoData = useMemo(() => buildGeoData(topology, selected), [topology, selected]);

  useEffect(() => {
    geoDataRef.current = geoData;
  }, [geoData]);

  // —— 实例生命周期：挂载即懒加载 maplibre → 建图 → 控件/事件；卸载全量回收 ——
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
      const map = new maplibregl.Map({
        container: div,
        style: buildGeoStyle(true),
        center: DEFAULT_CENTER,
        zoom: DEFAULT_ZOOM,
        // 初始 pitch/bearing 按目标视图设置（v0.3.1）：否则「挂载即 3D」时
        // view effect 会发 easeTo 纠正 pitch，打断首批 fitBounds 动画——
        // 相机停在 DEFAULT_CENTER，正是「3D 视图怼脸/不飞向数据」的竞态根因。
        pitch: viewRef.current === 'geo3d' ? PITCH_3D : PITCH_2D,
        bearing: viewRef.current === 'geo3d' ? BEARING_3D : 0,
        attributionControl: { compact: true },
      });
      localMap = map;
      mapRef.current = map;
      // e2e 调试探针（getPitch / getBearing / getLayoutProperty 断言用）
      (window as unknown as Record<string, unknown>).__geomap = map;

      map.addControl(new maplibregl.NavigationControl({ visualizePitch: true }), 'bottom-right');
      map.addControl(new maplibregl.ScaleControl({ unit: 'metric' }), 'bottom-left');

      // style 的 geojson source 就绪门（初始与 setStyle 重建后都经此置位）
      const onStyleData = (): void => {
        if (map.getSource('devices') === undefined) return;
        map.off('styledata', onStyleData);
        if (!disposed) setSourcesReady(true);
      };
      map.on('styledata', onStyleData);
      cleanups.push(() => map.off('styledata', onStyleData));
      onStyleData();

      // 底图瓦片失败统计：≥3 连续失败 → 离线底图模式（本地数据层恒可渲染）
      let tileFailStreak = 0;
      const onMapError = (ev: MLErrorEvent): void => {
        if (disposed || offlineRef.current || !isBasemapError(ev)) return;
        tileFailStreak += 1;
        if (tileFailStreak < TILE_FAIL_LIMIT) return;
        offlineRef.current = true;
        setOffline(true);
        try {
          map.setStyle(buildGeoStyle(false));
          // 样式重建后 source 全空：等 sources 重建再回填数据与塔可见性
          const reapply = (): void => {
            if (disposed || map.getSource('devices') === undefined) return;
            map.off('styledata', reapply);
            if (geoDataRef.current !== null) applyDataToMap(map, geoDataRef.current);
            const topo = topoRef.current;
            const tsrc = map.getSource('towers') as GeoJSONSourceLike | undefined;
            if (topo !== null && tsrc !== undefined) tsrc.setData(buildTowerCollection(topo, map.getZoom()));
            applyTowerVisibility(map, viewRef.current);
          };
          map.on('styledata', reapply);
          cleanups.push(() => map.off('styledata', reapply));
        } catch {
          // setStyle 异常：保留当前样式（本地数据层未受影响，仅无底图切换）
        }
      };
      const onMapData = (ev: MapSourceDataEvent | MapStyleDataEvent): void => {
        if ((ev as MapSourceDataEvent).sourceId === BASEMAP_SOURCE_ID) tileFailStreak = 0;
      };
      // 塔footprint随zoom重建（防抖 150ms；zoom 动画结束后一次到位）
      let towerTimer: ReturnType<typeof setTimeout> | null = null;
      const onZoomEnd = (): void => {
        if (disposed || towerTimer !== null) return;
        towerTimer = setTimeout(() => {
          towerTimer = null;
          if (disposed) return;
          const topo = topoRef.current;
          if (topo === null) return;
          const src = map.getSource('towers') as GeoJSONSourceLike | undefined;
          if (src !== undefined) src.setData(buildTowerCollection(topo, map.getZoom()));
        }, 150);
      };
      map.on('error', onMapError);
      map.on('data', onMapData);
      map.on('zoomend', onZoomEnd);
      cleanups.push(() => {
        map.off('error', onMapError);
        map.off('data', onMapData);
        map.off('zoomend', onZoomEnd);
        if (towerTimer !== null) clearTimeout(towerTimer);
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
      offlineRef.current = false;
      didFitRef.current = false;
      if ((window as unknown as Record<string, unknown>).__geomap === localMap) {
        delete (window as unknown as Record<string, unknown>).__geomap;
      }
      // 卸载时收掉悬停信息条（避免信息条残留在拓扑视图上）
      onHoverInfoRef.current?.(null);
    };
  }, []);

  // —— 数据 → GeoJSON source（setData 原地更新，相机保持；首批数据 fitBounds）——
  useEffect(() => {
    const map = mapRef.current;
    if (map === null || !sourcesReady) return;
    geoDataRef.current = geoData;
    applyDataToMap(map, geoData);
    // 塔footprint依赖当前 zoom：单独供给（zoomend 防抖重建，见挂载 effect）
    const tsrc = map.getSource('towers') as GeoJSONSourceLike | undefined;
    if (tsrc !== undefined) tsrc.setData(buildTowerCollection(topology, map.getZoom()));
    if (!didFitRef.current && geoData.deviceCount > 0) {
      didFitRef.current = true;
      fitGeoBounds(map, geoData, view);
    }
  }, [geoData, sourcesReady, topology, view]);

  // —— 地标（HTML Marker）：设备增删/状态变化重建；选中与标签走类切换 ——
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
    for (const node of topology.nodes) {
      const lat = node.geoLat;
      const lng = node.geoLng;
      if (lat === undefined || lng === undefined || !Number.isFinite(lat) || !Number.isFinite(lng)) continue;
      // 外层 div 由 maplibre 定位（anchor center，transform 由其管理）；
      // 内层 button 承载视觉与交互（原生 role=button，键盘 Enter/Space 触发选择）
      const outer = document.createElement('div');
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
        .setLngLat([lng, lat])
        .addTo(map);
      markersRef.current.set(node.id, { marker, btn });
    }
  }, [topology]);

  useEffect(() => {
    if (!sourcesReady) return;
    rebuildMarkers();
  }, [topology, sourcesReady, rebuildMarkers]);

  // 选中 / 标签开关 → 地标类切换（setView 数据更新由 links-selected 图层过滤承担）
  useEffect(() => {
    const selectedId = selected?.kind === 'device' ? selected.id : null;
    markersRef.current.forEach((entry, id) => {
      entry.btn.classList.toggle('geo-marker--selected', selectedId === id);
      entry.btn.classList.toggle('geo-marker--nolabel', !labels);
    });
  }, [selected, labels, topology]);

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
  }, [geoData.downLinks, sourcesReady, offline]);

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
      {offline && (
        <div className="geo-overlay geo-chip geo-chip--offline" role="status" title={LAB_GEO_OFFLINE_TITLE}>
          {LAB_GEO_OFFLINE}
        </div>
      )}
      {geoData.missingCount > 0 && geoData.deviceCount > 0 && (
        <div className="geo-overlay geo-chip geo-chip--partial" role="status" title={LAB_GEO_PARTIAL_TITLE}>
          {format(LAB_GEO_PARTIAL, { n: geoData.missingCount })}
        </div>
      )}
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
