import React, { useEffect, useState } from 'react';
import {
  Activity,
  AlertCircle,
  AlertTriangle,
  BookOpen,
  CheckCircle2,
  Clock,
  Download,
  Headphones,
  Loader2,
  PackageCheck,
  Pause,
  RefreshCw,
  Search,
  WifiOff,
  X,
} from 'lucide-react';
import { AcquisitionStage, Book, BookRequest, ConversionJob, DownloadItem, ImportLog, RequestEvent } from '../types';
import { BookDetailModal } from '../components/BookDetailModal';
import { ManualSearch } from '../components/ManualSearch';

interface ActivityViewProps {
  conversionJobs: ConversionJob[];
  downloads: DownloadItem[];
  logs: ImportLog[];
  requests?: BookRequest[];
  canManageJobs?: boolean;
  canRequest?: boolean;
  isAdmin?: boolean;
  onMetadataApplied?: () => void;
  connectionStatus: 'connected' | 'connecting' | 'disconnected';
  loading?: boolean;
  error?: string;
  onRetry?: () => void;
}

const percent = (value: number) => Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : 0;
const duration = (seconds: number) => seconds > 0 ? `${Math.ceil(seconds / 60)} min` : 'Unavailable';
const formats = { ebook: 'eBook', audiobook: 'Audiobook', both: 'eBook & Audiobook' };
const stages: { id: AcquisitionStage; label: string }[] = [
  { id: 'queued', label: 'Queued' },
  { id: 'searching', label: 'Searching indexers' },
  { id: 'evaluating', label: 'Evaluating releases' },
  { id: 'downloading', label: 'Downloading' },
  { id: 'importing', label: 'Importing' },
  { id: 'available', label: 'Available' },
];
const focusRing = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300';
const secondaryButton = `inline-flex items-center justify-center gap-1.5 min-h-11 sm:min-h-9 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-1.5 text-xs font-medium text-zinc-100 transition-colors hover:bg-zinc-700 disabled:opacity-50 disabled:cursor-not-allowed ${focusRing}`;

interface ActivityItem {
  id: string;
  title: string;
  author?: string;
  format?: string;
  phase: string;
  stage: AcquisitionStage;
  detail?: string;
  progress?: number;
  eta?: number;
  speed?: string;
  error?: string;
  bookId?: string;
  request?: BookRequest;
  downloadId?: string;
  conversionId?: string;
}

const isFinished = (item: ActivityItem) => ['Completed', 'Failed', 'Needs attention'].includes(item.phase);

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
    <div className="w-11 sm:w-12 shrink-0" aria-hidden="true">
      <div className="aspect-[2/3] w-full rounded-md overflow-hidden bg-zinc-950 border border-zinc-800 flex items-center justify-center">
        {showCover ? (
          <img
            src={coverUrl}
            alt=""
            referrerPolicy="no-referrer"
            onError={() => setBrokenUrl(coverUrl ?? null)}
            className="w-full h-full object-cover"
          />
        ) : audio ? (
          <Headphones className="w-5 h-5 text-purple-400" />
        ) : (
          <BookOpen className={`w-5 h-5 ${format === 'eBook & Audiobook' ? 'text-zinc-400' : 'text-blue-400'}`} />
        )}
      </div>
    </div>
  );
}

