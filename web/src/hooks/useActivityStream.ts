import { useState, useEffect, useRef, useCallback } from 'react';
import { ConversionJob, DownloadItem, ImportLog } from '../types';

export interface ActivityStreamEvent {
  type: 'conversion_progress' | 'download_progress' | 'job_status_change';
  payload: any;
}

interface ActivityStreamOptions {
  url?: string;
  initialConversionJobs?: ConversionJob[];
  initialDownloads?: DownloadItem[];
}

export function useActivityStream({
  url = '/api/events',
  initialConversionJobs = [],
  initialDownloads = [],
}: ActivityStreamOptions = {}) {
  const [conversionJobs, setConversionJobs] = useState<ConversionJob[]>(initialConversionJobs);
  const [logs, setLogs] = useState<ImportLog[]>([]);
  const [downloads, setDownloads] = useState<DownloadItem[]>(initialDownloads);
  const [connectionStatus, setConnectionStatus] = useState<'connected' | 'connecting' | 'disconnected'>('connecting');
  const eventSourceRef = useRef<EventSource | null>(null);

  const handleConversionUpdate = useCallback((payload: Partial<ConversionJob> & { id: string }) => {
    setConversionJobs((prev) =>
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

        es.addEventListener('activity_snapshot', (e: MessageEvent) => {
          try {
            const data = JSON.parse(e.data);
            setConversionJobs(data.conversionJobs || []);
            setDownloads(data.downloads || []);
            setLogs(data.logs || []);
          } catch (err) { console.error('Failed to read activity update', err); }
        });

        es.addEventListener('conversion_progress', (e: MessageEvent) => {
          try {
            const data = JSON.parse(e.data);
            handleConversionUpdate(data);
          } catch (err) {
            console.error('Failed to parse activity stream conversion progress event', err);
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
  }, [url, handleConversionUpdate, handleDownloadUpdate]);

  return {
    logs, setLogs,
    conversionJobs,
    setConversionJobs,
    downloads,
    setDownloads,
    connectionStatus,
    handleConversionUpdate,
    handleDownloadUpdate,
  };
}
