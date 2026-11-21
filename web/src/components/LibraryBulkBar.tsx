import React, { useState, useEffect } from 'react';
import {
  AlertTriangle,
  Bell,
  BellOff,
  BookOpen,
  Calendar,
  CheckCircle2,
  Layers,
  RefreshCw,
  Trash2,
  X,
} from 'lucide-react';
import type {
  BulkActionReport,
  BulkActionType,
  BulkItemKind,
  BulkSelectionItem,
} from '../types';

export interface LibraryBulkBarProps {
  selectedItems: BulkSelectionItem[];
  visibleSelectableCount: number;
  allVisibleSelected: boolean;
  hiddenSelectedCount: number;
  isAdmin?: boolean;
  canRefresh?: boolean;
  busy?: boolean;
  report: BulkActionReport | null;
  error: string;
  onToggleSelectAllVisible: () => void;
  onRemoveItem: (kind: BulkItemKind, id: string) => void;
  onClearSelection: () => void;
  onExitSelectionMode: () => void;
  onAction: (action: BulkActionType, options?: { deleteFiles?: boolean }) => void;
  onDismissReport: () => void;
}

function pluralize(count: number, singular: string, plural: string): string {
  return `${count} ${count === 1 ? singular : plural}`;
}

function formatSelectionBreakdown(bookCount: number, seriesCount: number): string {
  const parts: string[] = [];
  if (bookCount > 0) parts.push(pluralize(bookCount, 'book', 'books'));
  if (seriesCount > 0) parts.push(pluralize(seriesCount, 'series', 'series'));
  return parts.join(', ');
}

function formatReportHeadline(report: BulkActionReport): string {
  const actionLabel: Record<BulkActionType, { past: string; fail: string }> = {
    monitor: { past: 'Monitoring enabled', fail: 'Enable monitoring' },
    unmonitor: { past: 'Monitoring disabled', fail: 'Disable monitoring' },
    discover: { past: 'New release check queued', fail: 'Queue new release check' },
    refresh: { past: 'Metadata refreshed', fail: 'Refresh metadata' },
    delete: { past: 'Deleted from library', fail: 'Delete from library' },
  };
  const label = actionLabel[report.action];
  if (report.failed === 0) {
    const deletedFiles = report.results.reduce((sum, item) => sum + (item.deletedFiles ?? 0), 0);
    const base = `${label.past} for ${pluralize(report.succeeded, 'item', 'items')}.`;
    if (report.action === 'delete' && deletedFiles > 0) {
      return `${base} Removed ${pluralize(deletedFiles, 'file', 'files')} from disk.`;
    }
    return base;
  }
  if (report.succeeded > 0) {
    return `${label.past} for ${report.succeeded} of ${report.total} items. ${pluralize(report.failed, 'item', 'items')} failed.`;
  }
  return `${label.fail} failed for all ${pluralize(report.total, 'item', 'items')}.`;
}

