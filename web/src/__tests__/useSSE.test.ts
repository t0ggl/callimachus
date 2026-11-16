import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useSSE } from '../hooks/useSSE';
import { mockTranscodeJobs, mockDownloads } from '../data/mockData';

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

describe('useSSE Hook', () => {
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

  it('connects to SSE endpoint and registers listeners', async () => {
    const { result } = renderHook(() =>
      useSSE({
        url: '/api/events',
        initialTranscodeJobs: mockTranscodeJobs,
        initialDownloads: mockDownloads,
        enableMockFallback: false,
      })
    );

    expect(MockEventSource.instances.length).toBe(1);
    const es = MockEventSource.instances[0];
    expect(es.url).toBe('/api/events');
    expect(es.listeners['transcode_progress']).toBeDefined();
    expect(es.listeners['download_progress']).toBeDefined();

    // Trigger onopen
    act(() => {
      if (es.onopen) es.onopen();
    });

    expect(result.current.connectionStatus).toBe('connected');
  });

  it('updates transcode job state when receiving transcode_progress event', () => {
    const { result } = renderHook(() =>
      useSSE({
        url: '/api/events',
        initialTranscodeJobs: mockTranscodeJobs,
        initialDownloads: mockDownloads,
        enableMockFallback: false,
      })
    );

    const es = MockEventSource.instances[0];

    // Dispatch update for job tc_101
    act(() => {
      es.dispatchEvent('transcode_progress', {
        id: 'tc_101',
        progressPercent: 94.2,
        stage: 'muxing',
        elapsedSeconds: 980,
        etaSeconds: 42,
      });
    });

    const updatedJob = result.current.transcodeJobs.find((j) => j.id === 'tc_101');
    expect(updatedJob).toBeDefined();
    expect(updatedJob?.progressPercent).toBe(94.2);
    expect(updatedJob?.stage).toBe('muxing');
    expect(updatedJob?.etaSeconds).toBe(42);
  });

  it('updates download item state when receiving download_progress event', () => {
    const { result } = renderHook(() =>
      useSSE({
        url: '/api/events',
        initialTranscodeJobs: mockTranscodeJobs,
        initialDownloads: mockDownloads,
        enableMockFallback: false,
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

  it('handles manual transcode and download updates gracefully', () => {
    const { result } = renderHook(() =>
      useSSE({
        url: '/api/events',
        initialTranscodeJobs: mockTranscodeJobs,
        initialDownloads: mockDownloads,
        enableMockFallback: false,
      })
    );

    act(() => {
      result.current.handleTranscodeUpdate({
        id: 'tc_102',
        stage: 'encoding',
        progressPercent: 50.0,
      });
    });

    const updatedJob = result.current.transcodeJobs.find((j) => j.id === 'tc_102');
    expect(updatedJob?.stage).toBe('encoding');
    expect(updatedJob?.progressPercent).toBe(50.0);
  });
});
