import { useEffect, useRef, useState } from 'react';

type Entry = { title: string; series_index?: number; publish_date?: string; library_book_id?: string; monitored: boolean };
export function SeriesDetailModal({ title, onClose }: { title: string; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [data, setData] = useState<{ books: Entry[]; complete: boolean } | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog.current?.showModal();
    return () => { previous?.focus(); };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setData(null); setError('');
    fetch(`/api/series/books?title=${encodeURIComponent(title)}`, { signal: controller.signal })
      .then(async res => { if (!res.ok) throw new Error('Unable to load reading order. Try again.'); return res.json(); })
      .then(setData).catch(e => { if (!controller.signal.aborted) setError(e.message); });
    return () => controller.abort();
  }, [title, attempt]);
  return <dialog ref={dialog} onCancel={onClose} aria-labelledby="series-heading" className="w-[calc(100%-2rem)] max-w-2xl max-h-[85vh] overflow-y-auto rounded-lg border border-zinc-700 bg-zinc-900 p-6 text-zinc-100 backdrop:bg-black/70">
    <div className="flex justify-between items-start gap-4"><h2 id="series-heading" className="text-xl font-semibold">{title}</h2><button autoFocus onClick={onClose} className="text-blue-300 underline">Close</button></div>
    {!data && !error && <p role="status" className="mt-4">Loading reading order…</p>}
    {error && <div role="alert" className="mt-4"><p>{error}</p><button className="text-blue-300 underline" onClick={() => setAttempt(n => n + 1)}>Retry</button></div>}
    {data && <>
      {!data.complete && <p className="mt-4 text-sm text-zinc-300">Showing known catalog books. Full reading order is unavailable; configure Hardcover metadata or retry later.</p>}
      {data.books.length === 0 && <p className="mt-4">No books found for this series.</p>}
      <ol className="mt-4 divide-y divide-zinc-700">{data.books.map((book, i) => <li key={i} className="py-3 flex flex-wrap justify-between gap-2">
        <div><p>{book.series_index != null ? `Book ${book.series_index}: ` : ''}{book.title}</p><p className="text-sm text-zinc-300">{book.publish_date || 'Release date unknown'}</p></div>
        <span className="text-sm text-zinc-300">{[book.library_book_id ? 'In library' : 'Not in library', book.monitored ? 'Monitored' : 'Not monitored'].join(' · ')}</span>
      </li>)}</ol>
    </>}
  </dialog>;
}
