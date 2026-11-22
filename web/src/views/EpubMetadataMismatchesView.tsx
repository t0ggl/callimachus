import React, { useCallback, useEffect, useState } from 'react';
import {
  AlertCircle,
  BookOpen,
  CheckCircle2,
  Eye,
  FileWarning,
  Filter,
  Loader2,
  RefreshCw,
  Search,
} from 'lucide-react';
import {
  Book,
  EPUBMetadataMismatchItem,
  EPUBMetadataMismatchesResponse,
  EPUBMismatchField,
  MetadataCandidate,
} from '../types';
import { BookDetailModal } from '../components/BookDetailModal';

interface EpubMetadataMismatchesViewProps {
  onCatalogChanged?: () => void;
  onCountChanged?: (count: number) => void;
  canManage?: boolean;
  canRequest?: boolean;
}

type FieldFilter = 'ALL' | EPUBMismatchField;

const FIELD_LABELS: Record<EPUBMismatchField, string> = {
  title: 'Title',
  series: 'Series',
  seriesIndex: 'Series index',
};

const SOURCE_LABELS: Record<string, string> = {
  match: 'Matched metadata',
  import: 'Imported metadata',
  manual_repair: 'Repaired metadata',
  scan: 'Scanned metadata',
  catalog: 'Catalog metadata',
};

const FIELD_FILTERS: { id: FieldFilter; label: string }[] = [
  { id: 'ALL', label: 'All fields' },
  { id: 'title', label: 'Title' },
  { id: 'series', label: 'Series' },
  { id: 'seriesIndex', label: 'Series index' },
];

const focusRing = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300';
const secondaryButton = `inline-flex items-center justify-center gap-1.5 min-h-11 sm:min-h-9 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-1.5 text-xs font-medium text-zinc-100 transition-colors hover:bg-zinc-700 disabled:opacity-50 disabled:cursor-not-allowed ${focusRing}`;
const primaryButton = `inline-flex items-center justify-center gap-1.5 min-h-11 sm:min-h-9 rounded-md bg-blue-400 px-3 py-1.5 text-xs font-semibold text-zinc-950 transition-colors hover:bg-blue-300 disabled:opacity-50 disabled:cursor-not-allowed ${focusRing}`;

function MismatchThumbnail({ coverUrl }: { coverUrl?: string }) {
  const [brokenUrl, setBrokenUrl] = useState<string | null>(null);
  const showCover = Boolean(coverUrl) && brokenUrl !== coverUrl;

  return (
    <div className="w-8 sm:w-9 shrink-0" aria-hidden="true">
      <div className="aspect-[2/3] w-full rounded-sm overflow-hidden bg-zinc-950 border border-zinc-800 flex items-center justify-center">
        {showCover ? (
          <img
            src={coverUrl}
            alt=""
            referrerPolicy="no-referrer"
            onError={() => setBrokenUrl(coverUrl ?? null)}
            className="w-full h-full object-cover"
          />
        ) : (
          <BookOpen className="w-4 h-4 text-blue-400" />
        )}
      </div>
    </div>
  );
}

function buildIdealCandidate(item: EPUBMetadataMismatchItem, book: Book): MetadataCandidate | undefined {
  const idealTitle = item.ideal.title?.trim() ?? '';
  if (!idealTitle) return undefined;
  const idealSeries = item.ideal.series?.trim() ?? '';
  const idealIndex = idealSeries ? item.ideal.seriesIndex : undefined;

  const bookTitle = book.title?.trim() ?? '';
  const bookSeries = book.seriesTitle?.trim() ?? '';
  const bookIndex = bookSeries ? book.seriesIndex : undefined;

  const titleDiffers = idealTitle !== bookTitle;
  const seriesDiffers = idealSeries !== '' && idealSeries !== bookSeries;
  const indexDiffers =
    idealSeries !== '' &&
    idealIndex !== undefined &&
    (bookIndex === undefined || Math.abs(idealIndex - bookIndex) > 1e-6);

  if (!titleDiffers && !seriesDiffers && !indexDiffers) {
    return undefined;
  }
  return {
    title: idealTitle,
    subtitle: item.ideal.subtitle || undefined,
    series: idealSeries || undefined,
    series_index: idealIndex,
  };
}

