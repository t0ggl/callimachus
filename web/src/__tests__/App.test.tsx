import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { App } from '../App';
import {
  testUser,
  testBooks,
  testAuthors,
  testSeries,
  testConversionJobs,
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
              conversionJobs: testConversionJobs,
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

  it('loads the effective log level and applies the server response after saving', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const existingFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, options) => {
      if (url !== '/api/settings') return existingFetch(url, options);
      const envVar = 'CALLIMACHUS_LOG_LEVEL';
      const data = options?.method === 'PUT'
        ? { logLevel: 'warn', overrides: { log_level: { isOverridden: true, overriddenBy: envVar, envVar } } }
        : { logLevel: 'error' };
      return Promise.resolve(new Response(JSON.stringify(data)));
    });

    render(<App />);
    await user.click(await screen.findByRole('button', { name: /^Settings/ }));
    await user.click(await screen.findByRole('button', { name: 'Logging' }));
    const select = screen.getByRole('combobox', { name: 'Log Level' });
    await waitFor(() => expect(select).toHaveValue('error'));
    await user.selectOptions(select, 'debug');
    await user.click(screen.getByTestId('save-settings-btn'));

    const put = fetchMock.mock.calls.find(([url, options]) => url === '/api/settings' && options?.method === 'PUT');
    expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({ logLevel: 'debug' });
    await waitFor(() => {
      expect(select).toHaveValue('warn');
      expect(select).toBeDisabled();
    });
  });

  it('loads OpenLibrary enabled state and applies the server response after saving', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const existingFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, options) => {
      if (url !== '/api/settings') return existingFetch(url, options);
      const envVar = 'CALLIMACHUS_OPENLIBRARY_ENABLED';
      const data = options?.method === 'PUT'
        ? { metadata: { openlibrary_enabled: true }, overrides: { openlibrary_enabled: { isOverridden: true, overriddenBy: envVar, envVar } } }
        : { metadata: { openlibrary_enabled: false } };
      return Promise.resolve(new Response(JSON.stringify(data)));
    });

    render(<App />);
    await user.click(await screen.findByRole('button', { name: /^Settings/ }));
    const toggle = await screen.findByRole('checkbox', { name: 'Enable OpenLibrary metadata provider' });
    await waitFor(() => expect(toggle).not.toBeChecked());
    await user.click(toggle);
    await user.click(screen.getByTestId('save-settings-btn'));

    const put = fetchMock.mock.calls.find(([url, options]) => url === '/api/settings' && options?.method === 'PUT');
    expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({ metadata: { openlibrary_enabled: 'true' } });
    await waitFor(() => {
      expect(toggle).toBeChecked();
      expect(toggle).toBeDisabled();
    });
  });

  it('renders Callimachus brand and default catalog view with real data', async () => {
    render(<App />);

    expect(screen.getByText('Callimachus')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Library/i })).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText('The Obsidian Archive')).toBeInTheDocument();
    });
    expect(screen.getByTestId('series-card-ser_1')).toBeInTheDocument();
  });

  it('navigates between views when clicking sidebar navigation', async () => {
    const user = userEvent.setup({ delay: null });
    render(<App />);

    // Navigate to Activity view
    const activityNav = screen.getByTestId('nav-activity');
    await user.click(activityNav);

    expect(screen.getByRole('heading', { name: /Activity/i })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByRole('list', { name: 'Book activity' })).toBeInTheDocument();
    });

    expect(screen.queryByTestId('nav-requests')).not.toBeInTheDocument();
    expect(screen.queryByText('Request Portal')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Acquisitions' })).not.toBeInTheDocument();
    expect(await screen.findByRole('button', { name: /Manual Search.*for Iron Sigil/ })).toBeInTheDocument();

    // Navigate to Settings view
    const settingsNav = screen.getByTestId('nav-settings');
    await user.click(settingsNav);

    expect(screen.getByRole('heading', { name: /System Settings/i })).toBeInTheDocument();
    expect(screen.getByText('Indexers & Metadata')).toBeInTheDocument();
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
            conversionJobs: ['processing', 'processing', 'queued', 'paused', 'completed', 'failed'].map((status, i) => ({
              ...testConversionJobs[0], id: `job-${i}`, status, stage: i === 1 ? 'moving' : 'converting',
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
    expect(screen.getByRole('heading', { name: /Activity/i })).toBeInTheDocument();
  });

  it('opens and closes BookDetailModal when selecting a standalone book', async () => {
    const user = userEvent.setup({ delay: null });
    const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
    vi.mocked(global.fetch).mockImplementation((...args) => {
      if (String(args[0]).includes('/api/books')) {
        return Promise.resolve({
          ok: true,
          json: async () => [
            {
              ...testBooks[0],
              seriesId: undefined,
              seriesTitle: undefined,
              seriesIndex: undefined,
              seriesIndexDisplay: undefined,
            },
          ],
        } as unknown as Response);
      }
      return originalFetch(...args);
    });
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

  it('opens SeriesDetailModal when clicking a bundled series card and opens owned BookDetailModal from it', async () => {
    const user = userEvent.setup({ delay: null });
    const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
    vi.mocked(global.fetch).mockImplementation((...args) => {
      if (String(args[0]).startsWith('/api/series/books')) {
        return Promise.resolve({
          ok: true,
          json: async () => ({
            title: 'The Obsidian Archive',
            status: 'ongoing',
            monitored: true,
            authors: ['Alden Thorne'],
            total_books: 2,
            owned_count: 1,
            complete: true,
            books: [
              {
                title: 'The Obsidian Path',
                authors: ['Alden Thorne'],
                series_index: 1,
                series_index_display: 'Book 1',
                formats: ['EPUB', 'M4B'],
                library_book_id: 'book_1',
                monitored: true,
              },
            ],
          }),
        } as unknown as Response);
      }
      return originalFetch(...args);
    });

    render(<App />);

    const seriesCard = await screen.findByTestId('series-card-ser_1');
    await user.click(seriesCard);

    const seriesDialog = await screen.findByRole('dialog', { name: 'The Obsidian Archive' });
    expect(seriesDialog).toBeInTheDocument();

    await user.click(await screen.findByRole('button', { name: 'The Obsidian Path' }));
    await waitFor(() => {
      expect(screen.getByRole('dialog', { name: /Details for The Obsidian Path/i })).toBeInTheDocument();
    });
  });

  it('switches between grid and list view in catalog', async () => {
    const user = userEvent.setup({ delay: null });
    render(<App />);

    await waitFor(() => {
      expect(screen.getByTestId('series-card-ser_1')).toBeInTheDocument();
    });

    // Switch to list view
    const listBtn = screen.getByTestId('view-list-btn');
    await user.click(listBtn);

    // Now table rows are present
    expect(screen.getByTestId('series-row-ser_1')).toBeInTheDocument();

    // Switch back to grid view
    const gridBtn = screen.getByTestId('view-grid-btn');
    await user.click(gridBtn);
    expect(screen.getByTestId('series-card-ser_1')).toBeInTheDocument();
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

  it('allows admin users to access Settings via shortcut 3, command palette, and fetches /api/settings', async () => {
    const user = userEvent.setup({ delay: null });
    render(<App />);

    await waitFor(() => {
      expect(screen.getByTestId('nav-settings')).toBeInTheDocument();
    });

    await waitFor(() => expect(global.fetch).toHaveBeenCalledWith('/api/settings'));

    // Keyboard shortcut '3' navigates to Settings for admin
    await user.keyboard('3');
    expect(screen.getByRole('heading', { name: /System Settings/i })).toBeInTheDocument();

    // Command palette shows Settings for admin
    await user.keyboard('{Control>}k{/Control}');
    expect(await screen.findByTestId('palette-item-nav_settings')).toBeInTheDocument();
  });

  it.each(['curator', 'requester', 'guest'] as const)(
    'hides Settings UI, blocks shortcut 3, and skips /api/settings fetch for %s users',
    async (role) => {
      const user = userEvent.setup({ delay: null });
      const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
      vi.mocked(global.fetch).mockImplementation((...args) => {
        if (String(args[0]).includes('/api/auth/me')) {
          return Promise.resolve({
            ok: true,
            json: () =>
              Promise.resolve({
                ...testUser,
                role,
              }),
          } as unknown as Response);
        }
        return originalFetch(...args);
      });

      render(<App />);

      await waitFor(() => {
        expect(screen.getByText('The Obsidian Archive')).toBeInTheDocument();
      });

      // Sidebar Settings link must not be present
      expect(screen.queryByTestId('nav-settings')).not.toBeInTheDocument();

      // /api/settings must never be requested
      const calledUrls = (global.fetch as ReturnType<typeof vi.fn>).mock.calls.map(
        (c) => String(c[0])
      );
      expect(calledUrls).not.toContain('/api/settings');

      // Keyboard shortcut '3' must not switch to Settings
      await user.keyboard('3');
      expect(screen.queryByRole('heading', { name: /System Settings/i })).not.toBeInTheDocument();
      expect(screen.getByRole('heading', { name: /Library/i })).toBeInTheDocument();

      // Command palette must not show Settings
      await user.keyboard('{Control>}k{/Control}');
      expect(await screen.findByTestId('command-palette-input')).toBeInTheDocument();
      expect(screen.queryByTestId('palette-item-nav_settings')).not.toBeInTheDocument();
    }
  );

  it('applies non-admin bundling preference from /api/auth/me without requesting /api/settings', async () => {
    const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
    vi.mocked(global.fetch).mockImplementation((...args) => {
      if (String(args[0]).includes('/api/auth/me')) {
        return Promise.resolve({
          ok: true,
          json: async () => ({
            ...testUser,
            role: 'requester',
            preferences: { bundleSeriesInLibrary: false },
          }),
        } as unknown as Response);
      }
      return originalFetch(...args);
    });

    render(<App />);

    await waitFor(() => {
      expect(screen.getByTestId('book-card-book_1')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('series-card-ser_1')).not.toBeInTheDocument();

    const calledUrls = (global.fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0]));
    expect(calledUrls).not.toContain('/api/settings');
  });

  it('allows admin to toggle bundle series in Settings and updates Library view', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const existingFetch = fetchMock.getMockImplementation()!;
    let currentBundle = true;
    fetchMock.mockImplementation((url, options) => {
      if (url !== '/api/settings') return existingFetch(url, options);
      if (options?.method === 'PUT') {
        const body = JSON.parse(String(options.body));
        currentBundle = Boolean(body.library?.bundleSeriesInLibrary);
      }
      return Promise.resolve(
        new Response(
          JSON.stringify({
            indexers: [],
            library: {
              rootPath: './books',
              stagingPollInterval: '5s',
              indexerSyncInterval: '15m0s',
              bundleSeriesInLibrary: currentBundle,
            },
            qualityProfile: {
              id: 'default',
              name: 'Standard Audiobook',
            },
          })
        )
      );
    });

    render(<App />);
    await waitFor(() => expect(screen.getByTestId('series-card-ser_1')).toBeInTheDocument());

    await user.click(await screen.findByTestId('nav-settings'));
    await user.click(await screen.findByTestId('tab-settings-library'));

    const toggle = screen.getByRole('checkbox', { name: 'Bundle series in Library view' });
    expect(toggle).toBeChecked();
    await user.click(toggle);
    expect(toggle).not.toBeChecked();

    await user.click(screen.getByTestId('save-settings-btn'));
    await waitFor(() => expect(screen.getByTestId('settings-save-success')).toBeInTheDocument());

    const put = fetchMock.mock.calls.find(([url, opts]) => url === '/api/settings' && opts?.method === 'PUT');
    expect(JSON.parse(String(put?.[1]?.body)).library).toMatchObject({ bundleSeriesInLibrary: false });

    await user.click(screen.getByTestId('nav-catalog'));
    await waitFor(() => expect(screen.getByTestId('book-card-book_1')).toBeInTheDocument());
    expect(screen.queryByTestId('series-card-ser_1')).not.toBeInTheDocument();
  });

  it('renders access-denied fallback instead of SettingsView if a non-admin reaches settings state', async () => {
    const user = userEvent.setup({ delay: null });
    const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
    vi.mocked(global.fetch).mockImplementation((...args) => {
      if (String(args[0]).includes('/api/auth/me')) {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              ...testUser,
              role: 'curator',
            }),
        } as unknown as Response);
      }
      return originalFetch(...args);
    });

    render(<App initialView="settings" />);

    await waitFor(() => {
      expect(screen.getByTestId('settings-access-denied')).toBeInTheDocument();
    });
    expect(screen.queryByRole('heading', { name: /System Settings/i })).not.toBeInTheDocument();
    expect(screen.queryByTestId('save-settings-btn')).not.toBeInTheDocument();

    const calledUrls = (global.fetch as ReturnType<typeof vi.fn>).mock.calls.map(
      (c) => String(c[0])
    );
    expect(calledUrls).not.toContain('/api/settings');

    await user.click(screen.getByRole('button', { name: /Return to Library/i }));
    expect(screen.getByRole('heading', { name: /Library/i })).toBeInTheDocument();
  });
});
