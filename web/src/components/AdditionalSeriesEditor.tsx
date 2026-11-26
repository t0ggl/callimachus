import { useId, useState } from 'react';
import type { Book, BookSeriesMembership } from '../types';
import { EntityLink } from './EntityLink';

const control = 'rounded-md border border-zinc-700 bg-zinc-900 px-2 py-1.5 text-sm text-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50';

export function AdditionalSeriesEditor({ book, onSaved }: { book: Book; onSaved: (book: Book) => void }) {
  const id = useId();
  const [editing, setEditing] = useState<BookSeriesMembership>();
  const [title, setTitle] = useState('');
  const [index, setIndex] = useState('');
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(change: { seriesId?: string; title?: string; index?: number; remove?: boolean }) {
    setBusy(true);
    setError('');
    setStatus('');
    try {
      const response = await fetch(`/api/books/${encodeURIComponent(book.id)}/additional-series`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(change),
      });
      if (!response.ok) throw new Error((await response.text()).trim() || 'Unable to save the series membership.');
      const updated = await fetch(`/api/books?id=${encodeURIComponent(book.id)}`);
      if (!updated.ok) throw new Error('Membership saved. Reopen the book to see the change.');
      const books: Book[] = await updated.json();
      const saved = books.find((item) => item.id === book.id);
      if (!saved) throw new Error('Membership saved. Reopen the book to see the change.');
      setEditing(undefined);
      setTitle('');
      setIndex('');
      setStatus(change.remove ? 'Series membership removed.' : 'Series membership saved.');
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to update the series membership.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <details className="space-y-3 border-t border-zinc-800 py-3 text-sm">
      <summary className="cursor-pointer rounded-sm text-zinc-200 focus-visible:outline-2 focus-visible:outline-blue-300">Manage additional series</summary>
      <ul className="space-y-2">
        {book.seriesMemberships?.filter((membership) => !membership.isPrimary).map((membership) => (
          <li key={membership.seriesId} className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 flex-1 break-words">
              <EntityLink kind="series" name={membership.title} id={membership.seriesId} />
              {membership.index != null && ` · Book ${membership.index}`}
            </span>
            <button type="button" className={control} disabled={busy} aria-label={`Edit position in ${membership.title}`} onClick={() => {
              setEditing(membership);
              setTitle(membership.title);
              setIndex(membership.index?.toString() ?? '');
            }}>Edit</button>
            <button type="button" className={control} disabled={busy} aria-label={`Remove ${membership.title} membership`} onClick={() => void save({ seriesId: membership.seriesId, remove: true })}>Remove</button>
          </li>
        ))}
      </ul>
      <form className="flex flex-wrap items-end gap-2" onSubmit={(event) => {
        event.preventDefault();
        void save({ seriesId: editing?.seriesId, title: title.trim(), index: index === '' ? undefined : Number(index) });
      }}>
        <label htmlFor={id + '-title'} className="flex min-w-36 flex-1 flex-col gap-1">
          Series title
          <input id={id + '-title'} className={control} value={title} required disabled={busy} readOnly={Boolean(editing)} onChange={(event) => setTitle(event.target.value)} />
        </label>
        <label htmlFor={id + '-position'} className="flex w-28 flex-col gap-1">
          Position
          <input id={id + '-position'} className={control} type="number" step="any" value={index} disabled={busy} onChange={(event) => setIndex(event.target.value)} />
        </label>
        <button type="submit" className={control} disabled={busy || !title.trim()}>{editing ? 'Save position' : 'Add series'}</button>
        {editing && <button type="button" className={control} disabled={busy} onClick={() => {
          setEditing(undefined);
          setTitle('');
          setIndex('');
        }}>Cancel</button>}
      </form>
      {status && <p role="status" className="text-emerald-300">{status}</p>}
      {error && <p role="alert" className="text-rose-300">{error}</p>}
    </details>
  );
}
