import { useEffect, useId, useRef, useState } from 'react';
import { RefreshCw, X } from 'lucide-react';

type Format = 'epub' | 'm4b';
type RequestFormat = Format | 'both';
type RequestBook = { id: string; title: string; author: string };
type Acquisition = { id: number | string; book_id?: string; format: RequestFormat };
const formats = [
  { id: 'epub', label: 'eBook' },
  { id: 'm4b', label: 'Audiobook' },
] as const;
const focusStyle = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300';

async function findOrCreateRequest(book: RequestBook, format: Format): Promise<Acquisition> {
  const find = async () => {
    const res = await fetch('/api/requests');
    if (!res.ok) throw new Error('Unable to load acquisitions.');
    const requests: Acquisition[] = await res.json();
    return requests.find(r => r.book_id === book.id && r.format === format)
      ?? requests.find(r => r.book_id === book.id && r.format === 'both');
  };
  const existing = await find();
  if (existing) return existing;

  const res = await fetch('/api/requests', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      book_id: book.id, title: book.title, author: book.author, format, manual_only: true,
    }),
  });
  if (res.status === 409) {
    const concurrent = await find();
    if (concurrent) return concurrent;
  }
  if (!res.ok) throw new Error((await res.json()).error || 'Unable to open download options.');
  return res.json();
}

type Candidate = {
  key: string;
  release: { title: string; size: number; seeders: number };
  format: string;
  score: number;
  penalty: number;
  breakdown: Record<string, number>;
};
export function ManualSearch({
  requestId,
  requestFormat = 'epub',
  book,
  onClose,
}: ({ requestId: string; book?: RequestBook } | { requestId?: never; book: RequestBook }) & {
  requestFormat?: RequestFormat;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const id = useId();
  const [format, setFormat] = useState<Format>(requestFormat === 'm4b' ? 'm4b' : 'epub');
  const pending = useRef<Partial<Record<Format, Promise<Acquisition>>>>({});
  const resolveRequest = () => {
    if (requestId && (format === requestFormat || requestFormat === 'both')) {
      return Promise.resolve({ id: requestId, format: requestFormat });
    }
    if (!book) throw new Error('Book details are unavailable.');
    // Reuse an in-flight creation if the user returns to this tab before it finishes.
    return pending.current[format] ??= findOrCreateRequest(book, format).finally(() => {
      delete pending.current[format];
    });
  };
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, []);

  return (
    <dialog
      ref={dialog}
      onCancel={onClose}
      aria-labelledby={`${id}-heading`}
      className="m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-5xl rounded-xl border border-zinc-700 bg-zinc-900 text-zinc-100 backdrop:bg-black/75"
    >
      <header className="flex items-center justify-between gap-4 border-b border-zinc-800 bg-zinc-950/40 px-4 py-3 sm:px-6">
        <h2 id={`${id}-heading`} className="text-lg font-semibold">Download Options</h2>
        <button type="button" onClick={onClose} aria-label="Close"
          className={`rounded-md p-2 text-zinc-300 hover:bg-zinc-800 hover:text-white ${focusStyle}`}>
          <X size={18} aria-hidden="true" />
        </button>
      </header>
      <div className="px-4 pb-5 sm:px-6">
        <div role="tablist" aria-label="Download format" className="flex gap-2 border-b border-zinc-800">
          {formats.map(({ id: tab, label }) => (
            <button key={tab} type="button" role="tab" id={`${id}-${tab}`}
              aria-controls={`${id}-${tab}-panel`} aria-selected={format === tab}
              tabIndex={format === tab ? 0 : -1}
              disabled={!book && requestFormat !== 'both' && tab !== requestFormat}
              onClick={() => setFormat(tab)}
              onKeyDown={event => {
                const enabled = Array.from(event.currentTarget.parentElement!.querySelectorAll<HTMLButtonElement>('[role="tab"]:enabled'));
                const other = enabled.find(button => button !== event.currentTarget);
                const target = event.key === 'Home' ? enabled[0] : event.key === 'End' ? enabled.at(-1)
                  : ['ArrowLeft', 'ArrowRight'].includes(event.key) ? other : undefined;
                if (!target) return;
                event.preventDefault();
                target.focus();
                target.click();
              }}
              className={`border-b-2 border-transparent px-3 py-3 text-sm font-medium text-zinc-300 enabled:hover:bg-zinc-800 enabled:hover:text-white disabled:opacity-50 aria-selected:border-blue-400 aria-selected:text-blue-300 ${focusStyle}`}>
              {label}
            </button>
          ))}
        </div>
        {formats.map(({ id: tab }) => (
          <div key={tab} id={`${id}-${tab}-panel`} role="tabpanel"
            aria-labelledby={`${id}-${tab}`} hidden={format !== tab}>
            {format === tab && <CandidateList format={format} resolveRequest={resolveRequest} />}
          </div>
        ))}
      </div>
    </dialog>
  );
}

