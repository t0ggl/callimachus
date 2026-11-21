import { useEffect, useId, useRef, useState } from 'react';
import { Check, ImageOff, Loader2, Search } from 'lucide-react';
import type { CoverCandidate, CoverSearchResponse } from '../types';

const focusRing = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300';
const secondaryButton = `rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 text-sm font-medium text-zinc-100 transition-colors hover:bg-zinc-700 disabled:opacity-50 ${focusRing}`;

async function readErrorMessage(response: Response, fallback: string): Promise<string> {
  const contentType = response.headers.get('Content-Type') ?? '';
  if (contentType.startsWith('text/plain')) {
    const text = (await response.text()).trim();
    if (text) return text;
  } else if (contentType.includes('application/json')) {
    const payload = (await response.json().catch(() => null)) as { error?: string } | null;
    if (payload?.error?.trim()) return payload.error.trim();
  }
  return fallback;
}

interface CoverCandidateCardProps {
  candidate: CoverCandidate;
  selected: boolean;
  disabled: boolean;
  onSelect: () => void;
}

function CoverCandidateCard({ candidate, selected, disabled, onSelect }: CoverCandidateCardProps) {
  const [src, setSrc] = useState(candidate.thumbnailUrl || candidate.url);
  const [broken, setBroken] = useState(false);

  const dimsLabel =
    candidate.width > 0 && candidate.height > 0 ? `${candidate.width} × ${candidate.height}` : '';
  const accessibleLabel = [
    candidate.title || 'Book cover',
    dimsLabel,
    candidate.source,
  ]
    .filter(Boolean)
    .join(', ');

  return (
    <button
      type="button"
      disabled={disabled}
      aria-pressed={selected}
      aria-label={`Select cover: ${accessibleLabel}`}
      onClick={onSelect}
      className={`group relative flex flex-col overflow-hidden rounded-lg border text-left transition-colors disabled:opacity-60 ${focusRing} ${
        selected
          ? 'border-blue-400 bg-blue-950/30 ring-2 ring-blue-400/80'
          : 'border-zinc-800 bg-zinc-950/70 hover:border-zinc-600 hover:bg-zinc-900'
      }`}
    >
      <div className="relative aspect-[2/3] w-full overflow-hidden bg-zinc-950">
        {broken ? (
          <div className="flex h-full w-full flex-col items-center justify-center gap-1.5 px-2 text-center text-zinc-500">
            <ImageOff className="h-6 w-6 stroke-1" aria-hidden="true" />
            <span className="text-[11px] text-zinc-400">Preview unavailable</span>
          </div>
        ) : (
          <img
            src={src}
            referrerPolicy="no-referrer"
            loading="lazy"
            alt={candidate.title || 'Cover candidate'}
            onError={() => {
              if (src !== candidate.url && candidate.url) {
                setSrc(candidate.url);
              } else {
                setBroken(true);
              }
            }}
            className="h-full w-full object-contain"
          />
        )}
        {selected && (
          <span
            aria-hidden="true"
            className="absolute right-2 top-2 inline-flex h-6 w-6 items-center justify-center rounded-full bg-blue-400 text-zinc-950 shadow-sm"
          >
            <Check className="h-3.5 w-3.5 stroke-[2.5]" />
          </span>
        )}
      </div>
      <div className="flex flex-col gap-0.5 border-t border-zinc-800/80 px-2.5 py-2">
        <div className="flex items-center justify-between gap-1.5 text-[11px]">
          <span className="font-mono tabular-nums text-zinc-200">{dimsLabel || 'Verified'}</span>
          <span className="truncate text-zinc-400">{candidate.source}</span>
        </div>
        {candidate.title && (
          <span className="truncate text-[11px] text-zinc-400" title={candidate.title}>
            {candidate.title}
          </span>
        )}
      </div>
    </button>
  );
}

