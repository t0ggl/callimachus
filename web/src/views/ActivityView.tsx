import React, { useEffect, useState } from 'react';
import {
  Activity,
  AlertCircle,
  AlertTriangle,
  BookOpen,
  CheckCircle2,
  Clock,
  Download,
  Filter,
  Headphones,
  Loader2,
  PackageCheck,
  Pause,
  RefreshCw,
  Search,
  WifiOff,
  X,
} from 'lucide-react';
import { Book, BookRequest } from '../types';
import { activityItems, ActivityData, ActivityItem, isFinished, percent } from '../utils/activity';
import { AcquisitionProgress } from '../components/AcquisitionProgress';
import { BookDetailModal } from '../components/BookDetailModal';
import { ManualSearch } from '../components/ManualSearch';

interface ActivityViewProps extends ActivityData {
  canManageJobs?: boolean;
  canRequest?: boolean;
  isAdmin?: boolean;
  onMetadataApplied?: () => void;
  connectionStatus: 'connected' | 'connecting' | 'disconnected';
  loading?: boolean;
  error?: string;
  onRetry?: () => void;
}

const duration = (seconds: number) => seconds > 0 ? `${Math.ceil(seconds / 60)} min` : 'Unavailable';
const focusRing = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300';
const secondaryButton = `inline-flex items-center justify-center gap-1.5 min-h-11 sm:min-h-9 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-1.5 text-xs font-medium text-zinc-100 transition-colors hover:bg-zinc-700 disabled:opacity-50 disabled:cursor-not-allowed ${focusRing}`;

const isAudiobookFormat = (format?: string) => {
  const value = format?.toLowerCase();
  return value === 'audiobook' || value === 'm4b' || value === 'mp3';
};

function phaseStyle(phase: string): { badge: string; bar: string } {
  if (phase === 'Completed') {
    return {
      badge: 'border-emerald-800/60 bg-emerald-950/80 text-emerald-300',
      bar: 'bg-emerald-500',
    };
  }
  if (phase === 'Failed') {
    return {
      badge: 'border-red-800/60 bg-red-950/80 text-red-300',
      bar: 'bg-red-500',
    };
  }
  if (phase === 'Needs attention' || phase === 'Paused') {
    return {
      badge: 'border-amber-800/60 bg-amber-950/70 text-amber-300',
      bar: 'bg-amber-400',
    };
  }
  if (phase === 'Queued') {
    return {
      badge: 'border-zinc-700 bg-zinc-900 text-zinc-300',
      bar: 'bg-blue-400',
    };
  }
  return {
    badge: 'border-blue-800/60 bg-blue-950/80 text-blue-300',
    bar: 'bg-blue-400',
  };
}

function PhaseIcon({ phase }: { phase: string }) {
  const className = 'w-3.5 h-3.5 shrink-0';
  if (phase === 'Completed') return <CheckCircle2 className={className} aria-hidden="true" />;
  if (phase === 'Failed') return <AlertCircle className={className} aria-hidden="true" />;
  if (phase === 'Needs attention') return <AlertTriangle className={className} aria-hidden="true" />;
  if (phase === 'Paused') return <Pause className={className} aria-hidden="true" />;
  if (phase === 'Queued') return <Clock className={className} aria-hidden="true" />;
  if (phase === 'Downloading') return <Download className={className} aria-hidden="true" />;
  if (phase === 'Importing') return <PackageCheck className={className} aria-hidden="true" />;
  if (phase === 'Searching indexers' || phase === 'Evaluating releases') return <Search className={className} aria-hidden="true" />;
  return <RefreshCw className={`${className} motion-safe:animate-spin`} aria-hidden="true" />;
}

