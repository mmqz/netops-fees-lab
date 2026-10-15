/**
 * API 参考页 /dev（v0.10-R9）：GET /api/openapi.json 的目录视图。
 *
 * 后端把全部路由注册在一张表里（internal/api/routes.go），OpenAPI 文档
 * 由同一张表生成——本页再从该端点实时渲染，三层同源，接口目录与实现
 * 零漂移。页面本身是纯只读发现视图：按 tag 分组、方法徽章、路径 mono
 * 与中文摘要；不复制请求/响应 schema（那是 docs 与 TS 客户端的事）。
 *
 * 受众：接入本系统的运维工具开发者与 AI 集成方（人看目录，机器直接
 * 读 /api/openapi.json）。
 */

import { useEffect, useMemo, useState } from 'react';
import type { OpenApiOperation, OpenApiSpec } from '../api/types';
import { formatApiError, getOpenApiSpec } from '../api/client';
import {
  API_REF_EMPTY,
  API_REF_ENDPOINTS,
  API_REF_HINT,
  API_REF_TAG_GROUPS,
  API_TAG_LABELS,
} from '../i18n';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';

/** 一个端点行（路径 + 方法 + 摘要）。 */
interface EndpointEntry {
  path: string;
  method: string;
  summary: string;
  tag: string;
}

const METHOD_ORDER = ['GET', 'POST', 'PATCH', 'DELETE'];

/** 方法徽章的 CSS 变体。 */
function methodClass(method: string): string {
  switch (method) {
    case 'GET':
      return 'api-method api-method--get';
    case 'POST':
      return 'api-method api-method--post';
    case 'PATCH':
      return 'api-method api-method--patch';
    case 'DELETE':
      return 'api-method api-method--delete';
    default:
      return 'api-method';
  }
}

/** spec → 平铺端点列表（每路径恰一方法；防御性支持多方法）。 */
function flatten(spec: OpenApiSpec): EndpointEntry[] {
  const entries: EndpointEntry[] = [];
  for (const [path, item] of Object.entries(spec.paths)) {
    const perMethod: Array<[string, OpenApiOperation | undefined]> = [
      ['GET', item.get],
      ['POST', item.post],
      ['PATCH', item.patch],
      ['DELETE', item.delete],
    ];
    for (const [method, op] of perMethod) {
      if (op === undefined) continue;
      entries.push({
        path,
        method,
        summary: op.summary,
        tag: op.tags[0] ?? 'system',
      });
    }
  }
  return entries;
}

export function ApiReferencePage() {
  const [spec, setSpec] = useState<OpenApiSpec | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    getOpenApiSpec()
      .then((res) => {
        if (!live) return;
        setSpec(res);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!live) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
    return () => {
      live = false;
    };
  }, []);

  const entries = useMemo(() => (spec === null ? [] : flatten(spec)), [spec]);

  // 分组保持文档 tags 的顺序（后端路由表首次出现序，确定性）
  const groups = useMemo(() => {
    const byTag = new Map<string, EndpointEntry[]>();
    for (const tag of spec?.tags ?? []) {
      byTag.set(tag.name, []);
    }
    for (const entry of entries) {
      if (!byTag.has(entry.tag)) byTag.set(entry.tag, []);
      byTag.get(entry.tag)!.push(entry);
    }
    for (const list of byTag.values()) {
      list.sort((a, b) => a.path.localeCompare(b.path) || METHOD_ORDER.indexOf(a.method) - METHOD_ORDER.indexOf(b.method));
    }
    return [...byTag.entries()];
  }, [entries, spec]);

  return (
    <div className="card">
      <div className="muted-note table-hint">{API_REF_HINT}</div>

      {loading ? (
        <Spinner />
      ) : error !== null ? (
        <ErrorBlock message={error} detail={errorDetail} onRetry={() => window.location.reload()} />
      ) : spec === null ? (
        <div className="empty-note">{API_REF_EMPTY}</div>
      ) : (
        <>
          <div className="muted-note">
            {spec.info.title} · {spec.info.version} · {entries.length} {API_REF_ENDPOINTS}
          </div>
          <div className="evidence-title">{API_REF_TAG_GROUPS}</div>
          {groups.map(([tag, list]) => (
            <section key={tag} className="api-group">
              <h3 className="api-group-title">
                {API_TAG_LABELS[tag] ?? tag}
                <span className="api-group-count">{list.length}</span>
              </h3>
              <ul className="api-list">
                {list.map((entry) => (
                  <li key={`${entry.method} ${entry.path}`} className="api-item">
                    <span className={methodClass(entry.method)}>{entry.method}</span>
                    <code className="mono api-path">{entry.path}</code>
                    <span className="api-summary">{entry.summary}</span>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </>
      )}
    </div>
  );
}
