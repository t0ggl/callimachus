import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { EntityView } from '../views/EntityView';
import type { Book } from '../types';

afterEach(() => vi.unstubAllGlobals());
it.each([
  { kind: 'authors', name: 'An Author', label: 'Author', list: 'Books' },
  { kind: 'series', name: 'Sequence', label: 'Series', list: 'Reading Order' },
] as const)('renders $label books, saves monitoring, queues checks and opens book details', async ({ kind, name, label, list }) => {
  const user = userEvent.setup({ delay: null });
  const book: Book = {
    id: 'b', title: 'Collected Stories', authorId: 'a', authorName: 'An Author', seriesTitle: 'Sequence',
    seriesIndex: 0.5, state: 'In library', coverUrl: '/api/books/b/cover', formats: ['EPUB', 'M4B'], editions: [],
  };
  const upcoming = { ...book, id: 'next', title: 'Next Book', seriesIndex: 1, releaseDate: '2099-05-10', state: 'Scheduled', formats: [], coverUrl: undefined };
  const url = `/api/catalog/entity?kind=${kind}&name=${encodeURIComponent(name)}`;
  const fetchMock = vi.fn().mockResolvedValueOnce({
    ok: true,
    json: async () => ({ monitored: false, photoUrl: '/author.jpg', books: [book, upcoming] }),
  }).mockResolvedValue({ ok: true, json: async () => [upcoming] });
  vi.stubGlobal('fetch', fetchMock);
  const { container } = render(<EntityView kind={kind} name={name} canManage />);
  expect(screen.getByRole('heading', { name, level: 1 })).toBeInTheDocument();
  const books = await screen.findByRole('list', { name: list });
  const [owned, scheduled] = within(books).getAllByRole('listitem');
  expect(owned).toHaveTextContent('Book 0.5');
  expect(owned).toHaveTextContent('In library');
  expect(within(owned).getByText('EPUB')).toBeInTheDocument();
  expect(within(owned).getByText('M4B')).toBeInTheDocument();
  expect(within(owned).getByRole('img')).toHaveAttribute('src', '/api/books/b/cover');
  expect(scheduled).toHaveTextContent('Scheduled');
  expect(scheduled).toHaveTextContent('No files');
  expect(books).not.toHaveTextContent(/monitored/i);
  expect(screen.getByText('1 / 2 in library')).toBeInTheDocument();
  expect(container.querySelector('header img')).toHaveAttribute('src', kind === 'authors' ? '/author.jpg' : '/api/books/b/cover');
  const toggle = screen.getByRole('checkbox', { name: `Monitor ${label}` });
  await user.click(toggle);
  expect(fetchMock).toHaveBeenCalledWith(url, expect.objectContaining({ method: 'PUT', body: '{"monitored":true}' }));
  expect(toggle).toBeChecked();
  await user.click(screen.getByRole('button', { name: 'Check New Releases' }));
  expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('action=discover'), expect.objectContaining({ method: 'POST' }));
  expect(screen.getByRole('status')).toHaveTextContent('queued');
  await user.click(screen.getByRole('button', { name: 'Search Downloads' }));
  expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('action=downloads'), expect.objectContaining({ method: 'POST' }));
  await user.click(within(scheduled).getByRole('button', { name: 'Next Book' }));
  expect(fetchMock).toHaveBeenLastCalledWith('/api/books?id=next', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  const dialog = await screen.findByRole('dialog', { name: 'Details for Next Book' });
  expect(within(dialog).getByText('Scheduled')).toBeInTheDocument();
  await user.click(within(dialog).getByRole('button', { name: 'Close details' }));
  expect(dialog).not.toBeInTheDocument();
});
it('reports failed actions and keeps the saved toggle state', async () => {
  const user = userEvent.setup({ delay: null });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce({ ok: true, json: async () => ({ monitored: false, books: [] }) }).mockResolvedValue({ ok: false }));
  render(<EntityView kind="series" name="Sequence" canManage />);
  await waitFor(() => expect(screen.getByLabelText('Monitor Series')).toBeEnabled());
  await user.click(screen.getByLabelText('Monitor Series'));
  expect(await screen.findByRole('alert')).toHaveTextContent('retry');
  expect(screen.getByLabelText('Monitor Series')).not.toBeChecked();
});
it('allows guests to browse without management controls', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ monitored: false, books: [] }) }));
  render(<EntityView kind="series" name="Sequence" canManage={false} />);
  expect(await screen.findByText(/No known books/)).toBeInTheDocument();
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
});

