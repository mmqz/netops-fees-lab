/**
 * 质量报告页 /report：
 * - 批次范围选择（全部 / 各批次），支持 ?batchId= 深链；
 * - 常驻琥珀色来源中立免责声明横幅（issue #57②：不再断言「合成数据」）；
 * - iframe srcdoc 预览 GET /api/report?format=html 的独立 HTML 报告；
 * - 下载 HTML / 下载 JSON（Blob 下载）；刷新 / 重新校验（POST /api/validate）。
 */

import { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { BatchListItem } from '../api/types';
import { formatApiError, getReportHtml, getReportJson, listBatches, runValidate } from '../api/client';
import { useSeq } from '../hooks/useSeq';
import {
  BATCH_STATUS_LABELS,
  LAB_CAPPED_HINT,
  BTN_DOWNLOAD_HTML,
  BTN_DOWNLOAD_JSON,
  BTN_REFRESH,
  BTN_REVALIDATE,
  format,
  RPT_DISCLAIMER,
  RPT_FILE_EXT_HTML,
  RPT_FILE_EXT_JSON,
  RPT_FILENAME_HTML,
  RPT_FILENAME_JSON,
  RPT_LOADING,
  RPT_PREVIEW_TITLE,
  RPT_SCOPE,
  RPT_SCOPE_ALL,
  TOAST_VALIDATE_DONE,
} from '../i18n';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';
import { useToast } from '../components/Toast';
import { IconDownload, IconRefresh } from '../components/icons';

const BATCH_OPTION_LIMIT = 100;

function downloadText(text: string, filename: string, mime: string): void {
  const blob = new Blob([text], { type: mime });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function ReportPage() {
  const [searchParams] = useSearchParams();
  const urlBatchId = searchParams.get('batchId') ?? '';
  const toast = useToast();

  const [selectedBatch, setSelectedBatch] = useState<string>(urlBatchId);
  const [batches, setBatches] = useState<BatchListItem[] | null>(null);
  const [batchesCapped, setBatchesCapped] = useState(false);
  const [html, setHtml] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [validating, setValidating] = useState(false);
  const [downloadingJson, setDownloadingJson] = useState(false);
  /** 报告范围切换的陈旧响应守卫（R2-P2-3：防慢响应后到覆盖新批次预览）。 */
  const reportGuard = useSeq();

  useEffect(() => {
    setSelectedBatch(urlBatchId);
  }, [urlBatchId]);

  useEffect(() => {
    listBatches(1, BATCH_OPTION_LIMIT)
      .then((paged) => {
        setBatches(paged.items);
        setBatchesCapped(paged.total > paged.items.length);
      })
      .catch(() => setBatches([]));
  }, []);

  const load = useCallback((): void => {
    const isCurrent = reportGuard.begin();
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    getReportHtml(selectedBatch === '' ? undefined : selectedBatch)
      .then((text) => {
        if (!isCurrent()) return;
        setHtml(text);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [selectedBatch, reportGuard]);

  useEffect(() => {
    load();
  }, [load]);

  function reportFilename(base: string, ext: string): string {
    return selectedBatch === '' ? `${base}${ext}` : `${base}-${selectedBatch}${ext}`;
  }

  function revalidate(): void {
    setValidating(true);
    runValidate()
      .then((response) => {
        setValidating(false);
        toast.show(format(TOAST_VALIDATE_DONE, { score: response.score }), 'success');
        load();
      })
      .catch((err: unknown) => {
        setValidating(false);
        const display = formatApiError(err);
        toast.show(display.title, 'error', { detail: display.detail });
      });
  }

  function downloadHtml(): void {
    if (html === null) return;
    downloadText(html, reportFilename(RPT_FILENAME_HTML, RPT_FILE_EXT_HTML), 'text/html;charset=utf-8');
  }

  function downloadJson(): void {
    setDownloadingJson(true);
    getReportJson(selectedBatch === '' ? undefined : selectedBatch)
      .then((text) => {
        setDownloadingJson(false);
        downloadText(
          text,
          reportFilename(RPT_FILENAME_JSON, RPT_FILE_EXT_JSON),
          'application/json;charset=utf-8',
        );
      })
      .catch((err: unknown) => {
        setDownloadingJson(false);
        const display = formatApiError(err);
        toast.show(display.title, 'error', { detail: display.detail });
      });
  }

  return (
    <>
      <div className="banner banner--warning" role="note">
        {RPT_DISCLAIMER}
      </div>

      <div className="card report-controls">
        <label className="filter-field">
          <span className="filter-label">{RPT_SCOPE}</span>
          <select
            className="select"
            value={selectedBatch}
            disabled={batches === null}
            onChange={(event) => setSelectedBatch(event.target.value)}
          >
            <option value="">{RPT_SCOPE_ALL}</option>
            {(batches ?? []).map((batch) => (
              <option key={batch.id} value={batch.id}>
                {batch.workbookName}（{BATCH_STATUS_LABELS[batch.status] ?? batch.status}）
              </option>
            ))}
          </select>
          {batchesCapped && (
            <span className="muted-note">{format(LAB_CAPPED_HINT, { n: BATCH_OPTION_LIMIT })}</span>
          )}
        </label>
        <div className="report-actions">
          <button type="button" className="btn btn--secondary" onClick={load} disabled={loading}>
            <IconRefresh />
            {BTN_REFRESH}
          </button>
          <button
            type="button"
            className="btn btn--secondary"
            onClick={revalidate}
            disabled={validating}
          >
            {validating ? <Spinner size={14} /> : null}
            {BTN_REVALIDATE}
          </button>
          <button
            type="button"
            className="btn btn--secondary"
            onClick={downloadHtml}
            disabled={html === null || loading}
          >
            <IconDownload />
            {BTN_DOWNLOAD_HTML}
          </button>
          <button
            type="button"
            className="btn btn--secondary"
            onClick={downloadJson}
            disabled={downloadingJson}
          >
            <IconDownload />
            {BTN_DOWNLOAD_JSON}
          </button>
        </div>
      </div>

      <div className="card">
        <h2 className="card-title">{RPT_PREVIEW_TITLE}</h2>
        {loading ? (
          <div className="loading-panel">
            <Spinner />
            <span className="loading-text">{RPT_LOADING}</span>
          </div>
        ) : error !== null ? (
          <ErrorBlock message={error} detail={errorDetail} onRetry={load} />
        ) : (
          <iframe
            className="report-frame"
            title={RPT_PREVIEW_TITLE}
            srcDoc={html ?? ''}
            sandbox=""
          />
        )}
      </div>
    </>
  );
}
