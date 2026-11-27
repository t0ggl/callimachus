import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { EntityView } from '../views/EntityView';
import { ActivityRefreshContext, BookActivityContext } from '../utils/activity';
import type { Book } from '../types';

afterEach(() => vi.unstubAllGlobals());
it.each([
  { kind: 'authors', name: 'An Author', label: 'Author', list: 'Sequence' },
  { kind: 'series', name: 'Sequence', label: 'Series', list: 'Reading Order' },
] as const)('renders $label books, saves monitoring, queues checks and opens book details', async ({ kind, name, label, list }) => {
  const user = userEvent.setup({ delay: null });
  const book: Book = {
    id: 'b', title: 'Collected Stories', authorId: 'a', authorName: 'An Author', seriesTitle: 'Sequence',
    seriesIndex: 0.5, state: 'In library', coverUrl: '/api/books/b/cover', formats: ['EPUB', 'M4B'], editions: [],
  };
  const upcoming = { ...book, id: 'next', title: 'Next Book', seriesIndex: 1, releaseDate: '2099-05-10', state: 'Scheduled', formats: [], coverUrl: undefined };
  const excluded = { ...upcoming, id: 'excluded', title: 'Excluded Book', seriesIndex: 2 };
  const url = `/api/catalog/entity?kind=${kind}&name=${encodeURIComponent(name)}`;
  let monitored = false;
  let audiobookMonitored = false;
  const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === url && init?.method === 'PUT') {
      const update = JSON.parse(String(init.body));
      monitored = update.monitored ?? monitored;
      audiobookMonitored = update.audiobookMonitored ?? audiobookMonitored;
      return new Response(null, { status: 204 });
    }
    if (path === url) {
      return Response.json({
        monitored, audiobookMonitored, photoUrl: '/author.jpg',
        books: [book, { ...upcoming, monitored }, excluded],
      });
    }
    return Response.json([upcoming]);
  });
  vi.stubGlobal('fetch', fetchMock);
  const { container } = render(<EntityView kind={kind} name={name} canManage />);
  expect(screen.getByRole('heading', { name, level: 1 })).toBeInTheDocument();
  const books = await screen.findByRole('list', { name: list });
  const [owned, scheduled, excludedRow] = within(books).getAllByRole('listitem');
  expect(owned).toHaveTextContent('Book 0.5');
  expect(owned).toHaveTextContent('In library');
  expect(within(owned).getByText('EPUB')).toBeInTheDocument();
  expect(within(owned).getByText('M4B')).toBeInTheDocument();
  expect(within(owned).getByRole('img')).toHaveAttribute('src', '/api/books/b/cover');
  expect(scheduled).toHaveTextContent('Scheduled');
  expect(scheduled).toHaveTextContent('No files');
  expect(books).not.toHaveTextContent(/monitored/i);
  expect(screen.getByText('1 / 3 in library')).toBeInTheDocument();
  expect(container.querySelector('header img')).toHaveAttribute('src', kind === 'authors' ? '/author.jpg' : '/api/books/b/cover');
  const toggle = screen.getByRole('checkbox', { name: kind === 'series' ? 'Monitor series eBooks' : `Monitor ${label}` });
  await user.click(toggle);
  expect(fetchMock).toHaveBeenCalledWith(url, expect.objectContaining({ method: 'PUT', body: '{"monitored":true}' }));
  expect(toggle).toBeChecked();
  expect(within(scheduled).getByRole('button', { name: 'Monitor eBook' })).toHaveAttribute('aria-pressed', 'true');
  expect(within(excludedRow).getByRole('button', { name: 'Monitor eBook' })).toHaveAttribute('aria-pressed', 'false');
  const audioToggle = screen.queryByRole('checkbox', { name: 'Monitor series audiobooks' });
  if (kind === 'series') {
    expect(audioToggle).not.toBeChecked();
    await user.click(audioToggle!);
    expect(fetchMock).toHaveBeenCalledWith(url, expect.objectContaining({ method: 'PUT', body: '{"audiobookMonitored":true}' }));
    expect(audioToggle).toBeChecked();
    expect(toggle).toBeChecked();
  } else {
    expect(audioToggle).not.toBeInTheDocument();
  }
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
  await waitFor(() => expect(screen.getByLabelText('Monitor series eBooks')).toBeEnabled());
  await user.click(screen.getByLabelText('Monitor series eBooks'));
  expect(await screen.findByRole('alert')).toHaveTextContent('retry');
  expect(screen.getByLabelText('Monitor series eBooks')).not.toBeChecked();
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
  expect(within(detailDialog).getByRole('button', { name: 'Monitor eBook' })).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(0);
  await user.click(within(detailDialog).getByRole('button', { name: 'Close details' }));

  // Inline Download lazily resolves once & opens ManualSearch
  await user.click(within(missingRow).getByRole('button', { name: 'Download' }));
  expect(await screen.findByRole('tablist', { name: 'Download format' })).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(1);
  await user.click(screen.getByRole('button', { name: 'Close' }));

  // Monitor eBook inside modal on Scheduled item resolves & syncs parent row state
  await user.click(within(schedRow).getByRole('button', { name: 'Future Entry' }));
  const schedDialog = await screen.findByRole('dialog', { name: 'Details for Future Entry' });
  await user.click(within(schedDialog).getByRole('button', { name: 'Monitor eBook' }));
  expect(await within(schedDialog).findByRole('button', { name: 'Monitor eBook' })).toHaveAttribute('aria-pressed', 'true');
  await user.click(within(schedDialog).getByRole('button', { name: 'Close details' }));
  await waitFor(() => {
    expect(within(schedRow).getByRole('button', { name: 'Monitor eBook' })).toHaveAttribute('aria-pressed', 'true');
  });

  // Requester sees inline Download and Monitor controls (books & series), without curator-only manual triggers
  rerender(<EntityView kind="series" name="Sequence" canManage={false} canRequest />);
  expect(within(missingRow).getByRole('button', { name: 'Download' })).toBeInTheDocument();
  expect(within(schedRow).getByRole('button', { name: 'Monitor eBook' })).toBeInTheDocument();
  expect(screen.getByLabelText('Monitor series eBooks')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Check New Releases/i })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Search Downloads/i })).not.toBeInTheDocument();

  // Guests cannot see Download or Monitor actions inline or inside unowned details
  rerender(<EntityView kind="series" name="Sequence" canManage={false} canRequest={false} />);
  expect(within(missingRow).queryByRole('button', { name: 'Download' })).not.toBeInTheDocument();
  expect(within(schedRow).queryByRole('button', { name: /Monitor/i })).not.toBeInTheDocument();
  expect(screen.queryByLabelText('Monitor series eBooks')).not.toBeInTheDocument();

  await user.click(within(guestRow).getByRole('button', { name: 'Guest Visible Entry' }));
  const guestDialog = await screen.findByRole('dialog', { name: 'Details for Guest Visible Entry' });
  expect(within(guestDialog).getByText('Guest readable blurb')).toBeInTheDocument();
  expect(within(guestDialog).getByText('No files available.')).toBeInTheDocument();
  expect(within(guestDialog).queryByRole('button', { name: 'Show Download Options' })).not.toBeInTheDocument();
  expect(within(guestDialog).queryByRole('button', { name: /Monitor eBook/i })).not.toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(2);
});