export const EpubMetadataMismatchesView: React.FC<EpubMetadataMismatchesViewProps> = ({
  onCatalogChanged,
  onCountChanged,
  canManage = true,
  canRequest = true,
}) => {
  const [items, setItems] = useState<EPUBMetadataMismatchItem[]>([]);
  const [scannedBooks, setScannedBooks] = useState<number>(0);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string>('');
  const [statusMessage, setStatusMessage] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [fieldFilter, setFieldFilter] = useState<FieldFilter>('ALL');
  const [applyingIds, setApplyingIds] = useState<string[]>([]);
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});
  const [modalState, setModalState] = useState<{
    book: Book;
    initialPanel?: 'repair';
    initialRepairCandidate?: MetadataCandidate;
  } | null>(null);
  const [openingBookId, setOpeningBookId] = useState<string | null>(null);

  const loadMismatches = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError('');
    try {
      const response = await fetch('/api/activity/metadata-mismatches', { signal });
      if (response.status === 403) {
        throw new Error('Administrator privileges are required to view EPUB metadata mismatches.');
      }
      if (!response.ok) {
        throw new Error('Unable to inspect EPUB metadata right now. Try again.');
      }
      const data: EPUBMetadataMismatchesResponse = await response.json();
      if (signal?.aborted) return;
      const nextItems = Array.isArray(data?.items) ? data.items : [];
      setItems(nextItems);
      setScannedBooks(typeof data?.scannedBooks === 'number' ? data.scannedBooks : 0);
      onCountChanged?.(nextItems.length);
    } catch (err) {
      if (signal?.aborted) return;
      setError(err instanceof Error ? err.message : 'Unable to inspect EPUB metadata right now.');
    } finally {
      if (!signal?.aborted) {
        setLoading(false);
      }
    }
  }, [onCountChanged]);

  useEffect(() => {
    const controller = new AbortController();
    void loadMismatches(controller.signal);
    return () => controller.abort();
  }, [loadMismatches]);

  const handleApplyIdeal = async (item: EPUBMetadataMismatchItem) => {
    setApplyingIds((prev) => [...prev, item.bookId]);
    setRowErrors((prev) => ({ ...prev, [item.bookId]: '' }));
    setStatusMessage('');
    try {
      const response = await fetch(`/api/activity/metadata-mismatches/${encodeURIComponent(item.bookId)}/apply`, {
        method: 'POST',
      });
      if (!response.ok) {
        const message = response.headers.get('Content-Type')?.startsWith('text/plain')
          ? (await response.text()).trim()
          : '';
        throw new Error(message || 'Unable to apply ideal metadata to the EPUB.');
      }
      setItems((prev) => {
        const remaining = prev.filter((entry) => entry.bookId !== item.bookId);
        onCountChanged?.(remaining.length);
        return remaining;
      });
      setStatusMessage(`Applied ideal metadata to ${item.title}.`);
      onCatalogChanged?.();
    } catch (err) {
      setRowErrors((prev) => ({
        ...prev,
        [item.bookId]: err instanceof Error ? err.message : 'Unable to apply ideal metadata.',
      }));
    } finally {
      setApplyingIds((prev) => prev.filter((id) => id !== item.bookId));
    }
  };

  const handleOpenBook = async (item: EPUBMetadataMismatchItem, initialPanel?: 'repair') => {
    setOpeningBookId(item.bookId);
    setRowErrors((prev) => ({ ...prev, [item.bookId]: '' }));
    try {
      const response = await fetch(`/api/books?id=${encodeURIComponent(item.bookId)}`);
      if (!response.ok) {
        throw new Error('Unable to load book details.');
      }
      const list: Book[] = await response.json();
      const found = Array.isArray(list) ? list.find((entry) => entry.id === item.bookId) : undefined;
      if (!found) {
        throw new Error('This book is no longer in the catalog. Refresh the list.');
      }
      setModalState({
        book: found,
        initialPanel,
        initialRepairCandidate: initialPanel === 'repair' ? buildIdealCandidate(item, found) : undefined,
      });
    } catch (err) {
      setRowErrors((prev) => ({
        ...prev,
        [item.bookId]: err instanceof Error ? err.message : 'Unable to open book details.',
      }));
    } finally {
      setOpeningBookId(null);
    }
  };

  const hasActiveFilters = searchQuery.trim() !== '' || fieldFilter !== 'ALL';
  const filteredItems = items.filter((item) => {
    if (fieldFilter !== 'ALL' && !item.mismatchedFields.includes(fieldFilter)) {
      return false;
    }
    const query = searchQuery.trim().toLowerCase();
    if (!query) return true;
    const haystack = [
      item.title,
      item.authorName,
      item.ideal.title,
      item.ideal.series,
      ...item.files.flatMap((file) => [
        file.filename,
        file.current.title,
        file.current.series,
      ]),
    ]
      .filter(Boolean)
      .join(' ')
      .toLowerCase();
    return haystack.includes(query);
  });

  const isInitialLoading = loading && items.length === 0 && !error;

  return (
    <section aria-label="EPUB Metadata Mismatches" className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-zinc-800 pb-4">
        <div>
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="text-xl font-bold tracking-tight text-white">EPUB Metadata Mismatches</h1>
            {!isInitialLoading && (
              <span className="text-xs font-mono px-2 py-0.5 rounded-sm bg-zinc-900 border border-zinc-800 text-zinc-300 tabular-nums">
                {items.length} {items.length === 1 ? 'book' : 'books'}
              </span>
            )}
          </div>
          <p className="text-xs text-zinc-400 mt-0.5">
            Books whose current EPUB title, series name, or series index differs significantly from matched ideal metadata
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2.5">
          {!isInitialLoading && scannedBooks > 0 && (
            <span className="text-xs font-mono text-zinc-400 tabular-nums">
              {scannedBooks} EPUB {scannedBooks === 1 ? 'book' : 'books'} scanned
            </span>
          )}
          <button
            type="button"
            disabled={loading}
            onClick={() => void loadMismatches()}
            className={secondaryButton}
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'motion-safe:animate-spin' : ''}`} aria-hidden="true" />
            Refresh
          </button>
        </div>
      </div>

      {statusMessage && (
        <div
          role="status"
          className="rounded-lg border border-emerald-900/60 bg-emerald-950/30 px-4 py-2.5 flex items-center gap-2 text-xs text-emerald-200"
        >
          <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" aria-hidden="true" />
          <span>{statusMessage}</span>
        </div>
      )}

      {error && (
        <div
          role="alert"
          className="rounded-xl border border-red-900/60 bg-red-950/30 p-4 flex flex-wrap items-center justify-between gap-3 text-sm text-red-200"
        >
          <div className="flex items-center gap-2.5 min-w-0">
            <AlertCircle className="w-4 h-4 text-red-400 shrink-0" aria-hidden="true" />
            <span className="min-w-0 wrap-anywhere">{error}</span>
          </div>
          <button type="button" onClick={() => void loadMismatches()} className={secondaryButton}>
            <RefreshCw className="w-3.5 h-3.5" aria-hidden="true" />
            Retry
          </button>
        </div>
      )}

      {isInitialLoading ? (
        <div role="status" className="space-y-3">
          <div className="flex items-center gap-2 text-xs text-zinc-300">
            <Loader2 className="w-4 h-4 motion-safe:animate-spin text-blue-400 shrink-0" aria-hidden="true" />
            <span>Scanning EPUB metadata…</span>
          </div>
          <div className="space-y-2" aria-hidden="true">
            <div className="h-24 rounded-lg border border-zinc-800 bg-zinc-900/90 motion-safe:animate-pulse" />
            <div className="h-24 rounded-lg border border-zinc-800 bg-zinc-900/90 motion-safe:animate-pulse" />
          </div>
        </div>
      ) : items.length === 0 && !error ? (
        <div className="py-16 px-4 text-center border border-dashed border-zinc-800 rounded-xl bg-zinc-900/40">
          <FileWarning className="w-8 h-8 text-zinc-500 mx-auto mb-3" aria-hidden="true" />
          <p className="text-sm font-medium text-zinc-200">No significant EPUB metadata mismatches</p>
          <p className="text-xs text-zinc-400 mt-1 max-w-md mx-auto">
            {scannedBooks > 0
              ? `Checked ${scannedBooks} EPUB ${scannedBooks === 1 ? 'book' : 'books'}. Title, series name, and series index match their ideal metadata.`
              : 'Books with EPUB files and matched metadata will be compared automatically here.'}
          </p>
        </div>
      ) : items.length > 0 ? (
        <div className="space-y-3">
          <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-3">
            <div className="relative w-full sm:w-80">
              <Search className="w-4 h-4 text-zinc-500 absolute left-3 top-2.5" aria-hidden="true" />
              <input
                type="text"
                aria-label="Filter metadata mismatches"
                placeholder="Filter by title, author, or series…"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="w-full pl-9 pr-3 py-1.5 bg-zinc-900 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-hidden focus:border-zinc-700"
              />
            </div>

            <div className="flex flex-wrap items-center gap-2 text-xs">
              <span className="text-zinc-400 flex items-center gap-1">
                <Filter className="w-3.5 h-3.5" aria-hidden="true" /> Field:
              </span>
              <div role="group" aria-label="Filter by mismatched field" className="flex flex-wrap bg-zinc-900 border border-zinc-800 rounded-md p-0.5 text-xs">
                {FIELD_FILTERS.map((f) => (
                  <button
                    key={f.id}
                    type="button"
                    aria-pressed={fieldFilter === f.id}
                    onClick={() => setFieldFilter(f.id)}
                    className={`px-2.5 py-1 rounded-sm transition-colors ${focusRing} ${
                      fieldFilter === f.id
                        ? 'bg-zinc-800 text-white font-semibold'
                        : 'text-zinc-400 hover:text-zinc-200'
                    }`}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          {filteredItems.length === 0 ? (
            <div className="py-12 px-4 text-center border border-dashed border-zinc-800 rounded-xl bg-zinc-900/40 space-y-2">
              <p className="text-sm font-medium text-zinc-200">No matching books</p>
              <p className="text-xs text-zinc-400">Try adjusting your search or field filter.</p>
              {hasActiveFilters && (
                <button
                  type="button"
                  onClick={() => {
                    setSearchQuery('');
                    setFieldFilter('ALL');
                  }}
                  className={secondaryButton}
                >
                  Clear Filters
                </button>
              )}
            </div>
          ) : (
            <div className="rounded-xl border border-zinc-800 bg-zinc-900/90 overflow-hidden">
              <ul aria-label="EPUB metadata mismatches" className="divide-y divide-zinc-800/80">
                {filteredItems.map((item) => {
                  const busy = applyingIds.includes(item.bookId);
                  const opening = openingBookId === item.bookId;
                  const rowError = rowErrors[item.bookId];
                  const sourceLabel = item.idealSource ? SOURCE_LABELS[item.idealSource] ?? item.idealSource : undefined;

                  return (
                    <li
                      key={item.bookId}
                      data-testid={`mismatch-${item.bookId}`}
                      className="p-4 space-y-3 hover:bg-zinc-800/30 transition-colors"
                    >
                      <div className="flex flex-col sm:flex-row sm:items-start justify-between gap-3">
                        <div className="flex items-start gap-3 min-w-0">
                          <MismatchThumbnail coverUrl={item.coverUrl} />
                          <div className="min-w-0 space-y-1">
                            <div className="flex flex-wrap items-center gap-2">
                              <button
                                type="button"
                                disabled={opening}
                                onClick={() => void handleOpenBook(item)}
                                className={`text-left text-sm font-semibold text-zinc-100 underline decoration-zinc-700 underline-offset-4 hover:text-blue-300 hover:decoration-blue-300 transition-colors wrap-anywhere ${focusRing}`}
                              >
                                {item.title}
                              </button>
                              {item.mismatchedFields.map((field) => (
                                <span
                                  key={field}
                                  className="inline-flex items-center rounded-sm border border-amber-800/60 bg-amber-950/60 px-1.5 py-0.5 text-[11px] font-medium text-amber-300"
                                >
                                  {FIELD_LABELS[field]}
                                </span>
                              ))}
                            </div>
                            <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-zinc-400">
                              <span>by {item.authorName}</span>
                              {sourceLabel && (
                                <>
                                  <span aria-hidden="true">·</span>
                                  <span>{sourceLabel}</span>
                                </>
                              )}
                            </div>
                          </div>
                        </div>

                        <div className="flex flex-wrap items-center gap-2 shrink-0">
                          <button
                            type="button"
                            disabled={busy || opening}
                            onClick={() => void handleApplyIdeal(item)}
                            aria-label={`Apply ideal metadata now to ${item.title}`}
                            className={primaryButton}
                          >
                            {busy ? (
                              <>
                                <Loader2 className="w-3.5 h-3.5 motion-safe:animate-spin" aria-hidden="true" />
                                Applying…
                              </>
                            ) : (
                              'Apply Now'
                            )}
                          </button>
                          <button
                            type="button"
                            disabled={busy || opening}
                            onClick={() => void handleOpenBook(item, 'repair')}
                            aria-label={`Preview and edit metadata for ${item.title}`}
                            className={secondaryButton}
                          >
                            <Eye className="w-3.5 h-3.5" aria-hidden="true" />
                            Preview & Edit
                          </button>
                        </div>
                      </div>

                      {rowError && (
                        <div
                          role="alert"
                          className="rounded-md border border-red-900/60 bg-red-950/30 px-3 py-2 flex items-start gap-2 text-xs text-red-200"
                        >
                          <AlertCircle className="w-3.5 h-3.5 text-red-400 shrink-0 mt-0.5" aria-hidden="true" />
                          <span className="min-w-0 wrap-anywhere">{rowError}</span>
                        </div>
                      )}

                      <div className="space-y-2">
                        {item.files.map((file) => (
                          <div
                            key={file.assetId}
                            className="rounded-lg border border-zinc-800/80 bg-zinc-950/60 overflow-hidden"
                          >
                            <div className="px-3 py-1.5 border-b border-zinc-800/80 text-[11px] font-mono text-zinc-400 truncate" title={file.filename}>
                              {file.filename}
                            </div>
                            <table className="w-full table-fixed text-left text-xs [overflow-wrap:anywhere]">
                              <thead className="text-[11px] text-zinc-400 bg-zinc-950/40">
                                <tr>
                                  <th scope="col" className="w-28 sm:w-36 px-3 py-1.5 font-medium">Field</th>
                                  <th scope="col" className="px-3 py-1.5 font-medium">Current EPUB</th>
                                  <th scope="col" className="px-3 py-1.5 font-medium">Ideal Metadata</th>
                                </tr>
                              </thead>
                              <tbody className="divide-y divide-zinc-800/60">
                                {file.mismatches.map((diff) => (
                                  <tr key={diff.field} className="align-top">
                                    <th scope="row" className="px-3 py-2 font-medium text-zinc-300">
                                      {FIELD_LABELS[diff.field]}
                                    </th>
                                    <td className="px-3 py-2 text-zinc-300 tabular-nums">
                                      {diff.current ? (
                                        diff.current
                                      ) : (
                                        <span className="italic text-zinc-500">Missing</span>
                                      )}
                                    </td>
                                    <td className="px-3 py-2 font-medium text-zinc-100 tabular-nums">
                                      {diff.ideal}
                                    </td>
                                  </tr>
                                ))}
                              </tbody>
                            </table>
                          </div>
                        ))}
                      </div>
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
        </div>
      ) : null}

      {modalState && (
        <BookDetailModal
          book={modalState.book}
          initialPanel={modalState.initialPanel}
          initialRepairCandidate={modalState.initialRepairCandidate}
          isAdmin={true}
          canManage={canManage}
          canRequest={canRequest}
          onBookUpdated={(updated) => setModalState((prev) => (prev ? { ...prev, book: updated } : null))}
          onBookDeleted={() => {
            setModalState(null);
            onCatalogChanged?.();
            void loadMismatches();
          }}
          onMetadataApplied={() => {
            onCatalogChanged?.();
            void loadMismatches();
          }}
          onClose={() => setModalState(null)}
        />
      )}
    </section>
  );
};
