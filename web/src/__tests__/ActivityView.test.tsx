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

it('shows two format rows and cancels or removes them independently', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetchMock);
  render(<ActivityView {...base} requests={[ebook, audio]} downloads={[
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
  const { rerender } = render(<ActivityView {...base} requests={[ebook]} />);
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
  rerender(<ActivityView {...base} requests={[{ ...ebook, status: 'downloading', stage: 'importing' }]} />);
  expect(stages.getByText('Importing').parentElement).toHaveAttribute('aria-current', 'step');
  await user.click(dialog.getByRole('button', { name: 'Hide Search Log' }));
  expect(dialog.queryByRole('list', { name: 'Search log' })).not.toBeInTheDocument();
  await user.click(dialog.getByRole('button', { name: 'Close details' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('keeps the row and shows the API error when cancellation fails', async () => {
  const user = userEvent.setup({ delay: null });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: 'Import is finishing' }, { status: 409 })));
  render(<ActivityView {...base} requests={[ebook]} />);
  await user.click(screen.getByRole('button', { name: 'Cancel Dune eBook' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Import is finishing');
  expect(screen.getByRole('button', { name: 'Cancel Dune eBook' })).toBeEnabled();
});