it('groups author books by series with standalone books separate and preserves details, actions, and navigation', async () => {
  const user = userEvent.setup({ delay: null });
  const detailBook: Book = {
    id: 'mist-2',
    title: 'Well of Ascension',
    subtitle: 'Second Volume',
    authorId: 'a-bs',
    authorName: 'Brandon Sanderson',
    seriesId: 'ser-mist',
    seriesTitle: 'Mistborn',
    seriesIndex: 2,
    releaseDate: '2007-08-21',
    state: 'In library',
    formats: ['EPUB'],
    editions: [],
  };

  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/catalog/entity?kind=authors&name=Brandon%20Sanderson') {
      return Response.json({
        monitored: true,
        books: [
          // Intentionally intermingled order matching SQL ORDER BY series_index, release_date
          { id: 'sa-1', title: 'The Way of Kings', authorName: 'Brandon Sanderson', seriesId: 'ser-sa', seriesTitle: 'Stormlight Archive', seriesIndex: 1, releaseDate: '2010-08-31', state: 'In library', formats: ['EPUB', 'M4B'], monitored: false },
          { id: 'mist-2', title: 'Well of Ascension', subtitle: 'Second Volume', authorName: 'Brandon Sanderson', seriesId: 'ser-mist', seriesTitle: 'Mistborn', seriesIndex: 2, releaseDate: '2007-08-21', state: 'In library', formats: ['EPUB'], monitored: false },
          { id: 'solo-late', title: 'Tress of the Emerald Sea', authorName: 'Brandon Sanderson', seriesTitle: '   ', releaseDate: '2023-01-10', state: 'Not in library', formats: [], monitored: false },
          { id: 'mist-0', title: 'The Eleventh Metal', authorName: 'Brandon Sanderson', seriesId: 'ser-mist', seriesTitle: 'Mistborn', seriesIndex: 0, releaseDate: '2011-01-01', state: 'In library', formats: ['EPUB'], monitored: false },
          { id: 'mist-unnumbered', title: 'Mistborn Companion', authorName: 'Brandon Sanderson', seriesTitle: 'mistborn', releaseDate: '2016-11-22', state: 'Scheduled', formats: [], monitored: false },
          { id: 'solo-early', title: 'Elantris', authorName: 'Brandon Sanderson', releaseDate: '2005-04-21', state: 'In library', formats: ['M4B'], monitored: false },
          { id: 'solo-nodate', title: 'Undated Novella', authorName: 'Brandon Sanderson', releaseDate: '', state: 'Not in library', formats: [], monitored: false },
          { id: 'mist-1', title: 'The Final Empire', authorName: 'Brandon Sanderson', seriesId: 'ser-mist', seriesTitle: 'Mistborn', seriesIndex: 1, releaseDate: '2006-07-17', state: 'In library', formats: ['EPUB'], monitored: false },
          { id: 'sa-2.5', title: 'Edgedancer', authorName: 'Brandon Sanderson', seriesId: 'ser-sa', seriesTitle: 'Stormlight Archive', seriesIndex: 2.5, releaseDate: '2016-11-22', state: 'Not in library', formats: [], monitored: false },
        ],
      });
    }
    if (url === '/api/books?id=mist-2') {
      return Response.json([detailBook]);
    }
    if (url === '/api/books/mist-unnumbered/monitor' && init?.method === 'PUT') {
      return Response.json({ id: 'mist-unnumbered', monitored: true });
    }
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<EntityView kind="authors" name="Brandon Sanderson" canManage canRequest />);

  // Series groups sorted alphabetically, followed by Standalone Books
  const groupHeadings = await screen.findAllByRole('heading', { level: 3 });
  expect(groupHeadings.map((h) => h.textContent)).toEqual([
    'Mistborn',
    'Stormlight Archive',
    'Standalone Books',
  ]);

  // Series headings link to their series view with seriesId preserved
  expect(within(groupHeadings[0]).getByRole('link', { name: 'Mistborn' })).toHaveAttribute('href', '#/series/Mistborn?id=ser-mist');
  expect(within(groupHeadings[1]).getByRole('link', { name: 'Stormlight Archive' })).toHaveAttribute('href', '#/series/Stormlight%20Archive?id=ser-sa');
  expect(within(groupHeadings[2]).queryByRole('link')).not.toBeInTheDocument();

  // Mistborn reading order: Book 0 -> Book 1 -> Book 2 -> unnumbered companion (merged case-insensitively)
  const mistbornList = screen.getByRole('list', { name: 'Mistborn' });
  const mistbornItems = within(mistbornList).getAllByRole('listitem');
  expect(mistbornItems.map((li) => within(li).getAllByRole('button')[0].textContent)).toEqual([
    'The Eleventh Metal',
    'The Final Empire',
    'Well of Ascension',
    'Mistborn Companion',
  ]);
  expect(mistbornItems[0]).toHaveTextContent('Book 0');
  expect(mistbornItems[2]).toHaveTextContent('Second Volume');
  expect(mistbornItems[2]).toHaveTextContent('2007-08-21');
  expect(mistbornItems[2]).toHaveTextContent('EPUB');
  expect(mistbornItems[2]).toHaveTextContent('In library');
  expect(screen.getByText('3 of 4 in library')).toBeInTheDocument();

  // Stormlight Archive reading order: Book 1 -> Book 2.5
  const stormlightList = screen.getByRole('list', { name: 'Stormlight Archive' });
  const stormlightItems = within(stormlightList).getAllByRole('listitem');
  expect(stormlightItems.map((li) => within(li).getAllByRole('button')[0].textContent)).toEqual([
    'The Way of Kings',
    'Edgedancer',
  ]);
  expect(stormlightItems[1]).toHaveTextContent('Book 2.5');
  expect(screen.getByText('1 of 2 in library')).toBeInTheDocument();

  // Standalone Books ordered by releaseDate ascending with unknown dates last
  const standaloneList = screen.getByRole('list', { name: 'Standalone Books' });
  const standaloneItems = within(standaloneList).getAllByRole('listitem');
  expect(standaloneItems.map((li) => within(li).getAllByRole('button')[0].textContent)).toEqual([
    'Elantris',
    'Tress of the Emerald Sea',
    'Undated Novella',
  ]);
  expect(standaloneItems[2]).toHaveTextContent('Release date unknown');
  expect(screen.getByText('1 of 3 in library')).toBeInTheDocument();

  // Inline Monitor on a reordered series book updates that exact entry
  await user.click(within(mistbornItems[3]).getByRole('button', { name: 'Monitor eBook' }));
  await waitFor(() => {
    expect(within(mistbornItems[3]).getByRole('button', { name: 'Monitor eBook' })).toHaveAttribute('aria-pressed', 'true');
  });

  // Clicking a reordered book opens its BookDetailModal
  await user.click(within(mistbornItems[2]).getByRole('button', { name: 'Well of Ascension' }));
  const dialog = await screen.findByRole('dialog', { name: 'Details for Well of Ascension' });
  expect(within(dialog).getByText('Second Volume')).toBeInTheDocument();
  await user.click(within(dialog).getByRole('button', { name: 'Close details' }));
});

