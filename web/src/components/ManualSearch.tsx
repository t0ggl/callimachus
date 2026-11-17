import { useEffect, useRef, useState } from 'react';

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
  onClose,
}: {
  requestId: string;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const load = async (action = '', key?: string) => {
    setBusy(true);
    setError('');
    try {
      const res = await fetch(
        `/api/requests/${requestId}/${action || 'candidates'}`,
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
      setCandidates(await res.json());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    dialog.current?.showModal();
    void load();
  }, [requestId]);
  return (
    <dialog
      ref={dialog}
      onCancel={onClose}
      aria-label="Manual Search"
      className="w-[min(60rem,95vw)] max-h-[85vh] bg-zinc-900 text-zinc-100 rounded-xl border border-zinc-700 p-6 backdrop:bg-black/75"
    >
      <div className="flex justify-between gap-4 mb-4">
        <h2 className="text-lg font-semibold">Manual Search</h2>
        <button onClick={onClose}>Close</button>
      </div>
      <p className="text-sm text-zinc-300 mb-4">
        Cached releases, ranked by preference. Failed releases are excluded from
        automatic downloads; you can retry them here.
      </p>
      <button
        disabled={busy}
        onClick={() => void load('refresh')}
        className="px-4 py-2 bg-blue-600 rounded disabled:opacity-50"
      >
        {busy ? 'Working…' : 'Refresh'}
      </button>
      {error && (
        <p role="alert" className="text-red-300 mt-3">
          {error}
        </p>
      )}
      <div className="overflow-x-auto mt-4">
        <table className="w-full text-sm text-left">
          <thead>
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
                <th key={h} className="p-2">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {candidates.map((c) => (
              <tr key={c.key} className="border-t border-zinc-700">
                <td className="p-2">{c.release.title}</td>
                <td className="p-2">{c.format || 'Unknown'}</td>
                <td className="p-2">
                  {(c.release.size / 1048576).toFixed(1)} MB
                </td>
                <td className="p-2">{c.release.seeders || 0}</td>
                <td className="p-2">
                  {Object.entries(c.breakdown)
                    .map(([k, v]) => `${k}: ${v > 0 ? '+' : ''}${v}`)
                    .join(', ')}
                  ; failure: {c.penalty}
                </td>
                <td className="p-2">{c.score}</td>
                <td className="p-2">
                  <button
                    disabled={busy}
                    className="text-blue-300 disabled:opacity-50"
                    onClick={() => void load('download', c.key)}
                  >
                    Download
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!busy && !candidates.length && (
        <p className="mt-4 text-zinc-300">
          No cached candidates. Refresh to search enabled sources.
        </p>
      )}
    </dialog>
  );
}