function ActivityThumbnail({ coverUrl, format }: { coverUrl?: string; format?: string }) {
  const [brokenUrl, setBrokenUrl] = useState<string | null>(null);
  const showCover = Boolean(coverUrl) && brokenUrl !== coverUrl;
  const audio = isAudiobookFormat(format);

  return (
    <div className="w-7 sm:w-8 shrink-0" aria-hidden="true">
      <div className="aspect-[2/3] w-full rounded-sm overflow-hidden bg-zinc-950 border border-zinc-800 flex items-center justify-center">
        {showCover ? (
          <img
            src={coverUrl}
            alt=""
            referrerPolicy="no-referrer"
            onError={() => setBrokenUrl(coverUrl ?? null)}
            className="w-full h-full object-cover"
          />
        ) : audio ? (
          <Headphones className="w-3.5 h-3.5 text-purple-400" />
        ) : (
          <BookOpen className={`w-3.5 h-3.5 ${format === 'eBook & Audiobook' ? 'text-zinc-400' : 'text-blue-400'}`} />
        )}
      </div>
    </div>
  );
}

function ActivityDetails({ item, onClose, canManage, canRequest, isAdmin, onMetadataApplied }: {
  item: ActivityItem;
  onClose: () => void;
  canManage?: boolean;
  canRequest?: boolean;
  isAdmin?: boolean;
  onMetadataApplied?: () => void;
}) {
  const [book, setBook] = useState<Book | null>(null);
  const [bookRevision, setBookRevision] = useState(0);
  useEffect(() => {
    if (!item.bookId) return;
    const controller = new AbortController();
    void fetch(`/api/books?id=${encodeURIComponent(item.bookId)}`, { signal: controller.signal })
      .then(async response => {
        if (response.ok) {
          const [match] = await response.json() as Book[];
          if (!controller.signal.aborted) setBook(match || null);
        }
      })
      .catch(() => {});
    return () => controller.abort();
  }, [item.bookId, item.stage === 'available', bookRevision]);

  const metadata: Book = {
    id: item.bookId || '', title: item.title, authorName: item.author || 'Unknown author',
    authorId: '', releaseDate: item.request?.year ? String(item.request.year) : undefined,
    formats: [], editions: [], ...book, coverUrl: book?.coverUrl || item.request?.coverUrl,
  };
  return <BookDetailModal book={metadata} onClose={onClose} canManage={canManage} canRequest={canRequest} isAdmin={isAdmin}
    onBookUpdated={(updated) => setBook(updated)}
    onBookDeleted={() => { onClose(); onMetadataApplied?.(); }}
    onMetadataApplied={() => { setBookRevision((value) => value + 1); onMetadataApplied?.(); }}
    activity={<AcquisitionProgress key={item.id} item={item} />}
  />;
}

type StatusFilter = 'ALL' | 'ACTIVE' | 'COMPLETED' | 'ATTENTION';
type FormatFilter = 'ALL' | 'EBOOK' | 'AUDIOBOOK';

const STATUS_FILTERS: { id: StatusFilter; label: string }[] = [
  { id: 'ALL', label: 'All' },
  { id: 'ACTIVE', label: 'Active' },
  { id: 'COMPLETED', label: 'Done' },
  { id: 'ATTENTION', label: 'Attention' },
];

const FORMAT_FILTERS: { id: FormatFilter; label: string }[] = [
  { id: 'ALL', label: 'All Formats' },
  { id: 'EBOOK', label: 'eBooks' },
  { id: 'AUDIOBOOK', label: 'Audiobooks' },
];

