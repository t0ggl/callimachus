import { EntityLink, entityURL } from '../components/EntityLink';
import React, { useState, useMemo, useEffect, useRef, useCallback } from 'react';
import {
  Grid,
  List,
  Search,
  BookOpen,
  User,
  Layers,
  Filter,
  CheckSquare,
  Bell,
} from 'lucide-react';
import {
  Book,
  Author,
  Series,
  SeriesSelection,
  FormatType,
  FORMAT_TYPES,
  BulkActionReport,
  BulkActionType,
  BulkItemKind,
  BulkSelectionItem,
} from '../types';

import { useKeyboardNav } from '../hooks/useKeyboardNav';
import { SeriesBadge } from '../components/SeriesBadge';
import { SeriesDetailModal } from '../components/SeriesDetailModal';
import { LibraryBulkBar } from '../components/LibraryBulkBar';

type CatalogDisplayItem =
  | { kind: 'book'; book: Book }
  | {
      kind: 'series';
      key: string;
      seriesId?: string;
      title: string;
      authors: string[];
      books: Book[];
      coverUrl?: string;
      formats: FormatType[];
      totalBooks?: number;
      editionsCount: number;
      pendingCount: number;
      monitored?: boolean;
    };

function selectionKey(kind: BulkItemKind, id: string): string {
  return `${kind}:${id}`;
}

function buildCatalogDisplayItems(
  books: Book[],
  series: Series[],
  bundleSeries: boolean
): CatalogDisplayItem[] {
  if (!bundleSeries) {
    return books.map((book) => ({ kind: 'book', book }));
  }

  const seriesById = new Map<string, Series>();
  const seriesByLowerTitle = new Map<string, Series>();
  for (const s of series) {
    if (s.id) seriesById.set(s.id, s);
    if (s.title) seriesByLowerTitle.set(s.title.trim().toLowerCase(), s);
  }

  const standalone: CatalogDisplayItem[] = [];
  const grouped = new Map<
    string,
    {
      seriesId?: string;
      title: string;
      books: Book[];
      totalBooks?: number;
      monitored?: boolean;
    }
  >();

  for (const book of books) {
    const meta =
      (book.seriesId ? seriesById.get(book.seriesId) : undefined) ??
      (book.seriesTitle ? seriesByLowerTitle.get(book.seriesTitle.trim().toLowerCase()) : undefined);
    const resolvedTitle = book.seriesTitle?.trim() || meta?.title?.trim() || '';
    if (!resolvedTitle) {
      standalone.push({ kind: 'book', book });
      continue;
    }

    const groupKey = book.seriesId?.trim()
      ? `id:${book.seriesId.trim()}`
      : `title:${resolvedTitle.toLowerCase()}`;

    const existing = grouped.get(groupKey);
    if (existing) {
      existing.books.push(book);
      if (!existing.seriesId && (book.seriesId || meta?.id)) {
        existing.seriesId = book.seriesId || meta?.id;
      }
      if (!existing.totalBooks && meta?.totalBooks) {
        existing.totalBooks = meta.totalBooks;
      }
      if (existing.monitored === undefined && meta?.monitored !== undefined) {
        existing.monitored = meta.monitored;
      }
    } else {
      grouped.set(groupKey, {
        seriesId: book.seriesId?.trim() || meta?.id,
        title: resolvedTitle,
        books: [book],
        totalBooks: meta?.totalBooks,
        monitored: meta?.monitored,
      });
    }
  }

  const seriesItems: CatalogDisplayItem[] = Array.from(grouped.entries()).map(([key, group]) => {
    const orderedBooks = [...group.books].sort((a, b) => {
      const ai = a.seriesIndex ?? Number.POSITIVE_INFINITY;
      const bi = b.seriesIndex ?? Number.POSITIVE_INFINITY;
      if (ai !== bi) return ai - bi;
      return a.title.localeCompare(b.title);
    });

    const authors: string[] = [];
    const seenAuthors = new Set<string>();
    const formats: FormatType[] = [];
    const seenFormats = new Set<FormatType>();
    let editionsCount = 0;
    let pendingCount = 0;

    for (const b of orderedBooks) {
      if (b.state === 'Requested' || b.state === 'Searching' || b.state === 'Downloading') {
        pendingCount++;
      }
      const author = b.authorName.trim();
      if (author && !seenAuthors.has(author.toLowerCase())) {
        seenAuthors.add(author.toLowerCase());
        authors.push(author);
      }
      for (const fmt of b.formats) {
        if (!seenFormats.has(fmt)) {
          seenFormats.add(fmt);
          formats.push(fmt);
        }
      }
      editionsCount += b.editions.length;
    }

    const coverUrl = orderedBooks.find((b) => Boolean(b.coverUrl))?.coverUrl;

    return {
      kind: 'series',
      key,
      seriesId: group.seriesId,
      title: group.title,
      authors,
      books: orderedBooks,
      coverUrl,
      formats,
      totalBooks: group.totalBooks,
      editionsCount,
      pendingCount,
      monitored: group.monitored,
    };
  });

  const allItems = [...standalone, ...seriesItems];
  allItems.sort((a, b) => {
    const aTitle = a.kind === 'series' ? a.title : a.book.title;
    const bTitle = b.kind === 'series' ? b.title : b.book.title;
    const cmp = aTitle.localeCompare(bTitle, undefined, { sensitivity: 'base' });
    if (cmp !== 0) return cmp;
    const aKey = a.kind === 'series' ? a.key : a.book.id;
    const bKey = b.kind === 'series' ? b.key : b.book.id;
    return aKey.localeCompare(bKey);
  });

  return allItems;
}

interface CatalogViewProps {
  books: Book[];
  authors: Author[];
  series: Series[];
  bundleSeriesInLibrary?: boolean;
  canManage?: boolean;
  canRequest?: boolean;
  isAdmin?: boolean;
  canRefresh?: boolean;
  onBulkRefresh?: (items: BulkSelectionItem[]) => Promise<BulkActionReport>;
  onSelectBook: (book: Book) => void;
  keyboardNavigationEnabled?: boolean;
  selectedSeries?: SeriesSelection | null;
  onSelectSeries?: (series: SeriesSelection | null) => void;
  onScanComplete?: () => Promise<void>;
}

