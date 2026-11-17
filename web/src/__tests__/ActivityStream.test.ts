import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useActivityStream } from '../hooks/useActivityStream';
import { testConversionJobs, testDownloads } from './testFixtures';

// Mock EventSource
class MockEventSource {
  static instances: MockEventSource[] = [];
  listeners: Record<string, ((e: any) => void)[]> = {};
  url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  timeoutId: any = null;

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
    this.timeoutId = setTimeout(() => {
      if (this.onopen) this.onopen();
    }, 10);
  }

  addEventListener(type: string, callback: (e: any) => void) {
    if (!this.listeners[type]) this.listeners[type] = [];
    this.listeners[type].push(callback);
  }

  removeEventListener(type: string, callback: (e: any) => void) {
    if (this.listeners[type]) {
      this.listeners[type] = this.listeners[type].filter((cb) => cb !== callback);
    }
  }

  dispatchEvent(type: string, data: any) {
    const event = { data: JSON.stringify(data) };
    if (this.listeners[type]) {
      this.listeners[type].forEach((cb) => cb(event));
    }
  }

  close() {
    if (this.timeoutId) {
      clearTimeout(this.timeoutId);
      this.timeoutId = null;
    }
    this.listeners = {};
  }
}

describe('useActivityStream Hook', () => {
  beforeEach(() => {
    MockEventSource.instances = [];
    // @ts-ignore
    global.EventSource = MockEventSource;
  });

  afterEach(() => {
    MockEventSource.instances.forEach((es) => es.close());
    MockEventSource.instances = [];
    // @ts-ignore
    delete global.EventSource;
    vi.restoreAllMocks();
  });

  it('connects to activity stream endpoint and registers listeners', async () => {
    const { result } = renderHook(() =>
      useActivityStream({
        url: '/api/events',
        initialConversionJobs: testConversionJobs,
        initialDownloads: testDownloads,
      })
    );

    expect(MockEventSource.instances.length).toBe(1);
    const es = MockEventSource.instances[0];
    expect(es.url).toBe('/api/events');
    expect(es.listeners['conversion_progress']).toBeDefined();
    expect(es.listeners['download_progress']).toBeDefined();

    // Trigger onopen
    act(() => {
      if (es.onopen) es.onopen();
    });

    expect(result.current.connectionStatus).toBe('connected');
  });

  it('replaces the full activity snapshot as editions enter and leave the queue', () => {
    const { result } = renderHook(() => useActivityStream());
    act(() => MockEventSource.instances[0].dispatchEvent('activity_snapshot', {
      conversionJobs: testConversionJobs, downloads: testDownloads, logs: [],
    }));
    expect(result.current.conversionJobs).toEqual(testConversionJobs);
    expect(result.current.downloads).toEqual(testDownloads);
    act(() => MockEventSource.instances[0].dispatchEvent('activity_snapshot', {
      conversionJobs: [], downloads: [], logs: [],
    }));
    expect(result.current.conversionJobs).toEqual([]);
    expect(result.current.downloads).toEqual([]);
  });

  it('updates conversion job state when receiving conversion_progress event', () => {
    const { result } = renderHook(() =>
      useActivityStream({
        url: '/api/events',
        initialConversionJobs: testConversionJobs,
        initialDownloads: testDownloads,
      })
    );

    const es = MockEventSource.instances[0];

    // Dispatch update for job tc_101
    act(() => {
      es.dispatchEvent('conversion_progress', {
        id: 'tc_101',
        progressPercent: 94.2,
        stage: 'muxing',
        elapsedSeconds: 980,
        etaSeconds: 42,
      });
    });

    const updatedJob = result.current.conversionJobs.find((j) => j.id === 'tc_101');
    expect(updatedJob).toBeDefined();
    expect(updatedJob?.progressPercent).toBe(94.2);
    expect(updatedJob?.stage).toBe('muxing');
    expect(updatedJob?.etaSeconds).toBe(42);
  });

  it('updates download item state when receiving download_progress event', () => {
    const { result } = renderHook(() =>
      useActivityStream({
        url: '/api/events',
        initialConversionJobs: testConversionJobs,
        initialDownloads: testDownloads,
      })
    );

    const es = MockEventSource.instances[0];

    // Dispatch update for dl_201
    act(() => {
      es.dispatchEvent('download_progress', {
        id: 'dl_201',
        progressPercent: 99.1,
        downloadSpeedBps: 12500000,
        downloadedSizeBytes: 515000000,
        etaSeconds: 1,
      });
    });

    const updatedDl = result.current.downloads.find((d) => d.id === 'dl_201');
    expect(updatedDl).toBeDefined();
    expect(updatedDl?.progressPercent).toBe(99.1);
    expect(updatedDl?.downloadSpeedBps).toBe(12500000);
    expect(updatedDl?.etaSeconds).toBe(1);
  });

  it('handles manual conversion and download updates gracefully', () => {
    const { result } = renderHook(() =>
      useActivityStream({
        url: '/api/events',
        initialConversionJobs: testConversionJobs,
        initialDownloads: testDownloads,
      })
    );

    act(() => {
      result.current.handleConversionUpdate({
        id: 'tc_101',
        status: 'completed',
        progressPercent: 100,
      });
    });

    const job = result.current.conversionJobs.find((j) => j.id === 'tc_101');
    expect(job?.status).toBe('completed');
    expect(job?.progressPercent).toBe(100);
  });
});
