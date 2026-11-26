import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../App';
import { ActivityView } from '../views/ActivityView';
import { testConversionJobs, testDownloads } from './testFixtures';

const base = { conversionJobs: [], downloads: [], logs: [], connectionStatus: 'connected' as const };
it('renders downloads, converting editions and import history in one list without legacy clutter', () => {
  render(<ActivityView {...base} conversionJobs={[{ ...testConversionJobs[0], stage: 'preparing' }]}
    downloads={testDownloads} logs={[{ id: 'log', timestamp: '', action: 'import', itemName: 'Imported book', status: 'success', details: 'Organized and tagged' }]} />);
  const list = within(screen.getByRole('list', { name: 'Book activity' }));
  expect(list.getAllByRole('listitem')).toHaveLength(3);
  expect(list.getByText(/Converting · Preparing/)).toBeInTheDocument();
  expect(list.getByText(/Downloading/)).toBeInTheDocument();
  expect(list.getByText(/Completed/)).toBeInTheDocument();
  expect(screen.queryByRole('tab')).not.toBeInTheDocument();
  expect(screen.queryByText(/SSE Live|Transcode|Activity & Queues|probing|encoding/i)).not.toBeInTheDocument();
});
it.each(['preparing', 'moving', 'completed'] as const)('keeps one row for an edition through %s', stage => {
  render(<ActivityView {...base}
    conversionJobs={[{ ...testConversionJobs[0], editionId: 'edition', stage, status: stage === 'completed' ? 'completed' : 'processing' }]}
    downloads={[{ ...testDownloads[0], editionId: 'edition', status: 'completed', requestId: 'request' }]}
    requests={[{ id: 'request', title: 'Same book', author: '', requestedBy: '', requestedAt: '', requestType: 'audiobook', status: 'downloading' }]} />);
  expect(screen.getAllByRole('listitem')).toHaveLength(1);
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', stage === 'completed' ? '100' : '89.2');
  expect(screen.getByText(new RegExp(stage === 'moving' ? 'Importing · Organizing' : stage === 'completed' ? 'Completed' : 'Converting · Preparing'))).toBeInTheDocument();
});
it('keeps different editions separate and shows errors and unknown ETA honestly', () => {
  render(<ActivityView {...base} conversionJobs={[{ ...testConversionJobs[0], editionId: 'audio', status: 'failed', errorMessage: 'Unreadable file', etaSeconds: 0 }]}
    downloads={[{ ...testDownloads[0], editionId: 'ebook', category: 'callimachus-ebooks', etaSeconds: 0 }]} />);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  expect(screen.getByRole('alert')).toHaveTextContent('Unreadable file');
  expect(screen.getByText(/ETA: Unavailable/)).toBeInTheDocument();
});
it('shows one useful empty state', () => {
  render(<ActivityView {...base} />);
  expect(screen.getByText(/No activity yet/)).toBeInTheDocument();
});

const ebook = { id: '1', bookId: 'book', title: 'Dune', author: 'Frank Herbert', requestedBy: 'Reader', requestedAt: '', requestType: 'ebook', status: 'searching', stage: 'evaluating' } as const;
const audio = { ...ebook, id: '2', requestType: 'audiobook', status: 'available', stage: 'available' } as const;
afterEach(() => vi.unstubAllGlobals());

