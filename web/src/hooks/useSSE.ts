import { useState, useEffect, useRef, useCallback } from 'react';
import { TranscodeJob, DownloadItem } from '../types';

export interface SSEMessage {
  type: 'transcode_progress' | 'download_progress' | 'job_status_change';
  payload: any;
}

interface UseSSEOptions {
  url?: string;
  initialTranscodeJobs?: TranscodeJob[];
  initialDownloads?: DownloadItem[];
  enableMockFallback?: boolean;
}

export function useSSE({
  url = '/api/events',
  initialTranscodeJobs = [],
  initialDownloads = [],
  enableMockFallback = true,
}: UseSSEOptions = {}) {
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
    let mockInterval: any = null;

    if (typeof EventSource !== 'undefined') {
      try {
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
            console.error('Failed to parse SSE transcode progress event', err);
          }
        });

        es.addEventListener('download_progress', (e: MessageEvent) => {
          try {
            const data = JSON.parse(e.data);
            handleDownloadUpdate(data);
          } catch (err) {
            console.error('Failed to parse SSE download progress event', err);
          }
        });

        es.onerror = () => {
          setConnectionStatus('disconnected');
          es.close();

          // Fallback simulation in dev / offline demo mode if enabled
          if (enableMockFallback && !mockInterval) {
            startMockFallback();
          }
        };
      } catch (err) {
        setConnectionStatus('disconnected');
        if (enableMockFallback) {
          startMockFallback();
        }
      }
    } else if (enableMockFallback) {
      startMockFallback();
    }

    function startMockFallback() {
      // Simulate live transcode progress tick
      mockInterval = setInterval(() => {
        setTranscodeJobs((prevJobs) =>
          prevJobs.map((job) => {
            if (job.status !== 'processing') return job;
            const nextProgress = Math.min(100, +(job.progressPercent + 0.8).toFixed(1));
            const nextEta = Math.max(0, job.etaSeconds - 1);
            let nextStage = job.stage;
            if (nextProgress > 95) nextStage = 'moving';
            else if (nextProgress > 85) nextStage = 'muxing';
            else if (nextProgress > 20) nextStage = 'encoding';

            return {
              ...job,
              progressPercent: nextProgress,
              etaSeconds: nextEta,
              stage: nextStage,
              elapsedSeconds: job.elapsedSeconds + 1,
            };
          })
        );

        setDownloads((prevDls) =>
          prevDls.map((dl) => {
            if (dl.status !== 'downloading') return dl;
            const nextProgress = Math.min(100, +(dl.progressPercent + 0.5).toFixed(1));
            const nextDownloaded = Math.min(dl.totalSizeBytes, dl.downloadedSizeBytes + dl.downloadSpeedBps);
            const nextEta = Math.max(0, dl.etaSeconds - 1);
            return {
              ...dl,
              progressPercent: nextProgress,
              downloadedSizeBytes: nextDownloaded,
              etaSeconds: nextEta,
              status: nextProgress >= 100 ? 'completed' : 'downloading',
            };
          })
        );
      }, 1000);
    }

    return () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
      }
      if (mockInterval) {
        clearInterval(mockInterval);
      }
    };
  }, [url, enableMockFallback, handleTranscodeUpdate, handleDownloadUpdate]);

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
