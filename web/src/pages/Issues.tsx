/**
 * 问题清单页 /issues：
 * - 筛选：严重级别 / 规则（10 条规则中文名）/ 批次（来自 /api/batches）；
 * - 表格：级别徽章、规则、消息、实体（链接到资源抽屉）、来源（或「系统生成」）；
 * - 行点击展开证据明细（键/值表）；分页；
 * - 支持 ?batchId= 深链（导入向导「查看问题清单」入口）。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import type {
  BatchListItem,
  IssueItem,
  Paged,
  RuleCode,
  Severity,
} from '../api/types';
import { ApiError, listBatches, listIssues } from '../api/client';
import {
  APP_GENERATED_LABEL,
  BATCH_STATUS_LABELS,
  FILTER_ALL,
  format,
  ISS_BATCH_OPTION,
  ISS_COL_ENTITY,
  ISS_COL_MESSAGE,
  ISS_COL_RULE,
  ISS_COL_SEVERITY,
  ISS_COL_SOURCE,
  ISS_EVIDENCE_EMPTY,
  ISS_EVIDENCE_KEY,
  ISS_EVIDENCE_TITLE,
  ISS_EVIDENCE_VALUE,
  ISS_EXPAND_HINT,
  ISS_FILTER_BATCH,
  ISS_FILTER_RULE,
  ISS_FILTER_SEVERITY,
  ISS_SOURCE_TEXT,
  NAV_ISSUES,
  NO_VALUE,
  RULE_LABELS,
  SEVERITY_LABELS,
} from '../i18n';
import { ErrorBlock } from '../components/ErrorBlock';
import { Pagination } from '../components/Pagination';
import { RuleChip } from '../components/RuleChip';
import { SeverityBadge } from '../components/SeverityBadge';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';

const PAGE_SIZE = 20;
const BATCH_OPTION_LIMIT = 100;

const SEVERITY_OPTIONS: Severity[] = ['error', 'warning', 'info'];
const RULE_OPTIONS: RuleCode[] = [
  'E001',
  'E002',
  'E003',
  'E004',
  'E005',
  'E006',
  'W001',
  'W002',
  'W003',
  'I001',
];

export function IssuesPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const urlBatchId = searchParams.get('batchId') ?? '';

  const [severity, setSeverity] = useState<Severity | ''>('');
  const [ruleCode, setRuleCode] = useState<RuleCode | ''>('');
  const [batchId, setBatchId] = useState<string>(urlBatchId);
  const [page, setPage] = useState(1);

  const [batches, setBatches] = useState<BatchListItem[] | null>(null);
  const [data, setData] = useState<Paged<IssueItem> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const requestSeqRef = useRef(0);

  // 批次下拉选项（辅助数据：失败不阻塞页面，仅缺失该筛选器）
  useEffect(() => {
    listBatches(1, BATCH_OPTION_LIMIT)
      .then((paged) => setBatches(paged.items))
      .catch(() => setBatches([]));
  }, []);

  // 深链同步：?batchId= 变化时刷新筛选
  useEffect(() => {
    setBatchId(urlBatchId);
    setPage(1);
  }, [urlBatchId]);

  const fetchIssues = useCallback((): void => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    setError(null);
    listIssues({
      batchId: batchId === '' ? undefined : batchId,
      severity: severity === '' ? undefined : severity,
      ruleCode: ruleCode === '' ? undefined : ruleCode,
      page,
      pageSize: PAGE_SIZE,
    })
      .then((paged) => {
        if (requestSeqRef.current !== seq) return;
        setData(paged);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (requestSeqRef.current !== seq) return;
        setError(err instanceof ApiError ? err.message : null);
        setLoading(false);
      });
  }, [batchId, severity, ruleCode, page]);

  useEffect(() => {
    fetchIssues();
  }, [fetchIssues]);

  function changeBatch(value: string): void {
    setBatchId(value);
    setPage(1);
    const next = new URLSearchParams();
    if (value !== '') {
      next.set('batchId', value);
    }
    setSearchParams(next, { replace: true });
  }

  const columns = useMemo<TableColumn<IssueItem>[]>(
    () => [
      { key: 'severity', header: ISS_COL_SEVERITY, render: (row) => <SeverityBadge severity={row.severity} /> },
      {
        key: 'rule',
        header: ISS_COL_RULE,
        render: (row) => <RuleChip code={row.ruleCode} severity={row.severity} />,
      },
      { key: 'message', header: ISS_COL_MESSAGE, render: (row) => row.message },
      {
        key: 'entity',
        header: ISS_COL_ENTITY,
        render: (row) => {
          if (row.entityId !== undefined && row.entityId !== '') {
            const text = row.entityKey !== undefined && row.entityKey !== '' ? row.entityKey : row.entityId;
            return (
              <Link
                className="entity-link"
                to={`/resources?focus=${encodeURIComponent(row.entityId)}`}
                onClick={(event) => event.stopPropagation()}
              >
                {text}
              </Link>
            );
          }
          if (row.entityKey !== undefined && row.entityKey !== '') {
            return <span className="muted-note">{row.entityKey}</span>;
          }
          return NO_VALUE;
        },
      },
      {
        key: 'source',
        header: ISS_COL_SOURCE,
        render: (row) =>
          row.source === undefined || row.source === null ? (
            <span className="chip chip--muted">{APP_GENERATED_LABEL}</span>
          ) : (
            <span className="muted-note">
              {format(ISS_SOURCE_TEXT, {
                workbook: row.source.workbook,
                sheet: row.source.sheet,
                row: row.source.rowNumber,
              })}
            </span>
          ),
      },
    ],
    [],
  );

  return (
    <div className="card">
      <div className="filter-bar">
        <label className="filter-field">
          <span className="filter-label">{ISS_FILTER_SEVERITY}</span>
          <select
            className="select"
            value={severity}
            onChange={(event) => {
              setSeverity(event.target.value === '' ? '' : (event.target.value as Severity));
              setPage(1);
            }}
          >
            <option value="">{FILTER_ALL}</option>
            {SEVERITY_OPTIONS.map((value) => (
              <option key={value} value={value}>
                {SEVERITY_LABELS[value]}
              </option>
            ))}
          </select>
        </label>
        <label className="filter-field">
          <span className="filter-label">{ISS_FILTER_RULE}</span>
          <select
            className="select"
            value={ruleCode}
            onChange={(event) => {
              setRuleCode(event.target.value === '' ? '' : (event.target.value as RuleCode));
              setPage(1);
            }}
          >
            <option value="">{FILTER_ALL}</option>
            {RULE_OPTIONS.map((code) => (
              <option key={code} value={code}>
                {code} {RULE_LABELS[code]}
              </option>
            ))}
          </select>
        </label>
        <label className="filter-field">
          <span className="filter-label">{ISS_FILTER_BATCH}</span>
          <select
            className="select"
            value={batchId}
            disabled={batches === null}
            onChange={(event) => changeBatch(event.target.value)}
          >
            <option value="">{FILTER_ALL}</option>
            {(batches ?? []).map((batch) => (
              <option key={batch.id} value={batch.id}>
                {format(ISS_BATCH_OPTION, {
                  workbook: batch.workbookName,
                  status: BATCH_STATUS_LABELS[batch.status] ?? batch.status,
                })}
              </option>
            ))}
          </select>
        </label>
      </div>

      <div className="muted-note table-hint">{ISS_EXPAND_HINT}</div>

      {error !== null ? (
        <ErrorBlock message={error} onRetry={fetchIssues} />
      ) : (
        <>
          <Table
            columns={columns}
            rows={data?.items ?? []}
            rowKey={(row) => row.id}
            loading={loading}
            renderExpanded={(row) => (
              <div className="evidence">
                <div className="evidence-title">{ISS_EVIDENCE_TITLE}</div>
                {row.evidence === undefined || Object.keys(row.evidence).length === 0 ? (
                  <div className="muted-note">{ISS_EVIDENCE_EMPTY}</div>
                ) : (
                  <table className="kv-table">
                    <thead>
                      <tr>
                        <th scope="col">{ISS_EVIDENCE_KEY}</th>
                        <th scope="col">{ISS_EVIDENCE_VALUE}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {Object.entries(row.evidence).map(([key, value]) => (
                        <tr key={key}>
                          <th scope="row">{key}</th>
                          <td>
                            <span className="mono">{value}</span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            )}
            ariaLabel={NAV_ISSUES}
          />
          {data !== null && data.total > 0 && (
            <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />
          )}
        </>
      )}
    </div>
  );
}
