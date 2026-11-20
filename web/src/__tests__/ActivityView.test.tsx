import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
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
  expect(logToggle).toHaveAttribute('aria-controls', 'activity-search-log');
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
