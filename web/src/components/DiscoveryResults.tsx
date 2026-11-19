import { EntityLink } from './EntityLink';
import { useState, useEffect, useRef } from 'react';
import { Check, Plus, Loader2, BookOpen, AlertCircle, CheckCircle2, Search } from 'lucide-react';
import { RequestMetadata, RequestType, SeriesSelection } from '../types';
import { SeriesBadge } from './SeriesBadge';
import { SeriesDetailModal } from './SeriesDetailModal';

type RequestFormat = 'epub' | 'm4b';

const INITIAL_RESULT_LIMIT = 5;

type Match = {
  description?: string;
  cover_url?: string;
  series?: string;
  hardcover_series_id?: number;
  series_index?: number;
  series_total?: number;
  title: string;
  authors?: string[];
  year: number;
  library_book_id?: string;
  requested_formats?: string[];
  request_status?: string;
};

function requiredFormats(format: RequestType): RequestFormat[] {
  if (format === 'ebook') return ['epub'];
  if (format === 'audiobook') return ['m4b'];
  return ['epub', 'm4b'];
}

function normalizeRequestedFormats(formats?: string[]): RequestFormat[] {
  if (!formats) return [];
  const out: RequestFormat[] = [];
  for (const f of formats) {
    const norm = f.toLowerCase();
    if (norm === 'epub' || norm === 'ebook') out.push('epub');
    else if (norm === 'm4b' || norm === 'audiobook') out.push('m4b');
    else if (norm === 'both') out.push('epub', 'm4b');
  }
  return out;
}

interface Props {
  query: string;
  defaultSearchFormat?: RequestType;
  canManage?: boolean;
  canRequest?: boolean;
  isAdmin?: boolean;
  onOpenBook?: (id: string) => void;
  onSubmitRequest: (
    title: string,
    author: string,
    type: RequestType,
    year?: number,
    metadata?: RequestMetadata,
  ) => Promise<void>;
}

function DiscoveryCover({ coverUrl, title }: { coverUrl?: string; title: string }) {
  const [brokenUrl, setBrokenUrl] = useState<string | null>(null);
  const broken = Boolean(coverUrl) && brokenUrl === coverUrl;

  return (
    <div className="w-12 sm:w-14 shrink-0">
      <div className="aspect-[2/3] w-full rounded-md overflow-hidden bg-zinc-900 border border-zinc-800 shadow-xs">
        {coverUrl && !broken ? (
          <img
            src={coverUrl}
            alt={`Cover of ${title}`}
            onError={() => setBrokenUrl(coverUrl ?? null)}
            className="w-full h-full object-cover"
          />
        ) : (
          <div
            aria-label="No cover available"
            className="w-full h-full flex items-center justify-center text-zinc-500 bg-zinc-950"
          >
            <BookOpen className="w-5 h-5 stroke-1" aria-hidden="true" />
          </div>
        )}
      </div>
    </div>
  );
}

