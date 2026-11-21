import { useEffect, useRef, useState } from 'react';
import type { Book, BookState, SeriesSelection } from '../types';
import {
  X,
  Layers,
  BookOpen,
  User,
  Calendar,
  Check,
  Info,
  SlidersHorizontal,
  Download,
  Bell,
} from 'lucide-react';
import { EntityLink, entityURL } from './EntityLink';
import { BookDetailModal, resolveCatalogBook, type ResolveBookInput } from './BookDetailModal';
export { resolveCatalogBook, type ResolveBookInput };
import { ManualSearch } from './ManualSearch';

export interface SeriesBookEntry {
  book_id?: string;
  title: string;
  subtitle?: string;
  authors?: string[];
  description?: string;
  cover_url?: string;
  publish_date?: string;
  series_index?: number | null;
  series_index_display?: string;
  hardcover_series_id?: number;
  formats?: string[];
  library_book_id?: string;
  state?: BookState;
  monitored?: boolean;
}

export interface SeriesBooksData {
  id?: string;
  title?: string;
  description?: string;
  status?: string;
  monitored?: boolean;
  authors?: string[];
  total_books?: number;
  owned_count?: number;
  books: SeriesBookEntry[];
  complete: boolean;
}

interface SeriesDetailModalProps extends SeriesSelection {
  onClose: () => void;
  onOpenBook?: (bookId: string) => void;
  onBookDeleted?: () => void;
  canManage?: boolean;
  canRequest?: boolean;
  isAdmin?: boolean;
}

export function SeriesBookCover({ coverUrl, title }: { coverUrl?: string; title: string }) {
  const [brokenUrl, setBrokenUrl] = useState<string | null>(null);
  const broken = Boolean(coverUrl) && brokenUrl === coverUrl;

  return (
    <div className="w-14 sm:w-16 shrink-0">
      <div className="aspect-[2/3] rounded-md overflow-hidden bg-zinc-900 border border-zinc-800 shadow-xs">
        {coverUrl && !broken ? (
          <img
            src={coverUrl}
            alt={`Cover of ${title}`}
            onError={() => setBrokenUrl(coverUrl)}
            className="w-full h-full object-cover"
          />
        ) : (
          <div
            className="w-full h-full flex items-center justify-center text-zinc-500"
            aria-label="No cover available"
          >
            <BookOpen className="w-5 h-5 stroke-1" aria-hidden="true" />
          </div>
        )}
      </div>
    </div>
  );
}

