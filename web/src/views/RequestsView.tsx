import React, { useState } from 'react';
import { Check } from 'lucide-react';
import { BookRequest, RequestType } from '../types';
import { ManualSearch } from '../components/ManualSearch';
type Match = { title: string; authors: string[]; year: number; library_book_id?: string };
interface Props {
  requests: BookRequest[];
  onOpenBook?: (id: string) => void;
  onSubmitRequest: (
    title: string,
    author: string,
    type: RequestType,
    year?: number,
  ) => Promise<void>;
}
export function RequestsView({ requests, onSubmitRequest, onOpenBook }: Props) {
  const [query, setQuery] = useState('');
  const [format, setFormat] = useState<RequestType>('both');
  const [matches, setMatches] = useState<Match[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [searched, setSearched] = useState(false);
  const [manual, setManual] = useState<string | null>(null);
  const search = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    setMatches([]);
    setMessage('');
    try {
      const res = await fetch(
        `/api/discover?q=${encodeURIComponent(query.trim())}`,
      );
      if (!res.ok) throw new Error('Search unavailable. Try again shortly.');
      setMatches(await res.json());
      setSearched(true);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const add = async (m: Match) => {
    setBusy(true);
    setError('');
    try {
      await onSubmitRequest(m.title, m.authors.join(', '), format, m.year);
      setMessage(`Added ${m.title}. Acquisition will run in the background.`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="space-y-6 text-zinc-100">
      <div className="border-b border-zinc-800 pb-4">
        <h1 className="text-xl font-bold">Find a book</h1>
        <p className="text-sm text-zinc-300 mt-1">
          Search by title, author, or keywords. Confirm a match to add it.
        </p>
      </div>
      <form onSubmit={search} className="flex flex-wrap gap-3 items-end">
        <label className="flex-1 min-w-48 text-sm">
          Book search
          <input
            required
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Title, author, or keywords"
            className="block w-full mt-1 p-2 bg-zinc-950 border border-zinc-700 rounded-md"
          />
        </label>
        <label className="text-sm">
          Format
          <select
            value={format}
            onChange={(e) => setFormat(e.target.value as RequestType)}
            className="block mt-1 p-2 bg-zinc-950 border border-zinc-700 rounded-md"
          >
            <option value="both">Both formats</option>
            <option value="ebook">eBook</option>
            <option value="audiobook">Audiobook</option>
          </select>
        </label>
        <button
          disabled={busy}
          className="px-4 py-2 bg-blue-600 rounded-md disabled:opacity-50"
        >
          {busy ? 'Working…' : 'Search'}
        </button>
      </form>
      {error && (
        <p role="alert" className="text-red-300">
          {error}
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
            <div>
              <strong>{m.title}</strong>
              <p className="text-sm text-zinc-300">
                {m.authors.join(', ')} {m.year > 0 && `(${m.year})`}
              </p>
            </div>
            {m.library_book_id ? <button onClick={() => onOpenBook?.(m.library_book_id!)} className="flex items-center gap-2 text-emerald-300">
              <Check aria-hidden="true" className="w-4 h-4" /> In Library
              <span className="sr-only">: {m.title}</span>
            </button> : <button
              disabled={busy}
              onClick={() => void add(m)}
              className="px-4 py-2 bg-blue-600 rounded-md disabled:opacity-50"
              aria-label={`Add ${m.title}`}
            >
              Add
            </button>}
          </li>
        ))}
      </ul>
      {searched && !matches.length && !busy && (
        <p>No matches. Try another title or author.</p>
      )}
      <h2 className="text-lg font-semibold">Acquisitions</h2>
      {!requests.length && (
        <p className="text-zinc-300">Books you add will appear here.</p>
      )}
      <ul className="divide-y divide-zinc-800">
        {requests.map((r) => (
          <li
            key={r.id}
            className="flex flex-wrap justify-between items-center gap-4 py-3"
          >
            <div>
              <strong>{r.title}</strong>
              <p className="text-sm text-zinc-300">
                {r.author} · {r.requestType} · {r.status}
              </p>
            </div>
            <button className="text-blue-300" onClick={() => setManual(r.id)}>
              Manual Search<span className="sr-only"> for {r.title}</span>
            </button>
          </li>
        ))}
      </ul>
      {manual && (
        <ManualSearch requestId={manual} onClose={() => setManual(null)} />
      )}
    </div>
  );
}