export function DiscoveryResults({ query, defaultSearchFormat = 'ebook', canManage, canRequest, isAdmin, onSubmitRequest, onOpenBook }: Props) {
  const allowRequest = canRequest ?? (canManage !== undefined ? canManage : true);
  const [selectedSeries, setSelectedSeries] = useState<SeriesSelection | null>(null);
  const [selectedFormat, setSelectedFormat] = useState<RequestType>();
  const format = selectedFormat ?? defaultSearchFormat;
  const [matches, setMatches] = useState<Match[]>([]);
  const [expanded, setExpanded] = useState(false);
  const firstRevealedResult = useRef<HTMLLIElement>(null);
  const remainingCount = matches.length - INITIAL_RESULT_LIMIT;
  const [retry, setRetry] = useState(0);
  const [addingKey, setAddingKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [errorSource, setErrorSource] = useState<'search' | 'add' | null>(null);
  const [message, setMessage] = useState('');
  const [searched, setSearched] = useState(false);
  const [addedFormats, setAddedFormats] = useState<Record<string, RequestFormat[]>>({});

  useEffect(() => {
    const controller = new AbortController();
    let current = true;
    setMatches([]);
    setExpanded(false);
    setError('');
    setErrorSource(null);
    setMessage('');
    setSearched(false);
    setBusy(Boolean(query.trim()));
    if (!query.trim()) return;
    const timer = setTimeout(async () => {
      try {
        const res = await fetch(`/api/discover?q=${encodeURIComponent(query.trim())}`, {
          signal: controller.signal,
        });
        if (!res.ok) throw new Error('Search unavailable. Try again shortly.');
        const data = await res.json();
        if (current) {
          setMatches(Array.isArray(data) ? data : []);
          setSearched(true);
        }
      } catch (e) {
        if (current && !controller.signal.aborted) {
          setError((e as Error).message);
          setErrorSource('search');
        }
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

  useEffect(() => {
    if (expanded) firstRevealedResult.current?.focus();
  }, [expanded]);

  const add = async (m: Match) => {
    const authors = m.authors ?? [];
    const authorText = authors.join(', ');
    const itemKey = `${m.title}\0${authorText}`;
    const targetFormats = requiredFormats(format);
    setAddingKey(itemKey);
    setError('');
    setErrorSource(null);
    setMessage('');
    try {
      await onSubmitRequest(m.title, authorText, format, m.year, {
        book_id: m.library_book_id,
        cover_url: m.cover_url,
        series: m.series,
        hardcover_series_id: m.hardcover_series_id,
        series_index: m.series_index,
        synopsis: m.description,
      });
      setAddedFormats((prev) => {
        const existing = prev[itemKey] ?? [];
        const merged = Array.from(new Set<RequestFormat>([...existing, ...targetFormats]));
        return { ...prev, [itemKey]: merged };
      });
      setMessage(`Added ${m.title}. Acquisition will run in the background.`);
    } catch (e) {
      setError((e as Error).message);
      setErrorSource('add');
    } finally {
      setAddingKey(null);
    }
  };

  return (
    <div className="space-y-4 text-zinc-100">
      {selectedSeries && (
        <SeriesDetailModal
          {...selectedSeries}
          canManage={canManage}
          canRequest={allowRequest}
          isAdmin={isAdmin}
          onClose={() => setSelectedSeries(null)}
          onOpenBook={
            onOpenBook
              ? (id) => {
                  setSelectedSeries(null);
                  onOpenBook(id);
                }
              : undefined
          }
        />
      )}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-zinc-100">{allowRequest ? 'Find books to add' : 'Discover books'}</h2>
          {searched && matches.length > 0 && (
            <span className="text-xs font-mono text-zinc-400">({matches.length})</span>
          )}
        </div>

        {allowRequest && (
          <div className="flex items-center gap-2 shrink-0">
            <label htmlFor="discovery-format-select" className="text-xs font-medium text-zinc-400">
              Format
            </label>
            <select
              id="discovery-format-select"
              value={format}
              onChange={(e) => setSelectedFormat(e.target.value as RequestType)}
              className="px-2.5 py-1.5 bg-zinc-950 border border-zinc-700/80 hover:border-zinc-600 rounded-md text-xs font-medium text-zinc-200 focus:outline-hidden focus-visible:outline-2 focus-visible:outline-blue-400 focus:border-blue-500/70 transition-colors cursor-pointer"
            >
              <option value="both">Both</option>
              <option value="ebook">eBook</option>
              <option value="audiobook">Audiobook</option>
            </select>
          </div>
        )}
      </div>

      {(busy || addingKey) && (
        <div role="status" className="space-y-2.5">
          <div className="flex items-center gap-2 text-xs text-zinc-300">
            <Loader2 className="w-3.5 h-3.5 animate-spin text-blue-400 shrink-0" aria-hidden="true" />
            <span>{addingKey ? 'Adding book to queue…' : 'Searching catalog…'}</span>
          </div>
          {busy && matches.length === 0 && (
            <div className="space-y-2.5" aria-hidden="true">
              <div className="h-[88px] rounded-lg bg-zinc-950/60 border border-zinc-800 animate-pulse" />
              <div className="h-[88px] rounded-lg bg-zinc-950/60 border border-zinc-800 animate-pulse" />
            </div>
          )}
        </div>
      )}

      {error && (
        <div
          role="alert"
          className="rounded-lg border border-red-900/60 bg-red-950/30 px-3.5 py-2.5 flex flex-wrap items-center justify-between gap-3 text-xs text-red-200"
        >
          <div className="flex items-center gap-2 min-w-0">
            <AlertCircle className="w-4 h-4 text-red-400 shrink-0" aria-hidden="true" />
            <span className="min-w-0 break-words">{error}</span>
          </div>
          {errorSource === 'search' && (
            <button
              type="button"
              className="px-2.5 py-1 text-xs font-medium bg-zinc-800 hover:bg-zinc-700 text-zinc-100 rounded-md border border-zinc-700 transition-colors shrink-0 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
              onClick={() => setRetry((value) => value + 1)}
            >
              Retry search
            </button>
          )}
        </div>
      )}

      {message && !busy && !addingKey && (
        <div
          role="status"
          className="rounded-lg border border-emerald-800/60 bg-emerald-950/30 px-3.5 py-2.5 flex items-center gap-2.5 text-xs text-emerald-200"
        >
          <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" aria-hidden="true" />
          <span>{message}</span>
        </div>
      )}

      {matches.length > 0 && (
        <ul className="space-y-2.5" aria-label="Book discovery results">
          {(expanded ? matches : matches.slice(0, INITIAL_RESULT_LIMIT)).map((m, i) => {
            const authors = m.authors ?? [];
            const itemKey = `${m.title}\0${authors.join(', ')}`;
            const requestedForBook = new Set<RequestFormat>([
              ...normalizeRequestedFormats(m.requested_formats),
              ...(addedFormats[itemKey] ?? []),
            ]);
            const isAdded = requiredFormats(format).every((f) => requestedForBook.has(f));
            const isAddingThis = addingKey === itemKey;

            return (
              <li
                key={`${itemKey}-${i}`}
                ref={i === INITIAL_RESULT_LIMIT ? firstRevealedResult : undefined}
                tabIndex={i === INITIAL_RESULT_LIMIT ? -1 : undefined}
                className="bg-zinc-950/60 border border-zinc-800 hover:border-zinc-700 rounded-lg p-3 flex items-center justify-between gap-3 sm:gap-4 transition-colors duration-150 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
              >
                <div className="flex items-center gap-3 min-w-0 flex-1">
                  <DiscoveryCover coverUrl={m.cover_url} title={m.title} />
                  <div className="min-w-0 flex-1 space-y-1">
                    <h3 className="text-sm font-semibold text-zinc-100 leading-snug break-words">
                      {m.title}
                    </h3>
                    <SeriesBadge
                      title={m.series}
                      author={m.authors?.[0]}
                      hardcoverID={m.hardcover_series_id}
                      index={m.series_index}
                      total={m.series_total}
                      onSelect={setSelectedSeries}
                    />
                    <p className="text-xs text-zinc-400 flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
                      {authors.length > 0 ? (
                        <span>
                          {authors.map((name, idx) => (
                            <span key={name}>
                              {idx > 0 && ', '}
                              <EntityLink kind="authors" name={name} />
                            </span>
                          ))}
                        </span>
                      ) : (
                        <span className="text-zinc-400">Author unknown</span>
                      )}
                      {m.year > 0 && (
                        <>
                          <span className="text-zinc-600" aria-hidden="true">
                            ·
                          </span>
                          <span className="font-mono text-[11px] text-zinc-400 tabular-nums">
                            {m.year}
                          </span>
                        </>
                      )}
                      {!m.library_book_id && requestedForBook.size > 0 && (
                        <>
                          <span className="text-zinc-600" aria-hidden="true">
                            ·
                          </span>
                          <span className="text-[11px] font-medium text-emerald-300">
                            {[
                              requestedForBook.has('epub') ? 'eBook' : null,
                              requestedForBook.has('m4b') ? 'Audiobook' : null,
                            ]
                              .filter(Boolean)
                              .join(' & ')}{' '}
                            {m.request_status === 'downloading'
                              ? 'downloading'
                              : m.request_status === 'searching'
                                ? 'searching'
                                : 'requested'}
                          </span>
                        </>
                      )}
                    </p>
                  </div>
                </div>

                {m.library_book_id ? (
                  <button
                    type="button"
                    onClick={() => onOpenBook?.(m.library_book_id!)}
                    className="shrink-0 self-center inline-flex items-center gap-1.5 h-8 px-3 rounded-md text-xs font-medium bg-emerald-950/70 hover:bg-emerald-900/80 text-emerald-300 border border-emerald-800/60 hover:border-emerald-700/80 transition-colors whitespace-nowrap focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-400"
                  >
                    <Check aria-hidden="true" className="w-3.5 h-3.5 shrink-0" />
                    <span>In Library</span>
                    <span className="sr-only">: {m.title}</span>
                  </button>
                ) : allowRequest ? (
                  <button
                    type="button"
                    disabled={busy || Boolean(addingKey) || isAdded}
                    onClick={() => void add(m)}
                    aria-label={
                      isAdded
                        ? `Added ${m.title}`
                        : isAddingThis
                          ? `Adding ${m.title}`
                          : `Add ${m.title}`
                    }
                    className={`shrink-0 self-center inline-flex items-center justify-center gap-1.5 h-8 px-3 rounded-md text-xs font-medium transition-colors whitespace-nowrap focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 ${
                      isAdded
                        ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/60 cursor-default'
                        : 'bg-blue-600 hover:bg-blue-500 active:bg-blue-700 text-white border border-blue-500/40 shadow-xs disabled:opacity-50 disabled:cursor-not-allowed'
                    }`}
                  >
                    {isAdded ? (
                      <>
                        <Check aria-hidden="true" className="w-3.5 h-3.5 shrink-0" />
                        <span>Added</span>
                      </>
                    ) : isAddingThis ? (
                      <>
                        <Loader2 aria-hidden="true" className="w-3.5 h-3.5 shrink-0 animate-spin" />
                        <span>Adding…</span>
                      </>
                    ) : (
                      <>
                        <Plus aria-hidden="true" className="w-3.5 h-3.5 shrink-0" />
                        <span>Add</span>
                      </>
                    )}
                  </button>
                ) : null}
              </li>
            );
          })}
        </ul>
      )}
      {!expanded && remainingCount > 0 && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="px-3 py-2 text-xs font-medium bg-zinc-800 hover:bg-zinc-700 text-zinc-100 rounded-md border border-zinc-700 transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
        >
          Show more ({remainingCount} more)
        </button>
      )}

      {searched && !matches.length && !busy && !error && (
        <div className="py-8 px-4 text-center border border-dashed border-zinc-800 rounded-lg bg-zinc-950/40">
          <Search className="w-6 h-6 text-zinc-500 mx-auto mb-2" aria-hidden="true" />
          <p className="text-xs font-medium text-zinc-300">
            No matches. Try another title or author.
          </p>
          <p className="text-[11px] text-zinc-400 mt-1">
            Try a broader title, author name, or check your metadata provider settings.
          </p>
        </div>
      )}
    </div>
  );
}