it('opens an audiobook request on its own tab and can switch using its book context', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn(async (url: string) => {
    if (url === '/api/requests') return Response.json([{ id: 1, book_id: 'book', format: 'epub' }]);
    if (url === '/api/requests/1/candidates' || url === '/api/requests/2/candidates') return Response.json([]);
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  render(<ActivityView {...base} canRequest requests={[ebook, audio]} />);
  const row = within(screen.getByTestId('activity-request:2'));
  await user.click(row.getByRole('button', { name: 'Manual Search for Dune' }));
  const dialog = within(screen.getByRole('dialog', { name: 'Download Options' }));
  expect(dialog.getByRole('tab', { name: 'Audiobook', selected: true })).toBeInTheDocument();
  await dialog.findByText(/No cached candidates/);
  expect(fetchMock.mock.calls).toEqual([['/api/requests/2/candidates', undefined]]);
  await user.click(dialog.getByRole('tab', { name: 'eBook' }));
  await dialog.findByText(/No cached candidates/);
  expect(fetchMock).toHaveBeenCalledWith('/api/requests/1/candidates', undefined);
});

it('shows two format rows and cancels or removes them independently', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetchMock);
  render(<ActivityView {...base} canRequest requests={[ebook, audio]} downloads={[
    { ...testDownloads[0], id: 'ebook-download', editionId: 'shared-legacy-edition', requestId: '1', status: 'downloading' },
    { ...testDownloads[0], id: 'audio-download', editionId: 'shared-legacy-edition', requestId: '2', status: 'completed' },
  ]} />);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  await user.click(screen.getByRole('button', { name: 'Cancel Dune eBook' }));
  expect(fetchMock).toHaveBeenCalledWith('/api/requests/1', { method: 'DELETE' });
  await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(1));
  await user.click(screen.getByRole('button', { name: 'Remove Dune Audiobook' }));
  expect(fetchMock).toHaveBeenCalledWith('/api/requests/2', { method: 'DELETE' });
  expect(await screen.findByText(/No activity yet/)).toBeInTheDocument();
});