it('resolves unpersisted series books for details, inline Download, and inline Monitor', async () => {
  const user = userEvent.setup({ delay: null });
  const unownedBook: Book = {
    id: 'res-missing',
    title: 'Missing Entry',
    authorId: 'a1',
    authorName: 'Author One',
    seriesTitle: 'Sequence',
    seriesIndex: 2,
    state: 'Not in library',
    monitored: false,
    formats: [],
    editions: [],
  };
  const scheduledBook: Book = {
    id: 'res-sched',
    title: 'Future Entry',
    authorId: 'a1',
    authorName: 'Author One',
    seriesTitle: 'Sequence',
    seriesIndex: 3,
    releaseDate: '2099-09-01',
    state: 'Scheduled',
    monitored: false,
    formats: [],
    editions: [],
  };

  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/catalog/entity?kind=series&name=Sequence') {
      return Response.json({
        monitored: false,
        books: [
          { id: '', title: 'Missing Entry', authorName: 'Author One', seriesTitle: 'Sequence', seriesIndex: 2, state: 'Not in library', monitored: false, formats: [] },
          { id: '', title: 'Future Entry', authorName: 'Author One', seriesTitle: 'Sequence', seriesIndex: 3, releaseDate: '2099-09-01', state: 'Scheduled', monitored: false, formats: [] },
          { id: '', title: 'Guest Visible Entry', authorName: 'Author One', seriesTitle: 'Sequence', seriesIndex: 4, state: 'Not in library', synopsis: 'Guest readable blurb', monitored: false, formats: [] },
        ],
      });
    }
    if (url === '/api/books/resolve' && init?.method === 'POST') {
      const body = JSON.parse(String(init.body));
      if (body.title === 'Missing Entry') return Response.json(unownedBook);
      if (body.title === 'Future Entry') return Response.json(scheduledBook);
    }
    if (url === '/api/books?id=res-missing') {
      return Response.json([unownedBook]);
    }
    if (url === '/api/books/res-sched/monitor' && init?.method === 'PUT') {
      return Response.json({ id: 'res-sched', monitored: true });
    }
    if (url === '/api/requests' && (!init?.method || init.method === 'GET')) {
      return Response.json([{ id: 77, book_id: 'res-missing', format: 'epub' }]);
    }
    if (url === '/api/requests/77/candidates') {
      return Response.json([]);
    }
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  const { rerender } = render(<EntityView kind="series" name="Sequence" canManage canRequest />);
  const list = await screen.findByRole('list', { name: 'Reading Order' });
  const [missingRow, schedRow, guestRow] = within(list).getAllByRole('listitem');

  // Opening an unpersisted title renders BookDetailModal read-only WITHOUT calling POST /api/books/resolve
  await user.click(within(missingRow).getByRole('button', { name: 'Missing Entry' }));
  const detailDialog = await screen.findByRole('dialog', { name: 'Details for Missing Entry' });
  expect(within(detailDialog).getByRole('button', { name: 'Show Download Options' })).toBeInTheDocument();
  expect(within(detailDialog).getByRole('button', { name: 'Monitor Book' })).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(0);
  await user.click(within(detailDialog).getByRole('button', { name: 'Close details' }));

  // Inline Download lazily resolves once & opens ManualSearch
  await user.click(within(missingRow).getByRole('button', { name: 'Download' }));
  expect(await screen.findByRole('tablist', { name: 'Download format' })).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(1);
  await user.click(screen.getByRole('button', { name: 'Close' }));

  // Monitor Book inside modal on Scheduled item resolves & syncs parent row state
  await user.click(within(schedRow).getByRole('button', { name: 'Future Entry' }));
  const schedDialog = await screen.findByRole('dialog', { name: 'Details for Future Entry' });
  await user.click(within(schedDialog).getByRole('button', { name: 'Monitor Book' }));
  expect(await within(schedDialog).findByRole('button', { name: 'Monitoring Book' })).toHaveAttribute('aria-pressed', 'true');
  await user.click(within(schedDialog).getByRole('button', { name: 'Close details' }));
  await waitFor(() => {
    expect(within(schedRow).getByRole('button', { name: 'Monitoring' })).toHaveAttribute('aria-pressed', 'true');
  });

  // Requester sees inline Download (and Show Download Options) but never Monitor controls
  rerender(<EntityView kind="series" name="Sequence" canManage={false} canRequest />);
  expect(within(missingRow).getByRole('button', { name: 'Download' })).toBeInTheDocument();
  expect(within(schedRow).queryByRole('button', { name: /Monitor/i })).not.toBeInTheDocument();

  // Guests cannot see Download or Monitor actions inline or inside unowned details
  rerender(<EntityView kind="series" name="Sequence" canManage={false} canRequest={false} />);
  expect(within(missingRow).queryByRole('button', { name: 'Download' })).not.toBeInTheDocument();
  expect(within(schedRow).queryByRole('button', { name: /Monitor/i })).not.toBeInTheDocument();

  await user.click(within(guestRow).getByRole('button', { name: 'Guest Visible Entry' }));
  const guestDialog = await screen.findByRole('dialog', { name: 'Details for Guest Visible Entry' });
  expect(within(guestDialog).getByText('Guest readable blurb')).toBeInTheDocument();
  expect(within(guestDialog).getByText('No files available.')).toBeInTheDocument();
  expect(within(guestDialog).queryByRole('button', { name: 'Show Download Options' })).not.toBeInTheDocument();
  expect(within(guestDialog).queryByRole('button', { name: /Monitor Book/i })).not.toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(2);
});