export const CatalogView: React.FC<CatalogViewProps> = ({
  books,
  authors,
  series,
  bundleSeriesInLibrary = true,
  canManage,
  canRequest,
  isAdmin,
  canRefresh = false,
  onBulkRefresh,
  onSelectBook,
  keyboardNavigationEnabled = true,
  selectedSeries: controlledSelectedSeries,
  onSelectSeries,
  onScanComplete,
}) => {
  const [internalSelectedSeries, setInternalSelectedSeries] = useState<SeriesSelection | null>(null);
  const selectedSeries =
    controlledSelectedSeries !== undefined ? controlledSelectedSeries : internalSelectedSeries;
  const setSelectedSeries = onSelectSeries ?? setInternalSelectedSeries;
  const [scanning, setScanning] = useState(false);
  const [scanMessage, setScanMessage] = useState('');
  const scan = async () => {
    setScanning(true); setScanMessage('');
    try {
      const res = await fetch('/api/library/scan', { method: 'POST' });
      if (!res.ok) throw new Error('Library scan failed. Check the library path and permissions, then retry.');
      const report = await res.json();
      await onScanComplete?.();
      setScanMessage(`Scan complete: ${report.books} books found.`);
    } catch (e) { setScanMessage((e as Error).message); }
    finally { setScanning(false); }
  };
  const [viewMode, setViewMode] = useState<'grid' | 'list'>('grid');
  const [activeTab, setActiveTab] = useState<'books' | 'authors' | 'series'>('books');
  const [searchQuery, setSearchQuery] = useState('');
  const [formatFilter, setFormatFilter] = useState<'ALL' | FormatType>('ALL');

  // Bulk selection state (only available for curators/admins)
  const [selectionMode, setSelectionMode] = useState(false);
  const [selectedMap, setSelectedMap] = useState<Map<string, BulkSelectionItem>>(() => new Map());
  const [lastSelectedKey, setLastSelectedKey] = useState<string | null>(null);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkReport, setBulkReport] = useState<BulkActionReport | null>(null);
  const [bulkError, setBulkError] = useState('');
  const lastShiftRef = useRef(false);
  const refreshAvailable = Boolean(onBulkRefresh || canRefresh);

  // Filtered books
  const filteredBooks = useMemo(() => {
    return books.filter((b) => {
      const matchesSearch =
        b.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
        b.authorName.toLowerCase().includes(searchQuery.toLowerCase()) ||
        (b.seriesTitle && b.seriesTitle.toLowerCase().includes(searchQuery.toLowerCase())) ||
        b.seriesMemberships?.some((membership) => membership.title.toLowerCase().includes(searchQuery.toLowerCase()));

      const matchesFormat =
        formatFilter === 'ALL' || b.formats.includes(formatFilter);

      return matchesSearch && matchesFormat;
    });
  }, [books, searchQuery, formatFilter]);

  const displayItems = useMemo(
    () => buildCatalogDisplayItems(filteredBooks, series, bundleSeriesInLibrary),
    [filteredBooks, series, bundleSeriesInLibrary]
  );

  // Filtered authors
  const filteredAuthors = useMemo(() => {
    return authors.filter(
      (a) =>
        a.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
        a.sortName.toLowerCase().includes(searchQuery.toLowerCase())
    );
  }, [authors, searchQuery]);

  // Filtered series
  const filteredSeries = useMemo(() => {
    return series.filter((s) =>
      s.title.toLowerCase().includes(searchQuery.toLowerCase())
    );
  }, [series, searchQuery]);

  // Keep selected items synchronized with catalog updates (remove deleted items, update metadata).
  useEffect(() => {
    setSelectedMap((prev) => {
      if (prev.size === 0) return prev;
      const booksById = new Map(books.map((b) => [b.id, b]));
      const seriesById = new Map(series.map((s) => [s.id, s]));
      const bookCountBySeries = new Map<string, number>();
      for (const b of books) {
        if (b.seriesId) {
          bookCountBySeries.set(b.seriesId, (bookCountBySeries.get(b.seriesId) ?? 0) + 1);
        }
      }
      let changed = false;
      const next = new Map<string, BulkSelectionItem>();
      for (const [key, item] of prev.entries()) {
        if (item.kind === 'book') {
          const found = booksById.get(item.id);
          if (!found) {
            changed = true;
            continue;
          }
          if (found.title !== item.title || found.monitored !== item.monitored) {
            changed = true;
            next.set(key, {
              ...item,
              title: found.title,
              subtitle: found.authorName,
              monitored: found.monitored,
            });
          } else {
            next.set(key, item);
          }
        } else {
          const found = seriesById.get(item.id);
          const count = bookCountBySeries.get(item.id) ?? 0;
          if (!found && count === 0) {
            changed = true;
            continue;
          }
          const nextTitle = found?.title ?? item.title;
          const nextMonitored = found?.monitored ?? item.monitored;
          if (nextTitle !== item.title || nextMonitored !== item.monitored || count !== item.bookCount) {
            changed = true;
            next.set(key, {
              ...item,
              title: nextTitle,
              bookCount: count,
              monitored: nextMonitored,
            });
          } else {
            next.set(key, item);
          }
        }
      }
      return changed ? next : prev;
    });
  }, [books, series]);

  const toBulkItem = useCallback((item: CatalogDisplayItem): BulkSelectionItem | null => {
    if (item.kind === 'book') {
      return {
        kind: 'book',
        id: item.book.id,
        title: item.book.title,
        subtitle: item.book.authorName,
        monitored: item.book.monitored || item.book.audiobookMonitored,
      };
    }
    if (!item.seriesId) return null;
    return {
      kind: 'series',
      id: item.seriesId,
      title: item.title,
      bookCount: item.books.length,
      monitored: item.monitored,
    };
  }, []);

  const visibleSelectableItems = useMemo<BulkSelectionItem[]>(() => {
    if (activeTab === 'books') {
      const items: BulkSelectionItem[] = [];
      for (const item of displayItems) {
        const target = toBulkItem(item);
        if (target) items.push(target);
      }
      return items;
    }
    if (activeTab === 'series') {
      return filteredSeries.map((s) => ({
        kind: 'series',
        id: s.id,
        title: s.title,
        bookCount: books.filter((b) => b.seriesId === s.id || b.seriesMemberships?.some((membership) => membership.seriesId === s.id)).length,
        monitored: s.monitored,
      }));
    }
    return [];
  }, [activeTab, displayItems, filteredSeries, books, toBulkItem]);

  const selectedItems = useMemo(() => Array.from(selectedMap.values()), [selectedMap]);

  const visibleKeySet = useMemo(
    () => new Set(visibleSelectableItems.map((item) => selectionKey(item.kind, item.id))),
    [visibleSelectableItems]
  );

  const visibleSelectedCount = useMemo(
    () => visibleSelectableItems.filter((item) => selectedMap.has(selectionKey(item.kind, item.id))).length,
    [visibleSelectableItems, selectedMap]
  );

  const allVisibleSelected =
    visibleSelectableItems.length > 0 && visibleSelectedCount === visibleSelectableItems.length;

  const hiddenSelectedCount = useMemo(
    () => selectedItems.filter((item) => !visibleKeySet.has(selectionKey(item.kind, item.id))).length,
    [selectedItems, visibleKeySet]
  );

  const toggleItemSelection = useCallback(
    (target: BulkSelectionItem, shiftKey = false) => {
      const key = selectionKey(target.kind, target.id);
      setSelectedMap((prev) => {
        const next = new Map(prev);
        if (shiftKey && lastSelectedKey) {
          const fromIdx = visibleSelectableItems.findIndex(
            (item) => selectionKey(item.kind, item.id) === lastSelectedKey
          );
          const toIdx = visibleSelectableItems.findIndex(
            (item) => selectionKey(item.kind, item.id) === key
          );
          if (fromIdx >= 0 && toIdx >= 0) {
            const [start, end] = fromIdx < toIdx ? [fromIdx, toIdx] : [toIdx, fromIdx];
            for (let i = start; i <= end; i++) {
              const item = visibleSelectableItems[i];
              next.set(selectionKey(item.kind, item.id), item);
            }
            return next;
          }
        }
        if (next.has(key)) {
          next.delete(key);
        } else {
          next.set(key, target);
        }
        return next;
      });
      setLastSelectedKey(key);
    },
    [lastSelectedKey, visibleSelectableItems]
  );

  const handleToggleSelectAllVisible = useCallback(() => {
    setSelectedMap((prev) => {
      const next = new Map(prev);
      if (allVisibleSelected) {
        for (const item of visibleSelectableItems) {
          next.delete(selectionKey(item.kind, item.id));
        }
      } else {
        for (const item of visibleSelectableItems) {
          next.set(selectionKey(item.kind, item.id), item);
        }
      }
      return next;
    });
  }, [allVisibleSelected, visibleSelectableItems]);

  const handleRemoveSelectedItem = useCallback((kind: BulkItemKind, id: string) => {
    setSelectedMap((prev) => {
      const next = new Map(prev);
      next.delete(selectionKey(kind, id));
      return next;
    });
  }, []);

  const handleClearSelection = useCallback(() => {
    setSelectedMap(new Map());
    setLastSelectedKey(null);
  }, []);

  const exitSelectionMode = useCallback(() => {
    setSelectionMode(false);
    setSelectedMap(new Map());
    setLastSelectedKey(null);
  }, []);

  const handleBulkAction = useCallback(
    async (action: BulkActionType, options?: { deleteFiles?: boolean }) => {
      const targetItems =
        action === 'discover'
          ? selectedItems.filter((item) => item.kind === 'series')
          : action === 'delete'
            ? selectedItems.filter((item) => item.kind === 'book')
            : selectedItems;
      if (targetItems.length === 0 || bulkBusy) return;

      setBulkBusy(true);
      setBulkError('');
      setBulkReport(null);
      try {
        let report: BulkActionReport;
        if (action === 'refresh' && onBulkRefresh) {
          report = await onBulkRefresh(targetItems);
        } else {
          const payload: Record<string, unknown> = {
            action,
            items: targetItems.map(({ kind, id, title }) => ({ kind, id, title })),
          };
          if (action === 'delete' && options?.deleteFiles) {
            payload.deleteFiles = true;
          }
          const res = await fetch('/api/library/bulk', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
          });
          if (!res.ok) {
            const msg = res.headers.get('Content-Type')?.startsWith('text/plain')
              ? (await res.text()).trim()
              : '';
            throw new Error(msg || 'Bulk action failed. Check your permissions and try again.');
          }
          report = (await res.json()) as BulkActionReport;
        }

        setBulkReport(report);
        if (report.succeeded > 0) {
          await onScanComplete?.();
        }
        setSelectedMap((prev) => {
          const next = new Map(prev);
          for (const item of report.results) {
            if (item.ok) {
              next.delete(selectionKey(item.kind, item.id));
            }
          }
          return next;
        });
      } catch (e) {
        setBulkError(e instanceof Error ? e.message : 'Bulk action failed. Please try again.');
      } finally {
        setBulkBusy(false);
      }
    },
    [selectedItems, bulkBusy, onBulkRefresh, onScanComplete]
  );

  // Escape clears selection or exits selection mode when active
  useEffect(() => {
    if (!selectionMode || selectedSeries) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      const target = e.target instanceof Element ? e.target : null;
      if (target?.closest('input[type="text"], textarea, select')) return;
      if (selectedMap.size > 0) {
        handleClearSelection();
      } else {
        setSelectionMode(false);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [selectionMode, selectedSeries, selectedMap.size, handleClearSelection]);

  const selectedItemRef = useRef<HTMLElement | null>(null);
  const setSelectedItem = (item: HTMLElement | null) => {
    selectedItemRef.current = item;
  };
  const { selectedIndex, setSelectedIndex } = useKeyboardNav({
    itemsCount: activeTab === 'books' ? displayItems.length : 0,
    enabled: keyboardNavigationEnabled && activeTab === 'books' && !selectedSeries,
    onSelect: (index) => {
      const item = displayItems[index];
      if (!item) return;
      if (canManage && selectionMode) {
        const target = toBulkItem(item);
        if (target) toggleItemSelection(target);
        return;
      }
      if (item.kind === 'series') setSelectedSeries({ title: item.title, id: item.seriesId });
      else onSelectBook(item.book);
    },
  });

  useEffect(() => {
    const item = selectedItemRef.current;
    item?.querySelector<HTMLButtonElement>('[data-catalog-title]')?.focus({ preventScroll: true });
    item?.scrollIntoView?.({ block: 'nearest' });
  }, [selectedIndex]);

  return (
    <div className="space-y-6">
      {selectedSeries && (
        <SeriesDetailModal
          {...selectedSeries}
          canManage={canManage}
          canRequest={canRequest}
          isAdmin={isAdmin}
          onClose={() => setSelectedSeries(null)}
          onBookDeleted={() => { void onScanComplete?.(); }}
          onSeriesUpdated={() => { void onScanComplete?.(); }}
          onOpenBook={(bookId) => {
            const found = books.find((b) => b.id === bookId);
            if (found) {
              setSelectedSeries(null);
              onSelectBook(found);
              return;
            }
            void fetch(`/api/books?id=${encodeURIComponent(bookId)}`)
              .then((res) => (res.ok ? res.json() : []))
              .then((list: Book[]) => {
                const fetched = Array.isArray(list) ? list.find((b) => b.id === bookId) : undefined;
                if (fetched) {
                  setSelectedSeries(null);
                  onSelectBook(fetched);
                }
              })
              .catch(() => {});
          }}
        />
      )}
      {/* Top Header & Actions Bar */}
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3 border-b border-zinc-800 pb-4">
        <div>
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="text-xl font-bold tracking-tight text-white">Library</h1>
            <button
              type="button"
              disabled={scanning}
              onClick={() => void scan()}
              className="min-h-11 px-3 py-2 text-sm bg-zinc-800 text-zinc-100 rounded-md enabled:hover:bg-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 disabled:opacity-50"
            >
              {scanning ? 'Scanning…' : 'Scan Library'}
            </button>
          </div>
          <p className="text-xs text-zinc-400 mt-0.5">
            Organized book works, editions, series sequences, and media assets
          </p>
        </div>

        {/* View Switcher & Actions */}
        <div className="flex min-h-11 flex-wrap items-center gap-3">
          {/* Tabs */}
          <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5 text-xs font-medium">
            <button
              onClick={() => setActiveTab('books')}
              data-testid="tab-books"
              className={`px-3 py-1.5 rounded-md transition-colors ${
                activeTab === 'books'
                  ? 'bg-zinc-800 text-white shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Books ({books.length})
            </button>
            <button
              onClick={() => setActiveTab('authors')}
              data-testid="tab-authors"
              className={`px-3 py-1.5 rounded-md transition-colors ${
                activeTab === 'authors'
                  ? 'bg-zinc-800 text-white shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Authors ({authors.length})
            </button>
            <button
              onClick={() => setActiveTab('series')}
              data-testid="tab-series"
              className={`px-3 py-1.5 rounded-md transition-colors ${
                activeTab === 'series'
                  ? 'bg-zinc-800 text-white shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Series ({series.length})
            </button>
          </div>

          {/* Grid vs List toggle (active on books tab) */}
          {activeTab === 'books' && (
            <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5">
              <button
                onClick={() => {
                  if (viewMode !== 'grid') setSelectedIndex(-1);
                  setViewMode('grid');
                }}
                aria-pressed={viewMode === 'grid'}
                data-testid="view-grid-btn"
                className={`p-1.5 rounded-md transition-colors ${
                  viewMode === 'grid'
                    ? 'bg-zinc-800 text-white'
                    : 'text-zinc-400 hover:text-zinc-200'
                }`}
                title="Grid View"
              >
                <Grid className="w-4 h-4" />
              </button>
              <button
                onClick={() => {
                  if (viewMode !== 'list') setSelectedIndex(-1);
                  setViewMode('list');
                }}
                aria-pressed={viewMode === 'list'}
                data-testid="view-list-btn"
                className={`p-1.5 rounded-md transition-colors ${
                  viewMode === 'list'
                    ? 'bg-zinc-800 text-white'
                    : 'text-zinc-400 hover:text-zinc-200'
                }`}
                title="List View"
              >
                <List className="w-4 h-4" />
              </button>
            </div>
          )}

          {/* Selection Mode Toggle (Curator/Admin, Books & Series tabs) */}
          {canManage && activeTab !== 'authors' && (
            <button
              type="button"
              data-testid="toggle-select-mode"
              aria-pressed={selectionMode}
              onClick={() => {
                if (selectionMode) {
                  exitSelectionMode();
                } else {
                  setSelectionMode(true);
                }
              }}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 ${
                selectionMode
                  ? 'bg-blue-950/80 border-blue-600/80 text-blue-200 hover:bg-blue-900/70'
                  : 'bg-zinc-900 border-zinc-800 text-zinc-300 hover:text-white hover:border-zinc-700'
              }`}
            >
              <CheckSquare className="w-3.5 h-3.5" aria-hidden="true" />
              {selectionMode ? 'Done selecting' : 'Select'}
            </button>
          )}
        </div>
        {scanMessage && <p role="status" className="w-full text-sm text-zinc-300">{scanMessage}</p>}
      </div>

      {/* Bulk Action Bar & Reports */}
      {canManage &&
        activeTab !== 'authors' &&
        (selectionMode || selectedItems.length > 0 || bulkReport || bulkError) && (
          <LibraryBulkBar
            selectedItems={selectedItems}
            visibleSelectableCount={visibleSelectableItems.length}
            allVisibleSelected={allVisibleSelected}
            hiddenSelectedCount={hiddenSelectedCount}
            isAdmin={isAdmin}
            canRefresh={refreshAvailable}
            busy={bulkBusy}
            report={bulkReport}
            error={bulkError}
            onToggleSelectAllVisible={handleToggleSelectAllVisible}
            onRemoveItem={handleRemoveSelectedItem}
            onClearSelection={handleClearSelection}
            onExitSelectionMode={exitSelectionMode}
            onAction={(action, options) => void handleBulkAction(action, options)}
            onDismissReport={() => {
              setBulkReport(null);
              setBulkError('');
            }}
          />
        )}

      {/* Filter Toolbar */}
      <div className="flex flex-col sm:flex-row items-center justify-between gap-3">
        <div className="relative w-full sm:w-80">
          <Search className="w-4 h-4 text-zinc-500 absolute left-3 top-2.5" />
          <input
            type="text"
            placeholder={`Filter ${activeTab}...`}
            value={searchQuery}
            onChange={(e) => {
              setSelectedIndex(-1);
              setSearchQuery(e.target.value);
            }}
            className="w-full pl-9 pr-3 py-1.5 bg-zinc-900 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-hidden focus:border-zinc-700"
          />
        </div>

        {activeTab === 'books' && (
          <div className="flex items-center gap-2 flex-wrap self-start sm:self-auto text-xs">
            <span className="text-zinc-500 flex items-center gap-1">
              <Filter className="w-3.5 h-3.5" /> Format:
            </span>
            <div className="flex flex-wrap bg-zinc-900 border border-zinc-800 rounded-md p-0.5 text-xs font-mono">
              {(['ALL', ...FORMAT_TYPES] as const).map((fmt) => (
                <button
                  key={fmt}
                  onClick={() => {
                    if (formatFilter !== fmt) setSelectedIndex(-1);
                    setFormatFilter(fmt);
                  }}
                  data-testid={`filter-format-${fmt.toLowerCase()}`}
                  className={`px-2.5 py-1 rounded-sm transition-colors ${
                    formatFilter === fmt
                      ? 'bg-zinc-800 text-white font-semibold'
                      : 'text-zinc-400 hover:text-zinc-200'
                  }`}
                >
                  {fmt}
                </button>
              ))}
            </div>
            <span className="text-[11px] text-zinc-500 ml-2">
              (Use <kbd className="px-1 py-0.5 bg-zinc-900 border border-zinc-800 rounded-sm">j</kbd>/<kbd className="px-1 py-0.5 bg-zinc-900 border border-zinc-800 rounded-sm">k</kbd> + <kbd className="px-1 py-0.5 bg-zinc-900 border border-zinc-800 rounded-sm">↵</kbd>)
            </span>
          </div>
        )}
      </div>

      {/* TAB CONTENT: BOOKS */}
      {activeTab === 'books' && (
        <>
          {displayItems.length === 0 ? (
            <div className="py-20 text-center border border-dashed border-zinc-800 rounded-xl">
              <BookOpen className="w-8 h-8 text-zinc-600 mx-auto mb-2" />
              <div className="text-sm font-medium text-zinc-400">No books found</div>
              <div className="text-xs text-zinc-500 mt-1">
                Try refining your search or format filters.
              </div>
            </div>
          ) : viewMode === 'grid' ? (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
              {displayItems.map((item, index) => {
                const isSelected = index === selectedIndex;
                const bulkTarget = toBulkItem(item);
                const isBulkSelected = Boolean(
                  bulkTarget && selectedMap.has(selectionKey(bulkTarget.kind, bulkTarget.id))
                );
                if (item.kind === 'series') {
                  const seriesTestId = item.seriesId || item.title;
                  return (
                    <div
                      key={`series-${item.key}`}
                      ref={isSelected ? setSelectedItem : undefined}
                      data-testid={`series-card-${seriesTestId}`}
                      aria-selected={canManage && selectionMode ? isBulkSelected : undefined}
                      onClick={(e) => {
                        if (canManage && selectionMode && bulkTarget) {
                          toggleItemSelection(bulkTarget, e.shiftKey);
                          return;
                        }
                        setSelectedSeries({ title: item.title, id: item.seriesId });
                      }}
                      className={`group relative rounded-lg border overflow-hidden cursor-pointer transition-all duration-150 flex flex-col ${
                        isBulkSelected
                          ? 'bg-purple-950/30 border-purple-500 ring-2 ring-purple-500/80 shadow-lg shadow-purple-500/15'
                          : isSelected
                            ? 'bg-zinc-900/90 border-purple-500 ring-1 ring-purple-500 shadow-lg shadow-purple-500/10'
                            : 'bg-zinc-900/90 border-zinc-800/80 hover:border-purple-700/70 hover:bg-zinc-900'
                      }`}
                    >
                      {/* Representative Cover Image */}
                      <div className="aspect-[2/3] w-full bg-zinc-950 relative overflow-hidden">
                        {item.coverUrl ? (
                          <img
                            src={item.coverUrl}
                            alt={item.title}
                            className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-200"
                          />
                        ) : (
                          <div className="w-full h-full flex flex-col items-center justify-center text-zinc-600">
                            <Layers className="w-8 h-8 mb-1" aria-hidden="true" />
                          </div>
                        )}

                        {/* Selection Checkbox & Series Indicator Badge */}
                        <div className="absolute top-2 left-2 z-10 flex items-center gap-1.5">
                          {canManage && selectionMode && bulkTarget && (
                            <label
                              onClick={(e) => e.stopPropagation()}
                              className="flex items-center justify-center rounded-sm bg-zinc-950/90 border border-zinc-700 p-1 shadow-xs cursor-pointer"
                            >
                              <input
                                type="checkbox"
                                checked={isBulkSelected}
                                aria-label={`Select series ${item.title}`}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  lastShiftRef.current = e.shiftKey;
                                }}
                                onChange={() => toggleItemSelection(bulkTarget, lastShiftRef.current)}
                                className="size-3.5 rounded-sm accent-purple-500 cursor-pointer"
                              />
                            </label>
                          )}
                          <span className="inline-flex items-center gap-1 bg-purple-950/90 text-purple-200 border border-purple-700/60 px-1.5 py-0.5 rounded-sm text-[10px] font-medium shadow-xs">
                            <Layers className="w-3 h-3" aria-hidden="true" />
                            Series
                          </span>
                        </div>

                        {/* Format Badges Overlay */}
                        <div className="absolute top-2 right-2 flex flex-col gap-1 items-end">
                          {item.formats.map((fmt) => (
                            <span
                              key={fmt}
                              className={`text-[9px] font-mono px-1.5 py-0.5 rounded-sm shadow-sm font-semibold ${
                                fmt === 'M4B'
                                  ? 'bg-purple-900/90 text-purple-200 border border-purple-700/60'
                                  : 'bg-blue-900/90 text-blue-200 border border-blue-700/60'
                              }`}
                            >
                              {fmt}
                            </span>
                          ))}
                        </div>
                      </div>

                      {/* Meta info */}
                      <div className="p-3 flex-1 flex flex-col justify-between">
                        <div>
                          <h3 className="text-xs font-semibold text-zinc-100 group-hover:text-blue-400 transition-colors line-clamp-1">
                            <button
                              type="button"
                              data-catalog-title
                              onClick={(e) => {
                                e.stopPropagation();
                                if (canManage && selectionMode && bulkTarget) {
                                  toggleItemSelection(bulkTarget, e.shiftKey);
                                  return;
                                }
                                setSelectedSeries({ title: item.title, id: item.seriesId });
                              }}
                              className="text-left hover:text-blue-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 rounded-sm truncate max-w-full"
                            >
                              {item.title}
                            </button>
                          </h3>
                          <p className="text-[11px] text-zinc-400 mt-0.5 truncate">
                            {item.authors.length > 0 ? (
                              item.authors.map((author, idx) => (
                                <span key={author}>
                                  {idx > 0 && ', '}
                                  <EntityLink kind="authors" name={author} />
                                </span>
                              ))
                            ) : (
                              <span>Author unknown</span>
                            )}
                          </p>
                        </div>

                        <div className="mt-2 space-y-1">
                          <span className="block text-xs text-zinc-300">
                            <span className="font-mono font-medium text-purple-300">
                              {item.books.length} {item.books.length === 1 ? 'book' : 'books'}
                            </span>
                            {item.totalBooks && item.totalBooks > 0
                              ? ` of ${item.totalBooks} in series`
                              : ' in library'}
                            {item.pendingCount > 0 && <span className="block text-zinc-400">{item.pendingCount} pending</span>}
                          </span>
                          {item.monitored && (
                            <span className="inline-flex items-center gap-1 rounded-sm border border-blue-800/60 bg-blue-950/70 px-1.5 py-0.5 text-[10px] font-medium text-blue-200">
                              <Bell className="w-2.5 h-2.5" aria-hidden="true" />
                              Monitored
                            </span>
                          )}
                        </div>
                      </div>
                    </div>
                  );
                }

                const book = item.book;
                return (
                  <div
                    key={book.id}
                    ref={isSelected ? setSelectedItem : undefined}
                    data-testid={`book-card-${book.id}`}
                    aria-selected={canManage && selectionMode ? isBulkSelected : undefined}
                    onClick={(e) => {
                      if (canManage && selectionMode && bulkTarget) {
                        toggleItemSelection(bulkTarget, e.shiftKey);
                        return;
                      }
                      onSelectBook(book);
                    }}
                    className={`group relative rounded-lg border overflow-hidden cursor-pointer transition-all duration-150 flex flex-col ${
                      isBulkSelected
                        ? 'bg-blue-950/30 border-blue-500 ring-2 ring-blue-500/80 shadow-lg shadow-blue-500/15'
                        : isSelected
                          ? 'bg-zinc-900/90 border-blue-500 ring-1 ring-blue-500 shadow-lg shadow-blue-500/10'
                          : 'bg-zinc-900/90 border-zinc-800/80 hover:border-zinc-700 hover:bg-zinc-900'
                    }`}
                  >
                    {/* Cover Image */}
                    <div className="aspect-[2/3] w-full bg-zinc-950 relative overflow-hidden">
                      {book.coverUrl ? (
                        <img
                          src={book.coverUrl}
                          alt={book.title}
                          className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-200"
                        />
                      ) : (
                        <div className="w-full h-full flex flex-col items-center justify-center text-zinc-600">
                          <BookOpen className="w-8 h-8 mb-1" />
                        </div>
                      )}

                      {/* Selection Checkbox */}
                      {canManage && selectionMode && bulkTarget && (
                        <label
                          onClick={(e) => e.stopPropagation()}
                          className="absolute top-2 left-2 z-10 flex items-center justify-center rounded-sm bg-zinc-950/90 border border-zinc-700 p-1 shadow-xs cursor-pointer"
                        >
                          <input
                            type="checkbox"
                            checked={isBulkSelected}
                            aria-label={`Select book ${book.title}`}
                            onClick={(e) => {
                              e.stopPropagation();
                              lastShiftRef.current = e.shiftKey;
                            }}
                            onChange={() => toggleItemSelection(bulkTarget, lastShiftRef.current)}
                            className="size-3.5 rounded-sm accent-blue-500 cursor-pointer"
                          />
                        </label>
                      )}

                      {/* Format Badges Overlay */}
                      <div className="absolute top-2 right-2 flex flex-col gap-1 items-end">
                        {book.formats.length === 0 && (
                          <span className="rounded-sm border border-zinc-700 bg-zinc-900 px-1.5 py-0.5 text-[10px] text-zinc-200">
                            {book.state ?? 'No files'}
                          </span>
                        )}
                        {book.formats.map((fmt) => (
                          <span
                            key={fmt}
                            className={`text-[9px] font-mono px-1.5 py-0.5 rounded-sm shadow-sm font-semibold ${
                              fmt === 'M4B'
                                ? 'bg-purple-900/90 text-purple-200 border border-purple-700/60'
                                : 'bg-blue-900/90 text-blue-200 border border-blue-700/60'
                            }`}
                          >
                            {fmt}
                          </span>
                        ))}
                      </div>

                      {/* Series Index Badge */}
                      {book.seriesIndex != null && (
                        <div className="absolute bottom-2 left-2 bg-zinc-950/80 border border-zinc-800 px-1.5 py-0.5 rounded-sm text-[10px] font-mono text-zinc-300 font-medium">
                          {`Book ${book.seriesIndex}`}
                        </div>
                      )}
                    </div>

                    {/* Meta info */}
                    <div className="p-3 flex-1 flex flex-col justify-between">
                      <div>
                        <h3 className="text-xs font-semibold text-zinc-100 group-hover:text-blue-400 transition-colors line-clamp-1">
                          <button
                            type="button"
                            data-catalog-title
                            onClick={(e) => {
                              e.stopPropagation();
                              if (canManage && selectionMode && bulkTarget) {
                                toggleItemSelection(bulkTarget, e.shiftKey);
                                return;
                              }
                              onSelectBook(book);
                            }}
                            className="text-left hover:text-blue-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 rounded-sm truncate max-w-full"
                          >
                            {book.title}
                          </button>
                        </h3>
                        <p className="text-[11px] text-zinc-400 mt-0.5 truncate">
                          <EntityLink kind="authors" name={book.authorName} />
                        </p>
                      </div>

                      <div className="space-y-1">
                        <SeriesBadge title={book.seriesTitle} id={book.seriesId} author={book.authorName} index={book.seriesIndex} total={series.find(s => s.id === book.seriesId)?.totalBooks} onSelect={setSelectedSeries} />
                        {(book.monitored || book.audiobookMonitored) && (
                          <span className="mt-1.5 inline-flex items-center gap-1 rounded-sm border border-blue-800/60 bg-blue-950/70 px-1.5 py-0.5 text-[10px] font-medium text-blue-200">
                            <Bell className="w-2.5 h-2.5" aria-hidden="true" />
                            Monitored
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            /* Table / List View */
            <div className="bg-zinc-900/80 border border-zinc-800 rounded-lg overflow-hidden">
              <table className="w-full text-left border-collapse text-xs">
                <thead>
                  <tr className="border-b border-zinc-800 bg-zinc-950/50 text-zinc-400 font-medium">
                    {canManage && selectionMode && (
                      <th className="py-2.5 pl-4 pr-1 w-9">
                        <input
                          type="checkbox"
                          checked={allVisibleSelected}
                          aria-label="Select all visible items"
                          onChange={handleToggleSelectAllVisible}
                          className="size-3.5 rounded-sm accent-blue-500 cursor-pointer align-middle"
                        />
                      </th>
                    )}
                    <th className="py-2.5 px-4">Title</th>
                    <th className="py-2.5 px-4">Author</th>
                    <th className="py-2.5 px-4">Series & Order</th>
                    <th className="py-2.5 px-4">Formats</th>
                    <th className="py-2.5 px-4 text-right">Editions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800/60">
                  {displayItems.map((item, index) => {
                    const isSelected = index === selectedIndex;
                    const bulkTarget = toBulkItem(item);
                    const isBulkSelected = Boolean(
                      bulkTarget && selectedMap.has(selectionKey(bulkTarget.kind, bulkTarget.id))
                    );
                    if (item.kind === 'series') {
                      const seriesTestId = item.seriesId || item.title;
                      return (
                        <tr
                          key={`series-${item.key}`}
                          ref={isSelected ? setSelectedItem : undefined}
                          data-testid={`series-row-${seriesTestId}`}
                          aria-selected={canManage && selectionMode ? isBulkSelected : undefined}
                          onClick={(e) => {
                            if (canManage && selectionMode && bulkTarget) {
                              toggleItemSelection(bulkTarget, e.shiftKey);
                              return;
                            }
                            setSelectedSeries({ title: item.title, id: item.seriesId });
                          }}
                          className={`cursor-pointer transition-colors duration-150 ${
                            isBulkSelected
                              ? 'bg-purple-950/45 text-white'
                              : isSelected
                                ? 'bg-purple-600/15 text-white'
                                : 'hover:bg-zinc-800/50 text-zinc-300'
                          }`}
                        >
                          {canManage && selectionMode && (
                            <td className="py-3 pl-4 pr-1" onClick={(e) => e.stopPropagation()}>
                              {bulkTarget && (
                                <input
                                  type="checkbox"
                                  checked={isBulkSelected}
                                  aria-label={`Select series ${item.title}`}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    lastShiftRef.current = e.shiftKey;
                                  }}
                                  onChange={() => toggleItemSelection(bulkTarget, lastShiftRef.current)}
                                  className="size-3.5 rounded-sm accent-purple-500 cursor-pointer align-middle"
                                />
                              )}
                            </td>
                          )}
                          <td className="py-3 px-4 font-medium text-zinc-100 flex items-center gap-2">
                            <Layers className="w-3.5 h-3.5 text-purple-400 shrink-0" aria-hidden="true" />
                            <button
                              type="button"
                              data-catalog-title
                              onClick={(e) => {
                                e.stopPropagation();
                                if (canManage && selectionMode && bulkTarget) {
                                  toggleItemSelection(bulkTarget, e.shiftKey);
                                  return;
                                }
                                setSelectedSeries({ title: item.title, id: item.seriesId });
                              }}
                              className="truncate text-left hover:text-purple-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-purple-400 rounded-sm"
                            >
                              {item.title}
                            </button>
                          </td>
                          <td className="py-3 px-4 text-zinc-300">
                            {item.authors.length > 0 ? (
                              item.authors.map((author, idx) => (
                                <span key={author}>
                                  {idx > 0 && ', '}
                                  <EntityLink kind="authors" name={author} />
                                </span>
                              ))
                            ) : (
                              <span className="text-zinc-500">Author unknown</span>
                            )}
                          </td>
                          <td className="py-3 px-4">
                            <div className="flex flex-wrap items-center gap-2">
                              <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm bg-purple-950/80 text-purple-200 border border-purple-800/60 text-[10px] font-medium">
                                <Layers className="w-3 h-3" aria-hidden="true" />
                                Series
                              </span>
                              <span className="text-zinc-300 font-mono">
                                {item.books.length} {item.books.length === 1 ? 'book' : 'books'}
                                {item.totalBooks && item.totalBooks > 0 ? ` / ${item.totalBooks}` : ''}
                              </span>
                              {item.pendingCount > 0 && <span className="text-xs text-zinc-400">{item.pendingCount} pending</span>}
                              {item.monitored && (
                                <span className="inline-flex items-center gap-1 rounded-sm border border-blue-800/60 bg-blue-950/70 px-1.5 py-0.5 text-[10px] font-medium text-blue-200">
                                  <Bell className="w-2.5 h-2.5" aria-hidden="true" />
                                  Monitored
                                </span>
                              )}
                            </div>
                          </td>
                          <td className="py-3 px-4">
                            <div className="flex gap-1">
                              {item.formats.map((fmt) => (
                                <span
                                  key={fmt}
                                  className={`text-[10px] font-mono px-1.5 py-0.5 rounded-sm ${
                                    fmt === 'M4B'
                                      ? 'bg-purple-950 text-purple-300 border border-purple-800/60'
                                      : 'bg-blue-950 text-blue-300 border border-blue-800/60'
                                  }`}
                                >
                                  {fmt}
                                </span>
                              ))}
                            </div>
                          </td>
                          <td className="py-3 px-4 text-right font-mono text-zinc-400">
                            {item.editionsCount}
                          </td>
                        </tr>
                      );
                    }

                    const book = item.book;
                    return (
                      <tr
                        key={book.id}
                        ref={isSelected ? setSelectedItem : undefined}
                        data-testid={`book-row-${book.id}`}
                        aria-selected={canManage && selectionMode ? isBulkSelected : undefined}
                        onClick={(e) => {
                          if (canManage && selectionMode && bulkTarget) {
                            toggleItemSelection(bulkTarget, e.shiftKey);
                            return;
                          }
                          onSelectBook(book);
                        }}
                        className={`cursor-pointer transition-colors duration-150 ${
                          isBulkSelected
                            ? 'bg-blue-950/45 text-white'
                            : isSelected
                              ? 'bg-blue-600/15 text-white'
                              : 'hover:bg-zinc-800/50 text-zinc-300'
                        }`}
                      >
                        {canManage && selectionMode && (
                          <td className="py-3 pl-4 pr-1" onClick={(e) => e.stopPropagation()}>
                            {bulkTarget && (
                              <input
                                type="checkbox"
                                checked={isBulkSelected}
                                aria-label={`Select book ${book.title}`}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  lastShiftRef.current = e.shiftKey;
                                }}
                                onChange={() => toggleItemSelection(bulkTarget, lastShiftRef.current)}
                                className="size-3.5 rounded-sm accent-blue-500 cursor-pointer align-middle"
                              />
                            )}
                          </td>
                        )}
                        <td className="py-3 px-4 font-medium text-zinc-100 flex items-center gap-2">
                          <BookOpen className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
                          <button
                            type="button"
                            data-catalog-title
                            onClick={(e) => {
                              e.stopPropagation();
                              if (canManage && selectionMode && bulkTarget) {
                                toggleItemSelection(bulkTarget, e.shiftKey);
                                return;
                              }
                              onSelectBook(book);
                            }}
                            className="truncate text-left hover:text-blue-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 rounded-sm"
                          >
                            {book.title}
                          </button>
                        </td>
                        <td className="py-3 px-4 text-zinc-300"><EntityLink kind="authors" name={book.authorName} /></td>
                        <td className="py-3 px-4">
                          {book.seriesTitle ? (
                            <SeriesBadge title={book.seriesTitle} id={book.seriesId} author={book.authorName} index={book.seriesIndex} total={series.find(s => s.id === book.seriesId)?.totalBooks} onSelect={setSelectedSeries} />
                          ) : (
                            <span className="text-zinc-600">—</span>
                          )}
                        </td>
                        <td className="py-3 px-4">
                          <div className="flex flex-wrap items-center gap-1">
                            {book.formats.length === 0 && (
                              <span className="text-xs text-zinc-300">{book.state ?? 'No files'}</span>
                            )}
                            {book.formats.map((fmt) => (
                              <span
                                key={fmt}
                                className={`text-[10px] font-mono px-1.5 py-0.5 rounded-sm ${
                                  fmt === 'M4B'
                                    ? 'bg-purple-950 text-purple-300 border border-purple-800/60'
                                    : 'bg-blue-950 text-blue-300 border border-blue-800/60'
                                }`}
                              >
                                {fmt}
                              </span>
                            ))}
                            {(book.monitored || book.audiobookMonitored) && (
                              <span className="inline-flex items-center gap-1 rounded-sm border border-blue-800/60 bg-blue-950/70 px-1.5 py-0.5 text-[10px] font-medium text-blue-200">
                                <Bell className="w-2.5 h-2.5" aria-hidden="true" />
                                Monitored
                              </span>
                            )}
                          </div>
                        </td>
                        <td className="py-3 px-4 text-right font-mono text-zinc-400">
                          {book.editions.length}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}

      {/* TAB CONTENT: AUTHORS */}
      {activeTab === 'authors' && (
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-4">
          {filteredAuthors.map((author) => (
            <div
              key={author.id}
              className="bg-zinc-900 border border-zinc-800 rounded-lg p-4 space-y-3 hover:border-zinc-700 transition-colors"
            >
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-full bg-zinc-800 border border-zinc-700 flex items-center justify-center text-emerald-400 font-semibold text-sm">
                  <User className="w-5 h-5" />
                </div>
                <div>
                  <h3 className="text-sm font-semibold text-zinc-100"><EntityLink kind="authors" name={author.name} /></h3>
                  <p className="text-[11px] font-mono text-zinc-400">{author.sortName}</p>
                </div>
              </div>

              {author.biography && (
                <p className="text-xs text-zinc-400 line-clamp-2 leading-relaxed">
                  {author.biography}
                </p>
              )}

              <div className="pt-2 border-t border-zinc-800 flex items-center justify-between text-xs text-zinc-400">
                <span>Books in library:</span>
                <span className="font-mono font-semibold text-zinc-200">{author.bookCount}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* TAB CONTENT: SERIES */}
      {activeTab === 'series' && (
        <div className="space-y-4">
          {filteredSeries.map((s) => {
            const seriesBooks = books.flatMap((book) => {
              const membership = book.seriesMemberships?.find((item) => item.seriesId === s.id);
              if (membership) return [{ book, index: membership.index }];
              return book.seriesId === s.id ? [{ book, index: book.seriesIndex ?? null }] : [];
            }).sort((a, b) => {
              if (a.index != null && b.index != null && a.index !== b.index) return a.index - b.index;
              if (a.index == null && b.index != null) return 1;
              if (a.index != null && b.index == null) return -1;
              return (a.book.releaseDate ?? '').localeCompare(b.book.releaseDate ?? '') ||
                a.book.title.localeCompare(b.book.title) || a.book.id.localeCompare(b.book.id);
            });
            const seriesTarget: BulkSelectionItem = {
              kind: 'series',
              id: s.id,
              title: s.title,
              bookCount: seriesBooks.length,
              monitored: s.monitored,
            };
            const isBulkSelected = selectedMap.has(selectionKey('series', s.id));

            return (
              <div
                key={s.id}
                data-testid={`series-tab-card-${s.id}`}
                aria-selected={canManage && selectionMode ? isBulkSelected : undefined}
                onClick={(e) => {
                  if (canManage && selectionMode) {
                    toggleItemSelection(seriesTarget, e.shiftKey);
                  }
                }}
                className={`rounded-lg border p-4 space-y-4 transition-all duration-150 ${
                  canManage && selectionMode ? 'cursor-pointer' : ''
                } ${
                  isBulkSelected
                    ? 'bg-purple-950/25 border-purple-500 ring-2 ring-purple-500/75'
                    : 'bg-zinc-900 border-zinc-800'
                }`}
              >
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    {canManage && selectionMode && (
                      <input
                        type="checkbox"
                        checked={isBulkSelected}
                        aria-label={`Select series ${s.title}`}
                        onClick={(e) => {
                          e.stopPropagation();
                          lastShiftRef.current = e.shiftKey;
                        }}
                        onChange={() => toggleItemSelection(seriesTarget, lastShiftRef.current)}
                        className="size-4 rounded-sm accent-purple-500 cursor-pointer"
                      />
                    )}
                    <Layers className="w-4 h-4 text-purple-400" aria-hidden="true" />
                    <h3 className="text-sm font-bold text-zinc-100">
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (canManage && selectionMode) {
                            toggleItemSelection(seriesTarget, e.shiftKey);
                            return;
                          }
                          window.location.hash = entityURL('series', s.title, s.id);
                        }}
                        className="text-purple-300 underline underline-offset-2 rounded-sm focus-visible:outline-2 focus-visible:outline-purple-400"
                      >
                        {s.title}
                      </button>
                    </h3>
                    {s.status && (
                      <span className="text-[10px] font-mono px-2 py-0.5 rounded-sm bg-zinc-800 text-zinc-400 border border-zinc-700">
                        {s.status}
                      </span>
                    )}
                    {s.monitored && (
                      <span className="inline-flex items-center gap-1 rounded-sm border border-blue-800/60 bg-blue-950/70 px-1.5 py-0.5 text-[10px] font-medium text-blue-200">
                        <Bell className="w-2.5 h-2.5" aria-hidden="true" />
                        Monitored
                      </span>
                    )}
                  </div>
                  <span className="text-xs text-zinc-400 tabular-nums">
                    {seriesBooks.length} / {s.totalBooks || '—'} books collected
                  </span>
                </div>

                {s.description && (
                  <p className="text-xs text-zinc-400">{s.description}</p>
                )}

                {/* Series Reading Order Sequence */}
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3 pt-2 border-t border-zinc-800/80">
                  {seriesBooks.map(({ book, index }) => (
                    <div
                      key={book.id}
                      onClick={(e) => {
                        if (canManage && selectionMode) return;
                        e.stopPropagation();
                        onSelectBook(book);
                      }}
                      className="cursor-pointer bg-zinc-950 p-2.5 rounded-sm border border-zinc-800 hover:border-purple-600/60 transition-colors flex items-center justify-between gap-2"
                    >
                      <div className="truncate">
                        <div className="text-xs font-medium text-zinc-200 truncate">
                          {book.title}
                        </div>
                        <div className="text-[11px] text-zinc-500 truncate">
                          <EntityLink kind="authors" name={book.authorName} />
                        </div>
                      </div>
                      <span className="font-mono text-[10px] font-bold px-1.5 py-0.5 rounded-sm bg-purple-950 text-purple-300 border border-purple-800 shrink-0">
                        {index != null ? `Book ${index}` : 'Unindexed'}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};
