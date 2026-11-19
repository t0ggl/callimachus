import React, { useEffect, useState } from 'react';
import { AcquisitionStage, Book, BookRequest, ConversionJob, DownloadItem, ImportLog, RequestEvent } from '../types';
import { BookDetailModal } from '../components/BookDetailModal';
import { ManualSearch } from '../components/ManualSearch';

interface ActivityViewProps {
  conversionJobs: ConversionJob[];
  downloads: DownloadItem[];
  logs: ImportLog[];
  requests?: BookRequest[];
  canManageJobs?: boolean;
  connectionStatus: 'connected' | 'connecting' | 'disconnected';
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
const actionStyle = 'rounded px-2 py-1.5 text-sm hover:bg-zinc-800 focus-visible:outline-2 focus-visible:outline-blue-300 disabled:opacity-50';

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

function ActivityDetails({ item, onClose }: { item: ActivityItem; onClose: () => void }) {
  const [book, setBook] = useState<Book | null>(null);
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
  }, [item.bookId, item.stage === 'available']);

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

  return <BookDetailModal book={metadata} onClose={onClose} activity={
    <section aria-label="Acquisition progress" className="space-y-3 border-t border-zinc-800 pt-5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">{item.format || 'Acquisition'} · {item.phase}</h3>
        {requestId && <button type="button" aria-expanded={showLog} aria-controls="activity-search-log"
          className={`${actionStyle} text-blue-300`} onClick={() => setShowLog(visible => !visible)}>
          {showLog ? 'Hide Search Log' : 'View Search Log'}
        </button>}
      </div>
      <ol aria-label="Acquisition stages" className="grid grid-cols-3 gap-x-2 gap-y-3 sm:grid-cols-6">
        {stages.map((step, index) => <li key={step.id} aria-current={index === current ? 'step' : undefined}
          className={`space-y-1.5 text-xs ${index === current ? 'font-semibold text-blue-200' : 'text-zinc-300'}`}>
          <span aria-hidden="true" className={`block h-1 rounded-full ${index <= current ? 'bg-blue-400' : 'bg-zinc-700'}`} />
          <span>{step.label}</span>
        </li>)}
      </ol>
      {requestId && <div id="activity-search-log" hidden={!showLog} className="border-t border-zinc-800 pt-3 text-sm text-zinc-300">
        {logError && <p role="alert">Unable to load the search log. Retrying…</p>}
        {!logError && events === null && <p role="status">Loading search log…</p>}
        {events?.length === 0 && <p>No search events recorded yet.</p>}
        {!!events?.length && <ol aria-label="Search log" className="max-h-56 divide-y divide-zinc-800 overflow-y-auto">
          {events.map(event => <li key={event.id} className="grid gap-1 py-2 sm:grid-cols-[9rem_1fr]">
            <time dateTime={event.occurred_at} className="text-xs tabular-nums text-zinc-400">{new Date(event.occurred_at).toLocaleString()}</time>
            <span className="min-w-0 wrap-anywhere">{event.message}</span>
          </li>)}
        </ol>}
      </div>}
    </section>
  } />;
}

export const ActivityView: React.FC<ActivityViewProps> = (props) => {
  const [manual, setManual] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [removed, setRemoved] = useState<string[]>([]);
  const [pending, setPending] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const items = activityItems(props).filter(item => !removed.includes(item.id));
  const detail = items.find(item => item.id === selected);

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

  return <section aria-label="Activity" className="space-y-4">
    <h1 className="text-xl font-bold tracking-tight text-white border-b border-zinc-800 pb-4">Activity</h1>
    {items.length === 0 ? <p className="py-12 text-center text-zinc-300">No activity yet. Books you add will appear here.</p> :
      <ul aria-label="Book activity" className="divide-y divide-zinc-800">
        {items.map(item => {
          const path = item.request ? `/api/requests/${encodeURIComponent(item.request.id)}` : props.canManageJobs ?
            item.downloadId ? `/api/activity/downloads/${encodeURIComponent(item.downloadId)}` :
              item.conversionId ? `/api/activity/conversions/${encodeURIComponent(item.conversionId)}` : undefined : undefined;
          const busy = pending.includes(item.id);
          const action = busy ? 'Working…' : isFinished(item) ? 'Remove' : 'Cancel';
          return <li key={item.id} className="py-4 space-y-3" data-testid={`activity-${item.id}`}>
            <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
              <button type="button" onClick={() => setSelected(item.id)}
                aria-label={[item.title, [item.format, item.phase].filter(Boolean).join(' · ')].join(' ')}
                className="min-w-0 flex-1 rounded text-left wrap-anywhere focus-visible:outline-2 focus-visible:outline-blue-300 group">
                <span className="block text-sm font-semibold text-zinc-100 group-hover:underline underline-offset-4">{item.title}</span>
                <span className="block text-sm text-zinc-300">{[item.format, item.phase, item.detail].filter(Boolean).join(' · ')}</span>
              </button>
              <div className="flex flex-wrap gap-1">
                {item.request && item.request.status !== 'downloading' && <button type="button"
                  onClick={() => setManual(item.request!.id)} className={`${actionStyle} text-blue-300`}>Manual Search<span className="sr-only"> for {item.title}</span></button>}
                {path && <button type="button" disabled={busy} onClick={() => void dismiss(item.id, path)}
                  aria-label={[action, item.title, item.format].filter(Boolean).join(' ')}
                  className={`${actionStyle} ${isFinished(item) ? 'text-zinc-300' : 'text-red-300'}`}>
                  {action}
                </button>}
              </div>
            </div>
            {(errors[item.id] || item.error) && <p role="alert" className="text-sm text-red-300">{errors[item.id] || item.error}</p>}
            {item.progress !== undefined && <div className="space-y-1">
              <div className="flex flex-wrap justify-between gap-2 text-xs text-zinc-300 tabular-nums">
                <span>{percent(item.progress).toFixed(1)}%</span>
                {!isFinished(item) && <span>{`${item.speed ? `${item.speed} · ` : ''}ETA: ${duration(item.eta || 0)}`}</span>}
              </div>
              <div role="progressbar" aria-label={`${item.title} overall progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent(item.progress)} className="h-2 rounded-full overflow-hidden bg-zinc-800">
                <div className="h-full bg-blue-400" style={{ width: `${percent(item.progress)}%` }} />
              </div>
            </div>}
          </li>;
        })}
      </ul>}
    {manual && <ManualSearch requestId={manual} onClose={() => setManual(null)} />}
    {detail && <ActivityDetails key={detail.id} item={detail} onClose={() => setSelected(null)} />}
  </section>;
};
