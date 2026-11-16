import { useState, useEffect, useRef, useCallback } from 'react';
import { TranscodeJob, DownloadItem } from '../types';

export interface ActivityStreamEvent {
  type: 'transcode_progress' | 'download_progress' | 'job_status_change';
  payload: any;
}

interface ActivityStreamOptions {
  url?: string;
  initialTranscodeJobs?: TranscodeJob[];
  initialDownloads?: DownloadItem[];
}

export function useActivityStream({
  url = '/api/events',
  initialTranscodeJobs = [],
  initialDownloads = [],
}: ActivityStreamOptions = {}) {
  const [transcodeJobs, setTranscodeJobs] = useState<TranscodeJob[]>(initialTranscodeJobs);
  const [downloads, setDownloads] = useState<DownloadItem[]>(initialDownloads);
  const [connectionStatus, setConnectionStatus] = useState<'connected' | 'connecting' | 'disconnected'>('connecting');
  const eventSourceRef = useRef<EventSource | null>(null);

  const handleTranscodeUpdate = useCallback((payload: Partial<TranscodeJob> & { id: string }) => {
    setTranscodeJobs((prev) =>
      prev.map((job) => (job.id === payload.id ? { ...job, ...payload } : job))
    );
  }, []);

  const handleDownloadUpdate = useCallback((payload: Partial<DownloadItem> & { id: string }) => {
    setDownloads((prev) =>
      prev.map((item) => (item.id === payload.id ? { ...item, ...payload } : item))
    );
  }, []);

  useEffect(() => {
    if (typeof EventSource === 'undefined') {
      setConnectionStatus('disconnected');
      return;
    }

    let retryTimeout: any = null;

    function connect() {
      try {
        setConnectionStatus('connecting');
        const es = new EventSource(url);
        eventSourceRef.current = es;

        es.onopen = () => {
          setConnectionStatus('connected');
        };

        es.addEventListener('transcode_progress', (e: MessageEvent) => {
          try {
            const data = JSON.parse(e.data);
            handleTranscodeUpdate(data);
          } catch (err) {
            console.error('Failed to parse activity stream transcode progress event', err);
          }
        });

        es.addEventListener('download_progress', (e: MessageEvent) => {
          try {
            const data = JSON.parse(e.data);
            handleDownloadUpdate(data);
          } catch (err) {
            console.error('Failed to parse activity stream download progress event', err);
          }
        });

        es.onerror = () => {
          setConnectionStatus('disconnected');
          es.close();
          retryTimeout = setTimeout(connect, 5000);
        };
      } catch {
        setConnectionStatus('disconnected');
        retryTimeout = setTimeout(connect, 5000);
      }
    }

    connect();

    return () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
      }
      if (retryTimeout) {
        clearTimeout(retryTimeout);
      }
    };
  }, [url, handleTranscodeUpdate, handleDownloadUpdate]);

  return {
    transcodeJobs,
    setTranscodeJobs,
    downloads,
    setDownloads,
    connectionStatus,
    handleTranscodeUpdate,
    handleDownloadUpdate,
  };
}
