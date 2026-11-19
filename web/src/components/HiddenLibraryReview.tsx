import { useEffect, useRef, useState } from 'react';

interface ReviewTitle {
  id: string;
  title: string;
  author: string;
  series: string;
  hiddenPath: string;
  removalBlock: string;
}

const reviewURL = '/api/library/review';
const buttonClass = 'min-h-9 rounded-md px-3 py-1.5 text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 disabled:opacity-50';
const secondaryButton = `${buttonClass} bg-zinc-800 text-zinc-100 enabled:hover:bg-zinc-700`;
const removeButton = `${buttonClass} border border-rose-900 text-rose-300 enabled:hover:bg-rose-950`;

export function HiddenLibraryReview({ onCatalogChanged }: { onCatalogChanged?: () => void }) {
  const [titles, setTitles] = useState<ReviewTitle[] | null>(null);
  const [confirmID, setConfirmID] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const heading = useRef<HTMLHeadingElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  const removeTrigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (confirmID) cancel.current?.focus();
  }, [confirmID]);

  const load = async () => {
    setBusy(true);
    setError('');
    setStatus('Loading titles…');
    setConfirmID(null);
    try {
      const response = await fetch(reviewURL);
      if (!response.ok) throw new Error();
      setTitles(await response.json());
    } catch {
      setError('Could not load the review list. Try loading it again.');
    } finally {
      setStatus('');
      setBusy(false);
      heading.current?.focus();
    }
  };

  const decide = async (title: ReviewTitle, keep: boolean) => {
    setBusy(true);
    setError('');
    setStatus(keep ? 'Keeping title…' : 'Removing title…');
    const url = `${reviewURL}/${encodeURIComponent(title.id)}`;
    try {
      const response = await fetch(keep ? `${url}/keep` : url, { method: keep ? 'PUT' : 'DELETE' });
      if (response.status === 409) {
        const reason = (await response.text()).trim();
        setTitles((current) => current?.map((item) => item.id === title.id ? { ...item, removalBlock: reason } : item) ?? null);
        setConfirmID(null);
        setError(`${reason} You can keep the title or refresh the list.`);
        setStatus('');
        return;
      }
      if (response.status === 404) {
        setTitles((current) => current?.filter((item) => item.id !== title.id) ?? null);
        setConfirmID(null);
        setError('This title is no longer awaiting review. Refresh the list for the latest titles.');
        setStatus('');
        onCatalogChanged?.();
        return;
      }
      if (!response.ok) throw new Error();
      setTitles((current) => current?.filter((item) => item.id !== title.id) ?? null);
      setConfirmID(null);
      setStatus(keep ? `Kept ${title.title} in the catalog.` : `Removed ${title.title} from the catalog.`);
      if (!keep) onCatalogChanged?.();
    } catch {
      setError('Could not save your choice. Try again or refresh the list.');
      setStatus('');
    } finally {
      setBusy(false);
      heading.current?.focus();
    }
  };

  return (
    <section aria-labelledby="hidden-library-review" className="max-w-2xl space-y-4 rounded-xl border border-zinc-800 bg-zinc-900 p-6">
      <div>
        <h3 id="hidden-library-review" ref={heading} tabIndex={-1} className="text-sm font-semibold text-zinc-100 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-blue-400">
          Hidden import review
        </h3>
        <p className="mt-1 text-sm text-zinc-400">
          Review titles left without indexed media after a library scan excluded files from hidden paths.
          New automatic requests wait for your review.
        </p>
      </div>

      <button type="button" className={secondaryButton} disabled={busy} onClick={() => void load()}>
        {titles ? 'Refresh review list' : 'Load review list'}
      </button>
      <p role="status" className={status ? 'text-sm text-zinc-300' : 'sr-only'}>{status}</p>
      {error && <p role="alert" className="text-sm text-rose-300">{error}</p>}
      {titles?.length === 0 && <p className="text-sm text-zinc-400">No titles await review.</p>}

      {!!titles?.length && (
        <ul aria-label="Titles awaiting review" className="divide-y divide-zinc-800 border-t border-zinc-800">
          {titles.map((title) => (
            <li key={title.id} className="space-y-3 py-4 last:pb-0">
              <div className="min-w-0">
                <h4 className="wrap-anywhere text-sm font-semibold text-zinc-100">{title.title}</h4>
                <p className="wrap-anywhere text-sm text-zinc-300">{title.author}{title.series && ` · ${title.series}`}</p>
                <p className="mt-1 wrap-anywhere text-xs text-zinc-400">Example hidden path <code>{title.hiddenPath}</code></p>
                {title.removalBlock && <p className="mt-1 text-sm text-zinc-300">{title.removalBlock} You can keep it.</p>}
              </div>

              <div className="flex flex-wrap gap-2">
                <button type="button" className={secondaryButton} aria-label={`Keep ${title.title}`} disabled={busy}
                  onClick={() => void decide(title, true)}>
                  Keep
                </button>
                <button type="button" className={removeButton} aria-label={`Remove from catalog (${title.title})`}
                  aria-expanded={confirmID === title.id} disabled={busy || !!title.removalBlock}
                  onClick={(event) => {
                    removeTrigger.current = event.currentTarget;
                    setConfirmID(title.id);
                  }}>
                  Remove from catalog
                </button>
              </div>

              {confirmID === title.id && (
                <div className="space-y-2 rounded-md bg-zinc-950 p-3">
                  <p className="wrap-anywhere text-sm text-zinc-200">
                    Remove <strong>{title.title}</strong> and its editions from the catalog? This only removes catalog records.
                  </p>
                  <div className="flex flex-wrap gap-2">
                    <button type="button" className={secondaryButton} ref={cancel} disabled={busy} onClick={() => {
                      setConfirmID(null);
                      removeTrigger.current?.focus();
                    }}>
                      Cancel
                    </button>
                    <button type="button" aria-label={`Confirm removal of ${title.title}`} className={removeButton}
                      disabled={busy} onClick={() => void decide(title, false)}>
                      Confirm removal
                    </button>
                  </div>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