export function activityItems({ conversionJobs, downloads, logs, requests = [] }: ActivityViewProps): ActivityItem[] {
  const items = new Map<string, ActivityItem>();
  const downloadKeys = new Map<string, string>();
  for (const log of logs) {
    const id = log.editionId || `log:${log.id}`;
    if (!items.has(id)) items.set(id, {
      id, title: log.itemName,
      phase: log.status === 'error' ? 'Failed' : log.status === 'warning' ? 'Needs attention' : 'Completed',
      stage: log.status === 'success' ? 'available' : 'importing',
      detail: log.details, progress: log.status === 'success' ? 100 : undefined,
      error: log.status === 'error' ? log.details : undefined,
    });
  }
  for (const request of requests) {
    const id = `request:${request.id}`;
    const stage = request.stage || (request.status === 'available' ? 'available' : request.status === 'downloading' ? 'downloading' : 'queued');
    const phase = request.status === 'available' ? 'Completed' : request.status === 'error' ? 'Failed' :
      stages.find(step => step.id === stage)!.label;
    items.set(id, { id, title: request.title, author: request.author, format: formats[request.requestType],
      phase, stage, bookId: request.bookId, request, progress: stage === 'available' ? 100 : undefined });
  }
  for (const dl of downloads) {
    const id = dl.requestId ? `request:${dl.requestId}` : dl.editionId || `download:${dl.id}`;
    downloadKeys.set(dl.id, id);
    const previous = items.get(id);
    if (previous?.downloadId || (previous?.request?.status === 'searching' && dl.status === 'error')) continue;
    if (dl.editionId && dl.requestId && !items.has(dl.editionId)) downloadKeys.set(dl.editionId, id);
    items.set(id, {
      ...previous, id, title: previous?.title || dl.title, bookId: previous?.bookId || dl.bookId,
      format: previous?.format || (dl.category === 'callimachus-ebooks' ? 'eBook' : 'Audiobook'),
      phase: dl.status === 'error' ? 'Failed' : dl.status.charAt(0).toUpperCase() + dl.status.slice(1),
      stage: dl.status === 'completed' ? 'available' : dl.status === 'importing' ? 'importing' : dl.status === 'queued' && !dl.requestId ? 'queued' : 'downloading',
      detail: dl.status === 'importing' ? 'Organizing / Tagging' : undefined,
      progress: dl.status === 'completed' ? 100 : dl.status === 'importing' ? 90 : percent(dl.progressPercent) * 0.5,
      eta: dl.etaSeconds, speed: dl.downloadSpeedBps > 0 ? `${(dl.downloadSpeedBps / 1048576).toFixed(1)} MB/s` : undefined,
      error: dl.errorMessage, downloadId: dl.id,
    });
  }
  for (const job of conversionJobs) {
    const id = job.requestId ? `request:${job.requestId}` :
      (job.downloadId && downloadKeys.get(job.downloadId)) ||
      (job.editionId && downloadKeys.get(job.editionId)) || job.editionId || `conversion:${job.id}`;
    const previous = items.get(id);
    if ((previous?.request?.status === 'searching' && !previous.downloadId) || previous?.conversionId || (previous?.downloadId && job.downloadId && previous.downloadId !== job.downloadId)) continue;
    const importing = job.stage === 'moving' || job.stage === 'tagging';
    items.set(id, {
      ...previous, id, title: previous?.request?.title || job.bookTitle || previous?.title || 'Untitled edition',
      author: previous?.author || job.authorName, bookId: previous?.bookId || job.bookId,
      format: previous?.format || job.targetFormat, downloadId: job.downloadId || previous?.downloadId,
      conversionId: job.id, stage: job.status === 'completed' ? 'available' : 'importing',
      phase: job.status === 'completed' ? 'Completed' : job.status === 'failed' ? 'Failed' : job.status === 'processing' ? importing ? 'Importing' : 'Converting' : job.status === 'queued' ? 'Queued' : 'Paused',
      detail: job.stage === 'preparing' ? 'Preparing' : importing ? job.stage === 'moving' ? 'Organizing' : 'Tagging' : job.stage === 'converting' ? 'Converting' : job.stage === 'muxing' ? 'Muxing' : undefined,
      progress: job.status === 'completed' ? 100 : 50 + percent(job.progressPercent) * 0.5,
      eta: job.etaSeconds, speed: job.bitrateKbps > 0 ? `${job.bitrateKbps} kbps` : undefined,
      error: job.errorMessage,
    });
  }
  return [...items.values()].sort((a, b) => Number(a.phase === 'Completed') - Number(b.phase === 'Completed'));
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
  const [showLog, setShowLog] = useState(false);
  const [events, setEvents] = useState<RequestEvent[] | null>(null);
  const [logError, setLogError] = useState(false);
  const requestId = item.request?.id;
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

  useEffect(() => {
    if (!showLog || !requestId) return;
    const controller = new AbortController();
    let next: ReturnType<typeof setTimeout>;
    const load = async () => {
      try {
        const response = await fetch(`/api/requests/${encodeURIComponent(requestId)}`, { signal: controller.signal });
        if (!response.ok) throw new Error();
        const detail = await response.json();
        if (!controller.signal.aborted) {
          setEvents(detail.events || []);
          setLogError(false);
        }
      } catch {
        if (!controller.signal.aborted) setLogError(true);
      } finally {
        if (!controller.signal.aborted) next = setTimeout(load, 2500);
      }
    };
    void load();
    return () => {
      controller.abort();
      clearTimeout(next);
    };
  }, [showLog, requestId]);

  const metadata: Book = {
    id: item.bookId || '', title: item.title, authorName: item.author || 'Unknown author',
    authorId: '', releaseDate: item.request?.year ? String(item.request.year) : undefined,
    formats: [], editions: [], ...book, coverUrl: book?.coverUrl || item.request?.coverUrl,
  };
  const current = stages.findIndex(step => step.id === item.stage);

  return <BookDetailModal book={metadata} onClose={onClose} canManage={canManage} canRequest={canRequest} isAdmin={isAdmin}
    onMetadataApplied={() => { setBookRevision((value) => value + 1); onMetadataApplied?.(); }} activity={
    <section aria-label="Acquisition progress" className="space-y-4 border-t border-zinc-800 pt-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-zinc-100">{item.format || 'Acquisition'} · {item.phase}</h3>
        {requestId && <button type="button" aria-expanded={showLog} aria-controls="activity-search-log"
          className={`${secondaryButton} text-blue-300 hover:text-blue-200`} onClick={() => setShowLog(visible => !visible)}>
          {showLog ? 'Hide Search Log' : 'View Search Log'}
        </button>}
      </div>
      <ol aria-label="Acquisition stages" className="grid grid-cols-2 gap-x-3 gap-y-3 sm:grid-cols-3 lg:grid-cols-6">
        {stages.map((step, index) => <li key={step.id} aria-current={index === current ? 'step' : undefined}
          className={`space-y-1.5 text-xs ${index === current ? 'font-semibold text-blue-200' : index < current ? 'text-zinc-300' : 'text-zinc-400'}`}>
          <span aria-hidden="true" className={`block h-1.5 rounded-full transition-colors ${index === current ? 'bg-blue-400' : index < current ? 'bg-emerald-400' : 'bg-zinc-800'}`} />
          <span>{step.label}</span>
        </li>)}
      </ol>
      {requestId && <div id="activity-search-log" hidden={!showLog} className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-3.5 text-xs text-zinc-300">
        {logError && <div role="alert" className="flex items-center gap-2 text-red-200">
          <AlertCircle className="w-4 h-4 text-red-400 shrink-0" aria-hidden="true" />
          <span>Unable to load the search log. Retrying…</span>
        </div>}
        {!logError && events === null && <div role="status" className="flex items-center gap-2 text-zinc-300">
          <Loader2 className="w-3.5 h-3.5 motion-safe:animate-spin text-blue-400 shrink-0" aria-hidden="true" />
          <span>Loading search log…</span>
        </div>}
        {events?.length === 0 && <p className="text-zinc-400">No search events recorded yet.</p>}
        {!!events?.length && <ol aria-label="Search log" className="max-h-56 divide-y divide-zinc-800/80 overflow-y-auto">
          {events.map(event => <li key={event.id} className="grid gap-1 py-2 first:pt-0 last:pb-0 sm:grid-cols-[11rem_1fr] sm:gap-3">
            <time dateTime={event.occurred_at} className="text-xs font-mono tabular-nums text-zinc-400">{new Date(event.occurred_at).toLocaleString()}</time>
            <span className="min-w-0 wrap-anywhere text-zinc-200">{event.message}</span>
          </li>)}
        </ol>}
      </div>}
    </section>
  } />;
}

