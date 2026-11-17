import React, { useState } from 'react';
import { BookRequest, ConversionJob, DownloadItem, ImportLog } from '../types';
import { ManualSearch } from '../components/ManualSearch';

interface ActivityViewProps {
  conversionJobs: ConversionJob[];
  downloads: DownloadItem[];
  logs: ImportLog[];
  requests?: BookRequest[];
  connectionStatus: 'connected' | 'connecting' | 'disconnected';
}

const percent = (value: number) => Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : 0;
const duration = (seconds: number) => seconds > 0 ? `${Math.ceil(seconds / 60)} min` : 'Unavailable';

interface ActivityItem {
  id: string;
  title: string;
  format?: string;
  phase: string;
  detail?: string;
  progress?: number;
  eta?: number;
  speed?: string;
  error?: string;
  requestId?: string;
}

// IDs, never title guesses, join the same edition across pipeline phases.
export function activityItems({ conversionJobs, downloads, logs, requests = [] }: ActivityViewProps): ActivityItem[] {
  const items = new Map<string, ActivityItem>();
  const representedRequests = new Set(downloads.map(d => d.requestId).filter(Boolean));
  for (const log of logs) {
    const key = log.editionId || `log:${log.id}`;
    if (!items.has(key)) items.set(key, {
      id: key, title: log.itemName,
      phase: log.status === 'error' ? 'Failed' : log.status === 'warning' ? 'Needs attention' : 'Completed',
      detail: log.details, progress: log.status === 'success' ? 100 : undefined,
      error: log.status === 'error' ? log.details : undefined,
    });
  }
  for (const dl of downloads) {
    const key = dl.editionId || `download:${dl.id}`;
    if (items.has(key)) continue;
    items.set(key, {
      id: key, title: dl.title, format: dl.category === 'callimachus-ebooks' ? 'eBook' : 'Audiobook',
      phase: dl.status === 'error' ? 'Failed' : dl.status.charAt(0).toUpperCase() + dl.status.slice(1),
      detail: dl.status === 'importing' ? 'Organizing / Tagging' : undefined,
      progress: dl.status === 'completed' ? 100 : dl.status === 'importing' ? 90 : percent(dl.progressPercent) * 0.5,
      eta: dl.etaSeconds, speed: dl.downloadSpeedBps > 0 ? `${(dl.downloadSpeedBps / 1048576).toFixed(1)} MB/s` : undefined,
      error: dl.errorMessage, requestId: dl.requestId,
    });
  }
  const seenJobs = new Set<string>();
  for (const job of conversionJobs) {
    const key = job.editionId || `conversion:${job.id}`;
    if (seenJobs.has(key)) continue;
    seenJobs.add(key);
    const importing = job.stage === 'moving' || job.stage === 'tagging';
    const previous = items.get(key);
    items.set(key, {
      id: key, title: job.bookTitle || previous?.title || 'Untitled edition', format: job.targetFormat,
      phase: job.status === 'completed' ? 'Completed' : job.status === 'failed' ? 'Failed' : job.status === 'processing' ? importing ? 'Importing' : 'Converting' : job.status === 'queued' ? 'Queued' : 'Paused',
      detail: job.stage === 'preparing' ? 'Preparing' : importing ? job.stage === 'moving' ? 'Organizing' : 'Tagging' : job.stage === 'converting' ? 'Converting' : job.stage === 'muxing' ? 'Muxing' : undefined,
      progress: job.status === 'completed' ? 100 : 50 + percent(job.progressPercent) * 0.5,
      eta: job.etaSeconds, speed: job.bitrateKbps > 0 ? `${job.bitrateKbps} kbps` : undefined,
      error: job.errorMessage, requestId: previous?.requestId,
    });
  }
  for (const request of requests) {
    if (!representedRequests.has(request.id)) items.set(`request:${request.id}`, {
      id: `request:${request.id}`, title: request.title, format: request.requestType,
      phase: request.status === 'available' ? 'Completed' : request.status === 'error' ? 'Failed' : request.status === 'searching' ? 'Searching' : 'Downloading',
      progress: request.status === 'available' ? 100 : undefined, requestId: request.id,
    });
  }
  return [...items.values()].sort((a, b) => Number(a.phase === 'Completed') - Number(b.phase === 'Completed'));
}

export const ActivityView: React.FC<ActivityViewProps> = (props) => {
  const [manual, setManual] = useState<string | null>(null);
  const items = activityItems(props);
  return <section aria-label="Activity" className="space-y-4">
    <h1 className="text-xl font-bold tracking-tight text-white border-b border-zinc-800 pb-4">Activity</h1>
    {items.length === 0 ? <p className="py-12 text-center text-zinc-300">No activity yet. Books you add will appear here.</p> :
      <ul aria-label="Book activity" className="divide-y divide-zinc-800">
        {items.map(item => <li key={item.id} className="py-4 space-y-3" data-testid={`activity-${item.id}`}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0 break-words"><h2 className="text-sm font-semibold text-zinc-100">{item.title}</h2>
              <p className="text-sm text-zinc-300">{item.format} · {item.phase}{item.detail && ` · ${item.detail}`}</p>
            </div>
            {item.requestId && <button type="button" onClick={() => setManual(item.requestId!)} className="text-sm text-blue-300 hover:underline focus-visible:outline-2 focus-visible:outline-blue-400">Manual Search<span className="sr-only"> for {item.title}</span></button>}
          </div>
          {item.error && <p role="alert" className="text-sm text-red-300">{item.error}</p>}
          {item.progress !== undefined && <div className="space-y-1">
            <div className="flex flex-wrap justify-between gap-2 text-xs text-zinc-300 tabular-nums">
              <span>{percent(item.progress).toFixed(1)}%</span>
              {item.phase !== 'Completed' && <span>{item.speed && `${item.speed} · `}ETA: {duration(item.eta || 0)}</span>}
            </div>
            <div role="progressbar" aria-label={`${item.title} overall progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent(item.progress)} className="h-2 rounded-full overflow-hidden bg-zinc-800">
              <div className="h-full bg-blue-400" style={{ width: `${percent(item.progress)}%` }} />
            </div>
          </div>}
        </li>)}
      </ul>}
    {manual && <ManualSearch requestId={manual} onClose={() => setManual(null)} />}
  </section>;
};
