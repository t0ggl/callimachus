import { useEffect, useMemo, useState } from 'react';
import { ArrowLeft, Bell, BookOpen, Calendar, Check, Download, Layers, RefreshCw, Search, User } from 'lucide-react';
import { BookDetailModal } from '../components/BookDetailModal';
import { EntityLink } from '../components/EntityLink';
import { ManualSearch } from '../components/ManualSearch';
import { SeriesBookCover, resolveCatalogBook } from '../components/SeriesDetailModal';
import type { Book } from '../types';

type EntityBook = Pick<Book, 'id' | 'title' | 'subtitle' | 'authorName' | 'seriesTitle' | 'seriesId' | 'seriesMemberships' | 'coverUrl' | 'formats' | 'releaseDate' | 'seriesIndex' | 'synopsis' | 'state' | 'monitored' | 'audiobookMonitored'>;
type Data = { monitored: boolean; audiobookMonitored?: boolean; photoUrl?: string; books: EntityBook[] };
type IndexedEntityBook = { book: EntityBook; index: number };
type AuthorBookGroup = {
  key: string;
  kind: 'series' | 'standalone';
  title: string;
  seriesId?: string;
  items: IndexedEntityBook[];
};

function compareReleaseDate(a?: string, b?: string): number {
  const ad = a?.trim() ?? '';
  const bd = b?.trim() ?? '';
  if (ad === bd) return 0;
  if (!ad) return 1;
  if (!bd) return -1;
  return ad.localeCompare(bd);
}

function compareAuthorGroupItems(a: IndexedEntityBook, b: IndexedEntityBook, isSeriesGroup: boolean): number {
  if (isSeriesGroup) {
    const aHasIdx = a.book.seriesIndex != null && Number.isFinite(a.book.seriesIndex);
    const bHasIdx = b.book.seriesIndex != null && Number.isFinite(b.book.seriesIndex);
    if (aHasIdx && bHasIdx && a.book.seriesIndex! !== b.book.seriesIndex!) {
      return a.book.seriesIndex! - b.book.seriesIndex!;
    }
    if (aHasIdx !== bHasIdx) return aHasIdx ? -1 : 1;
  }
  const dateCmp = compareReleaseDate(a.book.releaseDate, b.book.releaseDate);
  if (dateCmp !== 0) return dateCmp;
  const titleCmp = a.book.title.localeCompare(b.book.title, undefined, { sensitivity: 'base' });
  if (titleCmp !== 0) return titleCmp;
  return a.index - b.index;
}

function groupAuthorBooks(books: EntityBook[]): AuthorBookGroup[] {
  const uniqueSeriesIdByTitle = new Map<string, string | null>();
  for (const book of books) {
    const title = book.seriesTitle?.trim();
    const seriesId = book.seriesId?.trim();
    if (!title || !seriesId) continue;
    const lower = title.toLowerCase();
    if (!uniqueSeriesIdByTitle.has(lower)) {
      uniqueSeriesIdByTitle.set(lower, seriesId);
    } else if (uniqueSeriesIdByTitle.get(lower) !== seriesId) {
      uniqueSeriesIdByTitle.set(lower, null);
    }
  }

  const seriesMap = new Map<string, AuthorBookGroup>();
  const standalone: IndexedEntityBook[] = [];

  books.forEach((book, index) => {
    const title = book.seriesTitle?.trim() ?? '';
    if (!title) {
      standalone.push({ book, index });
      return;
    }
    const lower = title.toLowerCase();
    const resolvedId = book.seriesId?.trim() || uniqueSeriesIdByTitle.get(lower) || undefined;
    const key = resolvedId ? `id:${resolvedId}` : `title:${lower}`;
    const existing = seriesMap.get(key);
    if (existing) {
      existing.items.push({ book, index });
      if (!existing.seriesId && resolvedId) existing.seriesId = resolvedId;
    } else {
      seriesMap.set(key, {
        key,
        kind: 'series',
        title,
        seriesId: resolvedId,
        items: [{ book, index }],
      });
    }
  });

  const groups = Array.from(seriesMap.values())
    .map((group) => ({
      ...group,
      items: [...group.items].sort((a, b) => compareAuthorGroupItems(a, b, true)),
    }))
    .sort((a, b) => {
      const titleCmp = a.title.localeCompare(b.title, undefined, { sensitivity: 'base' });
      return titleCmp !== 0 ? titleCmp : a.key.localeCompare(b.key);
    });

  if (standalone.length > 0) {
    standalone.sort((a, b) => compareAuthorGroupItems(a, b, false));
    groups.push({
      key: 'standalone',
      kind: 'standalone',
      title: 'Standalone Books',
      items: standalone,
    });
  }

  return groups;
}

