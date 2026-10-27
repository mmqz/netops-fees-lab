import { useEffect, useRef, useState } from 'react';
import type { Map as MlMap, StyleSpecification } from 'maplibre-gl';
import { fetchFeeRiskMap } from '../api/client';
import type { RiskMapReport } from '../api/types';
import { formatApiErrorText } from '../api/client';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';
import { computeFitTarget, filterGeoPoints, pointColor } from './feesRiskMapModel';

/**
 * FeesRiskMap — #18 站点风险地图：十场景线索按站址聚合，confirmed 关联资源
 * 的 geo_lat/geo_lng 落点着色（红=已核实 / 黄=待核实 / 绿=无线索）。
 *
 * 职责边界（#16 同款）：云端只做渲染能力，经纬度数据由本地资源表自带。
 *
 * 空数据纪律（#26 修复 1）：**无坐标即整体不渲染，不以数据是否到达为前提**
 * ——数据未到达时也不预挂地图组件（旧代码在 report===null 窗口期挂图，
 * 空点位流进 fitBounds 触发 MapLibre「Invalid LngLat latitude value」必现异常）。
 * 视口决策全部走 feesRiskMapModel.computeFitTarget（0 点不碰视口 / 单点居中
 * 定焦 / 多点 fitBounds——#26 修复 2+3），组件不自算经纬度。
 *
 * 渲染教义：确定性（同数据同输出）——点位数据驱动、无随机、无动画漂移；
 * maplibre 与其 CSS **动态 import**（零首屏成本——首屏不为此付出 1MB+）。
 */

const ESRI_DARK = 'https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Base/MapServer/tile/{z}/{y}/{x}';
const ESRI_ATTR = '<a href="https://www.arcgis.com/" target="_blank" rel="noreferrer">© Esri</a>';

const MAP_H = 320;

