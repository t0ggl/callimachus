import { EntityLink } from './EntityLink';
import { useState, useEffect, useRef } from 'react';
import { Check, Plus, Loader2, BookOpen, AlertCircle, CheckCircle2, Search, Layers } from 'lucide-react';
import { ProviderSeriesMembership, RequestMetadata, RequestType, SeriesSelection } from '../types';
import { SeriesBadge } from './SeriesBadge';
import { SeriesDetailModal } from './SeriesDetailModal';

type RequestFormat = 'epub' | 'm4b';

const INITIAL_RESULT_LIMIT = 5;
const LOAD_MORE_STEP = 10;
const DISCOVER_PAGE_SIZE = 10;
const MAX_PAGES_PER_LOAD = 3;

type Match = {
  description?: string;
  cover_url?: string;
  series?: string;
  hardcover_id?: string;
  openlibrary_id?: string;
  isbn13?: string;
  hardcover_series_id?: number;
  series_index?: number;
  series_memberships?: ProviderSeriesMembership[];
  series_total?: number;
  title: string;
  authors?: string[];
  year: number;
  library_book_id?: string;
  requested_formats?: string[];
  request_status?: string;
};

type SeriesMatch = {
  title: string;
  authors?: string[];
  books_count?: number;
  cover_url?: string;
  hardcover_series_id?: number;
  library_series_id?: string;
  monitored?: boolean;
  owned_count?: number;
};

type DiscoverPageResponse = {
  results: Match[];
  series: SeriesMatch[];
  page: number;
  hasMore: boolean;
  nextPage?: number;
};

function parseDiscoverResponse(data: unknown, requestedPage: number): DiscoverPageResponse {
  if (Array.isArray(data)) {
    return { results: data as Match[], series: [], page: requestedPage, hasMore: false };
  }
  if (data && typeof data === 'object' && Array.isArray((data as { results?: unknown }).results)) {
    const obj = data as {
      results: Match[];
      series?: SeriesMatch[];
      page?: number;
      hasMore?: boolean;
      nextPage?: number;
    };
    const hasMore = Boolean(obj.hasMore);
    const page = typeof obj.page === 'number' && obj.page >= 1 ? obj.page : requestedPage;
    const nextPage = hasMore
      ? typeof obj.nextPage === 'number' && obj.nextPage > page
        ? obj.nextPage
        : page + 1
      : undefined;
    return {
      results: obj.results,
      series: Array.isArray(obj.series) ? obj.series : [],
      page,
      hasMore,
      nextPage,
    };
  }
  return { results: [], series: [], page: requestedPage, hasMore: false };
}

function matchIdentityKeys(m: Match): string[] {
  const keys: string[] = [];
  if (m.library_book_id) keys.push(`lib:${m.library_book_id}`);
  if (m.hardcover_id) keys.push(`hc:${m.hardcover_id}`);
  if (m.openlibrary_id) keys.push(`ol:${m.openlibrary_id}`);
  if (m.isbn13) {
    const clean = m.isbn13.replace(/[-\s]/g, '');
    if (clean) keys.push(`isbn:${clean}`);
  }
  const authors = (m.authors ?? []).map((a) => a.trim().toLowerCase()).filter(Boolean).join(', ');
  keys.push(`ta:${m.title.trim().toLowerCase()}\0${authors}`);
  return keys;
}

function appendUniqueMatches(existing: Match[], incoming: Match[]): Match[] {
  const seen = new Set<string>();
  for (const item of existing) {
    for (const key of matchIdentityKeys(item)) seen.add(key);
  }
  const merged = [...existing];
  for (const item of incoming) {
    const keys = matchIdentityKeys(item);
    if (keys.some((key) => seen.has(key))) continue;
    for (const key of keys) seen.add(key);
    merged.push(item);
  }
  return merged;
}

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

