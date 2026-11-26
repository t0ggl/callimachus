import { createContext } from 'react';
import { AcquisitionStage, BookRequest, ConversionJob, DownloadItem, ImportLog } from '../types';

export interface ActivityData {
  conversionJobs: ConversionJob[];
  downloads: DownloadItem[];
  logs: ImportLog[];
  requests?: BookRequest[];
}

export const percent = (value: number) => Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : 0;
const formats = { ebook: 'eBook', audiobook: 'Audiobook', both: 'eBook & Audiobook' };
export const stages: { id: AcquisitionStage; label: string }[] = [
  { id: 'queued', label: 'Queued' },
  { id: 'searching', label: 'Searching indexers' },
  { id: 'evaluating', label: 'Evaluating releases' },
  { id: 'downloading', label: 'Downloading' },
  { id: 'importing', label: 'Importing' },
  { id: 'available', label: 'Available' },
];

export interface ActivityItem {
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

export const isFinished = (item: ActivityItem) => ['Completed', 'Failed', 'Needs attention'].includes(item.phase);

export function activityItems({ conversionJobs, downloads, logs, requests = [] }: ActivityData): ActivityItem[] {
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

export const BookActivityContext = createContext<ActivityItem[]>([]);