export function SeriesDetailModal({
  title,
  id,
  author,
  hardcoverID,
  onClose,
  onOpenBook,
  onBookDeleted,
  canManage = false,
  canRequest,
  isAdmin,
}: SeriesDetailModalProps) {
  const allowRequest = Boolean(canRequest ?? canManage);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const [data, setData] = useState<SeriesBooksData | null>(null);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [busyIndex, setBusyIndex] = useState<number | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [detailIndex, setDetailIndex] = useState<number | null>(null);
  const [detailBook, setDetailBook] = useState<Book | null>(null);
  const [downloadBook, setDownloadBook] = useState<{ id: string; title: string; author: string } | null>(null);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    closeButtonRef.current?.focus();
    return () => {
      previous?.focus?.();
    };
  }, []);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (detailBook || downloadBook) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown, true);
    return () => window.removeEventListener('keydown', handleKeyDown, true);
  }, [onClose, detailBook, downloadBook]);

  useEffect(() => {
    const controller = new AbortController();
    setData(null);
    setError('');
    setActionError('');
    const params = new URLSearchParams({ title });
    if (id) params.set('id', id);
    if (author) params.set('author', author);
    if (hardcoverID) params.set('hardcover_id', String(hardcoverID));
    fetch(`/api/series/books?${params}`, { signal: controller.signal })
      .then(async (res) => {
        if (!res.ok) throw new Error('Unable to load reading order. Try again.');
        return res.json();
      })
      .then((payload: SeriesBooksData) => setData(payload))
      .catch((e) => {
        if (!controller.signal.aborted) setError((e as Error).message);
      });
    return () => controller.abort();
  }, [title, id, author, hardcoverID, attempt]);

  const displayTitle = data?.title || title;
  const books = data?.books ?? [];
  const ownedCount = data?.owned_count ?? books.filter((b) => b.state ? b.state === 'In library' : Boolean(b.library_book_id)).length;
  const totalBooks = data?.total_books ?? books.length;
  const seriesAuthors =
    data?.authors && data.authors.length > 0
      ? data.authors
      : Array.from(
          new Set(
            books.flatMap((b) => b.authors ?? []).map((a) => a.trim()).filter(Boolean)
          )
        );

  const updateBookEntry = (index: number, patch: Partial<SeriesBookEntry>) => {
    setData((prev) => {
      if (!prev) return prev;
      return {
        ...prev,
        books: prev.books.map((entry, idx) => (idx === index ? { ...entry, ...patch } : entry)),
      };
    });
  };

  const ensureBookRecord = async (book: SeriesBookEntry, index: number): Promise<Book> => {
    const existingId = book.library_book_id || book.book_id;
    const resolved = await resolveCatalogBook({
      bookId: existingId,
      title: book.title,
      subtitle: book.subtitle,
      author: book.authors?.join(', ') || seriesAuthors[0] || 'Unknown',
      series: displayTitle,
      seriesId: data?.id ?? id,
      hardcoverSeriesId: book.hardcover_series_id ?? hardcoverID,
      seriesIndex: book.series_index,
      releaseDate: book.publish_date,
      synopsis: book.description,
      coverUrl: book.cover_url,
    });
    updateBookEntry(index, {
      book_id: resolved.id,
      monitored: resolved.monitored,
    });
    return resolved;
  };

  const handleOpenBook = async (book: SeriesBookEntry, index: number) => {
    if (book.library_book_id && onOpenBook) {
      onOpenBook(book.library_book_id);
      return;
    }
    const bookAuthor = book.authors?.join(', ') || seriesAuthors[0] || 'Unknown';
    const existingId = book.library_book_id || book.book_id;
    if (!existingId) {
      setActionError('');
      setDetailIndex(index);
      setDetailBook({
        id: '',
        monitored: Boolean(book.monitored),
        state: book.state ?? 'Not in library',
        title: book.title,
        subtitle: book.subtitle,
        authorId: '',
        authorName: bookAuthor,
        seriesId: data?.id ?? id,
        seriesTitle: displayTitle,
        seriesIndex: book.series_index ?? undefined,
        seriesIndexDisplay: book.series_index_display,
        releaseDate: book.publish_date,
        synopsis: book.description,
        coverUrl: book.cover_url,
        formats: (book.formats ?? []) as Book['formats'],
        editions: [],
      });
      return;
    }
    setBusyIndex(index);
    setActionError('');
    try {
      const res = await fetch(`/api/books?id=${encodeURIComponent(existingId)}`);
      if (!res.ok) throw new Error('Unable to load book details. Try again.');
      const list: Book[] = await res.json();
      const found = list.find((item) => item.id === existingId);
      if (!found) throw new Error('Unable to load book details. Try again.');
      setDetailIndex(index);
      setDetailBook({
        ...found,
        coverUrl: found.coverUrl || book.cover_url,
        synopsis: found.synopsis || book.description,
        subtitle: found.subtitle || book.subtitle,
        releaseDate: found.releaseDate || book.publish_date,
        seriesTitle: found.seriesTitle || displayTitle,
        seriesIndex: found.seriesIndex ?? (book.series_index ?? undefined),
        seriesIndexDisplay: found.seriesIndexDisplay || book.series_index_display,
        state: found.state || book.state,
      });
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusyIndex(null);
    }
  };

  const handleOpenDownloadOptions = async (book: SeriesBookEntry, index: number) => {
    const existingId = book.library_book_id || book.book_id;
    const author = book.authors?.join(', ') || seriesAuthors[0] || 'Unknown';
    if (existingId) {
      setActionError('');
      setDownloadBook({ id: existingId, title: book.title, author });
      return;
    }
    setBusyIndex(index);
    setActionError('');
    try {
      const resolved = await ensureBookRecord(book, index);
      setDownloadBook({
        id: resolved.id,
        title: resolved.title,
        author: resolved.authorName || author,
      });
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusyIndex(null);
    }
  };

  const handleToggleBookMonitor = async (book: SeriesBookEntry, index: number) => {
    setBusyIndex(index);
    setActionError('');
    try {
      let bookId = book.library_book_id || book.book_id;
      if (!bookId) {
        const resolved = await ensureBookRecord(book, index);
        bookId = resolved.id;
      }
      const next = !book.monitored;
      const res = await fetch(`/api/books/${encodeURIComponent(bookId)}/monitor`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ monitored: next }),
      });
      if (!res.ok) {
        throw new Error('Unable to update monitoring state. Check permissions and retry.');
      }
      updateBookEntry(index, { book_id: bookId, monitored: next });
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusyIndex(null);
    }
  };

  return (
    <>
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="series-heading"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-xs animate-in fade-in duration-150"
      onClick={onClose}
    >
      <div
        className="w-full max-w-3xl max-h-[90vh] bg-zinc-900 border border-zinc-800 rounded-xl shadow-2xl overflow-hidden flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-start justify-between gap-4 px-6 py-4 border-b border-zinc-800 bg-zinc-950/40">
          <div className="min-w-0 space-y-1.5">
            <div className="flex flex-wrap items-center gap-2.5">
              <Layers className="w-5 h-5 text-purple-400 shrink-0" aria-hidden="true" />
              <h2 id="series-heading" className="text-lg font-semibold text-zinc-100 leading-snug">
                {displayTitle}
              </h2>
              {data?.status && (
                <span className="text-[11px] font-mono px-2 py-0.5 rounded-sm bg-zinc-800 text-zinc-300 border border-zinc-700 capitalize">
                  {data.status}
                </span>
              )}
            </div>

            {data && (
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span
                  className={`px-2 py-0.5 rounded-sm font-medium border ${
                    totalBooks > 0 && ownedCount >= totalBooks
                      ? 'bg-emerald-950/80 text-emerald-300 border-emerald-800/60'
                      : 'bg-zinc-800/80 text-zinc-300 border-zinc-700/60'
                  }`}
                >
                  {ownedCount} / {totalBooks} in library
                </span>
                {data.monitored !== undefined && (
                  <span
                    className={`px-2 py-0.5 rounded-sm font-medium border ${
                      data.monitored
                        ? 'bg-blue-950/80 text-blue-300 border-blue-800/60'
                        : 'bg-zinc-900 text-zinc-400 border-zinc-800'
                    }`}
                  >
                    {data.monitored ? 'Series Monitored' : 'Series Unmonitored'}
                  </span>
                )}
              </div>
            )}
          </div>

          <button
            ref={closeButtonRef}
            type="button"
            onClick={onClose}
            aria-label="Close"
            data-testid="close-series-detail"
            className="p-1.5 text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800 rounded-lg transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 shrink-0"
            title="Close modal (Esc)"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content body */}
        <div className="flex-1 overflow-y-auto p-6 space-y-6">
          {!data && !error && (
            <div role="status" className="space-y-4">
              <p className="text-xs text-zinc-400">Loading reading order…</p>
              <div className="space-y-3" aria-hidden="true">
                <div className="h-16 rounded-lg bg-zinc-950/60 border border-zinc-800 animate-pulse" />
                <div className="h-24 rounded-lg bg-zinc-950/60 border border-zinc-800 animate-pulse" />
                <div className="h-24 rounded-lg bg-zinc-950/60 border border-zinc-800 animate-pulse" />
              </div>
            </div>
          )}

          {error && (
            <div
              role="alert"
              className="rounded-lg border border-red-900/60 bg-red-950/30 p-4 flex flex-wrap items-center justify-between gap-3 text-sm text-red-200"
            >
              <p>{error}</p>
              <button
                type="button"
                className="px-3 py-1.5 text-xs font-medium bg-zinc-800 hover:bg-zinc-700 text-zinc-100 rounded-md border border-zinc-700 transition-colors"
                onClick={() => setAttempt((n) => n + 1)}
              >
                Retry
              </button>
            </div>
          )}

          {data && (
            <>
              {/* Authors & Blurb */}
              <div className="space-y-3">
                <div className="flex flex-wrap items-center justify-between gap-3 text-xs">
                  <div className="flex flex-wrap items-center gap-2 text-zinc-300">
                    <User className="w-4 h-4 text-zinc-400 shrink-0" aria-hidden="true" />
                    <span className="text-zinc-400">
                      {seriesAuthors.length > 1 ? 'Authors:' : 'Author:'}
                    </span>
                    {seriesAuthors.length > 0 ? (
                      <span className="font-medium text-zinc-200">
                        {seriesAuthors.map((author, idx) => (
                          <span key={author}>
                            {idx > 0 && ', '}
                            <EntityLink kind="authors" name={author} />
                          </span>
                        ))}
                      </span>
                    ) : (
                      <span className="text-zinc-400">Author unknown</span>
                    )}
                  </div>

                  <button
                    type="button"
                    onClick={() => {
                      let destination = entityURL('series', displayTitle, data?.id ?? id);
                      if (!data?.id && !id && (author || hardcoverID)) {
                        const params = new URLSearchParams();
                        if (author) params.set('author', author);
                        if (hardcoverID) params.set('hardcover_id', String(hardcoverID));
                        destination += `?${params}`;
                      }
                      window.location.hash = destination;
                      onClose();
                    }}
                    className="inline-flex items-center gap-2 px-3 py-2 text-xs font-medium text-zinc-200 bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 rounded-md transition-colors focus-visible:ring-2 focus-visible:ring-blue-400 focus-visible:ring-offset-2 focus-visible:ring-offset-zinc-900"
                  >
                    <SlidersHorizontal className="w-4 h-4" aria-hidden="true" />
                    Manage monitoring
                  </button>
                </div>

                {data.description?.trim() ? (
                  <div className="bg-zinc-950/50 p-3.5 rounded-lg border border-zinc-800/80 text-xs text-zinc-300 leading-relaxed max-w-[75ch]">
                    <h3 className="text-xs font-semibold text-zinc-200 mb-1">Series Overview</h3>
                    <p>{data.description}</p>
                  </div>
                ) : (
                  <p className="text-xs text-zinc-400">No series description available.</p>
                )}

                {!data.complete && (
                  <div className="flex items-start gap-2.5 px-3.5 py-2.5 rounded-lg bg-zinc-950/60 border border-zinc-800 text-xs text-zinc-300">
                    <Info className="w-4 h-4 text-zinc-400 shrink-0 mt-0.5" aria-hidden="true" />
                    <span>
                      Showing known catalog books. Full reading order is unavailable; configure Hardcover metadata or retry later.
                    </span>
                  </div>
                )}
              </div>

              {/* Ordered Library Book View */}
              <div className="space-y-3 border-t border-zinc-800 pt-5">
                <h3 className="text-sm font-semibold text-zinc-200 flex items-center gap-2">
                  Reading Order
                  <span className="text-xs font-mono font-normal text-zinc-400">
                    ({books.length})
                  </span>
                </h3>

                {actionError && (
                  <p role="alert" className="rounded-lg border border-red-900/60 bg-red-950/30 px-3.5 py-2 text-xs text-red-200">
                    {actionError}
                  </p>
                )}

                {books.length === 0 ? (
                  <div className="py-10 text-center border border-dashed border-zinc-800 rounded-lg">
                    <BookOpen className="w-7 h-7 text-zinc-500 mx-auto mb-2" aria-hidden="true" />
                    <p className="text-xs font-medium text-zinc-300">
                      No books found for this series.
                    </p>
                  </div>
                ) : (
                  <ol className="space-y-3" aria-label="Series reading order">
                    {books.map((book, i) => {
                      const indexLabel = book.series_index != null ? `Book ${book.series_index}` : 'Unindexed';
                      const formats = book.formats ?? [];
                      const state = book.state ?? (book.library_book_id ? 'In library' : 'Not in library');
                      const isOwned = state === 'In library';
                      const rowBusy = busyIndex === i;

                      return (
                        <li
                          key={`${book.title}-${book.series_index ?? 'u'}-${i}`}
                          data-testid={`series-book-item-${i}`}
                          className={`bg-zinc-950/60 border rounded-lg p-3.5 flex gap-4 items-start transition-colors duration-150 ${
                            isOwned
                              ? 'border-zinc-800 hover:border-zinc-700'
                              : 'border-zinc-800/80 hover:border-zinc-700'
                          }`}
                        >
                          <SeriesBookCover coverUrl={book.cover_url} title={book.title} />

                          <div className="flex-1 min-w-0 space-y-1.5">
                            <div className="flex flex-wrap items-start justify-between gap-2">
                              <div className="flex flex-wrap items-center gap-2 min-w-0">
                                <span className="font-mono text-[11px] font-semibold px-2 py-0.5 rounded-sm bg-purple-950/80 text-purple-300 border border-purple-800/60 shrink-0">
                                  {indexLabel}
                                </span>
                                <button
                                  type="button"
                                  disabled={rowBusy}
                                  onClick={() => void handleOpenBook(book, i)}
                                  className="text-sm font-semibold text-zinc-100 hover:text-blue-300 text-left underline decoration-zinc-700 underline-offset-2 hover:decoration-blue-300 rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 transition-colors disabled:opacity-50"
                                >
                                  {book.title}
                                </button>
                              </div>

                              {formats.length > 0 && (
                                <div className="flex flex-wrap gap-1 shrink-0">
                                  {formats.map((fmt) => (
                                    <span
                                      key={fmt}
                                      className={`text-[10px] font-mono px-1.5 py-0.5 rounded-sm font-semibold ${
                                        fmt === 'M4B'
                                          ? 'bg-purple-950/80 text-purple-300 border border-purple-800/60'
                                          : 'bg-blue-950/80 text-blue-300 border border-blue-800/60'
                                      }`}
                                    >
                                      {fmt}
                                    </span>
                                  ))}
                                </div>
                              )}
                            </div>

                            {book.subtitle && (
                              <p className="text-xs text-zinc-400">{book.subtitle}</p>
                            )}

                            {book.authors && book.authors.length > 0 && (
                              <p className="text-xs text-zinc-400">
                                by{' '}
                                {book.authors.map((author, idx) => (
                                  <span key={author}>
                                    {idx > 0 && ', '}
                                    <EntityLink kind="authors" name={author} />
                                  </span>
                                ))}
                              </p>
                            )}

                            {book.description && (
                              <p className="text-xs text-zinc-300 line-clamp-2 leading-relaxed max-w-[75ch]">
                                {book.description}
                              </p>
                            )}

                            <div className="flex flex-wrap items-center justify-between gap-2 pt-1 text-xs">
                              <span className="flex items-center gap-1.5 text-zinc-400">
                                <Calendar className="w-3.5 h-3.5 text-zinc-400 shrink-0" aria-hidden="true" />
                                {book.publish_date || 'Release date unknown'}
                              </span>

                              <div className="flex flex-wrap items-center gap-1.5">
                                {isOwned ? (
                                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-sm text-[11px] font-medium bg-emerald-950/80 text-emerald-300 border border-emerald-800/60">
                                    <Check className="w-3 h-3" aria-hidden="true" />
                                    In library
                                  </span>
                                ) : (
                                  <>
                                    <span className="px-2 py-0.5 rounded-sm text-[11px] font-medium bg-zinc-900 text-zinc-400 border border-zinc-800">
                                      {state}
                                    </span>
                                    {allowRequest && state === 'Not in library' && (
                                      <button
                                        type="button"
                                        disabled={rowBusy}
                                        onClick={() => void handleOpenDownloadOptions(book, i)}
                                        className="inline-flex items-center gap-1 rounded-sm border border-zinc-700 bg-zinc-800 px-2 py-0.5 text-[11px] font-medium text-zinc-100 hover:bg-zinc-700 transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-50"
                                      >
                                        <Download className="w-3 h-3" aria-hidden="true" />
                                        Download
                                      </button>
                                    )}
                                    {state === 'Scheduled' && canManage && (
                                      <button
                                        type="button"
                                        disabled={rowBusy}
                                        aria-pressed={Boolean(book.monitored)}
                                        onClick={() => void handleToggleBookMonitor(book, i)}
                                        className={`inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 text-[11px] font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-50 ${
                                          book.monitored
                                            ? 'border-blue-700/80 bg-blue-950/80 text-blue-200 hover:bg-blue-900/70'
                                            : 'border-zinc-700 bg-zinc-800 text-zinc-100 hover:bg-zinc-700'
                                        }`}
                                      >
                                        <Bell className="w-3 h-3" aria-hidden="true" />
                                        {book.monitored ? 'Monitoring' : 'Monitor'}
                                      </button>
                                    )}
                                  </>
                                )}
                              </div>
                            </div>
                          </div>
                        </li>
                      );
                    })}
                  </ol>
                )}
              </div>
            </>
          )}
        </div>

        {/* Footer */}
        <div className="px-6 py-3 border-t border-zinc-800 bg-zinc-950/60 flex items-center justify-between gap-3">
          <span className="text-xs text-zinc-400">
            {data ? `${ownedCount} of ${totalBooks} books in library` : ''}
          </span>
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-1.5 text-xs font-medium text-zinc-300 hover:text-white bg-zinc-800 hover:bg-zinc-700 rounded-md transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
          >
            Close (Esc)
          </button>
        </div>
      </div>
    </div>
    {detailBook && (
      <BookDetailModal
        book={detailBook}
        isAdmin={isAdmin}
        canManage={canManage}
        canRequest={allowRequest}
        onBookUpdated={(updated) => {
          setDetailBook(updated);
          const idx =
            detailIndex !== null
              ? detailIndex
              : books.findIndex(
                  (b) => (b.book_id && b.book_id === updated.id) || (b.library_book_id && b.library_book_id === updated.id)
                );
          if (idx >= 0) {
            updateBookEntry(idx, {
              book_id: updated.id,
              monitored: updated.monitored,
              ...(updated.coverUrl ? { cover_url: updated.coverUrl } : {}),
            });
          }
        }}
        onBookDeleted={() => {
          setDetailBook(null);
          setDetailIndex(null);
          setAttempt((n) => n + 1);
          onBookDeleted?.();
        }}
        onMetadataApplied={() => setAttempt((n) => n + 1)}
        onClose={() => {
          setDetailBook(null);
          setDetailIndex(null);
        }}
      />
    )}
    {downloadBook && (
      <ManualSearch
        key={downloadBook.id}
        book={downloadBook}
        onClose={() => setDownloadBook(null)}
      />
    )}
    </>
  );
}