export interface CoverFinderPanelProps {
  bookID: string;
  defaultQuery: string;
  onBack: () => void;
  onApplied: (result: { coverUrl?: string; epubUpdated?: number }) => void;
}

export function CoverFinderPanel({ bookID, defaultQuery, onBack, onApplied }: CoverFinderPanelProps) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const id = useId();
  const [queryInput, setQueryInput] = useState(defaultQuery);
  const [activeSearch, setActiveSearch] = useState({ term: defaultQuery.trim(), attempt: 0 });
  const [candidates, setCandidates] = useState<CoverCandidate[]>([]);
  const [page, setPage] = useState(1);
  const [hasMore, setHasMore] = useState(false);
  const [nextPage, setNextPage] = useState<number | undefined>(undefined);
  const [loadingInitial, setLoadingInitial] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [searchError, setSearchError] = useState('');
  const [emptyPageNote, setEmptyPageNote] = useState('');
  const [selectedURL, setSelectedURL] = useState<string | null>(null);
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState('');
  const loadMoreController = useRef<AbortController | null>(null);

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    loadMoreController.current?.abort();
    loadMoreController.current = null;
    setLoadingInitial(true);
    setLoadingMore(false);
    setSearchError('');
    setEmptyPageNote('');
    setApplyError('');
    setSelectedURL(null);
    setCandidates([]);
    setPage(1);
    setHasMore(false);
    setNextPage(undefined);

    const params = new URLSearchParams({ page: '1' });
    if (activeSearch.term) {
      params.set('q', activeSearch.term);
    }

    void fetch(`/api/books/${encodeURIComponent(bookID)}/cover/search?${params.toString()}`, {
      signal: controller.signal,
    })
      .then(async (res) => {
        if (!res.ok) {
          throw new Error(await readErrorMessage(res, 'Unable to search for covers right now. Try again.'));
        }
        return (await res.json()) as CoverSearchResponse;
      })
      .then((data) => {
        if (controller.signal.aborted) return;
        setCandidates(Array.isArray(data.candidates) ? data.candidates : []);
        setPage(data.page || 1);
        setHasMore(Boolean(data.hasMore));
        setNextPage(data.nextPage);
        setLoadingInitial(false);
      })
      .catch((error: Error) => {
        if (controller.signal.aborted) return;
        setSearchError(error.message || 'Unable to search for covers right now. Try again.');
        setLoadingInitial(false);
      });

    return () => {
      controller.abort();
      loadMoreController.current?.abort();
    };
  }, [bookID, activeSearch]);

  const handleLoadNextPage = async () => {
    if (!hasMore || loadingMore || loadingInitial || applying) return;
    const targetPage = nextPage ?? page + 1;
    const controller = new AbortController();
    loadMoreController.current?.abort();
    loadMoreController.current = controller;

    setLoadingMore(true);
    setSearchError('');
    setEmptyPageNote('');

    try {
      const params = new URLSearchParams({ page: String(targetPage) });
      if (activeSearch.term) {
        params.set('q', activeSearch.term);
      }
      const res = await fetch(`/api/books/${encodeURIComponent(bookID)}/cover/search?${params.toString()}`, {
        signal: controller.signal,
      });
      if (!res.ok) {
        throw new Error(await readErrorMessage(res, 'Unable to load the next page of covers. Try again.'));
      }
      const data = (await res.json()) as CoverSearchResponse;
      if (controller.signal.aborted) return;

      const incoming = Array.isArray(data.candidates) ? data.candidates : [];
      let addedCount = 0;
      setCandidates((prev) => {
        const seen = new Set(prev.map((item) => item.url.toLowerCase()));
        const unique = incoming.filter((item) => {
          const key = item.url.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        });
        addedCount = unique.length;
        return [...prev, ...unique];
      });
      setPage(data.page || targetPage);
      setHasMore(Boolean(data.hasMore));
      setNextPage(data.nextPage);
      if (addedCount === 0) {
        setEmptyPageNote(
          data.hasMore
            ? `No additional matching covers on page ${targetPage}. Load the next page or refine your search.`
            : `No additional matching covers on page ${targetPage}.`
        );
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      setSearchError(error instanceof Error ? error.message : 'Unable to load the next page of covers.');
    } finally {
      if (!controller.signal.aborted) {
        setLoadingMore(false);
      }
    }
  };

  const selectedCandidate = candidates.find((c) => c.url === selectedURL) ?? null;

  const handleApply = async () => {
    if (!selectedCandidate || applying) return;
    setApplying(true);
    setApplyError('');
    try {
      const res = await fetch(`/api/books/${encodeURIComponent(bookID)}/cover/select`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: selectedCandidate.url }),
      });
      if (!res.ok) {
        throw new Error(
          await readErrorMessage(res, 'Unable to apply the selected cover. Choose another cover or try again.')
        );
      }
      const data = (await res.json()) as { coverUrl?: string; epubUpdated?: number };
      onApplied(data);
    } catch (error) {
      setApplyError(
        error instanceof Error ? error.message : 'Unable to apply the selected cover. Try again.'
      );
    } finally {
      setApplying(false);
    }
  };

  return (
    <section aria-labelledby={id} className="flex min-h-0 flex-1 flex-col">
      <div className="flex-1 space-y-5 overflow-y-auto p-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 id={id} ref={headingRef} tabIndex={-1} className="text-lg font-semibold text-zinc-100">
            Find Cover
          </h3>
          <button type="button" className={secondaryButton} disabled={applying} onClick={onBack}>
            Back to details
          </button>
        </div>

        <form
          className="space-y-2"
          onSubmit={(event) => {
            event.preventDefault();
            const trimmed = queryInput.trim();
            if (!trimmed || applying) return;
            setActiveSearch((prev) => ({ term: trimmed, attempt: prev.attempt + 1 }));
          }}
        >
          <label htmlFor={`${id}-query`} className="block text-sm font-medium text-zinc-200">
            Search for cover art
          </label>
          <div className="flex flex-wrap gap-2">
            <input
              id={`${id}-query`}
              type="search"
              value={queryInput}
              maxLength={500}
              disabled={applying}
              placeholder="Title and author"
              onChange={(event) => setQueryInput(event.target.value)}
              className={`min-w-0 flex-1 rounded-md border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder:text-zinc-500 ${focusRing}`}
            />
            <button
              type="submit"
              disabled={applying || !queryInput.trim()}
              className={`inline-flex items-center gap-1.5 ${secondaryButton}`}
            >
              <Search className="h-4 w-4 shrink-0" aria-hidden="true" />
              Search
            </button>
          </div>
          <p className="text-xs text-zinc-400">
            Results are filtered to book-cover aspect ratios and minimum 200×200 px resolution.
          </p>
        </form>

        {loadingInitial && (
          <div className="space-y-3">
            <p role="status" className="text-sm text-zinc-300">
              Searching cover sources…
            </p>
            <div className="grid grid-cols-2 gap-3.5 sm:grid-cols-3 md:grid-cols-4" aria-hidden="true">
              {Array.from({ length: 8 }).map((_, idx) => (
                <div
                  key={idx}
                  className="overflow-hidden rounded-lg border border-zinc-800 bg-zinc-950/60"
                >
                  <div className="aspect-[2/3] w-full animate-pulse bg-zinc-800/60" />
                  <div className="space-y-1.5 border-t border-zinc-800/80 px-2.5 py-2">
                    <div className="h-3 w-20 animate-pulse rounded bg-zinc-800/70" />
                    <div className="h-2.5 w-24 animate-pulse rounded bg-zinc-800/50" />
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {!loadingInitial && searchError && candidates.length === 0 && (
          <div className="space-y-3 rounded-lg border border-red-900/60 bg-red-950/30 p-4">
            <p role="alert" className="text-sm text-red-200">
              {searchError}
            </p>
            <button
              type="button"
              className={secondaryButton}
              onClick={() =>
                setActiveSearch((prev) => ({ term: queryInput.trim() || prev.term, attempt: prev.attempt + 1 }))
              }
            >
              Retry search
            </button>
          </div>
        )}

        {!loadingInitial && !searchError && candidates.length === 0 && (
          <div className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/60 p-5 text-sm text-zinc-300">
            <p>
              No matching covers found for <span className="font-medium text-zinc-100">{activeSearch.term}</span>.
            </p>
            <p className="text-xs text-zinc-400">
              Try searching with just the book title or author name, or go back to upload a cover image directly.
            </p>
            {hasMore && (
              <button
                type="button"
                disabled={loadingMore || applying}
                onClick={() => void handleLoadNextPage()}
                className={secondaryButton}
              >
                {loadingMore ? 'Loading next page…' : 'Load next page'}
              </button>
            )}
          </div>
        )}

        {!loadingInitial && candidates.length > 0 && (
          <div className="space-y-4">
            <div
              role="group"
              aria-label="Cover search results"
              className="grid grid-cols-2 gap-3.5 sm:grid-cols-3 md:grid-cols-4"
            >
              {candidates.map((candidate) => (
                <CoverCandidateCard
                  key={candidate.url}
                  candidate={candidate}
                  selected={selectedURL === candidate.url}
                  disabled={applying}
                  onSelect={() => {
                    setApplyError('');
                    setSelectedURL(candidate.url);
                  }}
                />
              ))}
            </div>

            {searchError && (
              <p role="alert" className="text-xs text-red-300">
                {searchError}
              </p>
            )}
            {emptyPageNote && !searchError && (
              <p role="status" className="text-xs text-zinc-400">
                {emptyPageNote}
              </p>
            )}

            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-zinc-800/80 pt-3">
              <span className="text-xs tabular-nums text-zinc-400">
                Showing {candidates.length} {candidates.length === 1 ? 'cover' : 'covers'} · Page {page}
              </span>
              {hasMore && (
                <button
                  type="button"
                  disabled={loadingMore || applying}
                  onClick={() => void handleLoadNextPage()}
                  className={`inline-flex items-center gap-1.5 ${secondaryButton}`}
                >
                  {loadingMore && <Loader2 className="h-3.5 w-3.5 animate-spin shrink-0" aria-hidden="true" />}
                  {loadingMore ? 'Loading next page…' : 'Load next page'}
                </button>
              )}
            </div>
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-zinc-800 bg-zinc-950/60 px-6 py-3">
        <div className="min-w-0 flex-1 text-xs">
          {applyError ? (
            <p role="alert" className="text-red-300">
              {applyError}
            </p>
          ) : selectedCandidate ? (
            <p className="truncate text-zinc-300">
              Selected:{' '}
              <span className="font-mono tabular-nums text-zinc-100">
                {selectedCandidate.width} × {selectedCandidate.height}
              </span>{' '}
              · {selectedCandidate.source}
            </p>
          ) : (
            <p className="text-zinc-400">Select a cover to apply.</p>
          )}
        </div>
        <div className="flex items-center gap-2">
          <button type="button" disabled={applying} onClick={onBack} className={secondaryButton}>
            Cancel
          </button>
          <button
            type="button"
            disabled={!selectedCandidate || applying}
            onClick={() => void handleApply()}
            className={`inline-flex items-center gap-1.5 rounded-md bg-blue-400 px-4 py-2 text-sm font-semibold text-zinc-950 transition-colors hover:bg-blue-300 disabled:opacity-50 ${focusRing}`}
          >
            {applying && <Loader2 className="h-4 w-4 animate-spin shrink-0" aria-hidden="true" />}
            {applying ? 'Applying cover…' : 'Apply Cover'}
          </button>
        </div>
      </div>
    </section>
  );
}