export const LibraryBulkBar: React.FC<LibraryBulkBarProps> = ({
  selectedItems,
  visibleSelectableCount,
  allVisibleSelected,
  hiddenSelectedCount,
  isAdmin,
  canRefresh,
  busy = false,
  report,
  error,
  onToggleSelectAllVisible,
  onRemoveItem,
  onClearSelection,
  onExitSelectionMode,
  onAction,
  onDismissReport,
}) => {
  const [showAllAffected, setShowAllAffected] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);

  const selectedBooks = selectedItems.filter((item) => item.kind === 'book');
  const selectedSeries = selectedItems.filter((item) => item.kind === 'series');
  const totalSelected = selectedItems.length;

  useEffect(() => {
    if (selectedBooks.length === 0) {
      setConfirmDelete(false);
      setDeleteFiles(false);
    }
  }, [selectedBooks.length]);

  const maxVisibleChips = 5;
  const visibleChips =
    showAllAffected || selectedItems.length <= maxVisibleChips
      ? selectedItems
      : selectedItems.slice(0, maxVisibleChips - 1);
  const overflowCount = selectedItems.length - visibleChips.length;

  const failedResults = report?.results.filter((item) => !item.ok) ?? [];

  const actionBtnClass =
    'inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-800 px-2.5 py-1.5 text-xs font-medium text-zinc-100 transition-colors enabled:hover:bg-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 disabled:opacity-45 disabled:cursor-not-allowed';

  return (
    <div className="space-y-2.5" data-testid="bulk-selection-container">
      <div
        role="region"
        aria-label="Bulk library actions"
        className="sticky top-2 z-20 rounded-lg border border-zinc-700/90 bg-zinc-900 px-3.5 py-3 shadow-lg shadow-black/40 space-y-2.5"
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          {/* Left: Selection count & selection controls */}
          <div className="flex flex-wrap items-center gap-2.5 text-xs">
            {totalSelected > 0 ? (
              <span className="font-semibold text-zinc-100 tabular-nums" data-testid="bulk-selected-count">
                {totalSelected} selected
                <span className="font-normal text-zinc-400">
                  {' '}({formatSelectionBreakdown(selectedBooks.length, selectedSeries.length)})
                </span>
              </span>
            ) : (
              <span className="text-zinc-300" data-testid="bulk-selected-count">
                Select books or series to run bulk actions
              </span>
            )}

            {hiddenSelectedCount > 0 && (
              <span className="inline-flex items-center rounded-sm border border-amber-700/60 bg-amber-950/60 px-2 py-0.5 text-[11px] font-medium text-amber-200 tabular-nums">
                {hiddenSelectedCount} outside current view
              </span>
            )}

            <div className="flex items-center gap-1.5">
              {visibleSelectableCount > 0 && (
                <button
                  type="button"
                  disabled={busy}
                  onClick={onToggleSelectAllVisible}
                  className="rounded-md border border-zinc-700/80 bg-zinc-950 px-2 py-1 text-[11px] font-medium text-zinc-200 transition-colors enabled:hover:bg-zinc-800 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 disabled:opacity-45 tabular-nums"
                >
                  {allVisibleSelected
                    ? 'Deselect visible'
                    : `Select all visible (${visibleSelectableCount})`}
                </button>
              )}
              {totalSelected > 0 && (
                <button
                  type="button"
                  disabled={busy}
                  onClick={onClearSelection}
                  className="rounded-md px-2 py-1 text-[11px] font-medium text-zinc-300 transition-colors enabled:hover:bg-zinc-800 enabled:hover:text-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 disabled:opacity-45"
                >
                  Clear
                </button>
              )}
              <button
                type="button"
                disabled={busy}
                onClick={onExitSelectionMode}
                className="rounded-md px-2 py-1 text-[11px] font-medium text-zinc-400 transition-colors enabled:hover:bg-zinc-800 enabled:hover:text-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 disabled:opacity-45"
              >
                Done
              </button>
            </div>
          </div>

          {/* Right: Bulk action buttons */}
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              disabled={busy || totalSelected === 0}
              onClick={() => {
                setConfirmDelete(false);
                onAction('monitor');
              }}
              className={actionBtnClass}
            >
              <Bell className="w-3.5 h-3.5 text-blue-300" aria-hidden="true" />
              Monitor
            </button>

            <button
              type="button"
              disabled={busy || totalSelected === 0}
              onClick={() => {
                setConfirmDelete(false);
                onAction('unmonitor');
              }}
              className={actionBtnClass}
            >
              <BellOff className="w-3.5 h-3.5 text-zinc-300" aria-hidden="true" />
              Unmonitor
            </button>

            {selectedSeries.length > 0 && (
              <button
                type="button"
                disabled={busy}
                onClick={() => {
                  setConfirmDelete(false);
                  onAction('discover');
                }}
                className={actionBtnClass}
              >
                <Calendar className="w-3.5 h-3.5 text-purple-300" aria-hidden="true" />
                {selectedBooks.length > 0
                  ? `Check New Releases (${selectedSeries.length} series)`
                  : 'Check New Releases'}
              </button>
            )}

            {canRefresh && (
              <button
                type="button"
                disabled={busy || totalSelected === 0}
                onClick={() => {
                  setConfirmDelete(false);
                  onAction('refresh');
                }}
                className={actionBtnClass}
              >
                <RefreshCw
                  className={`w-3.5 h-3.5 text-zinc-200 ${busy ? 'motion-safe:animate-spin' : ''}`}
                  aria-hidden="true"
                />
                Refresh
              </button>
            )}

            {isAdmin && selectedBooks.length > 0 && (
              <button
                type="button"
                disabled={busy}
                onClick={() => setConfirmDelete((prev) => !prev)}
                className="inline-flex items-center gap-1.5 rounded-md border border-red-800/80 bg-red-950/60 px-2.5 py-1.5 text-xs font-medium text-red-200 transition-colors enabled:hover:bg-red-900/60 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-red-400 disabled:opacity-45"
              >
                <Trash2 className="w-3.5 h-3.5" aria-hidden="true" />
                {selectedSeries.length > 0
                  ? `Delete (${pluralize(selectedBooks.length, 'book', 'books')})`
                  : 'Delete'}
              </button>
            )}
          </div>
        </div>

        {/* Affected items preview */}
        {totalSelected > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-zinc-800 pt-2.5 text-[11px]">
            <span className="text-zinc-400 mr-0.5">Affected:</span>
            {visibleChips.map((item) => (
              <span
                key={`${item.kind}:${item.id}`}
                className={`inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 max-w-64 ${
                  item.kind === 'series'
                    ? 'border-purple-800/60 bg-purple-950/60 text-purple-200'
                    : 'border-zinc-700/80 bg-zinc-950 text-zinc-200'
                }`}
              >
                {item.kind === 'series' ? (
                  <Layers className="w-3 h-3 shrink-0 text-purple-300" aria-hidden="true" />
                ) : (
                  <BookOpen className="w-3 h-3 shrink-0 text-blue-300" aria-hidden="true" />
                )}
                <span className="truncate">{item.title}</span>
                {item.kind === 'series' && typeof item.bookCount === 'number' && (
                  <span className="text-purple-300/80 shrink-0 tabular-nums">
                    ({pluralize(item.bookCount, 'book', 'books')})
                  </span>
                )}
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => onRemoveItem(item.kind, item.id)}
                  aria-label={`Remove ${item.title} from selection`}
                  className="ml-0.5 rounded-sm p-0.5 text-zinc-400 hover:text-white focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-45"
                >
                  <X className="w-3 h-3" aria-hidden="true" />
                </button>
              </span>
            ))}
            {overflowCount > 0 && (
              <button
                type="button"
                onClick={() => setShowAllAffected(true)}
                className="rounded-sm border border-zinc-700 bg-zinc-950 px-2 py-0.5 text-zinc-300 hover:text-white focus-visible:outline-2 focus-visible:outline-blue-400 tabular-nums"
              >
                +{overflowCount} more
              </button>
            )}
            {showAllAffected && selectedItems.length > maxVisibleChips && (
              <button
                type="button"
                onClick={() => setShowAllAffected(false)}
                className="rounded-sm px-2 py-0.5 text-zinc-400 hover:text-zinc-200 focus-visible:outline-2 focus-visible:outline-blue-400"
              >
                Show fewer
              </button>
            )}
          </div>
        )}

        {/* Inline Admin Delete Confirmation */}
        {isAdmin && confirmDelete && selectedBooks.length > 0 && (
          <div
            role="region"
            aria-label="Confirm bulk delete"
            className="rounded-md border border-red-900/70 bg-red-950/35 px-3 py-2.5 space-y-2 text-xs"
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="space-y-0.5">
                <p className="font-medium text-red-100">
                  Delete {pluralize(selectedBooks.length, 'selected book', 'selected books')} from Callimachus?
                </p>
                {selectedSeries.length > 0 && (
                  <p className="text-red-200/80">
                    {pluralize(selectedSeries.length, 'selected series', 'selected series')} will not be deleted.
                  </p>
                )}
              </div>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setConfirmDelete(false);
                    setDeleteFiles(false);
                  }}
                  className="rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs font-medium text-zinc-200 hover:bg-zinc-800 focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-45"
                >
                  Cancel
                </button>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    const includeFiles = deleteFiles;
                    setConfirmDelete(false);
                    setDeleteFiles(false);
                    onAction('delete', { deleteFiles: includeFiles });
                  }}
                  className="rounded-md border border-red-700 bg-red-700 px-2.5 py-1.5 text-xs font-semibold text-white hover:bg-red-600 focus-visible:outline-2 focus-visible:outline-red-300 disabled:opacity-45"
                >
                  Confirm Delete
                </button>
              </div>
            </div>
            <label className="inline-flex items-center gap-2 text-red-100 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={deleteFiles}
                disabled={busy}
                onChange={(e) => setDeleteFiles(e.target.checked)}
                className="size-3.5 rounded-sm accent-red-500"
              />
              Also delete book files from disk
            </label>
          </div>
        )}
      </div>

      {/* Request-level error banner */}
      {error && (
        <div
          role="alert"
          className="flex items-start justify-between gap-3 rounded-lg border border-red-900/70 bg-red-950/35 px-3.5 py-2.5 text-xs text-red-200"
        >
          <div className="flex items-start gap-2">
            <AlertTriangle className="w-4 h-4 shrink-0 text-red-300 mt-0.5" aria-hidden="true" />
            <span>{error}</span>
          </div>
          <button
            type="button"
            onClick={onDismissReport}
            aria-label="Dismiss bulk action error"
            className="rounded-sm p-0.5 text-red-300 hover:text-white focus-visible:outline-2 focus-visible:outline-red-300"
          >
            <X className="w-3.5 h-3.5" aria-hidden="true" />
          </button>
        </div>
      )}

      {/* Structured result & partial failure report */}
      {report && (
        <div
          role={report.failed > 0 ? 'alert' : 'status'}
          data-testid="bulk-action-report"
          className={`rounded-lg border px-3.5 py-2.5 text-xs space-y-2 ${
            report.failed === 0
              ? 'border-emerald-800/70 bg-emerald-950/35 text-emerald-100'
              : report.succeeded > 0
                ? 'border-amber-800/70 bg-amber-950/35 text-amber-100'
                : 'border-red-900/70 bg-red-950/35 text-red-100'
          }`}
        >
          <div className="flex items-start justify-between gap-3">
            <div className="flex items-start gap-2">
              {report.failed === 0 ? (
                <CheckCircle2 className="w-4 h-4 shrink-0 text-emerald-300 mt-0.5" aria-hidden="true" />
              ) : (
                <AlertTriangle
                  className={`w-4 h-4 shrink-0 mt-0.5 ${
                    report.succeeded > 0 ? 'text-amber-300' : 'text-red-300'
                  }`}
                  aria-hidden="true"
                />
              )}
              <div className="space-y-0.5">
                <p className="font-medium tabular-nums">{formatReportHeadline(report)}</p>
                {report.failed > 0 && (
                  <p className={report.succeeded > 0 ? 'text-amber-200/85' : 'text-red-200/85'}>
                    Failed items remain selected so you can review or retry them.
                  </p>
                )}
              </div>
            </div>
            <button
              type="button"
              onClick={onDismissReport}
              aria-label="Dismiss bulk action summary"
              className="rounded-sm p-0.5 opacity-80 hover:opacity-100 focus-visible:outline-2 focus-visible:outline-blue-300"
            >
              <X className="w-3.5 h-3.5" aria-hidden="true" />
            </button>
          </div>

          {failedResults.length > 0 && (
            <ul className="space-y-1 border-t border-white/10 pt-2">
              {failedResults.map((item) => (
                <li
                  key={`${item.kind}:${item.id}`}
                  className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5"
                >
                  <span className="rounded-sm border border-white/15 bg-black/25 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide">
                    {item.kind === 'series' ? 'Series' : 'Book'}
                  </span>
                  <span className="font-medium">{item.title || item.id}</span>
                  <span className="opacity-85">— {item.error || 'Action failed.'}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
};