it('omits standalone section when all author books belong to series and omits series sections when all are standalone', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      monitored: false,
      books: [
        { id: 'b1', title: 'Lone Novel', authorName: 'Solo Author', releaseDate: '2021-01-01', state: 'In library', formats: ['EPUB'] },
      ],
    }),
  }));

  const { unmount } = render(<EntityView kind="authors" name="Solo Author" canManage={false} />);
  const headings = await screen.findAllByRole('heading', { level: 3 });
  expect(headings.map((h) => h.textContent)).toEqual(['Standalone Books']);
  expect(screen.getByRole('list', { name: 'Standalone Books' })).toHaveTextContent('Lone Novel');
  unmount();

  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      monitored: false,
      books: [
        { id: 'b2', title: 'Series Book', authorName: 'Series Author', seriesId: 's1', seriesTitle: 'Only Series', seriesIndex: 1, releaseDate: '2022-01-01', state: 'In library', formats: ['EPUB'] },
      ],
    }),
  }));

  render(<EntityView kind="authors" name="Series Author" canManage={false} />);
  const seriesHeadings = await screen.findAllByRole('heading', { level: 3 });
  expect(seriesHeadings.map((h) => h.textContent)).toEqual(['Only Series']);
  expect(screen.queryByRole('heading', { name: 'Standalone Books' })).not.toBeInTheDocument();
});

