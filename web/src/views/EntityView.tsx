import { useEffect, useState } from 'react';
import type { BookState } from '../types';

type Data = { monitored: boolean; books: { id: string; title: string; releaseDate: string; seriesIndex?: number; state: BookState }[] };
export function EntityView({ kind, name, canManage, onOpenBook }: { kind: 'authors' | 'series'; name: string; canManage: boolean; onOpenBook: (id: string) => void }) {
  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);
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
  const button = 'px-3 py-2 rounded-md border border-zinc-700 text-sm text-zinc-100 hover:bg-zinc-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-300 disabled:opacity-50';
  return <section className="space-y-6">
    <a href="#" className="text-blue-300 underline">Back to library</a>
    <h1 className="text-xl font-semibold text-zinc-100">{name}</h1>
    <p className="text-sm text-zinc-400">Monitor new announcements monthly. Released titles are checked for downloads daily until acquired. Series discovery requires Hardcover metadata in Settings.</p>
    {canManage && <div className="flex flex-wrap items-center gap-3">
      <label className="flex items-center gap-2 text-sm text-zinc-100"><input type="checkbox" checked={data?.monitored || false} disabled={!data || busy} onChange={() => void act()} className="accent-blue-500" />Monitor {kind === 'authors' ? 'Author' : 'Series'}</label>
      <button className={button} disabled={!data || busy} onClick={() => void act('discover')}>Check New Releases</button>
      <button className={button} disabled={!data || busy} onClick={() => void act('downloads')}>Search Downloads</button>
    </div>}
    <button className={button} disabled={busy} onClick={() => setAttempt(n => n + 1)}>Refresh books</button>
    {toast && <p role="status" className="fixed bottom-6 right-6 z-50 max-w-sm rounded-md border border-zinc-600 bg-zinc-800 p-4 text-sm text-zinc-100">{toast}</p>}
    {error && <p role="alert" className="text-red-300">{error}</p>}
    {!data && !error && <p role="status" className="text-zinc-400">Loading books…</p>}
    {data?.books.length === 0 && <p className="text-zinc-400">No known books yet. Check new releases to discover titles.</p>}
    <ol className="divide-y divide-zinc-800">{data?.books.map(book => <li key={book.id} className="py-4 flex flex-wrap justify-between gap-3">
      <div><button className="text-blue-300 underline text-left" onClick={() => onOpenBook(book.id)}>{book.seriesIndex != null && kind === 'series' ? `Book ${book.seriesIndex}: ` : ''}{book.title}</button><p className="text-sm text-zinc-400 mt-1">{book.releaseDate || 'Release date unknown'}</p></div>
      <span className="text-sm text-zinc-300">{book.state}</span>
    </li>)}</ol>
  </section>;
}