export function FeesRiskMap(props: { feeType: string; year: number; month: number }) {
  const { feeType, year, month } = props;
  const [report, setReport] = useState<RiskMapReport | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [mapReady, setMapReady] = useState(false);
  // 数据版本号：每次成功取数自增，作 MapCanvas 的 remount key——
  // MapCanvas 的建图 effect 是挂载期一次性的（[], 挂载时把点位快照进 GeoJSON
  // source），账期/费用类型切换后点位集变了就必须重挂（#26 连带修复：
  // 旧代码无 key，切换后地图显示过期点位）。
  const [dataSeq, setDataSeq] = useState(0);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<MlMap | null>(null);

  useEffect(() => {
    let cancelled = false;
    // v0.20.3 双护栏的内层：month<=0（账期未解析/所选年全年无数据）没有
    // 可定义的地图范围——不取数不落态（外层 FeesOverview 已不挂载本组件，
    // 这里防的是未来其它挂载点；旧版正是把 month=0 发出去吃 400 校验错）。
    if (month <= 0) {
      setReport(null);
      setError('');
      setLoading(false);
      return;
    }
    setError('');
    setLoading(true);
    fetchFeeRiskMap({ feeType, year, month })
      .then((r) => {
        if (!cancelled) {
          setReport(r);
          setDataSeq((n) => n + 1);
        }
      })
      .catch((err) => {
        if (!cancelled) setError(formatApiErrorText(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [feeType, year, month]);

  // 错误先行（#26 修复时重排）：非 501 错误必须可见，不能被下面
  // 「无坐标不渲染」吞掉——错误面板优先于一切空态。
  if (error !== '' && report === null) {
    // 501（默认构建无引擎）静默——与页面其它引擎依赖面板同款降级
    if (error.includes('501') || error.includes('engine')) return null;
    return <ErrorBlock message={error} />;
  }

  // v0.20.3：month<=0 不渲染（与外层护栏互为冗余——本组件自身也绝不
  // 在无可定义账期范围时占位）。数据未到时 report 仍为 null，走下方
  // 「无坐标不渲染」同款空态。
  if (month <= 0) return null;

  // #26 修复 1：geoPoints 为空即整体不渲染——无 report 前置（数据未达时
  // 也不预挂地图）；loading 窗口期本面板保持不出现，数据到了自然恢复。
  const geoPoints = filterGeoPoints(report?.points ?? []);
  if (geoPoints.length === 0) return null;

  return (
    <section className="panel">
      <h2 className="panel__title">站点风险地图</h2>
      <p className="page__desc">
        线索站址按 confirmed 关联资源的经纬度落点——红=已核实 / 黄=待核实 / 绿=无线索；
        坐标来自本地资源表（无坐标的关联站址 {Math.max((report?.linkedSites ?? 0) - (report?.geoSites ?? 0), 0)} 个不落点）。
      </p>
      <div style={{ position: 'relative', height: MAP_H }}>
        <div ref={containerRef} style={{ position: 'absolute', inset: 0, borderRadius: 8, overflow: 'hidden' }} />
        {(loading || !mapReady) && (
          <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', background: 'rgba(255,255,255,.6)' }}>
            <Spinner />
          </div>
        )}
      </div>
      <MapCanvas
        key={dataSeq}
        containerRef={containerRef}
        mapRef={mapRef}
        points={geoPoints}
        onReady={() => setMapReady(true)}
      />
    </section>
  );
}

/** 地图画布本体：动态加载 maplibre（库+CSS 同点），点位 GeoJSON 数据驱动。 */
function MapCanvas(props: {
  containerRef: React.RefObject<HTMLDivElement | null>;
  mapRef: { current: MlMap | null };
  points: Array<{ siteCode: string; lat: number; lng: number; clueCount: number; pending: number; verified: number; scenarios: string[]; resourceKey: string }>;
  onReady: () => void;
}) {
  const { containerRef, mapRef, points, onReady } = props;
  const [libError, setLibError] = useState('');

  useEffect(() => {
    let disposed = false;
    let map: MlMap | null = null;
    // 动态 import：maplibre 全家（库 / CSS / worker URL）同点加载（首屏零成本）。
    // worker URL 必须在建图之前设置：默认按 import.meta.url 推导，打包后指向
    // 不存在的路径 → "Worker failed to load"（主仓 GeoCanvas 同款教训 W5-D）。
    Promise.all([
      import('maplibre-gl'),
      import('maplibre-gl/dist/maplibre-gl.css'),
      import('maplibre-gl/dist/maplibre-gl-worker.mjs?url'),
    ])
      .then(([ml, , workerUrlMod]) => {
        ml.setWorkerUrl((workerUrlMod as { default: string }).default)
        if (disposed || !containerRef.current) return;
        map = new ml.Map({
          container: containerRef.current,
          style: {
            version: 8,
            sources: {
              basemap: {
                type: 'raster',
                tiles: [ESRI_DARK],
                tileSize: 256,
                attribution: ESRI_ATTR,
              },
            },
            layers: [
              { id: 'basemap', type: 'raster', source: 'basemap' },
            ],
          } as StyleSpecification,
          center: [126.63, 45.75], // 数据驱动视口会立即接管
          zoom: 4,
          attributionControl: false,
        });
        mapRef.current = map;
        map.on('load', () => {
          if (disposed || map === null) return;
          map.addSource('sites', {
            type: 'geojson',
            data: {
              type: 'FeatureCollection',
              features: points.map((p) => ({
                type: 'Feature' as const,
                geometry: { type: 'Point' as const, coordinates: [p.lng, p.lat] },
                properties: {
                  siteCode: p.siteCode,
                  color: pointColor(p),
                  clueCount: p.clueCount,
                  pending: p.pending,
                  verified: p.verified,
                  scenarios: p.scenarios.join('、'),
                  resourceKey: p.resourceKey,
                },
              })),
            },
          });
          map.addLayer({
            id: 'sites-circle',
            type: 'circle',
            source: 'sites',
            paint: {
              'circle-color': ['get', 'color'],
              'circle-radius': ['case', ['>', ['get', 'clueCount'], 1], 9, 6],
              'circle-stroke-width': 1.5,
              'circle-stroke-color': '#ffffff',
            },
          });
          // 视口目标来自模型层（#26 修复 2+3）：
          // - 0 点 → 'none'：不碰视口（绝不把空数组塞进 fitBounds——
          //   Math.min(...[]) === Infinity 会抛 Invalid LngLat；父层保证 ≥1 点，
          //   这里是第二道保险）；
          // - 单点 → setCenter+setZoom（min==max 的退化矩形不交给 fitBounds）；
          // - 多点 → fitBounds 外接矩形（同数据同 fit——确定性）。
          const target = computeFitTarget(points);
          if (target.kind === 'point') {
            map.setCenter(target.center);
            map.setZoom(target.zoom);
          } else if (target.kind === 'bounds') {
            map.fitBounds(target.bounds, { padding: 48, maxZoom: 12, duration: 0 });
          }
          map.on('click', 'sites-circle', (e) => {
            const f = e.features?.[0];
            if (!f || !map) return;
            const pr = f.properties as Record<string, string>;
            new ml.Popup({ offset: 12 })
              .setLngLat(((f.geometry as unknown) as { coordinates: [number, number] }).coordinates)
              .setHTML(
                `<strong>${pr.siteCode}</strong><br/>资源：${pr.resourceKey}<br/>线索 ${pr.clueCount} 条（待核实 ${pr.pending} / 已核实 ${pr.verified}）<br/><span style="font-size:12px;color:#666">${pr.scenarios}</span>`,
              )
              .addTo(map);
          });
          onReady();
        });
      })
      .catch((err) => {
        if (!disposed) setLibError(String(err));
      });
    return () => {
      disposed = true;
      map?.remove();
      mapRef.current = null;
    };
    // points 由父组件每次数据变化重挂（dataSeq 作 key——见 FeesRiskMap 注释），
    // 此处只在挂载时建图。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (libError !== '') return <ErrorBlock message={`地图组件加载失败：${libError}`} />;
  return null;
}
