import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { App } from '../App';
import {
  testUser,
  testBooks,
  testAuthors,
  testSeries,
  testTranscodeJobs,
  testDownloads,
} from './testFixtures';

describe('App Integration', () => {
  beforeEach(() => {
    // @ts-ignore
    global.fetch = vi.fn((url: string) => {
      if (url.includes('/api/auth/me')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(testUser),
        });
      }
      if (url.includes('/api/books')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(testBooks),
        });
      }
      if (url.includes('/api/authors')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(testAuthors),
        });
      }
      if (url.includes('/api/series')) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(testSeries),
        });
      }
      if (url.includes('/api/requests')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve([
              {
                id: '1',
                title: 'Iron Sigil',
                author: 'Elena Rostova',
                username: 'reader_jane',
                format: 'both',
                status: 'pending',
                created_at: new Date().toISOString(),
              },
            ]),
        });
      }
      if (url.includes('/api/activity')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              transcodeJobs: testTranscodeJobs,
              downloads: testDownloads,
              logs: [],
            }),
        });
      }
      if (url.includes('/api/settings')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              indexers: [],
              downloadClients: [],
              qualityProfile: {
                id: 'default',
                name: 'Standard Audiobook',
                targetBitrateKbps: 64,
                codec: 'aac',
                sampleRateHz: 44100,
                channels: 1,
              },
            }),
        });
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve([]),
      });
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders Callimachus brand and default catalog view with real data', async () => {
    render(<App />);

    expect(screen.getByText('Callimachus')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Library & Catalog/i })).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText('The Obsidian Path')).toBeInTheDocument();
    });
  });

  it('navigates between views when clicking sidebar navigation', async () => {
    const user = userEvent.setup({ delay: null });
    render(<App />);

    // Navigate to Activity view
    const activityNav = screen.getByTestId('nav-activity');
    await user.click(activityNav);

    expect(screen.getByRole('heading', { name: /Activity & Queues/i })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId('tab-activity-transcode')).toBeInTheDocument();
    });

    expect(screen.queryByTestId('nav-requests')).not.toBeInTheDocument();
    expect(screen.queryByText('Request Portal')).not.toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Acquisitions' })).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: /Manual Search.*for Iron Sigil/ })).toBeInTheDocument();

    // Navigate to Settings view
    const settingsNav = screen.getByTestId('nav-settings');
    await user.click(settingsNav);

    expect(screen.getByRole('heading', { name: /System Settings/i })).toBeInTheDocument();
    expect(screen.getByText('Indexers & Prowlarr')).toBeInTheDocument();
  });

  it('counts downloading, converting and importing items and opens Activity', async () => {
    const user = userEvent.setup({ delay: null });
    const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
    vi.mocked(global.fetch).mockImplementation((...args) => {
      if (String(args[0]) === '/api/activity') {
        return Promise.resolve({
          ok: true,
          json: async () => ({
            downloads: ['downloading', 'importing', 'queued', 'paused', 'completed', 'error'].map((status, i) => ({
              ...testDownloads[0], id: `download-${i}`, status,
            })),
            transcodeJobs: ['processing', 'processing', 'queued', 'paused', 'completed', 'failed'].map((status, i) => ({
              ...testTranscodeJobs[0], id: `job-${i}`, status, stage: i === 1 ? 'moving' : 'encoding',
            })),
            logs: [],
          }),
        } as unknown as Response);
      }
      return originalFetch(...args);
    });
    render(<App />);
    const metric = await screen.findByRole('button', { name: 'Active Downloads: 4' });
    await user.click(metric);
    expect(screen.getByRole('heading', { name: /Activity & Queues/i })).toBeInTheDocument();
  });

  it('opens and closes BookDetailModal when selecting a book', async () => {
    const user = userEvent.setup({ delay: null });
    render(<App />);

    await waitFor(() => {
      expect(screen.getByTestId('book-card-book_1')).toBeInTheDocument();
    });

    // Click on book card
    const bookCard = screen.getByTestId('book-card-book_1');
    await user.click(bookCard);

    // Modal should be visible with metadata and file asset info
    await waitFor(() => {
      expect(screen.getByRole('dialog', { name: /Details for The Obsidian Path/i })).toBeInTheDocument();
    });
    expect(screen.getAllByText('Apex Horizon Press').length).toBeGreaterThan(0);
    expect(screen.getByText(/Jonathan Bell/)).toBeInTheDocument();

    // Close modal via close button
    const closeBtn = screen.getByTestId('close-book-detail');
    await user.click(closeBtn);

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: /Details for The Obsidian Path/i })).not.toBeInTheDocument();
    });
  });

  it('switches between grid and list view in catalog', async () => {
    const user = userEvent.setup({ delay: null });
    render(<App />);

    await waitFor(() => {
      expect(screen.getByTestId('book-card-book_1')).toBeInTheDocument();
    });

    // Switch to list view
    const listBtn = screen.getByTestId('view-list-btn');
    await user.click(listBtn);

    // Now table rows are present
    expect(screen.getByTestId('book-row-book_1')).toBeInTheDocument();

    // Switch back to grid view
    const gridBtn = screen.getByTestId('view-grid-btn');
    await user.click(gridBtn);
    expect(screen.getByTestId('book-card-book_1')).toBeInTheDocument();
  });

  it('redirects to /api/auth/oidc/login on 401 without rendering local sign-in UI or fetching protected endpoints', async () => {
    const assignMock = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...originalLocation, assign: assignMock },
    });

    try {
      // @ts-ignore
      global.fetch = vi.fn((url: string) => {
        if (url.includes('/api/auth/me')) {
          return Promise.resolve({
            ok: false,
            status: 401,
            json: () => Promise.resolve({ error: 'unauthorized' }),
          });
        }
        return Promise.resolve({
          ok: false,
          status: 401,
          json: () => Promise.resolve({ error: 'unauthorized' }),
        });
      });

      render(<App />);

      expect(screen.queryByTestId('login-link')).not.toBeInTheDocument();
      expect(screen.queryByRole('link', { name: /sign in/i })).not.toBeInTheDocument();

      await waitFor(() => {
        expect(assignMock).toHaveBeenCalledWith('/api/auth/oidc/login');
      });

      const calledUrls = (global.fetch as ReturnType<typeof vi.fn>).mock.calls.map(
        (c) => String(c[0])
      );
      expect(calledUrls).toEqual(['/api/auth/me']);
    } finally {
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: originalLocation,
      });
    }
  });
});