export const ActivityView: React.FC<ActivityViewProps> = (props) => {
  const [manual, setManual] = useState<BookRequest | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [removed, setRemoved] = useState<string[]>([]);
  const [pending, setPending] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const items = activityItems(props).filter(item => !removed.includes(item.id));
  const detail = items.find(item => item.id === selected);
  const activeCount = items.filter(item => !isFinished(item)).length;
  const isInitialLoading = Boolean(props.loading) && items.length === 0;

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
        <div className="space-y-3" aria-hidden="true">
          <div className="h-24 rounded-xl border border-zinc-800 bg-zinc-900/90 motion-safe:animate-pulse" />
          <div className="h-24 rounded-xl border border-zinc-800 bg-zinc-900/90 motion-safe:animate-pulse" />
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
      <ul aria-label="Book activity" className="space-y-3">
        {items.map(item => {
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
            className="rounded-xl border border-zinc-800 bg-zinc-900/90 p-4 sm:p-5 space-y-3.5 transition-colors hover:border-zinc-700 focus-within:border-zinc-700"
          >
            <div className="flex flex-col sm:flex-row sm:items-start justify-between gap-3 sm:gap-4">
              <div className="flex items-start gap-3 sm:gap-3.5 min-w-0 flex-1">
                <ActivityThumbnail coverUrl={item.request?.coverUrl} format={item.format} />
                <div className="min-w-0 flex-1 space-y-2">
                  <button
                    type="button"
                    onClick={() => setSelected(item.id)}
                    aria-label={[item.title, [item.format, item.phase].filter(Boolean).join(' · ')].join(' ')}
                    className={`w-full sm:w-fit sm:max-w-full min-h-11 sm:min-h-0 flex flex-col justify-center rounded-sm text-left wrap-anywhere group ${focusRing}`}
                  >
                    <span className="text-sm font-semibold text-zinc-100 leading-snug underline decoration-zinc-700 underline-offset-4 group-hover:text-blue-300 group-hover:decoration-blue-300 transition-colors">
                      {item.title}
                    </span>
                    {item.author && (
                      <span className="text-xs text-zinc-400 mt-0.5">
                        by {item.author}
                      </span>
                    )}
                  </button>

                  <div className="flex flex-wrap items-center gap-1.5">
                    {item.format && (
                      <span className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[11px] font-mono font-medium ${formatBadgeTone}`}>
                        {item.format}
                      </span>
                    )}
                    <span className={`inline-flex items-center gap-1.5 rounded-sm border px-2 py-0.5 text-xs font-medium ${tone.badge}`}>
                      <PhaseIcon phase={item.phase} />
                      {statusText}
                    </span>
                  </div>
                </div>
              </div>

              {((allowRequest && item.request && item.request.status !== 'downloading') || path) && (
                <div className="flex flex-wrap items-center gap-2 shrink-0">
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
                        : `inline-flex items-center justify-center gap-1.5 min-h-11 sm:min-h-9 rounded-md border border-red-900/60 bg-red-950/30 px-3 py-1.5 text-xs font-medium text-red-300 transition-colors hover:bg-red-950/60 hover:text-red-200 disabled:opacity-50 disabled:cursor-not-allowed ${focusRing}`
                      }
                    >
                      <X className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
                      {action}
                    </button>
                  )}
                </div>
              )}
            </div>

            {(errors[item.id] || item.error) && (
              <div
                role="alert"
                className="rounded-lg border border-red-900/60 bg-red-950/30 px-3 py-2 flex items-start gap-2 text-xs text-red-200"
              >
                <AlertCircle className="w-4 h-4 text-red-400 shrink-0 mt-0.5" aria-hidden="true" />
                <span className="min-w-0 wrap-anywhere">{errors[item.id] || item.error}</span>
              </div>
            )}

            {item.progress !== undefined && (
              <div className="space-y-1.5 pt-0.5">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs tabular-nums">
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
                  className="h-2 rounded-full overflow-hidden bg-zinc-800"
                >
                  <div
                    className={`h-full rounded-full transition-[width] duration-300 ease-out ${tone.bar}`}
                    style={{ width: `${percent(item.progress)}%` }}
                  />
                </div>
              </div>
            )}
          </li>;
        })}
      </ul>
    ) : null}

    {manual && <ManualSearch key={manual.id} requestId={manual.id}
      requestFormat={manual.requestType === 'audiobook' ? 'm4b' : manual.requestType === 'ebook' ? 'epub' : 'both'}
      book={manual.bookId ? { id: manual.bookId, title: manual.title, author: manual.author } : undefined}
      onClose={() => setManual(null)} />}
    {detail && <ActivityDetails key={detail.id} item={detail} canManage={props.canManageJobs} canRequest={Boolean(props.canRequest ?? props.canManageJobs)} isAdmin={props.isAdmin}
      onMetadataApplied={props.onMetadataApplied} onClose={() => setSelected(null)} />}
  </section>;
};
