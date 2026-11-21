import { render, screen, waitFor, within } from '@testing-library/react';
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
    window.location.hash = '';
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

  it('loads stored API keys from /api/settings masked by default and saves changes cleanly', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const existingFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, options) => {
      if (url !== '/api/settings') return existingFetch(url, options);
      const data = options?.method === 'PUT'
        ? {
            prowlarr: { url: 'http://prowlarr:9696', apiKey: 'new-prowlarr-key', apiKeyConfigured: true, syncInterval: '1h0m0s' },
            metadata: { google_books_api_key: '', hardcover_api_key: 'saved-hardcover-key', openlibrary_enabled: true },
          }
        : {
            prowlarr: { url: 'http://prowlarr:9696', apiKey: 'saved-prowlarr-key', apiKeyConfigured: true, syncInterval: '1h0m0s' },
            metadata: { google_books_api_key: 'saved-google-key', hardcover_api_key: 'saved-hardcover-key', openlibrary_enabled: true },
          };
      return Promise.resolve(new Response(JSON.stringify(data)));
    });

    render(<App />);
    await user.click(await screen.findByRole('button', { name: /^Settings/ }));

    const prowlarrInput = await screen.findByLabelText('Prowlarr API Key');
    const googleInput = screen.getByLabelText('Google Books API key (optional)');
    const hardcoverInput = screen.getByLabelText('Hardcover API key');

    await waitFor(() => expect(prowlarrInput).toHaveValue('saved-prowlarr-key'));
    expect(prowlarrInput).toHaveAttribute('type', 'password');
    expect(googleInput).toHaveValue('saved-google-key');
    expect(googleInput).toHaveAttribute('type', 'password');
    expect(hardcoverInput).toHaveValue('saved-hardcover-key');
    expect(hardcoverInput).toHaveAttribute('type', 'password');

    await user.clear(prowlarrInput);
    await user.type(prowlarrInput, 'new-prowlarr-key');
    await user.clear(googleInput);
    await user.click(screen.getByTestId('save-settings-btn'));

    const put = fetchMock.mock.calls.find(([url, options]) => url === '/api/settings' && options?.method === 'PUT');
    const sent = JSON.parse(String(put?.[1]?.body));
    expect(sent.prowlarr.apiKey).toBe('new-prowlarr-key');
    expect(sent.metadata.google_books_api_key).toBe('');
    expect(sent.metadata).not.toHaveProperty('hardcover_api_key');

    await waitFor(() => expect(prowlarrInput).toHaveValue('new-prowlarr-key'));
    expect(googleInput).toHaveValue('');
    expect(hardcoverInput).toHaveValue('saved-hardcover-key');
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
    const metric = await screen.findByRole('button', { name: 'Activity 4' });
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

  it('removes a deleted standalone book from the library when an admin deletes it', async () => {
    const user = userEvent.setup({ delay: null });
    const standalone = {
      ...testBooks[0],
      seriesId: undefined,
      seriesTitle: undefined,
      seriesIndex: undefined,
      seriesIndexDisplay: undefined,
    };
    let currentBooks = [standalone];
    const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
    vi.mocked(global.fetch).mockImplementation((url, options) => {
      const target = String(url);
      if (target === '/api/books') {
        return Promise.resolve({
          ok: true,
          json: async () => currentBooks,
        } as unknown as Response);
      }
      if (target === `/api/books/${standalone.id}` && options?.method === 'DELETE') {
        currentBooks = [];
        return Promise.resolve({
          ok: true,
          json: async () => ({ deleted: true, deletedFiles: 0 }),
        } as unknown as Response);
      }
      return originalFetch(url, options);
    });
    render(<App />);

    await user.click(await screen.findByTestId('book-card-book_1'));
    const dialog = await screen.findByRole('dialog', { name: /Details for The Obsidian Path/i });
    await user.click(within(dialog).getByRole('button', { name: 'Delete Book' }));
    await user.click(within(dialog).getByRole('button', { name: 'Confirm Delete' }));

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: /Details for The Obsidian Path/i })).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.queryByTestId('book-card-book_1')).not.toBeInTheDocument();
    });
  });

  it('opens author book details with the keyboard from the library', async () => {
    const user = userEvent.setup({ delay: null });
    const book = testBooks[0];
    const fetchMock = vi.mocked(global.fetch);
    const originalFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, options) => {
      if (String(url).startsWith('/api/catalog/entity?kind=authors&')) {
        return Promise.resolve(new Response(JSON.stringify({ monitored: false, books: [book] })));
      }
      return originalFetch(url, options);
    });
    const originalUrl = window.location.href;
    window.history.replaceState(null, '', `#/authors/${encodeURIComponent(book.authorName)}`);
    try {
      render(<App />);
      const title = await screen.findByRole('button', { name: book.title });
      title.focus();
      await user.keyboard('{Enter}');
      expect(fetchMock).toHaveBeenCalledWith(`/api/books?id=${book.id}`, expect.anything());
      expect(await screen.findByRole('dialog', { name: `Details for ${book.title}` })).toBeInTheDocument();
    } finally {
      window.history.replaceState(null, '', originalUrl);
    }
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
                series_index_display: '#1',
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
    expect(within(seriesDialog).getAllByRole('listitem')).toHaveLength(1);
    expect(within(seriesDialog).getByText('Book 1')).toBeInTheDocument();

    await user.click(await screen.findByRole('button', { name: 'The Obsidian Path' }));
    await waitFor(() => {
      const detail = screen.getByRole('dialog', { name: /Details for The Obsidian Path/i });
      expect(within(detail).getByText('(Book 1)')).toBeInTheDocument();
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

  it('applies non-admin UI preferences from /api/auth/me without requesting /api/settings', async () => {
    const user = userEvent.setup({ delay: null });
    const originalFetch = vi.mocked(global.fetch).getMockImplementation()!;
    vi.mocked(global.fetch).mockImplementation((...args) => {
      if (String(args[0]).includes('/api/auth/me')) {
        return Promise.resolve({
          ok: true,
          json: async () => ({
            ...testUser,
            role: 'requester',
            preferences: { bundleSeriesInLibrary: false, defaultSearchFormat: 'audiobook' },
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

    await user.keyboard('{Control>}k{/Control}');
    await user.type(screen.getByTestId('command-palette-input'), 'Dune');
    expect(screen.getByRole('combobox', { name: 'Format' })).toHaveValue('audiobook');

    const calledUrls = (global.fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0]));
    expect(calledUrls).not.toContain('/api/settings');
  });

  it('uses the saved admin setting the next time search is opened', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const existingFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, options) => {
      if (url !== '/api/settings') return existingFetch(url, options);
      const format = options?.method === 'PUT'
        ? JSON.parse(String(options.body)).defaultSearchFormat
        : 'both';
      return Promise.resolve(new Response(JSON.stringify({ defaultSearchFormat: format })));
    });

    render(<App />);
    await user.click(await screen.findByTestId('nav-settings'));
    await user.click(screen.getByTestId('tab-settings-library'));
    const select = screen.getByRole('combobox', { name: 'Default Search/Add Format' });
    await waitFor(() => expect(select).toHaveValue('both'));
    await user.selectOptions(select, 'audiobook');
    await user.click(screen.getByTestId('save-settings-btn'));
    await screen.findByTestId('settings-save-success');

    await user.keyboard('{Control>}k{/Control}');
    await user.type(screen.getByTestId('command-palette-input'), 'Dune');
    expect(screen.getByRole('combobox', { name: 'Format' })).toHaveValue('audiobook');
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

  it('shows an error alert and retries when loading activity fails', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const originalFetch = fetchMock.getMockImplementation()!;
    let activityAttempts = 0;
    fetchMock.mockImplementation((url, options) => {
      if (String(url) === '/api/activity') {
        activityAttempts += 1;
        if (activityAttempts === 1) {
          return Promise.resolve(new Response(null, { status: 500 }));
        }
        return Promise.resolve(
          new Response(JSON.stringify({ conversionJobs: testConversionJobs, downloads: testDownloads, logs: [] }))
        );
      }
      return originalFetch(url, options);
    });

    render(<App />);
    await user.click(screen.getByTestId('nav-activity'));

    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load activity. Try again.');
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('list', { name: 'Book activity' })).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('allows guests to view unowned series book details from discovery without triggering catalog writes', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const originalFetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, options) => {
      if (String(url).includes('/api/auth/me')) {
        return Promise.resolve({
          ok: true,
          json: async () => ({ ...testUser, role: 'guest' }),
        } as unknown as Response);
      }
      if (String(url).startsWith('/api/discover?')) {
        return Promise.resolve(
          new Response(
            JSON.stringify([
              { title: 'Guest Discovery Book', authors: ['Guest Author'], year: 2024, series: 'Guest Chronicle', series_index: 2 },
            ])
          )
        );
      }
      if (String(url).startsWith('/api/series/books?')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              title: 'Guest Chronicle',
              monitored: false,
              authors: ['Guest Author'],
              total_books: 1,
              owned_count: 0,
              complete: true,
              books: [
                {
                  title: 'Guest Discovery Book',
                  authors: ['Guest Author'],
                  description: 'Unowned blurb visible to guests.',
                  publish_date: '2024-06',
                  series_index: 2,
                  state: 'Not in library',
                  monitored: false,
                  formats: [],
                },
              ],
            })
          )
        );
      }
      if (String(url).startsWith('/api/catalog/entity?kind=series&name=Guest%20Chronicle')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              monitored: false,
              books: [
                {
                  id: '',
                  title: 'Guest Discovery Book',
                  authorName: 'Guest Author',
                  seriesTitle: 'Guest Chronicle',
                  seriesIndex: 2,
                  releaseDate: '2024-06',
                  synopsis: 'Unowned blurb visible to guests.',
                  state: 'Not in library',
                  monitored: false,
                  formats: [],
                },
              ],
            })
          )
        );
      }
      return originalFetch(url, options);
    });

    render(<App />);
    await waitFor(() => expect(screen.getByText('The Obsidian Archive')).toBeInTheDocument());

    await user.click(screen.getByTestId('global-search-trigger'));
    await user.type(screen.getByTestId('command-palette-input'), 'Guest Discovery');

    const seriesButton = await screen.findByRole('button', { name: 'Guest Chronicle' });
    expect(screen.queryByRole('button', { name: /Add Guest Discovery Book/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Format' })).not.toBeInTheDocument();

    await user.click(seriesButton);

    const modalReadingOrder = await screen.findByRole('list', { name: 'Series reading order' });
    expect(within(modalReadingOrder).queryByRole('button', { name: 'Download' })).not.toBeInTheDocument();
    expect(within(modalReadingOrder).queryByRole('button', { name: /Monitor/i })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Manage monitoring' }));

    const readingOrder = await screen.findByRole('list', { name: 'Reading Order' });
    expect(within(readingOrder).queryByRole('button', { name: 'Download' })).not.toBeInTheDocument();
    expect(within(readingOrder).queryByRole('button', { name: /Monitor/i })).not.toBeInTheDocument();
    await user.click(within(readingOrder).getByRole('button', { name: 'Guest Discovery Book' }));

    const detailDialog = await screen.findByRole('dialog', { name: 'Details for Guest Discovery Book' });
    expect(within(detailDialog).getByText('Unowned blurb visible to guests.')).toBeInTheDocument();
    expect(within(detailDialog).getByText('No files available.')).toBeInTheDocument();
    expect(within(detailDialog).queryByRole('button', { name: 'Show Download Options' })).not.toBeInTheDocument();
    expect(within(detailDialog).queryByRole('button', { name: /Monitor Book/i })).not.toBeInTheDocument();

    const writeCalls = fetchMock.mock.calls.filter(([u, opts]) => {
      const method = opts?.method?.toUpperCase() ?? 'GET';
      return method !== 'GET' || String(u).includes('/api/books/resolve');
    });
    expect(writeCalls).toEqual([]);
  });

  it('allows admins to navigate to Metadata Mismatches under Activity and blocks non-admins', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.mocked(global.fetch);
    const originalFetch = fetchMock.getMockImplementation()!;

    fetchMock.mockImplementation((url, options) => {
      if (String(url) === '/api/activity/metadata-mismatches') {
        return Promise.resolve(
          Response.json({
            scannedBooks: 3,
            items: [
              {
                bookId: 'book-96',
                title: 'The Final Empire',
                authorName: 'Brandon Sanderson',
                ideal: { title: 'The Final Empire', series: 'Mistborn', seriesIndex: 1 },
                idealSource: 'match',
                mismatchedFields: ['title'],
                files: [
                  {
                    assetId: 'asset-96',
                    filename: 'final-empire.epub',
                    current: { title: 'Wrong Title' },
                    mismatches: [{ field: 'title', current: 'Wrong Title', ideal: 'The Final Empire' }],
                  },
                ],
              },
            ],
          }),
        );
      }
      return originalFetch(url, options);
    });

    const { unmount } = render(<App />);
    const navBtn = await screen.findByTestId('nav-activity-mismatches');
    await user.click(navBtn);

    expect(await screen.findByRole('heading', { name: 'EPUB Metadata Mismatches' })).toBeInTheDocument();
    expect(await screen.findByText('Wrong Title')).toBeInTheDocument();
    unmount();

    // Non-admin user visiting #/activity/metadata-mismatches directly gets Access Denied and no sidebar entry.
    window.location.hash = '#/activity/metadata-mismatches';
    fetchMock.mockImplementation((url, options) => {
      if (String(url).includes('/api/auth/me')) {
        return Promise.resolve(
          Response.json({ id: 'u-cur', username: 'curator', role: 'curator', email: 'curator@example.com' }),
        );
      }
      return originalFetch(url, options);
    });

    render(<App />);
    expect(await screen.findByTestId('mismatches-access-denied')).toBeInTheDocument();
    expect(screen.queryByTestId('nav-activity-mismatches')).not.toBeInTheDocument();
  });
});
