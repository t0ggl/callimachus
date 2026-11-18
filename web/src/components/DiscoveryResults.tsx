import { EntityLink } from './EntityLink';
import { useState, useEffect } from 'react';
import { Check } from 'lucide-react';
import { RequestType } from '../types';
import { SeriesBadge } from './SeriesBadge';
import { SeriesDetailModal } from './SeriesDetailModal';
type Match = { cover_url?: string; series?: string; series_index?: number; series_total?: number; title: string; authors: string[]; year: number; library_book_id?: string };
interface Props {
  query: string;
  onOpenBook?: (id: string) => void;
  onSubmitRequest: (
    title: string,
    author: string,
    type: RequestType,
    year?: number,
  ) => Promise<void>;
}
export function DiscoveryResults({ query, onSubmitRequest, onOpenBook }: Props) {
  const [selectedSeries, setSelectedSeries] = useState<string | null>(null);
  const [format, setFormat] = useState<RequestType>('both');
  const [matches, setMatches] = useState<Match[]>([]);
  const [retry, setRetry] = useState(0);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [searched, setSearched] = useState(false);
  const [added, setAdded] = useState<string[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    let current = true;
    setMatches([]);
    setError('');
    setMessage('');
    setSearched(false);
    setBusy(Boolean(query.trim()));
    if (!query.trim()) return;
    const timer = setTimeout(async () => {
      try {
        const res = await fetch(`/api/discover?q=${encodeURIComponent(query.trim())}`, { signal: controller.signal });
        if (!res.ok) throw new Error('Search unavailable. Try again shortly.');
        const matches = await res.json();
        if (current) {
          setMatches(matches);
          setSearched(true);
        }
      } catch (e) {
        if (current) setError((e as Error).message);
      } finally {
        if (current) setBusy(false);
      }
    }, 250);
    return () => {
      current = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [query, retry]);
  const add = async (m: Match) => {
    setAdding(true);
    setError('');
    try {
      await onSubmitRequest(m.title, m.authors.join(', '), format, m.year);
      setAdded((prev) => [...prev, `${m.title}\0${m.authors.join(', ')}`]);
      setMessage(`Added ${m.title}. Acquisition will run in the background.`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(false);
    }
  };
  return (
    <div className="space-y-6 text-zinc-100">
      {selectedSeries && <SeriesDetailModal title={selectedSeries} onClose={() => setSelectedSeries(null)} />}
      <div className="flex items-center justify-between gap-3">
        <h2 className="font-semibold">Find books to add</h2>
        <label className="text-sm">
          Format
          <select
            value={format}
            onChange={(e) => setFormat(e.target.value as RequestType)}
            className="block mt-1 p-2 bg-zinc-950 border border-zinc-700 rounded-md"
          >
            <option value="both">Both</option>
            <option value="ebook">eBook</option>
            <option value="audiobook">Audiobook</option>
          </select>
        </label>
      </div>
      {(busy || adding) && <p role="status" className="text-sm text-zinc-300">Working…</p>}
      {error && (
        <p role="alert" className="text-red-300">
          {error} <button className="underline" onClick={() => setRetry((value) => value + 1)}>Retry search</button>
        </p>
      )}
      {message && (
        <p role="status" className="text-emerald-300">
          {message}
        </p>
      )}
      <ul className="divide-y divide-zinc-800">
        {matches.map((m, i) => (
          <li key={i} className="flex justify-between gap-4 py-3">
            <div className="flex gap-3 min-w-0">
              {m.cover_url ? <img src={m.cover_url} alt={`Cover of ${m.title}`} className="w-12 h-18 object-contain shrink-0" /> : <div aria-label="No cover available" className="w-12 h-18 bg-zinc-800 shrink-0" />}
              <div className="min-w-0 break-words"><strong>{m.title}</strong>
              <SeriesBadge title={m.series} index={m.series_index} total={m.series_total} onSelect={setSelectedSeries} />
              <p className="text-sm text-zinc-300">
                {m.authors.map((name, i) => <span key={name}>{i > 0 && ', '}<EntityLink kind="authors" name={name} /></span>)} {m.year > 0 && `(${m.year})`}
              </p>
              </div>
            </div>
            {m.library_book_id ? <button onClick={() => onOpenBook?.(m.library_book_id!)} className="flex items-center gap-2 text-emerald-300">
              <Check aria-hidden="true" className="w-4 h-4" /> In Library
              <span className="sr-only">: {m.title}</span>
            </button> : <button
              disabled={busy || adding || added.includes(`${m.title}\0${m.authors.join(', ')}`)}
              onClick={() => void add(m)}
              className="px-4 py-2 bg-blue-600 rounded-md disabled:opacity-50"
              aria-label={`${added.includes(`${m.title}\0${m.authors.join(', ')}`) ? 'Added' : 'Add'} ${m.title}`}
            >
              {added.includes(`${m.title}\0${m.authors.join(', ')}`) ? 'Added' : 'Add'}
            </button>}
          </li>
        ))}
      </ul>
      {searched && !matches.length && !busy && (
        <p>No matches. Try another title or author.</p>
      )}

    </div>
  );
}