export const ActivityView: React.FC<ActivityViewProps> = (props) => {
  const [manual, setManual] = useState<BookRequest | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [removed, setRemoved] = useState<string[]>([]);
  const [pending, setPending] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('ALL');
  const [formatFilter, setFormatFilter] = useState<FormatFilter>('ALL');
  const items = activityItems(props).filter(item => !removed.includes(item.id));
  const detail = items.find(item => item.id === selected);
  const activeCount = items.filter(item => !isFinished(item)).length;
  const isInitialLoading = Boolean(props.loading) && items.length === 0;
  const hasActiveFilters = searchQuery.trim() !== '' || statusFilter !== 'ALL' || formatFilter !== 'ALL';

  const filteredItems = items.filter(item => {
    const query = searchQuery.trim().toLowerCase();
    if (query) {
      const haystack = [item.title, item.author, item.phase, item.detail, item.format, item.error]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      if (!haystack.includes(query)) return false;
    }
    if (statusFilter === 'ACTIVE' && isFinished(item)) return false;
    if (statusFilter === 'COMPLETED' && item.phase !== 'Completed') return false;
    if (statusFilter === 'ATTENTION' && !(['Failed', 'Needs attention'].includes(item.phase) || Boolean(item.error))) return false;
    if (formatFilter === 'EBOOK' && isAudiobookFormat(item.format)) return false;
    if (formatFilter === 'AUDIOBOOK' && !isAudiobookFormat(item.format) && item.format !== 'eBook & Audiobook') return false;
    return true;
  });

  const dismiss = async (id: string, path: string) => {
    setPending(previous => [...previous, id]);
    setErrors(previous => ({ ...previous, [id]: '' }));
    try {
      const response = await fetch(path, { method: 'DELETE' });
      if (!response.ok && response.status !== 404) {
        const message = response.headers.get('Content-Type')?.includes('application/json') ?
          (await response.json()).error : await response.text();
        throw new Error(message || 'Unable to remove activity. Try again.');
      }
      setRemoved(previous => [...previous, id]);
    } catch (error) {
      setErrors(previous => ({ ...previous, [id]: error instanceof Error ? error.message : 'Unable to remove activity.' }));
    } finally {
      setPending(previous => previous.filter(entry => entry !== id));
    }
  };

  return <section aria-label="Activity" className="space-y-6">
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-zinc-800 pb-4">
      <div>
        <div className="flex flex-wrap items-center gap-2.5">
          <h1 className="text-xl font-bold tracking-tight text-white">Activity</h1>
          {items.length > 0 && (
            <span className="text-xs font-mono px-2 py-0.5 rounded-sm bg-zinc-900 border border-zinc-800 text-zinc-300 tabular-nums">
              {activeCount > 0 ? `${activeCount} active` : `${items.length} ${items.length === 1 ? 'item' : 'items'}`}
            </span>
          )}
        </div>
        <p className="text-xs text-zinc-400 mt-0.5">
          Track active downloads, conversions, and completed library imports
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2.5">
        {props.connectionStatus === 'connecting' && (
          <span className="inline-flex items-center gap-1.5 rounded-sm border border-zinc-800 bg-zinc-900 px-2.5 py-1 text-xs text-zinc-300">
            <Loader2 className="w-3.5 h-3.5 motion-safe:animate-spin text-blue-400" aria-hidden="true" />
            Connecting…
          </span>
        )}
        {props.connectionStatus === 'disconnected' && (
          <span className="inline-flex items-center gap-1.5 rounded-sm border border-zinc-800 bg-zinc-900 px-2.5 py-1 text-xs text-zinc-400">
            <WifiOff className="w-3.5 h-3.5 text-zinc-400" aria-hidden="true" />
            Live updates paused
          </span>
        )}
        {props.onRetry && (
          <button
            type="button"
            disabled={props.loading}
            onClick={props.onRetry}
            className={secondaryButton}
          >
            <RefreshCw className={`w-3.5 h-3.5 ${props.loading ? 'motion-safe:animate-spin' : ''}`} aria-hidden="true" />
            Refresh
          </button>
        )}
      </div>
    </div>

    {props.error && (
      <div
        role="alert"
        className="rounded-xl border border-red-900/60 bg-red-950/30 p-4 flex flex-wrap items-center justify-between gap-3 text-sm text-red-200"
      >
        <div className="flex items-center gap-2.5 min-w-0">
          <AlertCircle className="w-4 h-4 text-red-400 shrink-0" aria-hidden="true" />
          <span className="min-w-0 wrap-anywhere">{props.error}</span>
        </div>
        {props.onRetry && (
          <button type="button" onClick={props.onRetry} className={secondaryButton}>
            <RefreshCw className="w-3.5 h-3.5" aria-hidden="true" />
            Retry
          </button>
        )}
      </div>
    )}

    {isInitialLoading ? (
      <div role="status" className="space-y-3">
        <div className="flex items-center gap-2 text-xs text-zinc-300">
          <Loader2 className="w-4 h-4 motion-safe:animate-spin text-blue-400 shrink-0" aria-hidden="true" />
          <span>Loading activity…</span>
        </div>
        <div className="space-y-2" aria-hidden="true">
          <div className="h-12 rounded-lg border border-zinc-800 bg-zinc-900/90 motion-safe:animate-pulse" />
          <div className="h-12 rounded-lg border border-zinc-800 bg-zinc-900/90 motion-safe:animate-pulse" />
        </div>
      </div>
    ) : items.length === 0 && !props.error ? (
      <div className="py-16 px-4 text-center border border-dashed border-zinc-800 rounded-xl bg-zinc-900/40">
        <Activity className="w-8 h-8 text-zinc-500 mx-auto mb-3" aria-hidden="true" />
        <p className="text-sm font-medium text-zinc-200">No activity yet. Books you add will appear here.</p>
        <p className="text-xs text-zinc-400 mt-1">
          Downloads, format conversions, and completed imports will show progress here.
        </p>
      </div>
    ) : items.length > 0 ? (
      <div className="@container space-y-3">
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-3">
          <div className="relative w-full sm:w-80">
            <Search className="w-4 h-4 text-zinc-500 absolute left-3 top-2.5" aria-hidden="true" />
            <input
              type="text"
              aria-label="Filter activity"
              placeholder="Filter by title, author, or status…"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="w-full pl-9 pr-3 py-1.5 bg-zinc-900 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-hidden focus:border-zinc-700"
            />
          </div>

          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="text-zinc-400 flex items-center gap-1">
              <Filter className="w-3.5 h-3.5" aria-hidden="true" /> Status:
            </span>
            <div role="group" aria-label="Filter by status" className="flex flex-wrap bg-zinc-900 border border-zinc-800 rounded-md p-0.5 text-xs">
              {STATUS_FILTERS.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  aria-pressed={statusFilter === f.id}
                  onClick={() => setStatusFilter(f.id)}
                  className={`px-2.5 py-1 rounded-sm transition-colors ${focusRing} ${
                    statusFilter === f.id
                      ? 'bg-zinc-800 text-white font-semibold'
                      : 'text-zinc-400 hover:text-zinc-200'
                  }`}
                >
                  {f.label}
                </button>
              ))}
            </div>

            <div role="group" aria-label="Filter by format" className="flex flex-wrap bg-zinc-900 border border-zinc-800 rounded-md p-0.5 text-xs">
              {FORMAT_FILTERS.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  aria-pressed={formatFilter === f.id}
                  onClick={() => setFormatFilter(f.id)}
                  className={`px-2.5 py-1 rounded-sm transition-colors ${focusRing} ${
                    formatFilter === f.id
                      ? 'bg-zinc-800 text-white font-semibold'
                      : 'text-zinc-400 hover:text-zinc-200'
                  }`}
                >
                  {f.label}
                </button>
              ))}
            </div>
          </div>
        </div>

        {filteredItems.length === 0 ? (
          <div className="py-12 px-4 text-center border border-dashed border-zinc-800 rounded-xl bg-zinc-900/40 space-y-2">
            <p className="text-sm font-medium text-zinc-200">No matching activity</p>
            <p className="text-xs text-zinc-400">Try adjusting your search or filter selections.</p>
            {hasActiveFilters && (
              <button
                type="button"
                onClick={() => {
                  setSearchQuery('');
                  setStatusFilter('ALL');
                  setFormatFilter('ALL');
                }}
                className={secondaryButton}
              >
                Clear Filters
              </button>
            )}
          </div>
        ) : (
          <div className="rounded-xl border border-zinc-800 bg-zinc-900/90 overflow-hidden @4xl:grid @4xl:grid-cols-[minmax(10rem,2fr)_7.5rem_minmax(10rem,1.4fr)_minmax(7.5rem,1.2fr)_fit-content(15.5rem)] @4xl:gap-x-3">
            <div
              aria-hidden="true"
              className="hidden @4xl:grid @4xl:col-span-full @4xl:grid-cols-subgrid items-center border-b border-zinc-800 bg-zinc-950/60 px-4 py-2 text-[11px] font-medium text-zinc-400"
            >
              <span>Book</span>
              <span>Format</span>
              <span>Stage / Status</span>
              <span>Progress</span>
              <span className="text-right">Actions</span>
            </div>

            <ul aria-label="Book activity" className="divide-y divide-zinc-800/80 @4xl:grid @4xl:col-span-full @4xl:grid-cols-subgrid">
              {filteredItems.map(item => {
                const allowRequest = Boolean(props.canRequest ?? props.canManageJobs);
                const path = item.request && allowRequest ? `/api/requests/${encodeURIComponent(item.request.id)}` : props.canManageJobs ?
                  item.downloadId ? `/api/activity/downloads/${encodeURIComponent(item.downloadId)}` :
                    item.conversionId ? `/api/activity/conversions/${encodeURIComponent(item.conversionId)}` : undefined : undefined;
                const busy = pending.includes(item.id);
                const action = busy ? 'Working…' : isFinished(item) ? 'Remove' : 'Cancel';
                const tone = phaseStyle(item.phase);
                const statusText = [item.phase, item.detail].filter(Boolean).join(' · ');
                const formatBadgeTone = isAudiobookFormat(item.format)
                  ? 'border-purple-800/60 bg-purple-950/80 text-purple-300'
                  : item.format === 'eBook & Audiobook'
                    ? 'border-zinc-700 bg-zinc-800 text-zinc-200'
                    : 'border-blue-800/60 bg-blue-950/80 text-blue-300';

                return <li
                  key={item.id}
                  data-testid={`activity-${item.id}`}
                  className="grid grid-cols-1 gap-y-2.5 @4xl:col-span-full @4xl:grid-cols-subgrid @4xl:items-center rounded-none border-0 bg-zinc-900/90 px-3.5 py-2.5 sm:px-4 transition-colors hover:bg-zinc-800/40"
                >
                  <div className="flex items-center gap-2.5 min-w-0">
                    <ActivityThumbnail coverUrl={item.request?.coverUrl} format={item.format} />
                    <button
                      type="button"
                      onClick={() => setSelected(item.id)}
                      aria-label={[item.title, [item.format, item.phase].filter(Boolean).join(' · ')].join(' ')}
                      className={`min-w-0 flex-1 min-h-11 sm:min-h-0 flex flex-col justify-center rounded-sm text-left wrap-anywhere group ${focusRing}`}
                    >
                      <span className="text-xs sm:text-sm font-semibold text-zinc-100 leading-snug underline decoration-zinc-700 underline-offset-4 group-hover:text-blue-300 group-hover:decoration-blue-300 transition-colors truncate">
                        {item.title}
                      </span>
                      {item.author && (
                        <span className="text-xs text-zinc-400 truncate">
                          by {item.author}
                        </span>
                      )}
                    </button>
                  </div>

                  <div className="flex items-center">
                    {item.format ? (
                      <span className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[11px] font-mono font-medium ${formatBadgeTone}`}>
                        {item.format}
                      </span>
                    ) : (
                      <span className="text-xs text-zinc-500">—</span>
                    )}
                  </div>

                  <div className="flex flex-col gap-1 min-w-0">
                    <span className={`inline-flex w-fit items-center gap-1.5 rounded-sm border px-2 py-0.5 text-xs font-medium wrap-anywhere ${tone.badge}`}>
                      <PhaseIcon phase={item.phase} />
                      {statusText}
                    </span>
                    {(errors[item.id] || item.error) && (
                      <div
                        role="alert"
                        className="rounded-md border border-red-900/60 bg-red-950/30 px-2.5 py-1 flex items-start gap-1.5 text-xs text-red-200"
                      >
                        <AlertCircle className="w-3.5 h-3.5 text-red-400 shrink-0 mt-0.5" aria-hidden="true" />
                        <span className="min-w-0 wrap-anywhere">{errors[item.id] || item.error}</span>
                      </div>
                    )}
                  </div>

                  <div className="min-w-0">
                    {item.progress !== undefined ? (
                      <div className="space-y-1">
                        <div className="flex flex-wrap items-center justify-between gap-1.5 text-[11px] tabular-nums">
                          <span className="font-mono font-medium text-zinc-200">{percent(item.progress).toFixed(1)}%</span>
                          {!isFinished(item) && (
                            <span className="font-mono text-zinc-400">
                              {`${item.speed ? `${item.speed} · ` : ''}ETA: ${duration(item.eta || 0)}`}
                            </span>
                          )}
                        </div>
                        <div
                          role="progressbar"
                          aria-label={`${item.title} overall progress`}
                          aria-valuemin={0}
                          aria-valuemax={100}
                          aria-valuenow={percent(item.progress)}
                          className="h-1.5 rounded-full overflow-hidden bg-zinc-800"
                        >
                          <div
                            className={`h-full rounded-full transition-[width] duration-300 ease-out ${tone.bar}`}
                            style={{ width: `${percent(item.progress)}%` }}
                          />
                        </div>
                      </div>
                    ) : (
                      <span className="text-xs font-mono text-zinc-500">—</span>
                    )}
                  </div>

                  <div className="flex flex-wrap items-center @4xl:justify-end gap-1.5">
                    {allowRequest && item.request && item.request.status !== 'downloading' && (
                      <button
                        type="button"
                        aria-label={`Manual Search for ${item.title}`}
                        onClick={() => setManual(item.request!)}
                        className={`${secondaryButton} text-blue-300 hover:text-blue-200`}
                      >
                        <Search className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
                        Manual Search
                      </button>
                    )}
                    {path && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => void dismiss(item.id, path)}
                        aria-label={[action, item.title, item.format].filter(Boolean).join(' ')}
                        className={isFinished(item)
                          ? secondaryButton
                          : `inline-flex items-center justify-center gap-1.5 min-h-11 sm:min-h-9 rounded-md border border-red-900/60 bg-red-950/30 px-2.5 py-1 text-xs font-medium text-red-300 transition-colors hover:bg-red-950/60 hover:text-red-200 disabled:opacity-50 disabled:cursor-not-allowed ${focusRing}`
                        }
                      >
                        <X className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
                        {action}
                      </button>
                    )}
                  </div>
                </li>;
              })}
            </ul>
          </div>
        )}
      </div>
    ) : null}

    {manual && <ManualSearch key={manual.id} requestId={manual.id}
      requestFormat={manual.requestType === 'audiobook' ? 'm4b' : manual.requestType === 'ebook' ? 'epub' : 'both'}
      book={manual.bookId ? { id: manual.bookId, title: manual.title, author: manual.author } : undefined}
      onClose={() => setManual(null)} />}
    {detail && <ActivityDetails key={detail.id} item={detail} canManage={props.canManageJobs} canRequest={Boolean(props.canRequest ?? props.canManageJobs)} isAdmin={props.isAdmin}
      onMetadataApplied={props.onMetadataApplied} onClose={() => setSelected(null)} />}
  </section>;
};