function DiscoveryCover({ coverUrl, title, isSeries = false }: { coverUrl?: string; title: string; isSeries?: boolean }) {
  const [brokenUrl, setBrokenUrl] = useState<string | null>(null);
  const broken = Boolean(coverUrl) && brokenUrl === coverUrl;

  return (
    <div className="w-12 sm:w-14 shrink-0">
      <div
        className={`aspect-[2/3] w-full rounded-md overflow-hidden bg-zinc-900 shadow-xs ${
          isSeries ? 'border border-purple-700/60' : 'border border-zinc-800'
        }`}
      >
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
            className={`w-full h-full flex items-center justify-center ${
              isSeries ? 'text-purple-400 bg-purple-950/30' : 'text-zinc-500 bg-zinc-950'
            }`}
          >
            {isSeries ? (
              <Layers className="w-5 h-5 stroke-[1.5]" aria-hidden="true" />
            ) : (
              <BookOpen className="w-5 h-5 stroke-1" aria-hidden="true" />
            )}
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
  const [seriesMatches, setSeriesMatches] = useState<SeriesMatch[]>([]);
  const [visibleCount, setVisibleCount] = useState(INITIAL_RESULT_LIMIT);
  const [hasMore, setHasMore] = useState(false);
  const [nextPage, setNextPage] = useState<number | undefined>(undefined);
  const [loadingMore, setLoadingMore] = useState(false);
  const [revealFocus, setRevealFocus] = useState<{ index: number; seq: number } | null>(null);
  const firstRevealedResult = useRef<HTMLLIElement>(null);
  const loadMoreControllerRef = useRef<AbortController | null>(null);
  const [retry, setRetry] = useState(0);
  const [addingKey, setAddingKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [errorSource, setErrorSource] = useState<'search' | 'more' | 'add' | null>(null);
  const [message, setMessage] = useState('');
  const [searched, setSearched] = useState(false);
  const [addedFormats, setAddedFormats] = useState<Record<string, RequestFormat[]>>({});

  useEffect(() => {
    const controller = new AbortController();
    let current = true;
    loadMoreControllerRef.current?.abort();
    loadMoreControllerRef.current = null;
    setMatches([]);
    setSeriesMatches([]);
    setVisibleCount(INITIAL_RESULT_LIMIT);
    setHasMore(false);
    setNextPage(undefined);
    setLoadingMore(false);
    setRevealFocus(null);
    setError('');
    setErrorSource(null);
    setMessage('');
    setSearched(false);
    const trimmed = query.trim();
    setBusy(Boolean(trimmed));
    if (!trimmed) return;
    const timer = setTimeout(async () => {
      try {
        const res = await fetch(
          `/api/discover?q=${encodeURIComponent(trimmed)}&page=1&limit=${DISCOVER_PAGE_SIZE}`,
          { signal: controller.signal },
        );
        if (!res.ok) throw new Error('Search unavailable. Try again shortly.');
        const data = await res.json();
        const parsed = parseDiscoverResponse(data, 1);
        if (current) {
          setMatches(parsed.results);
          setSeriesMatches(parsed.series);
          setHasMore(parsed.hasMore);
          setNextPage(parsed.nextPage);
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
      loadMoreControllerRef.current?.abort();
      loadMoreControllerRef.current = null;
    };
  }, [query, retry]);

  useEffect(() => {
    if (revealFocus) firstRevealedResult.current?.focus();
  }, [revealFocus]);

  const handleViewMore = async () => {
    const trimmed = query.trim();
    if (!trimmed || busy || loadingMore) return;
    const revealIndex = visibleCount;
    const targetVisible = visibleCount + LOAD_MORE_STEP;
    setError('');
    setErrorSource(null);

    if (matches.length >= targetVisible || !hasMore || !nextPage) {
      const nextVisible = Math.min(targetVisible, matches.length);
      setVisibleCount(nextVisible);
      if (nextVisible > revealIndex) {
        setRevealFocus((prev) => ({ index: revealIndex, seq: (prev?.seq ?? 0) + 1 }));
      }
      return;
    }

    loadMoreControllerRef.current?.abort();
    const controller = new AbortController();
    loadMoreControllerRef.current = controller;
    setLoadingMore(true);

    let combined = matches;
    let currentHasMore: boolean = hasMore;
    let currentNextPage: number | undefined = nextPage;
    let pagesFetched = 0;

    try {
      while (
        combined.length < targetVisible &&
        currentHasMore &&
        currentNextPage &&
        pagesFetched < MAX_PAGES_PER_LOAD
      ) {
        const requestedPage = currentNextPage;
        const res = await fetch(
          `/api/discover?q=${encodeURIComponent(trimmed)}&page=${requestedPage}&limit=${DISCOVER_PAGE_SIZE}`,
          { signal: controller.signal },
        );
        if (!res.ok) throw new Error('Unable to load more results. Try again shortly.');
        const data = await res.json();
        if (controller.signal.aborted) return;
        const parsed = parseDiscoverResponse(data, requestedPage);
        pagesFetched++;
        combined = appendUniqueMatches(combined, parsed.results);
        if (parsed.results.length === 0) {
          currentHasMore = false;
          currentNextPage = undefined;
          break;
        }
        currentHasMore = parsed.hasMore;
        currentNextPage = parsed.nextPage;
      }

      if (controller.signal.aborted) return;
      setMatches(combined);
      setHasMore(currentHasMore);
      setNextPage(currentNextPage);
      const nextVisible = Math.min(targetVisible, combined.length);
      setVisibleCount(nextVisible);
      if (nextVisible > revealIndex) {
        setRevealFocus((prev) => ({ index: revealIndex, seq: (prev?.seq ?? 0) + 1 }));
      }
    } catch (e) {
      if (!controller.signal.aborted) {
        if (combined.length > matches.length) {
          setMatches(combined);
          setHasMore(currentHasMore);
          setNextPage(currentNextPage);
          const nextVisible = Math.min(targetVisible, combined.length);
          setVisibleCount(nextVisible);
          if (nextVisible > revealIndex) {
            setRevealFocus((prev) => ({ index: revealIndex, seq: (prev?.seq ?? 0) + 1 }));
          }
        }
        setError((e as Error).message);
        setErrorSource('more');
      }
    } finally {
      if (loadMoreControllerRef.current === controller) {
        loadMoreControllerRef.current = null;
      }
      if (!controller.signal.aborted) {
        setLoadingMore(false);
      }
    }
  };

  const visibleMatches = matches.slice(0, visibleCount);
  const canViewMore = matches.length > visibleMatches.length || hasMore;

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
        series_memberships: m.series_memberships,
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
          onSeriesUpdated={(monitored) => {
            setSeriesMatches((prev) =>
              prev.map((item) => {
                const sameId =
                  (selectedSeries.id && item.library_series_id === selectedSeries.id) ||
                  (selectedSeries.hardcoverID && item.hardcover_series_id === selectedSeries.hardcoverID);
                const sameTitle = item.title.toLowerCase() === selectedSeries.title.toLowerCase();
                return sameId || sameTitle ? { ...item, monitored } : item;
              }),
            );
          }}
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
            <span className="text-xs font-mono tabular-nums text-zinc-400">
              ({visibleMatches.length}
              {hasMore ? '+' : visibleMatches.length < matches.length ? ` of ${matches.length}` : ''})
            </span>
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
          {(errorSource === 'search' || errorSource === 'more') && (
            <button
              type="button"
              className="px-2.5 py-1 text-xs font-medium bg-zinc-800 hover:bg-zinc-700 text-zinc-100 rounded-md border border-zinc-700 transition-colors shrink-0 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
              onClick={() => {
                if (errorSource === 'more') {
                  void handleViewMore();
                } else {
                  setRetry((value) => value + 1);
                }
              }}
            >
              {errorSource === 'more' ? 'Retry' : 'Retry search'}
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

      {seriesMatches.length > 0 && (
        <section className="space-y-2" aria-label="Series results">
          <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-purple-300">
            <Layers className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
            <span>Series</span>
            <span className="font-mono font-normal tabular-nums text-zinc-400">
              ({seriesMatches.length})
            </span>
          </div>
          <ul className="space-y-2.5" aria-label="Series discovery results">
            {seriesMatches.map((s, idx) => {
              const authors = s.authors ?? [];
              const countLabel =
                s.owned_count && s.owned_count > 0 && s.books_count && s.books_count > 0
                  ? `${s.owned_count} / ${s.books_count} in library`
                  : s.books_count && s.books_count > 0
                    ? `${s.books_count} ${s.books_count === 1 ? 'book' : 'books'}`
                    : s.owned_count && s.owned_count > 0
                      ? `${s.owned_count} in library`
                      : '';
              return (
                <li
                  key={`${s.hardcover_series_id ?? s.library_series_id ?? s.title}-${idx}`}
                  className="bg-purple-950/15 border border-purple-800/40 hover:border-purple-700/60 rounded-lg p-3 flex items-center justify-between gap-3 sm:gap-4 transition-colors duration-150"
                >
                  <div className="flex items-center gap-3 min-w-0 flex-1">
                    <DiscoveryCover coverUrl={s.cover_url} title={s.title} isSeries />
                    <div className="min-w-0 flex-1 space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-sm font-semibold text-zinc-100 leading-snug break-words">
                          {s.title}
                        </h3>
                        <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-[10px] font-semibold uppercase tracking-wide bg-purple-950/80 text-purple-300 border border-purple-800/60 shrink-0">
                          <Layers className="w-3 h-3" aria-hidden="true" />
                          Series
                        </span>
                        {s.monitored && (
                          <span className="inline-flex items-center px-1.5 py-0.5 rounded-sm text-[10px] font-medium bg-blue-950/80 text-blue-300 border border-blue-800/60 shrink-0">
                            Monitored
                          </span>
                        )}
                      </div>
                    <p className="text-xs text-zinc-400 flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
                        {authors.length > 0 ? (
                          <span>
                            {authors.map((name, aIdx) => (
                              <span key={name}>
                                {aIdx > 0 && ', '}
                                <EntityLink kind="authors" name={name} />
                              </span>
                            ))}
                          </span>
                        ) : (
                          <span className="text-zinc-400">Author unknown</span>
                        )}
                        {countLabel && (
                          <>
                            <span className="text-zinc-600" aria-hidden="true">
                              ·
                            </span>
                            <span className="font-mono text-[11px] text-zinc-300 tabular-nums">
                              {countLabel}
                            </span>
                          </>
                        )}
                      </p>
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() =>
                      setSelectedSeries({
                        title: s.title,
                        id: s.library_series_id,
                        author: authors[0],
                        hardcoverID: s.hardcover_series_id,
                      })
                    }
                    aria-label={`View series ${s.title}`}
                    className="shrink-0 self-center inline-flex items-center justify-center gap-1.5 h-8 px-3 rounded-md text-xs font-medium bg-purple-600/20 hover:bg-purple-600/30 active:bg-purple-600/40 text-purple-200 border border-purple-500/40 transition-colors whitespace-nowrap focus-visible:outline focus-visible:outline-2 focus-visible:outline-purple-400"
                  >
                    <Layers aria-hidden="true" className="w-3.5 h-3.5 shrink-0" />
                    <span>View Series</span>
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {matches.length > 0 && (
        <div className="space-y-2">
          {seriesMatches.length > 0 && (
            <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-zinc-400">
              <BookOpen className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
              <span>Books</span>
              <span className="font-mono font-normal tabular-nums text-zinc-400">
                ({visibleMatches.length}
                {hasMore ? '+' : visibleMatches.length < matches.length ? ` of ${matches.length}` : ''})
              </span>
            </div>
          )}
          <ul className="space-y-2.5" aria-label="Book discovery results">
          {visibleMatches.map((m, i) => {
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
                ref={revealFocus?.index === i ? firstRevealedResult : undefined}
                tabIndex={revealFocus?.index === i ? -1 : undefined}
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
                    {m.series_memberships?.filter((membership) => membership.title !== m.series).map((membership) => (
                      <SeriesBadge key={membership.hardcover_id ?? membership.title} title={membership.title}
                        author={m.authors?.[0]} hardcoverID={membership.hardcover_id}
                        index={membership.index} onSelect={setSelectedSeries} />
                    ))}
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
        </div>
      )}
      {!busy && matches.length > 0 && canViewMore && (
        <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
          <button
            type="button"
            disabled={loadingMore}
            onClick={() => void handleViewMore()}
            className="inline-flex items-center gap-1.5 px-3 py-2 text-xs font-medium bg-zinc-800 hover:bg-zinc-700 text-zinc-100 rounded-md border border-zinc-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
          >
            {loadingMore && <Loader2 className="w-3.5 h-3.5 animate-spin shrink-0" aria-hidden="true" />}
            {loadingMore ? 'Loading more…' : 'View more'}
          </button>
          <span className="text-xs font-mono tabular-nums text-zinc-400">
            Showing {visibleMatches.length}
            {hasMore ? '+' : ` of ${matches.length}`}
          </span>
        </div>
      )}

      {searched && !matches.length && !seriesMatches.length && !busy && !error && (
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