it.each([
  { kind: 'series' as const, name: 'Sequence', list: 'Reading Order' },
  { kind: 'authors' as const, name: 'Author One', list: 'Sequence' },
])('cancels in-progress downloads and unmonitors individual books in $kind view', async ({ kind, name, list }) => {
  const user = userEvent.setup({ delay: null });
  const refreshActivity = vi.fn();
  const onMetadataApplied = vi.fn();
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.startsWith('/api/catalog/entity?')) {
      return Response.json({
        monitored: true,
        books: [
          { id: 'b-dl', title: 'Downloading Book', authorName: 'Author One', seriesId: 's1', seriesTitle: 'Sequence', seriesIndex: 1, state: 'Downloading', monitored: true, audiobookMonitored: false, formats: [] },
          { id: 'b-mon', title: 'Monitored Book', authorName: 'Author One', seriesId: 's1', seriesTitle: 'Sequence', seriesIndex: 2, state: 'Not in library', monitored: true, audiobookMonitored: true, formats: [] },
          { id: 'b-owned-audio', title: 'Owned With Audio Download', authorName: 'Author One', seriesId: 's1', seriesTitle: 'Sequence', seriesIndex: 3, state: 'In library', monitored: false, audiobookMonitored: true, formats: ['EPUB'] },
          { id: 'b-conflict', title: 'Importing Book', authorName: 'Author One', seriesId: 's1', seriesTitle: 'Sequence', seriesIndex: 4, state: 'Downloading', monitored: true, formats: [] },
        ],
      });
    }
    if (url === '/api/books/b-dl/cancel' && init?.method === 'POST') {
      return Response.json({ id: 'b-dl', monitored: false, audiobookMonitored: false, state: 'Not in library', cancelledRequests: 1, cancelledDownloads: 0, cancelledConversions: 0 });
    }
    if (url === '/api/books/b-mon/cancel' && init?.method === 'POST') {
      return Response.json({ id: 'b-mon', monitored: false, audiobookMonitored: false, state: 'Not in library', cancelledRequests: 0, cancelledDownloads: 0, cancelledConversions: 0 });
    }
    if (url === '/api/books/b-owned-audio/cancel' && init?.method === 'POST') {
      return Response.json({ id: 'b-owned-audio', monitored: false, audiobookMonitored: false, state: 'In library', cancelledRequests: 1, cancelledDownloads: 0, cancelledConversions: 0 });
    }
    if (url === '/api/books/b-conflict/cancel' && init?.method === 'POST') {
      return Response.json({ error: 'import is finishing; try again when it completes' }, { status: 409 });
    }
    throw new Error(`Unexpected fetch ${url} (${init?.method ?? 'GET'})`);
  });
  vi.stubGlobal('fetch', fetchMock);

  const activeItems = [
    {
      id: 'request:99',
      bookId: 'b-owned-audio',
      title: 'Owned With Audio Download',
      format: 'Audiobook',
      phase: 'Downloading',
      stage: 'downloading' as const,
      request: { id: '99', bookId: 'b-owned-audio', title: 'Owned With Audio Download', author: 'Author One', requestedBy: 'u1', requestType: 'audiobook' as const, status: 'downloading' as const, requestedAt: '' },
    },
  ];

  const { rerender } = render(
    <ActivityRefreshContext.Provider value={refreshActivity}>
      <BookActivityContext.Provider value={activeItems}>
        <EntityView kind={kind} name={name} canManage={false} canRequest onMetadataApplied={onMetadataApplied} />
      </BookActivityContext.Provider>
    </ActivityRefreshContext.Provider>
  );

  const bookList = await screen.findByRole('list', { name: list });
  const [dlRow, monRow, ownedAudioRow, conflictRow] = within(bookList).getAllByRole('listitem');

  // 1. Cancel & Unmonitor on downloading book
  await user.click(within(dlRow).getByRole('button', { name: 'Cancel download and unmonitor Downloading Book' }));
  await waitFor(() => {
    expect(within(dlRow).getByText('Not in library')).toBeInTheDocument();
  });
  expect(within(dlRow).queryByRole('button', { name: /Cancel download and unmonitor/i })).not.toBeInTheDocument();
  expect(within(dlRow).getByRole('button', { name: 'Download' })).toBeInTheDocument();

  // 2. Unmonitor on monitored (non-downloading) book
  await user.click(within(monRow).getByRole('button', { name: 'Unmonitor Monitored Book' }));
  await waitFor(() => {
    expect(within(monRow).queryByRole('button', { name: 'Unmonitor Monitored Book' })).not.toBeInTheDocument();
  });

  // 3. Cancel & Unmonitor on In library book with active secondary-format download
  expect(within(ownedAudioRow).getByText('Downloading')).toBeInTheDocument();
  await user.click(within(ownedAudioRow).getByRole('button', { name: 'Cancel download and unmonitor Owned With Audio Download' }));
  expect(fetchMock).toHaveBeenCalledWith('/api/books/b-owned-audio/cancel', { method: 'POST' });

  // 4. Error handling on 409 conflict
  await user.click(within(conflictRow).getByRole('button', { name: 'Cancel download and unmonitor Importing Book' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('import is finishing');
  expect(within(conflictRow).getByRole('button', { name: 'Cancel download and unmonitor Importing Book' })).toBeEnabled();
  expect(refreshActivity).toHaveBeenCalledTimes(3);
  expect(onMetadataApplied).toHaveBeenCalledTimes(3);

  // 5. Guests cannot see cancel or unmonitor buttons
  rerender(
    <ActivityRefreshContext.Provider value={refreshActivity}>
      <BookActivityContext.Provider value={activeItems}>
        <EntityView kind={kind} name={name} canManage={false} canRequest={false} />
      </BookActivityContext.Provider>
    </ActivityRefreshContext.Provider>
  );
  expect(within(conflictRow).queryByRole('button', { name: /Cancel download and unmonitor/i })).not.toBeInTheDocument();
});