function CandidateList({ format, resolveRequest }: {
  format: Format;
  resolveRequest: () => Promise<Acquisition>;
}) {
  const requestRef = useRef<Acquisition>(null);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  const load = async (action = '', key?: string) => {
    setBusy(true);
    setError('');
    try {
      const request = requestRef.current ??= await resolveRequest();
      const res = await fetch(
        `/api/requests/${request.id}/${action || 'candidates'}`,
        action
          ? {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ key }),
            }
          : undefined,
      );
      if (!res.ok)
        throw new Error(
          (await res.json()).error || 'Unable to load candidates. Try Refresh.',
        );
      const results: Candidate[] = await res.json();
      setCandidates(request.format === 'both' ? results.filter(candidate => {
        const audio = candidate.format === 'm4b' || candidate.format === 'mp3';
        return audio === (format === 'm4b');
      }) : results);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    void load();
  }, []);
  return (
    <>
      <div className="my-4 flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-prose text-sm text-zinc-300">
          Cached releases are ranked by preference. You can retry failed releases here.
        </p>
        <button
          type="button"
          disabled={busy}
          onClick={() => void load('refresh')}
          className={`inline-flex items-center gap-2 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 text-sm font-medium hover:bg-zinc-700 disabled:opacity-50 ${focusStyle}`}
        >
          <RefreshCw size={15} aria-hidden="true" />
          Refresh
        </button>
      </div>
      {busy && <p role="status" className="my-3 text-sm text-zinc-300">Loading download options…</p>}
      {error && (
        <p role="alert" className="text-red-300 mt-3">
          {error}
        </p>
      )}
      {candidates.length > 0 && <div className="overflow-x-auto rounded-md border border-zinc-800">
        <table className="w-full text-left text-sm">
          <thead className="bg-zinc-950/60 text-xs text-zinc-300">
            <tr>
              {[
                'Release',
                'Format',
                'Size',
                'Seeders',
                'Score breakdown',
                'Total',
                'Action',
              ].map((h) => (
                <th key={h} scope="col" className="px-3 py-2.5">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {candidates.map((c) => (
              <tr key={c.key} className="border-t border-zinc-800 hover:bg-zinc-800/50">
                <td className="min-w-48 p-2 font-medium wrap-anywhere">{c.release.title}</td>
                <td className="p-2">{c.format || 'Unknown'}</td>
                <td className="whitespace-nowrap p-2 tabular-nums">
                  {(c.release.size / 1048576).toFixed(1)} MB
                </td>
                <td className="p-2 tabular-nums">{c.release.seeders || 0}</td>
                <td className="p-2">
                  {Object.entries(c.breakdown)
                    .map(([k, v]) => `${k}: ${v > 0 ? '+' : ''}${v}`)
                    .join(', ')}
                  ; failure: {c.penalty}
                </td>
                <td className="p-2 tabular-nums">{c.score}</td>
                <td className="p-2">
                  <button
                    disabled={busy}
                    type="button"
                    className={`rounded px-2 py-1.5 font-medium text-blue-300 hover:bg-zinc-800 disabled:opacity-50 ${focusStyle}`}
                    onClick={() => void load('download', c.key)}
                  >
                    Download
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>}
      {!busy && !error && !candidates.length && (
        <p className="mt-4 text-sm text-zinc-300">
          No cached candidates. Refresh to search enabled sources.
        </p>
      )}
    </>
  );
}
