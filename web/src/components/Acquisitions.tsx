import { useState } from 'react';
import { BookRequest } from '../types';
import { ManualSearch } from './ManualSearch';

export function Acquisitions({ requests }: { requests: BookRequest[] }) {
  const [manual, setManual] = useState<string | null>(null);
  return <section aria-label="Acquisitions" className="space-y-3">
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
  </section>;
}
