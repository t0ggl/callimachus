import { useEffect, useState } from 'react';
import { ArrowLeft, Calendar, Check, Layers, RefreshCw, Search, User } from 'lucide-react';
import { BookDetailModal } from '../components/BookDetailModal';
import { EntityLink } from '../components/EntityLink';
import { SeriesBookCover } from '../components/SeriesDetailModal';
import type { Book } from '../types';

type EntityBook = Pick<Book, 'id' | 'title' | 'subtitle' | 'authorName' | 'seriesTitle' | 'coverUrl' | 'formats' | 'releaseDate' | 'seriesIndex' | 'state'>;
type Data = { monitored: boolean; photoUrl?: string; books: EntityBook[] };

const button = 'inline-flex items-center gap-2 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 text-sm font-medium text-zinc-100 transition-colors hover:bg-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50';
const badge = 'inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 text-xs font-medium';
const neutralBadge = 'border-zinc-700 bg-zinc-900 text-zinc-300';
const availableBadge = 'border-emerald-800/60 bg-emerald-950/80 text-emerald-300';
const blueBadge = 'border-blue-800/60 bg-blue-950/80 text-blue-300';
const purpleBadge = 'border-purple-800/60 bg-purple-950/80 text-purple-300';

export function EntityView({ kind, name, canManage }: { kind: 'authors' | 'series'; name: string; canManage: boolean }) {
  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [photoFailed, setPhotoFailed] = useState(false);
  const [bookID, setBookID] = useState<string | null>(null);
  const [selectedBook, setSelectedBook] = useState<Book | null>(null);
  const [detailError, setDetailError] = useState('');
  const url = `/api/catalog/entity?kind=${kind}&name=${encodeURIComponent(name)}`;
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
        if (!controller.signal.aborted) setSelectedBook(book);
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setDetailError('Unable to open book details. Select the book to try again.');
          setBookID(null);
        }
      });
    return () => controller.abort();
  }, [bookID]);
  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(''), 6000);
    return () => window.clearTimeout(timer);
  }, [toast]);
  async function act(action?: 'discover' | 'downloads') {
    setBusy(true); setError('');
    try {
      const res = await fetch(url + (action ? `&action=${action}` : ''), {
        method: action ? 'POST' : 'PUT', headers: { 'Content-Type': 'application/json' },
        ...(action ? {} : { body: JSON.stringify({ monitored: !data?.monitored }) }),
      });
      if (!res.ok) throw new Error('Unable to save this action. Check your permissions and retry.');
      if (!action) setData(prev => prev && ({ ...prev, monitored: !prev.monitored }));
      setToast(action ? `${action === 'discover' ? 'New release check' : 'Download search'} queued. Results will appear after the background check. Refresh this view to see updates.` : 'Monitoring updated.');
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  const isSeries = kind === 'series';
  const representativeCover = data?.books.find(book => book.coverUrl);
  const ownedCount = data?.books.filter(book => book.state === 'In library').length ?? 0;
  const heading = isSeries ? 'Reading Order' : 'Books';

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
                <span className={`${badge} ${neutralBadge}`}>{ownedCount} / {data.books.length} in library</span>
                <span className={`${badge} ${data.monitored ? blueBadge : neutralBadge}`}>{data.monitored ? 'Monitored' : 'Unmonitored'}</span>
              </div>
            )}
          </div>
        </div>
        {canManage && (
          <div className="space-y-3 border-t border-zinc-800 bg-zinc-950/40 p-4 sm:px-6">
            <div className="flex flex-wrap items-center gap-2">
              <label className={`${button} has-disabled:opacity-50`}>
                <input type="checkbox" checked={data?.monitored ?? false} disabled={!data || busy} onChange={() => void act()} className="size-4 accent-blue-500" />
                Monitor {isSeries ? 'Series' : 'Author'}
              </label>
              <button type="button" className={button} disabled={!data || busy} onClick={() => void act('discover')}>
                <Calendar size={16} aria-hidden="true" /> Check New Releases
              </button>
              <button type="button" className={button} disabled={!data || busy} onClick={() => void act('downloads')}>
                <Search size={16} aria-hidden="true" /> Search Downloads
              </button>
            </div>
            <p className="max-w-[75ch] text-sm text-zinc-400">
              Monitoring checks for new releases monthly and searches for downloads daily until a book is acquired.
              {isSeries && ' Series discovery requires Hardcover metadata in Settings.'}
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
        {data?.books.length === 0 && (
          <p className="rounded-lg border border-dashed border-zinc-800 p-6 text-center text-sm text-zinc-400">
            No known books yet.{canManage && ' Check new releases to discover titles.'}
          </p>
        )}
        {data && data.books.length > 0 && (
          <ol aria-labelledby="entity-books" className="space-y-3">
            {data.books.map(book => {
              const state = book.state ?? 'Not in library';
              const hasFiles = state === 'In library';
              const stateColor = hasFiles ? availableBadge : state === 'Not in library' || state === 'Scheduled' ? neutralBadge : blueBadge;
              return (
                <li key={book.id} className="flex items-start gap-3 rounded-lg border border-zinc-800 bg-zinc-950/60 p-3.5 focus-within:border-zinc-700 hover:border-zinc-700 sm:gap-4">
                  <SeriesBookCover coverUrl={book.coverUrl} title={book.title} />
                  <div className="min-w-0 flex-1 space-y-2">
                    <div className="flex flex-wrap items-center gap-2">
                      {book.seriesIndex != null && <span className={`${badge} ${purpleBadge} font-mono`}>Book {book.seriesIndex}</span>}
                      <button
                        type="button"
                        className="min-w-0 rounded-sm text-left text-sm font-semibold text-zinc-100 underline decoration-zinc-600 underline-offset-2 wrap-break-word hover:text-blue-300 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300"
                        onClick={() => { setDetailError(''); setBookID(book.id); }}
                      >
                        {book.title}
                      </button>
                    </div>
                    {book.subtitle && <p className="text-xs text-zinc-400">{book.subtitle}</p>}
                    {isSeries ? (
                      <p className="text-xs text-zinc-400">by <EntityLink kind="authors" name={book.authorName} /></p>
                    ) : book.seriesTitle && (
                      <p className="text-xs text-zinc-400"><EntityLink kind="series" name={book.seriesTitle} /></p>
                    )}
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="inline-flex items-center gap-1.5 text-xs text-zinc-400">
                        <Calendar size={14} aria-hidden="true" /> {book.releaseDate || 'Release date unknown'}
                      </span>
                      <div className="flex flex-wrap gap-1.5">
                        {book.formats.length ? book.formats.map(format => (
                          <span key={format} className={`${badge} ${format === 'M4B' ? purpleBadge : blueBadge} font-mono`}>{format}</span>
                        )) : <span className={`${badge} ${neutralBadge}`}>No files</span>}
                        <span className={`${badge} ${stateColor}`}>{hasFiles && <Check size={12} aria-hidden="true" />}{state}</span>
                      </div>
                    </div>
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </div>
      {selectedBook && <BookDetailModal book={selectedBook} onClose={() => { setSelectedBook(null); setBookID(null); }} />}
    </section>
  );
}