it('opens book details with current stage and lazily reveals the timestamped search log', async () => {
  const user = userEvent.setup({ delay: null });
  const time = '2026-11-18T09:00:00Z';
  const fetchMock = vi.fn(async (url: string) => {
    if (url === '/api/books?id=book') return Response.json([{ id: 'book', title: 'Dune', authorName: 'Frank Herbert', seriesTitle: 'Dune series',
      synopsis: 'A desert planet', releaseDate: '1965', coverUrl: '/cover.jpg', formats: [], editions: [] }]);
    if (url === '/api/requests/1') return Response.json({ events: [{ id: 1, kind: 'candidate_scored', message: 'Scored Dune EPUB', occurred_at: time }] });
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  const { rerender } = render(<ActivityView {...base} canRequest requests={[ebook]} />);
  await user.click(screen.getByRole('button', { name: /Dune eBook · Evaluating releases/ }));
  const dialog = within(screen.getByRole('dialog', { name: 'Details for Dune' }));
  expect(await dialog.findByText('A desert planet')).toBeInTheDocument();
  expect(dialog.getByText('Dune series')).toBeInTheDocument();
  expect(dialog.getByText('1965')).toBeInTheDocument();
  expect(dialog.getByRole('img', { name: 'Dune' })).toHaveAttribute('src', '/cover.jpg');
  const stages = within(dialog.getByRole('list', { name: 'Acquisition stages' }));
  expect(stages.getByText('Evaluating releases').parentElement).toHaveAttribute('aria-current', 'step');
  expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/books?id=book']);
  await user.click(dialog.getByRole('button', { name: 'View Search Log' }));
  const event = await dialog.findByText('Scored Dune EPUB');
  expect(event.parentElement?.querySelector('time')).toHaveAttribute('datetime', time);
  rerender(<ActivityView {...base} canRequest requests={[{ ...ebook, status: 'downloading', stage: 'importing' }]} />);
  expect(stages.getByText('Importing').parentElement).toHaveAttribute('aria-current', 'step');
  await user.click(dialog.getByRole('button', { name: 'Hide Search Log' }));
  expect(dialog.queryByRole('list', { name: 'Search log' })).not.toBeInTheDocument();
  await user.click(dialog.getByRole('button', { name: 'Close details' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('keeps the row and shows the API error when cancellation fails', async () => {
  const user = userEvent.setup({ delay: null });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: 'Import is finishing' }, { status: 409 })));
  render(<ActivityView {...base} canRequest requests={[ebook]} />);
  await user.click(screen.getByRole('button', { name: 'Cancel Dune eBook' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Import is finishing');
  expect(screen.getByRole('button', { name: 'Cancel Dune eBook' })).toBeEnabled();
});

it('renders loading skeleton on initial load and keeps items visible during refresh', async () => {
  const user = userEvent.setup({ delay: null });
  const onRetry = vi.fn();
  const { rerender } = render(<ActivityView {...base} loading={true} onRetry={onRetry} />);

  expect(screen.getByRole('status')).toHaveTextContent('Loading activity…');
  expect(screen.queryByText(/No activity yet/)).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled();

  rerender(<ActivityView {...base} downloads={testDownloads} loading={false} onRetry={onRetry} />);
  expect(screen.getByRole('list', { name: 'Book activity' })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Refresh' }));
  expect(onRetry).toHaveBeenCalledOnce();
});

it('renders load error alert with retry control without showing empty state', async () => {
  const user = userEvent.setup({ delay: null });
  const onRetry = vi.fn();
  render(<ActivityView {...base} error="Unable to load activity. Try again." onRetry={onRetry} />);

  expect(screen.getByRole('alert')).toHaveTextContent('Unable to load activity. Try again.');
  expect(screen.queryByText(/No activity yet/)).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Retry' }));
  expect(onRetry).toHaveBeenCalledOnce();
});

it('renders card metadata, responsive touch targets, cover fallback, and connection states cleanly', async () => {
  const { fireEvent } = await import('@testing-library/react');
  const { rerender } = render(
    <ActivityView
      {...base}
      canRequest
      connectionStatus="connecting"
      requests={[{ ...ebook, coverUrl: '/broken-cover.jpg', requestType: 'audiobook', stage: 'downloading' }]}
    />
  );

  expect(screen.getByText('Connecting…')).toBeInTheDocument();
  expect(screen.getByText('by Frank Herbert')).toBeInTheDocument();
  expect(within(screen.getByTestId('activity-request:1')).getByText('Audiobook')).toBeInTheDocument();

  const card = screen.getByTestId('activity-request:1');
  expect(card).toHaveClass('bg-zinc-900/90');

  const cover = card.querySelector('img');
  expect(cover).toHaveAttribute('src', '/broken-cover.jpg');
  fireEvent.error(cover!);
  expect(card.querySelector('img')).not.toBeInTheDocument();

  const cancelBtn = screen.getByRole('button', { name: 'Cancel Dune Audiobook' });
  expect(cancelBtn).toHaveClass('min-h-11');

  rerender(<ActivityView {...base} connectionStatus="disconnected" />);
  expect(screen.getByText('Live updates paused')).toBeInTheDocument();
  expect(screen.queryByText(/SSE Live/i)).not.toBeInTheDocument();
});

it('filters activity rows by search text, status, and format and clears filters', async () => {
  const user = userEvent.setup({ delay: null });
  render(
    <ActivityView
      {...base}
      requests={[
        ebook,
        audio,
        { id: '3', title: 'Foundation', author: 'Isaac Asimov', requestedBy: 'Reader', requestedAt: '', requestType: 'ebook', status: 'error' },
      ]}
    />
  );

  expect(screen.getAllByRole('listitem')).toHaveLength(3);

  const searchInput = screen.getByRole('textbox', { name: 'Filter activity' });
  await user.type(searchInput, 'Foundation');
  expect(screen.getAllByRole('listitem')).toHaveLength(1);
  expect(screen.getByText('Foundation')).toBeInTheDocument();

  await user.clear(searchInput);
  expect(screen.getAllByRole('listitem')).toHaveLength(3);

  await user.click(screen.getByRole('button', { name: 'Done' }));
  expect(screen.getAllByRole('listitem')).toHaveLength(1);
  expect(within(screen.getByTestId('activity-request:2')).getByText('Completed')).toBeInTheDocument();

  await user.click(screen.getByRole('button', { name: 'Attention' }));
  expect(screen.getAllByRole('listitem')).toHaveLength(1);
  expect(screen.getByText('Foundation')).toBeInTheDocument();

  await user.click(screen.getByRole('button', { name: 'eBooks' }));
  expect(screen.getAllByRole('listitem')).toHaveLength(1);

  await user.click(screen.getByRole('button', { name: 'Audiobooks' }));
  expect(screen.getByText('No matching activity')).toBeInTheDocument();

  await user.click(screen.getByRole('button', { name: 'Clear Filters' }));
  expect(screen.getAllByRole('listitem')).toHaveLength(3);
});

it('supports keyboard navigation across card controls and search log', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn(async (url: string) => {
    if (url === '/api/books?id=book') {
      return Response.json([{ id: 'book', title: 'Dune', authorName: 'Frank Herbert', formats: [], editions: [] }]);
    }
    if (url === '/api/requests/1') {
      return Response.json({ events: [] });
    }
    if (url === '/api/requests/1/candidates') {
      return Response.json([]);
    }
    return new Response(null, { status: 204 });
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<ActivityView {...base} canRequest requests={[ebook]} />);

  const titleBtn = screen.getByRole('button', { name: /Dune eBook · Evaluating releases/ });
  const manualBtn = screen.getByRole('button', { name: 'Manual Search for Dune' });
  const cancelBtn = screen.getByRole('button', { name: 'Cancel Dune eBook' });

  titleBtn.focus();
  expect(titleBtn).toHaveFocus();
  await user.keyboard('{Enter}');

  const detailsDialog = await screen.findByRole('dialog', { name: 'Details for Dune' });
  const logToggle = within(detailsDialog).getByRole('button', { name: 'View Search Log' });
  expect(logToggle).toHaveAttribute('aria-expanded', 'false');
  const logPanel = document.getElementById(logToggle.getAttribute('aria-controls')!);
  expect(logPanel).toHaveAttribute('hidden');
  logToggle.focus();
  await user.keyboard('{Enter}');
  expect(logToggle).toHaveAttribute('aria-expanded', 'true');
  expect(await within(detailsDialog).findByText('No search events recorded yet.')).toBeInTheDocument();

  await user.click(within(detailsDialog).getByRole('button', { name: 'Close details' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

  titleBtn.focus();
  await user.tab();
  expect(manualBtn).toHaveFocus();
  await user.tab();
  expect(cancelBtn).toHaveFocus();
  await user.keyboard(' ');
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/requests/1', { method: 'DELETE' }));
});

it('hides Manual Search and Cancel/Remove controls for guests in ActivityView', () => {
  render(<ActivityView {...base} canRequest={false} canManageJobs={false} requests={[ebook, audio]} />);
  expect(screen.queryByRole('button', { name: /Manual Search/i })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Cancel/i })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Remove/i })).not.toBeInTheDocument();
});

class MockEventSource {
  static current: MockEventSource;
  listeners: Record<string, ((e: MessageEvent) => void)[]> = {};
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(public url: string) {
    MockEventSource.current = this;
  }

  addEventListener(type: string, callback: (e: MessageEvent) => void) {
    this.listeners[type] ??= [];
    this.listeners[type].push(callback);
  }

  emit(type: string, data: unknown) {
    const event = { data: JSON.stringify(data) } as MessageEvent;
    for (const listener of this.listeners[type] ?? []) listener(event);
  }

  close() {
    this.listeners = {};
  }
}


it('shows the live download stages and request log when opening a book from its author page', async () => {
  const user = userEvent.setup({ delay: null });
  vi.stubGlobal('EventSource', MockEventSource);
  const book = {
    id: 'book', title: 'Dune', authorName: 'Frank Herbert', authorId: 'author',
    state: 'Downloading', formats: [], editions: [],
  };
  const request = { id: 1, book_id: book.id, title: book.title, author: book.authorName, format: 'epub', status: 'downloading', stage: 'downloading' };
  const download = { ...testDownloads[0], bookId: book.id, requestId: '1', status: 'downloading', progressPercent: 40 };
  const snapshot = {
    requests: [
      request,
      { ...request, id: 2, book_id: 'other-book', format: 'm4b' },
      { ...request, id: 3, format: 'm4b', status: 'available', stage: 'available' },
    ],
    downloads: [download], conversionJobs: [], logs: [],
  };
  const fetchMock = vi.fn(async (url: string, _options?: RequestInit) => {
    if (url === '/api/auth/me') return Response.json({ id: 'curator', role: 'curator' });
    if (url === '/api/catalog/entity?kind=authors&name=Frank%20Herbert') return Response.json({ books: [book] });
    if (url === '/api/books' || url === '/api/books?id=book') return Response.json([book]);
    if (url === '/api/authors' || url === '/api/series') return Response.json([]);
    if (url === '/api/requests') return Response.json(snapshot.requests);
    if (url === '/api/activity') return Response.json(snapshot);
    if (url === '/api/requests/1') {
      return Response.json({ events: [{ id: 1, message: 'Import started', occurred_at: '2026-11-26T20:00:00Z' }] });
    }
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  const originalUrl = window.location.href;
  window.history.replaceState(null, '', '#/authors/Frank%20Herbert');
  try {
    render(<App />);
    await user.click(await screen.findByRole('button', { name: 'Dune' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Details for Dune' }));
    const stages = within(await dialog.findByRole('list', { name: 'Acquisition stages' }));
    expect(stages.getAllByRole('listitem')).toHaveLength(6);
    expect(stages.getByText('Downloading').parentElement).toHaveAttribute('aria-current', 'step');
    expect(dialog.getByText('20.0% overall')).toBeVisible();
    expect(fetchMock.mock.calls.some(([url]) => url.startsWith('/api/requests/'))).toBe(false);

    act(() => MockEventSource.current.emit('download_progress', { id: download.id, progressPercent: 90 }));
    expect(dialog.getByText('45.0% overall')).toBeVisible();

    await user.click(dialog.getByRole('button', { name: 'View Search Log' }));
    expect(await dialog.findByText('Import started')).toBeVisible();
    const logSignal = fetchMock.mock.calls.find(([url]) => url === '/api/requests/1')?.[1]?.signal;

    act(() => MockEventSource.current.emit('activity_snapshot', {
      ...snapshot,
      downloads: [{ ...download, status: 'importing' }],
    }));
    expect(stages.getByText('Importing').parentElement).toHaveAttribute('aria-current', 'step');

    act(() => MockEventSource.current.emit('activity_snapshot', {
      ...snapshot,
      requests: snapshot.requests.map(item => item.id === 1 ? { ...item, status: 'available', stage: 'available' } : item),
      downloads: [{ ...download, status: 'completed' }],
    }));
    expect(stages.getByText('Available').parentElement).toHaveAttribute('aria-current', 'step');
    expect(dialog.getByRole('list', { name: 'Search log' })).toBeVisible();

    await user.click(dialog.getByRole('button', { name: 'Close details' }));
    expect(logSignal?.aborted).toBe(true);
  } finally {
    window.history.replaceState(null, '', originalUrl);
  }
});

it('keeps Fix Metadata open across SSE activity updates when details stayed open from in-progress download to completion', async () => {
  const user = userEvent.setup({ delay: null });

  vi.stubGlobal('EventSource', MockEventSource);

  const inProgressRequest = {
    id: 1,
    book_id: 'book-104',
    title: 'Dune',
    author: 'Frank Herbert',
    username: 'admin',
    format: 'epub',
    status: 'downloading',
    stage: 'downloading',
    created_at: '2026-11-21T20:00:00Z',
  };
  const inProgressDownload = {
    ...testDownloads[0],
    id: 'dl-104',
    bookId: 'book-104',
    requestId: '1',
    title: 'Dune',
    category: 'callimachus-ebooks' as const,
    status: 'downloading' as const,
    progressPercent: 45,
  };
  const completedRequest = { ...inProgressRequest, status: 'available', stage: 'available' };
  const completedDownload = { ...inProgressDownload, status: 'completed' as const, progressPercent: 100 };

  let downloadCompleted = false;
  let metadataFixed = false;

  const bookBeforeComplete = {
    id: 'book-104',
    title: 'Dune',
    authorId: 'author-1',
    authorName: 'Frank Herbert',
    state: 'Downloading',
    formats: [],
    editions: [],
  };
  const bookAfterComplete = {
    ...bookBeforeComplete,
    state: 'In library',
    formats: ['EPUB'],
    editions: [
      {
        id: 'ed-104',
        bookId: 'book-104',
        format: 'EPUB',
        language: 'en',
        assets: [
          {
            id: 'asset-104',
            editionId: 'ed-104',
            filename: 'Dune (1965) - Frank Herbert.epub',
            format: 'EPUB',
            fileSizeBytes: 512000,
          },
        ],
      },
    ],
  };

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === '/api/auth/me') {
      return Response.json({ id: 'admin-1', username: 'admin', email: 'admin@example.test', role: 'admin' });
    }
    if (url === '/api/books?id=book-104') {
      const current = downloadCompleted
        ? { ...bookAfterComplete, ...(metadataFixed ? { subtitle: 'Book One of Dune' } : {}) }
        : bookBeforeComplete;
      return Response.json([current]);
    }
    if (url === '/api/books') {
      return Response.json(downloadCompleted ? [bookAfterComplete] : [bookBeforeComplete]);
    }
    if (url === '/api/authors' || url === '/api/series') return Response.json([]);
    if (url === '/api/requests') {
      return Response.json([downloadCompleted ? completedRequest : inProgressRequest]);
    }
    if (url === '/api/activity') {
      return Response.json({
        requests: [downloadCompleted ? completedRequest : inProgressRequest],
        downloads: [downloadCompleted ? completedDownload : inProgressDownload],
        conversionJobs: [],
        logs: [],
      });
    }
    if (url === '/api/settings') {
      return Response.json({ qualityProfile: { allowedLanguages: ['en'] } });
    }
    if (url === '/api/books/book-104/metadata-candidates') {
      return Response.json({
        candidates: [{ title: 'Dune', subtitle: 'Book One of Dune', authors: ['Frank Herbert'], language: 'en' }],
        defaultLanguage: 'en',
        language: 'en',
        availableLanguages: ['en'],
      });
    }
    if (url === '/api/books/book-104/metadata-diff') {
      return Response.json({
        preview: 'preview-token-104',
        files: [
          {
            id: 'asset-104',
            filename: 'Dune (1965) - Frank Herbert.epub',
            diffs: [{ field: 'title', fileValue: 'dune_raw', libraryValue: 'Dune', changed: true }],
          },
        ],
      });
    }
    if (url === '/api/books/book-104/fix-metadata' && init?.method === 'POST') {
      metadataFixed = true;
      return Response.json({ updated: 1 });
    }
    throw new Error(`Unexpected fetch: ${init?.method ?? 'GET'} ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<App initialView="activity" />);

  // 1. Open book details from the download/activity view while the download is in progress.
  const rowButton = await screen.findByRole('button', { name: /Dune eBook · Downloading/ });
  await user.click(rowButton);
  const dialog = await screen.findByRole('dialog', { name: 'Details for Dune' });
  expect(within(dialog).getByText('No files available.')).toBeVisible();

  // 2. Leave book details open while SSE reports the download completing.
  downloadCompleted = true;
  const stream = MockEventSource.current;
  expect(stream).toBeDefined();
  act(() => {
    stream.emit('activity_snapshot', {
      requests: [completedRequest],
      downloads: [completedDownload],
      conversionJobs: [],
      logs: [],
    });
  });

  // Wait for the completed EPUB file to load into the open modal.
  expect(await within(dialog).findByText('Dune (1965) - Frank Herbert.epub')).toBeVisible();

  // 3. Click Fix Metadata from More actions.
  await user.click(within(dialog).getByRole('button', { name: 'More actions' }));
  await user.click(within(dialog).getByRole('menuitem', { name: 'Fix Metadata' }));
  expect(await within(dialog).findByRole('heading', { name: 'Fix Metadata' })).toBeVisible();
  expect(await within(dialog).findByText('dune_raw')).toBeVisible();

  // 4. Subsequent periodic SSE activity_snapshot ticks must NOT close Fix Metadata.
  act(() => {
    stream.emit('activity_snapshot', {
      requests: [completedRequest],
      downloads: [completedDownload],
      conversionJobs: [],
      logs: [],
    });
  });
  expect(within(dialog).getByRole('heading', { name: 'Fix Metadata' })).toBeVisible();
  expect(within(dialog).getByText('dune_raw')).toBeVisible();

  // 5. Applying metadata keeps the completion confirmation visible even after book/catalog reload.
  await user.click(within(dialog).getByRole('button', { name: 'Apply Metadata' }));
  expect(await within(dialog).findByText('Metadata applied to the EPUB files.')).toBeVisible();

  await user.click(within(dialog).getByRole('button', { name: 'Back to details' }));
  expect(await within(dialog).findByText('Book One of Dune')).toBeVisible();
});