const button = 'inline-flex items-center gap-2 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 text-sm font-medium text-zinc-100 transition-colors hover:bg-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50';
const badge = 'inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 text-xs font-medium';
const neutralBadge = 'border-zinc-700 bg-zinc-900 text-zinc-300';
const availableBadge = 'border-emerald-800/60 bg-emerald-950/80 text-emerald-300';
const blueBadge = 'border-blue-800/60 bg-blue-950/80 text-blue-300';
const purpleBadge = 'border-purple-800/60 bg-purple-950/80 text-purple-300';

export function EntityView({ kind, name, seriesID, seriesAuthor, hardcoverID, canManage, canRequest, isAdmin, onMetadataApplied }: {
  kind: 'authors' | 'series';
  name: string;
  seriesID?: string;
  seriesAuthor?: string;
  hardcoverID?: string;
  canManage: boolean;
  canRequest?: boolean;
  isAdmin?: boolean;
  onMetadataApplied?: () => void;
}) {
  const allowRequest = Boolean(canRequest ?? canManage);
  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [busy, setBusy] = useState(false);
  const [rowBusyKey, setRowBusyKey] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [photoFailed, setPhotoFailed] = useState(false);
  const [bookID, setBookID] = useState<string | null>(null);
  const [detailIndex, setDetailIndex] = useState<number | null>(null);
  const [selectedBook, setSelectedBook] = useState<Book | null>(null);
  const [downloadBook, setDownloadBook] = useState<{ id: string; title: string; author: string } | null>(null);
  const [detailError, setDetailError] = useState('');
  const url = `/api/catalog/entity?kind=${kind}&name=${encodeURIComponent(name)}`
    + (seriesID ? `&id=${encodeURIComponent(seriesID)}` : '')
    + (seriesAuthor ? `&author=${encodeURIComponent(seriesAuthor)}` : '')
    + (hardcoverID ? `&hardcover_id=${encodeURIComponent(hardcoverID)}` : '');
  useEffect(() => {
    const controller = new AbortController();
    setData(null); setError(''); setToast('');
    fetch(url, { signal: controller.signal }).then(async res => {
      if (!res.ok) throw new Error('Unable to load books. Try again.');
      return res.json();
    }).then(setData).catch(e => { if (!controller.signal.aborted) setError(e.message); });
    return () => controller.abort();
  }, [url, attempt]);
  useEffect(() => {
    if (!bookID) return;
    const controller = new AbortController();
    fetch(`/api/books?id=${encodeURIComponent(bookID)}`, { signal: controller.signal })
      .then(async res => {
        if (!res.ok) throw new Error();
        const books: Book[] = await res.json();
        const book = books.find(item => item.id === bookID);
        if (!book) throw new Error();
        if (!controller.signal.aborted) {
          const entry = data?.books.find((item) => item.id === bookID);
          setSelectedBook({
            ...book,
            coverUrl: book.coverUrl || entry?.coverUrl,
            synopsis: book.synopsis || entry?.synopsis,
            subtitle: book.subtitle || entry?.subtitle,
            releaseDate: book.releaseDate || entry?.releaseDate,
            seriesId: book.seriesId ?? (book.seriesMemberships ? undefined : entry?.seriesId),
            seriesTitle: book.seriesTitle ?? (book.seriesMemberships ? undefined : entry?.seriesTitle),
            seriesIndex: book.seriesIndex ?? (book.seriesMemberships ? undefined : entry?.seriesIndex),
            state: book.state || entry?.state,
          });
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setDetailError('Unable to open book details. Select the book to try again.');
          setBookID(null);
        }
      });
    return () => controller.abort();
  }, [bookID, attempt]);
  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(''), 6000);
    return () => window.clearTimeout(timer);
  }, [toast]);
  async function act(action?: 'discover' | 'downloads', field: 'monitored' | 'audiobookMonitored' = 'monitored') {
    setBusy(true); setError('');
    try {
      const res = await fetch(url + (action ? `&action=${action}` : ''), {
        method: action ? 'POST' : 'PUT', headers: { 'Content-Type': 'application/json' },
        ...(action ? {} : { body: JSON.stringify({ [field]: !data?.[field] }) }),
      });
      if (!res.ok) throw new Error('Unable to save this action. Check your permissions and retry.');
      if (!action) {
        setData(prev => prev && ({ ...prev, [field]: !prev[field] }));
        const updated = await fetch(url);
        if (!updated.ok) throw new Error('Monitoring was saved. Refresh this view to see the updated books.');
        setData(await updated.json());
      }
      setToast(action ? `${action === 'discover' ? 'New release check' : 'Download search'} queued. Results will appear after the background check. Refresh this view to see updates.` : 'Monitoring updated.');
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  const isSeries = kind === 'series';
  const books = useMemo(() => (Array.isArray(data?.books) ? data.books : []), [data]);
  const authorGroups = useMemo(() => (isSeries ? [] : groupAuthorBooks(books)), [isSeries, books]);
  const representativeCover = books.find(book => book.coverUrl);
  const ownedCount = books.filter(book => book.state === 'In library').length;
  const heading = isSeries ? 'Reading Order' : 'Books';

  const updateBookEntry = (index: number, patch: Partial<EntityBook>) => {
    setData(prev => prev ? {
      ...prev,
      books: prev.books.map((item, idx) => (idx === index ? { ...item, ...patch } : item)),
    } : prev);
  };

  const parsedHardcoverID = hardcoverID ? Number(hardcoverID) : undefined;
  const resolveEntry = async (book: EntityBook, index: number): Promise<Book> => {
    const resolved = await resolveCatalogBook({
      bookId: book.id || undefined,
      title: book.title,
      subtitle: book.subtitle,
      author: book.authorName || (isSeries ? 'Unknown' : name),
      series: book.seriesTitle || (isSeries ? name : undefined),
      seriesId: book.seriesId || (isSeries ? seriesID : undefined),
      hardcoverSeriesId: Number.isFinite(parsedHardcoverID) ? parsedHardcoverID : undefined,
      seriesIndex: book.seriesIndex,
      releaseDate: book.releaseDate,
      synopsis: book.synopsis,
      coverUrl: book.coverUrl,
    });
    updateBookEntry(index, { id: resolved.id, seriesId: resolved.seriesId, monitored: resolved.monitored, audiobookMonitored: resolved.audiobookMonitored });
    return resolved;
  };

  const handleOpenEntityBook = (book: EntityBook, index: number) => {
    setDetailError('');
    setDetailIndex(index);
    if (book.id) {
      setBookID(book.id);
      return;
    }
    setBookID(null);
    setSelectedBook({
      id: '',
      monitored: Boolean(book.monitored),
      audiobookMonitored: book.audiobookMonitored,
      state: book.state ?? 'Not in library',
      title: book.title,
      subtitle: book.subtitle,
      authorId: '',
      authorName: book.authorName || (isSeries ? 'Unknown' : name),
      seriesId: book.seriesId || (isSeries ? seriesID : undefined),
      seriesTitle: book.seriesTitle || (isSeries ? name : undefined),
      seriesIndex: book.seriesIndex,
      releaseDate: book.releaseDate,
      synopsis: book.synopsis,
      coverUrl: book.coverUrl,
      formats: book.formats ?? [],
      editions: [],
    });
  };

  const handleDownloadBook = async (book: EntityBook, index: number, itemKey: string) => {
    setDetailError('');
    const author = book.authorName || (isSeries ? 'Unknown' : name);
    if (book.id) {
      setDownloadBook({ id: book.id, title: book.title, author });
      return;
    }
    setRowBusyKey(itemKey);
    try {
      const resolved = await resolveEntry(book, index);
      setDownloadBook({ id: resolved.id, title: resolved.title, author: resolved.authorName || author });
    } catch {
      setDetailError('Unable to open download options. Select the action to try again.');
    } finally {
      setRowBusyKey(null);
    }
  };

  const handleToggleBookMonitor = async (book: EntityBook, index: number, itemKey: string) => {
    setDetailError('');
    setRowBusyKey(itemKey);
    try {
      let id = book.id;
      if (!id) {
        const resolved = await resolveEntry(book, index);
        id = resolved.id;
      }
      const next = !book.monitored;
      const res = await fetch(`/api/books/${encodeURIComponent(id)}/monitor`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ monitored: next }),
      });
      if (!res.ok) {
        throw new Error('Unable to save monitoring state. Check permissions and retry.');
      }
      updateBookEntry(index, { id, monitored: next });
    } catch (e) {
      setDetailError((e as Error).message);
    } finally {
      setRowBusyKey(null);
    }
  };

  const renderBookItem = (book: EntityBook, index: number) => {
    const itemKey = `${book.title}-${book.seriesIndex ?? 'u'}-${index}`;
    const state = book.state ?? 'Not in library';
    const hasFiles = state === 'In library';
    const stateColor = hasFiles ? availableBadge : state === 'Not in library' || state === 'Scheduled' ? neutralBadge : blueBadge;
    const rowBusy = rowBusyKey === itemKey;
    return (
      <li key={itemKey} className="flex items-start gap-3 rounded-lg border border-zinc-800 bg-zinc-950/60 p-3.5 focus-within:border-zinc-700 hover:border-zinc-700 sm:gap-4">
        <SeriesBookCover coverUrl={book.coverUrl} title={book.title} />
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            {book.seriesIndex != null && <span className={`${badge} ${purpleBadge} font-mono`}>Book {book.seriesIndex}</span>}
            <button
              type="button"
              disabled={rowBusy}
              className="min-w-0 rounded-sm text-left text-sm font-semibold text-zinc-100 underline decoration-zinc-600 underline-offset-2 wrap-break-word hover:text-blue-300 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50"
              onClick={() => handleOpenEntityBook(book, index)}
            >
              {book.title}
            </button>
          </div>
          {book.subtitle && <p className="text-xs text-zinc-400">{book.subtitle}</p>}
          {!isSeries && book.seriesMemberships?.some((membership) => !membership.isPrimary) && (
            <p className="flex flex-wrap gap-x-2 gap-y-1 text-xs text-zinc-400">
              <span>Also in</span>
              {book.seriesMemberships.filter((membership) => !membership.isPrimary).map((membership) => (
                <EntityLink key={membership.seriesId} kind="series" name={membership.title} id={membership.seriesId} />
              ))}
            </p>
          )}
          {isSeries && (
            <p className="text-xs text-zinc-400">by <EntityLink kind="authors" name={book.authorName} /></p>
          )}
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="inline-flex items-center gap-1.5 text-xs text-zinc-400">
              <Calendar size={14} aria-hidden="true" /> {book.releaseDate || 'Release date unknown'}
            </span>
            <div className="flex flex-wrap items-center gap-1.5">
              {book.formats.length ? book.formats.map(format => (
                <span key={format} className={`${badge} ${format === 'M4B' ? purpleBadge : blueBadge} font-mono`}>{format}</span>
              )) : <span className={`${badge} ${neutralBadge}`}>No files</span>}
              <span className={`${badge} ${stateColor}`}>{hasFiles && <Check size={12} aria-hidden="true" />}{state}</span>
              {allowRequest && state === 'Not in library' && (
                <button
                  type="button"
                  disabled={rowBusy}
                  onClick={() => void handleDownloadBook(book, index, itemKey)}
                  className="inline-flex items-center gap-1 rounded-sm border border-zinc-700 bg-zinc-800 px-2 py-0.5 text-xs font-medium text-zinc-100 transition-colors hover:bg-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50"
                >
                  <Download size={12} aria-hidden="true" />
                  Download
                </button>
              )}
              {allowRequest && state === 'Scheduled' && (
                <button
                  type="button"
                  disabled={rowBusy}
                  aria-pressed={Boolean(book.monitored)}
                  onClick={() => void handleToggleBookMonitor(book, index, itemKey)}
                  className={`inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 text-xs font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50 ${
                    book.monitored
                      ? 'border-blue-700/80 bg-blue-950/80 text-blue-200 hover:bg-blue-900/70'
                      : 'border-zinc-700 bg-zinc-800 text-zinc-100 hover:bg-zinc-700'
                  }`}
                >
                  <Bell size={12} aria-hidden="true" />
                  Monitor eBook
                </button>
              )}
            </div>
          </div>
        </div>
      </li>
    );
  };

  return (
    <section className="mx-auto max-w-5xl space-y-6" aria-labelledby="entity-heading">
      <a href="#" className="inline-flex items-center gap-2 rounded-sm text-sm text-zinc-300 hover:text-zinc-100 focus-visible:outline-2 focus-visible:outline-blue-300">
        <ArrowLeft size={16} aria-hidden="true" />
        Back to library
      </a>

      <header className="rounded-xl border border-zinc-800 bg-zinc-900">
        <div className="flex items-center gap-4 p-4 sm:p-6">
          {isSeries && representativeCover ? (
            <SeriesBookCover coverUrl={representativeCover.coverUrl} title={representativeCover.title} />
          ) : (
            <div className={`flex size-14 shrink-0 items-center justify-center overflow-hidden border border-zinc-700 bg-zinc-800 text-zinc-400 ${isSeries ? 'rounded-md' : 'rounded-full'}`}>
              {isSeries ? <Layers size={24} aria-hidden="true" /> : data?.photoUrl && !photoFailed ? (
                <img src={data.photoUrl} alt="" referrerPolicy="no-referrer" onError={() => setPhotoFailed(true)} className="size-full object-cover" />
              ) : <User size={24} aria-hidden="true" />}
            </div>
          )}
          <div className="min-w-0 space-y-2">
            <h1 id="entity-heading" className="wrap-break-word text-xl font-semibold text-zinc-100">{name}</h1>
            {data && (
              <div className="flex flex-wrap gap-2">
                <span className={`${badge} ${neutralBadge}`}>{ownedCount} / {books.length} in library</span>
                <span className={`${badge} ${(data.monitored || data.audiobookMonitored) ? blueBadge : neutralBadge}`}>{(data.monitored || data.audiobookMonitored) ? 'Monitored' : 'Unmonitored'}</span>
              </div>
            )}
          </div>
        </div>
        {allowRequest && (
          <div className="space-y-3 border-t border-zinc-800 bg-zinc-950/40 p-4 sm:px-6">
            <div className="flex flex-wrap items-center gap-2">
              <label className={`${button} has-disabled:opacity-50`}>
                <input type="checkbox" checked={data?.monitored ?? false} disabled={!data || busy} onChange={() => void act()} className="size-4 accent-blue-500" />
                Monitor {isSeries ? 'series eBooks' : 'Author'}
              </label>
              {isSeries && (
                <label className={`${button} has-disabled:opacity-50`}>
                  <input type="checkbox" checked={data?.audiobookMonitored ?? false} disabled={!data || busy} onChange={() => void act(undefined, 'audiobookMonitored')} className="size-4 accent-blue-500" />
                  Monitor series audiobooks
                </label>
              )}
              {canManage && (
                <>
                  <button type="button" className={button} disabled={!data || busy} onClick={() => void act('discover')}>
                    <Calendar size={16} aria-hidden="true" /> Check New Releases
                  </button>
                  <button type="button" className={button} disabled={!data || busy} onClick={() => void act('downloads')}>
                    <Search size={16} aria-hidden="true" /> Search Downloads
                  </button>
                </>
              )}
            </div>
            <p className="max-w-[75ch] text-sm text-zinc-400">
              Monitoring checks for new releases monthly and searches for downloads daily until a book is acquired.
              {isSeries && ' Series discovery requires Hardcover metadata. Turning off a series format leaves individual book monitoring in place.'}
            </p>
          </div>
        )}
      </header>

      <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 id="entity-books" className="text-base font-semibold text-zinc-200">{heading}</h2>
          <button type="button" className={button} disabled={busy || (!data && !error)} onClick={() => { setPhotoFailed(false); setAttempt(n => n + 1); }}>
            <RefreshCw size={16} aria-hidden="true" /> Refresh books
          </button>
        </div>
        {toast && <p role="status" className="rounded-lg border border-zinc-700 bg-zinc-900 p-3 text-sm text-zinc-100">{toast}</p>}
        {error && <p role="alert" className="rounded-lg border border-red-900/60 bg-red-950/30 p-3 text-sm text-red-200">{error}</p>}
        {detailError && <p role="alert" className="rounded-lg border border-red-900/60 bg-red-950/30 p-3 text-sm text-red-200">{detailError}</p>}
        {!data && !error && (
          <div role="status" className="space-y-3 text-sm text-zinc-400">
            <p>Loading books…</p>
            <div aria-hidden="true" className="h-24 rounded-lg border border-zinc-800 bg-zinc-900 motion-safe:animate-pulse" />
          </div>
        )}
        {bookID && !selectedBook && <p role="status" className="text-sm text-zinc-400">Loading book details…</p>}
        {data && books.length === 0 && (
          <p className="rounded-lg border border-dashed border-zinc-800 p-6 text-center text-sm text-zinc-400">
            No known books yet.{canManage && ' Check new releases to discover titles.'}
          </p>
        )}
        {data && books.length > 0 && (
          isSeries ? (
            <ol aria-labelledby="entity-books" className="space-y-3">
              {books.map((book, index) => renderBookItem(book, index))}
            </ol>
          ) : (
            <div className="space-y-7 pt-1">
              {authorGroups.map((group, groupIndex) => {
                const groupHeadingId = `entity-group-${groupIndex}`;
                const groupOwned = group.items.filter(({ book }) => book.state === 'In library').length;
                const ListTag = group.kind === 'series' ? 'ol' : 'ul';
                return (
                  <section key={group.key} aria-labelledby={groupHeadingId} className="space-y-3">
                    <div className="flex flex-wrap items-center justify-between gap-2 border-b border-zinc-800/80 pb-2">
                      <div className="flex min-w-0 items-center gap-2">
                        {group.kind === 'series' ? (
                          <Layers size={15} aria-hidden="true" className="shrink-0 text-purple-300" />
                        ) : (
                          <BookOpen size={15} aria-hidden="true" className="shrink-0 text-zinc-400" />
                        )}
                        <h3 id={groupHeadingId} className="min-w-0 wrap-break-word text-sm font-semibold text-zinc-100">
                          {group.kind === 'series' ? (
                            <EntityLink kind="series" name={group.title} id={group.seriesId} />
                          ) : (
                            group.title
                          )}
                        </h3>
                      </div>
                      <span className="shrink-0 text-xs tabular-nums text-zinc-400">
                        {groupOwned} of {group.items.length} in library
                      </span>
                    </div>
                    <ListTag aria-labelledby={groupHeadingId} className="space-y-3">
                      {group.items.map(({ book, index }) => renderBookItem(book, index))}
                    </ListTag>
                  </section>
                );
              })}
            </div>
          )
        )}
      </div>
      {selectedBook && <BookDetailModal book={selectedBook} isAdmin={isAdmin} canManage={canManage} canRequest={allowRequest}
        onBookUpdated={(updated) => {
          setSelectedBook(updated);
          setData(prev => prev ? {
            ...prev,
            books: prev.books.map((item, idx) =>
              (detailIndex !== null ? idx === detailIndex : item.id === updated.id)
                ? {
                    ...item,
                    id: updated.id,
                    title: updated.title,
                    subtitle: updated.subtitle,
                    releaseDate: updated.releaseDate,
                    synopsis: updated.synopsis,
                    seriesId: isSeries ? item.seriesId : updated.seriesId,
                    seriesTitle: isSeries ? item.seriesTitle : updated.seriesTitle,
                    seriesIndex: isSeries
                      ? (updated.seriesMemberships?.find((membership) => membership.seriesId === item.seriesId)?.index ?? item.seriesIndex)
                      : updated.seriesIndex,
                    seriesMemberships: updated.seriesMemberships,
                    monitored: updated.monitored,
                    audiobookMonitored: updated.audiobookMonitored,
                    coverUrl: updated.coverUrl ?? item.coverUrl,
                  }
                : item
            ),
          } : prev);
        }}
        onBookDeleted={(deletedId) => {
          setSelectedBook(null);
          setBookID(null);
          setDetailIndex(null);
          setData(prev => prev ? {
            ...prev,
            books: prev.books.filter((item) => item.id !== deletedId),
          } : prev);
          setAttempt((value) => value + 1);
          onMetadataApplied?.();
        }}
        onMetadataApplied={() => { setAttempt((value) => value + 1); onMetadataApplied?.(); }}
        onClose={() => { setSelectedBook(null); setBookID(null); setDetailIndex(null); }} />}
      {downloadBook && (
        <ManualSearch
          key={downloadBook.id}
          book={downloadBook}
          onClose={() => setDownloadBook(null)}
        />
      )}
    </section>
  );
}
