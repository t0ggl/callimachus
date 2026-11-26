import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { SeriesBadge } from '../components/SeriesBadge';
import { SeriesDetailModal } from '../components/SeriesDetailModal';
import { DiscoveryResults } from '../components/DiscoveryResults';
import { CatalogView } from '../views/CatalogView';
import { Book } from '../types';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it.each([
  [1, 3, 'Book 1 of 3 in'],
  [2.5, 4, 'Book 2.5 of 4 in'],
  [0, undefined, 'Book 0 in'],
  [0.5, 0, 'Book 0.5 in'],
  [undefined, 3, 'In'],
])('formats index %s with total %s', async (index, total, text) => {
  const select = vi.fn();
  render(<SeriesBadge title="Broken Earth" index={index} total={total} onSelect={select} />);
  expect(screen.getByRole('button').parentElement).toHaveTextContent(text);
  await userEvent.setup({ delay: null }).click(screen.getByRole('button', { name: 'Broken Earth' }));
  expect(select).toHaveBeenCalledWith({ title: 'Broken Earth' });
});

it('opens reading order from discovery with series details, covers, formats, and owned book navigation', async () => {
  const openBook = vi.fn();
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => [
          { title: 'Novella', authors: ['Author One'], series: 'Sequence', series_index: 2.5, series_total: 4 },
        ],
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          title: 'Sequence',
          description: 'An epic multi-volume chronicle.',
          status: 'ongoing',
          monitored: true,
          authors: ['Author One', 'Co-Author'],
          total_books: 4,
          owned_count: 1,
          complete: true,
          books: [
            {
              title: 'Prelude',
              subtitle: 'Before the Fall',
              authors: ['Author One'],
              description: 'Short prequel blurb.',
              cover_url: 'https://covers.example/prelude.jpg',
              series_index: 0.5,
              publish_date: '2018-03-01',
              formats: [],
              state: 'Searching',
              library_book_id: 'book-pending',
            },
            {
              title: 'Novella',
              subtitle: 'Book 2.5 Companion',
              authors: ['Author One'],
              description: 'Owned side story.',
              cover_url: '/api/books/book-25/cover',
              series_index: 2.5,
              series_index_display: '#2.5',
              publish_date: '2020-01-02',
              formats: ['EPUB', 'M4B'],
              state: 'In library',
              library_book_id: 'book-25',
            },
            {
              title: 'Companion Appendix',
              authors: ['Co-Author'],
              series_index: null,
              formats: [],
              state: 'Not in library',
            },
          ],
        }),
      })
  );
  const user = userEvent.setup({ delay: null });
  render(<DiscoveryResults query="Dune" onSubmitRequest={vi.fn()} onOpenBook={openBook} />);
  await user.click(await screen.findByRole('button', { name: 'Sequence' }));

  const dialog = await screen.findByRole('dialog');
  expect(dialog).toHaveAttribute('aria-modal', 'true');
  expect(within(dialog).getByRole('heading', { name: 'Sequence' })).toBeInTheDocument();
  expect(within(dialog).getByText('ongoing')).toBeInTheDocument();
  expect(within(dialog).getByText('1 / 4 in library')).toBeInTheDocument();
  expect(within(dialog).getByText('Series Monitored')).toBeInTheDocument();
  expect(within(dialog).getByText('An epic multi-volume chronicle.')).toBeInTheDocument();
  expect(within(dialog).getAllByRole('link', { name: 'Co-Author' })[0]).toHaveAttribute('href', '#/authors/Co-Author');

  const items = within(dialog).getAllByRole('listitem');
  expect(items).toHaveLength(3);
  expect(within(items[0]).getByText('Book 0.5')).toBeInTheDocument();
  expect(within(items[0]).getByText('Prelude')).toBeInTheDocument();
  expect(within(items[0]).getByRole('img', { name: 'Cover of Prelude' })).toHaveAttribute('src', 'https://covers.example/prelude.jpg');
  expect(within(items[0]).getByText('Searching')).toBeInTheDocument();
  expect(within(items[0]).getByRole('button', { name: 'Prelude' })).toBeInTheDocument();

  expect(within(dialog).getAllByText('Book 2.5')).toHaveLength(1);
  expect(within(items[1]).getByText('Book 2.5')).toBeInTheDocument();
  expect(within(dialog).queryByText('#2.5')).not.toBeInTheDocument();
  expect(within(items[1]).getByText('EPUB')).toBeInTheDocument();
  expect(within(items[1]).getByText('M4B')).toBeInTheDocument();
  expect(within(items[1]).getByText('2020-01-02')).toBeInTheDocument();
  expect(within(items[1]).getByText('In library')).toBeInTheDocument();

  expect(within(items[2]).getByText('Unindexed')).toBeInTheDocument();
  expect(within(items[2]).getByText('Release date unknown')).toBeInTheDocument();
  expect(within(items[2]).getByLabelText('No cover available')).toBeInTheDocument();
  expect(within(items[2]).getByText('Not in library')).toBeInTheDocument();
  expect(within(dialog).queryByText(/^(Not monitored|Monitored)$/)).not.toBeInTheDocument();

  await user.click(within(items[1]).getByRole('button', { name: 'Novella' }));
  expect(openBook).toHaveBeenCalledWith('book-25');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('opens the full series page from the modal button and closes the modal', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ title: 'Earth / Sea', complete: true, books: [] }),
    })
  );
  const onClose = vi.fn();
  const user = userEvent.setup({ delay: null });
  const originalUrl = window.location.href;
  render(<SeriesDetailModal title="Earth" onClose={onClose} />);

  const button = await screen.findByRole('button', { name: 'Series page' });
  expect(button).toHaveAttribute('type', 'button');
  expect(screen.queryByRole('link', { name: 'Series page' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Monitor series eBooks' })).not.toBeInTheDocument();

  try {
    await user.click(button);
    expect(window.location.hash).toBe('#/series/Earth%20%2F%20Sea');
    expect(onClose).toHaveBeenCalledOnce();
  } finally {
    window.history.replaceState(null, '', originalUrl);
  }
});

it.each([false, true])('toggles series eBook monitoring directly with audiobook monitoring set to %s', async (audiobookMonitored) => {
  const user = userEvent.setup({ delay: null });
  const onClose = vi.fn();
  const onSeriesUpdated = vi.fn();
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.startsWith('/api/series/books?')) {
      return Response.json({
        id: 'ser-hdm',
        title: 'His Dark Materials',
        authors: ['Philip Pullman'],
        monitored: false,
        audiobookMonitored,
        total_books: 3,
        owned_count: 0,
        complete: true,
        books: [],
      });
    }
    if (url.startsWith('/api/catalog/entity?') && init?.method === 'PUT') {
      return new Response(null, { status: 204 });
    }
    throw new Error(`Unexpected fetch ${url} (${init?.method ?? 'GET'})`);
  });
  vi.stubGlobal('fetch', fetchMock);

  render(
    <SeriesDetailModal
      title="His Dark Materials"
      hardcoverID={42}
      canManage
      onClose={onClose}
      onSeriesUpdated={onSeriesUpdated}
    />
  );

  const monitorBtn = await screen.findByRole('button', { name: 'Monitor series eBooks' });
  expect(monitorBtn).toHaveAttribute('aria-pressed', 'false');
  expect(screen.getByText(audiobookMonitored ? 'Series Monitored' : 'Series Unmonitored')).toBeInTheDocument();

  await user.click(monitorBtn);

  await waitFor(() => {
    expect(screen.getByRole('button', { name: 'Monitor series eBooks' })).toHaveAttribute('aria-pressed', 'true');
  });
  expect(screen.getByText('Series Monitored')).toBeInTheDocument();
  expect(onSeriesUpdated).toHaveBeenCalledWith(true);
  expect(onClose).not.toHaveBeenCalled();

  const putCall = fetchMock.mock.calls.find(([u, init]) => String(u).startsWith('/api/catalog/entity?') && init?.method === 'PUT');
  expect(putCall).toBeDefined();
  expect(String(putCall?.[0])).toContain('kind=series');
  expect(String(putCall?.[0])).toContain('name=His+Dark+Materials');
  expect(String(putCall?.[0])).toContain('id=ser-hdm');
  expect(putCall?.[1]?.body).toBe('{"monitored":true}');

  // Toggling off works in place too
  await user.click(screen.getByRole('button', { name: 'Monitor series eBooks' }));
  await waitFor(() => {
    expect(screen.getByRole('button', { name: 'Monitor series eBooks' })).toHaveAttribute('aria-pressed', 'false');
  });
  expect(screen.getByText(audiobookMonitored ? 'Series Monitored' : 'Series Unmonitored')).toBeInTheDocument();
  expect(onSeriesUpdated).toHaveBeenLastCalledWith(audiobookMonitored);
});

it('handles missing metadata gracefully and supports Escape, backdrop, and footer close', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        title: 'Sparse Series',
        complete: true,
        books: [
          {
            title: 'Lone Entry',
            cover_url: 'https://covers.example/broken.jpg',
            monitored: false,
          },
        ],
      }),
    })
  );

  const onClose = vi.fn();
  const user = userEvent.setup({ delay: null });
  render(<SeriesDetailModal title="Sparse Series" onClose={onClose} />);

  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByTestId('close-series-detail')).toHaveFocus();
  expect(within(dialog).getByText('No series description available.')).toBeInTheDocument();
  expect(within(dialog).getByText('Author unknown')).toBeInTheDocument();
  expect(within(dialog).getByText('0 / 1 in library')).toBeInTheDocument();
  expect(within(dialog).getByText('Unindexed')).toBeInTheDocument();
  expect(within(dialog).getByText('Release date unknown')).toBeInTheDocument();

  // Broken image falls back to placeholder
  const cover = within(dialog).getByRole('img', { name: 'Cover of Lone Entry' });
  fireEvent.error(cover);
  expect(await within(dialog).findByLabelText('No cover available')).toBeInTheDocument();

  // Close via Escape key
  await user.keyboard('{Escape}');
  expect(onClose).toHaveBeenCalledTimes(1);

  // Close via backdrop click (but not inner modal click)
  fireEvent.click(within(dialog).getByRole('heading', { name: 'Sparse Series' }));
  expect(onClose).toHaveBeenCalledTimes(1);
  fireEvent.click(dialog);
  expect(onClose).toHaveBeenCalledTimes(2);

  // Close via footer Close (Esc) button and header Close button
  await user.click(within(dialog).getByRole('button', { name: 'Close (Esc)' }));
  expect(onClose).toHaveBeenCalledTimes(3);
  await user.click(within(dialog).getByRole('button', { name: 'Close' }));
  expect(onClose).toHaveBeenCalledTimes(4);
});

it('opens owned catalog book from SeriesDetailModal inside CatalogView', async () => {
  const sampleBook: Book = {
    id: 'book-1',
    title: 'Dune',
    authorId: 'auth-1',
    authorName: 'Frank Herbert',
    seriesId: 'ser-1',
    seriesTitle: 'Dune Saga',
    seriesIndex: 1,
    seriesIndexDisplay: '#1',
    releaseDate: '1965-08-01',
    formats: ['EPUB'],
    editions: [],
  };
  const onSelectBook = vi.fn();
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        title: 'Dune Saga',
        description: 'Desert planet chronicle.',
        status: 'completed',
        monitored: true,
        authors: ['Frank Herbert'],
        total_books: 1,
        owned_count: 1,
        complete: true,
        books: [
          {
            title: 'Dune',
            authors: ['Frank Herbert'],
            series_index: 1,
            series_index_display: 'Book 1',
            publish_date: '1965-08-01',
            formats: ['EPUB'],
            library_book_id: 'book-1',
            monitored: true,
          },
        ],
      }),
    })
  );

  const user = userEvent.setup({ delay: null });
  render(
    <CatalogView
      books={[sampleBook]}
      authors={[{ id: 'auth-1', name: 'Frank Herbert', sortName: 'Herbert, Frank', bookCount: 1 }]}
      series={[{ id: 'ser-1', title: 'Dune Saga', totalBooks: 1, status: 'completed' }]}
      onSelectBook={onSelectBook}
    />
  );

  await user.click(screen.getByRole('button', { name: 'Dune Saga' }));
  const dialog = await screen.findByRole('dialog');
  await user.click(within(dialog).getByRole('button', { name: 'Dune' }));
  expect(onSelectBook).toHaveBeenCalledWith(sampleBook);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('offers retry on failure and explains incomplete order', async () => {
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockResolvedValueOnce({ ok: false })
      .mockResolvedValueOnce({ ok: true, json: async () => ({ complete: false, books: [] }) })
  );
  render(<SeriesDetailModal title="Sequence" onClose={vi.fn()} />);
  await screen.findByRole('alert');
  await userEvent.setup({ delay: null }).click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByText(/Showing known catalog/)).toBeInTheDocument());
  expect(screen.getByText('No books found for this series.')).toBeInTheDocument();
});

it('bundles series books in grid and list views by default while keeping standalone books individual', async () => {
  const books: Book[] = [
    {
      id: 'book-dune-2',
      title: 'Dune Messiah',
      authorId: 'auth-1',
      authorName: 'Frank Herbert',
      seriesId: 'ser-dune',
      seriesTitle: 'Dune Chronicles',
      seriesIndex: 2,
      seriesIndexDisplay: 'Book 2',
      coverUrl: '/api/books/book-dune-2/cover',
      releaseDate: '1969-10-15',
      formats: ['M4B'],
      editions: [{ id: 'ed-2', bookId: 'book-dune-2', format: 'M4B', language: 'en', assets: [] }],
    },
    {
      id: 'book-dune-1',
      title: 'Dune',
      authorId: 'auth-1',
      authorName: 'Frank Herbert',
      seriesId: 'ser-dune',
      seriesTitle: 'Dune Chronicles',
      seriesIndex: 1,
      seriesIndexDisplay: '#1',
      coverUrl: '/api/books/book-dune-1/cover',
      releaseDate: '1965-08-01',
      formats: ['EPUB'],
      editions: [{ id: 'ed-1', bookId: 'book-dune-1', format: 'EPUB', language: 'en', assets: [] }],
    },
    {
      id: 'book-standalone',
      title: 'Standalone Tales',
      authorId: 'auth-2',
      authorName: 'Ursula Le Guin',
      coverUrl: '/api/books/book-standalone/cover',
      releaseDate: '1975-01-01',
      formats: ['EPUB'],
      editions: [{ id: 'ed-3', bookId: 'book-standalone', format: 'EPUB', language: 'en', assets: [] }],
    },
  ];
  const onSelectBook = vi.fn();
  const user = userEvent.setup({ delay: null });

  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        title: 'Dune Chronicles',
        status: 'ongoing',
        monitored: true,
        authors: ['Frank Herbert'],
        total_books: 6,
        owned_count: 2,
        complete: true,
        books: [
          { title: 'Dune', authors: ['Frank Herbert'], series_index: 1, library_book_id: 'book-dune-1', formats: ['EPUB'], monitored: true },
          { title: 'Dune Messiah', authors: ['Frank Herbert'], series_index: 2, library_book_id: 'book-dune-2', formats: ['M4B'], monitored: true },
        ],
      }),
    })
  );

  render(
    <CatalogView
      books={books}
      authors={[
        { id: 'auth-1', name: 'Frank Herbert', sortName: 'Herbert, Frank', bookCount: 2 },
        { id: 'auth-2', name: 'Ursula Le Guin', sortName: 'Le Guin, Ursula', bookCount: 1 },
      ]}
      series={[{ id: 'ser-dune', title: 'Dune Chronicles', totalBooks: 6, status: 'ongoing' }]}
      onSelectBook={onSelectBook}
    />
  );

  // Grid view: 1 bundled series card + 1 standalone book card
  const seriesCard = screen.getByTestId('series-card-ser-dune');
  expect(seriesCard).toBeInTheDocument();
  expect(within(seriesCard).getByText('Series')).toBeInTheDocument();
  expect(within(seriesCard).getByText('2 books')).toBeInTheDocument();
  expect(within(seriesCard).getByText(/of 6 in series/)).toBeInTheDocument();
  expect(within(seriesCard).getByText('EPUB')).toBeInTheDocument();
  expect(within(seriesCard).getByText('M4B')).toBeInTheDocument();
  // Picks Book 1's cover (lowest seriesIndex)
  expect(within(seriesCard).getByRole('img', { name: 'Dune Chronicles' })).toHaveAttribute(
    'src',
    '/api/books/book-dune-1/cover'
  );
  expect(screen.queryByTestId('book-card-book-dune-1')).not.toBeInTheDocument();
  expect(screen.queryByTestId('book-card-book-dune-2')).not.toBeInTheDocument();

  const standaloneCard = screen.getByTestId('book-card-book-standalone');
  expect(standaloneCard).toBeInTheDocument();

  // Search filter works cleanly with bundled series (matches book title inside series)
  const searchInput = screen.getByPlaceholderText('Filter books...');
  await user.type(searchInput, 'Messiah');
  expect(screen.getByTestId('series-card-ser-dune')).toBeInTheDocument();
  expect(screen.queryByTestId('book-card-book-standalone')).not.toBeInTheDocument();
  await user.clear(searchInput);

  // Format filter works cleanly with bundled series
  await user.click(screen.getByTestId('filter-format-m4b'));
  expect(screen.getByTestId('series-card-ser-dune')).toBeInTheDocument();
  expect(within(screen.getByTestId('series-card-ser-dune')).getByText('1 book')).toBeInTheDocument();
  expect(screen.queryByTestId('book-card-book-standalone')).not.toBeInTheDocument();
  await user.click(screen.getByTestId('filter-format-all'));

  // Clicking standalone book opens BookDetailModal callback
  await user.click(screen.getByTestId('book-card-book-standalone'));
  expect(onSelectBook).toHaveBeenCalledWith(books[2]);

  // Clicking bundled series card opens SeriesDetailModal
  await user.click(screen.getByTestId('series-card-ser-dune'));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByRole('heading', { name: 'Dune Chronicles' })).toBeInTheDocument();
  await user.click(within(dialog).getByTestId('close-series-detail'));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

  // List view also bundles series books and opens SeriesDetailModal on click
  await user.click(screen.getByTestId('view-list-btn'));
  const seriesRow = screen.getByTestId('series-row-ser-dune');
  expect(seriesRow).toBeInTheDocument();
  expect(within(seriesRow).getByText('Series')).toBeInTheDocument();
  expect(within(seriesRow).getByText('2 books / 6')).toBeInTheDocument();
  expect(within(seriesRow).getByText('2')).toBeInTheDocument(); // combined editions count
  expect(screen.getByTestId('book-row-book-standalone')).toBeInTheDocument();
  expect(screen.queryByTestId('book-row-book-dune-1')).not.toBeInTheDocument();

  await user.click(seriesRow);
  expect(await screen.findByRole('dialog')).toBeInTheDocument();
});

it('renders every book individually in grid and list views when bundling is disabled', async () => {
  const books: Book[] = [
    {
      id: 'book-dune-1',
      title: 'Dune',
      authorId: 'auth-1',
      authorName: 'Frank Herbert',
      seriesId: 'ser-dune',
      seriesTitle: 'Dune Chronicles',
      seriesIndex: 1,
      formats: ['EPUB'],
      editions: [],
    },
    {
      id: 'book-dune-2',
      title: 'Dune Messiah',
      authorId: 'auth-1',
      authorName: 'Frank Herbert',
      seriesId: 'ser-dune',
      seriesTitle: 'Dune Chronicles',
      seriesIndex: 2,
      formats: ['M4B'],
      editions: [],
    },
  ];
  const onSelectBook = vi.fn();
  const user = userEvent.setup({ delay: null });

  render(
    <CatalogView
      books={books}
      authors={[{ id: 'auth-1', name: 'Frank Herbert', sortName: 'Herbert, Frank', bookCount: 2 }]}
      series={[{ id: 'ser-dune', title: 'Dune Chronicles', totalBooks: 6, status: 'ongoing' }]}
      bundleSeriesInLibrary={false}
      onSelectBook={onSelectBook}
    />
  );

  expect(screen.queryByTestId('series-card-ser-dune')).not.toBeInTheDocument();
  expect(screen.getByTestId('book-card-book-dune-1')).toBeInTheDocument();
  expect(screen.getByTestId('book-card-book-dune-2')).toBeInTheDocument();

  await user.click(screen.getByTestId('book-card-book-dune-1'));
  expect(onSelectBook).toHaveBeenCalledWith(books[0]);

  await user.click(screen.getByTestId('view-list-btn'));
  expect(screen.queryByTestId('series-row-ser-dune')).not.toBeInTheDocument();
  expect(screen.getByTestId('book-row-book-dune-1')).toBeInTheDocument();
  expect(screen.getByTestId('book-row-book-dune-2')).toBeInTheDocument();
});

it('opens unowned book details, inline Download options, and inline Monitor for Scheduled books in SeriesDetailModal', async () => {
  const user = userEvent.setup({ delay: null });
  const unownedBook: Book = {
    id: 'book-unowned',
    title: 'Missing Tome',
    authorId: 'auth-1',
    authorName: 'Author One',
    seriesTitle: 'Sequence',
    seriesIndex: 2,
    releaseDate: '2020-03-01',
    synopsis: 'Missing blurb.',
    coverUrl: 'https://covers.example/missing.jpg',
    state: 'Not in library',
    monitored: false,
    formats: [],
    editions: [],
  };
  const scheduledBook: Book = {
    id: 'book-sched',
    title: 'Future Tome',
    authorId: 'auth-1',
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
    if (url.startsWith('/api/series/books?')) {
      return Response.json({
        title: 'Sequence',
        authors: ['Author One'],
        total_books: 2,
        owned_count: 0,
        complete: true,
        books: [
          {
            title: 'Missing Tome',
            authors: ['Author One'],
            description: 'Missing blurb.',
            cover_url: 'https://covers.example/missing.jpg',
            publish_date: '2020-03-01',
            series_index: 2,
            formats: [],
            state: 'Not in library',
          },
          {
            title: 'Future Tome',
            authors: ['Author One'],
            publish_date: '2099-09-01',
            series_index: 3,
            formats: [],
            state: 'Scheduled',
            monitored: false,
          },
        ],
      });
    }
    if (url === '/api/books/resolve' && init?.method === 'POST') {
      const body = JSON.parse(String(init.body));
      if (body.title === 'Missing Tome') return Response.json(unownedBook);
      if (body.title === 'Future Tome') return Response.json(scheduledBook);
    }
    if (url === '/api/books/book-sched/monitor' && init?.method === 'PUT') {
      return Response.json({ id: 'book-sched', monitored: true });
    }
    if (url === '/api/requests' && (!init?.method || init.method === 'GET')) {
      return Response.json([{ id: 42, book_id: 'book-unowned', format: 'epub' }]);
    }
    if (url === '/api/requests/42/candidates') {
      return Response.json([{ key: '1:g', format: 'epub', score: 90, penalty: 0, breakdown: { format: 30 }, release: { title: 'Missing Tome EPUB', size: 1024, seeders: 10 } }]);
    }
    throw new Error(`Unexpected fetch ${url} (${init?.method ?? 'GET'})`);
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<SeriesDetailModal title="Sequence" canRequest canManage onClose={vi.fn()} />);

  const list = await screen.findByRole('list', { name: 'Series reading order' });
  const [missingItem, scheduledItem] = within(list).getAllByRole('listitem');

  // 1. Clicking unowned book title opens BookDetailModal read-only WITHOUT calling POST /api/books/resolve.
  await user.click(within(missingItem).getByRole('button', { name: 'Missing Tome' }));
  const detailDialog = await screen.findByRole('dialog', { name: 'Details for Missing Tome' });
  expect(within(detailDialog).getByText('Missing blurb.')).toBeInTheDocument();
  expect(within(detailDialog).getByText('2020-03-01')).toBeInTheDocument();
  expect(within(detailDialog).getByRole('img', { name: 'Missing Tome' })).toHaveAttribute('src', 'https://covers.example/missing.jpg');
  expect(within(detailDialog).getByRole('button', { name: 'Show Download Options' })).toBeInTheDocument();
  expect(within(detailDialog).getByRole('button', { name: 'Monitor eBook' })).toHaveAttribute('aria-pressed', 'false');
  expect(within(detailDialog).getByText('No files available.')).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(0);
  await user.click(within(detailDialog).getByRole('button', { name: 'Close details' }));

  // 2. Inline Download next to "Not in library" lazily resolves once and opens ManualSearch.
  await user.click(within(missingItem).getByRole('button', { name: 'Download' }));
  const downloadDialog = await screen.findByRole('dialog', { name: 'Download Options' });
  expect(within(downloadDialog).getByRole('tablist', { name: 'Download format' })).toBeInTheDocument();
  expect(await within(downloadDialog).findByText('Missing Tome EPUB')).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(1);
  await user.click(within(downloadDialog).getByRole('button', { name: 'Close' }));

  // 3. Inline Monitor next to "Scheduled" resolves book & persists books.monitored.
  const monitorBtn = within(scheduledItem).getByRole('button', { name: 'Monitor eBook' });
  expect(monitorBtn).toHaveAttribute('aria-pressed', 'false');
  await user.click(monitorBtn);
  await waitFor(() => {
    expect(within(scheduledItem).getByRole('button', { name: 'Monitor eBook' })).toHaveAttribute('aria-pressed', 'true');
  });
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/books/book-sched/monitor',
    expect.objectContaining({ method: 'PUT', body: '{"monitored":true}' }),
  );
});

it('lets guests inspect unowned book details read-only and allows requesters to download and monitor in SeriesDetailModal', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.startsWith('/api/series/books?')) {
      return Response.json({
        title: 'Sequence',
        authors: ['Author One'],
        total_books: 2,
        owned_count: 0,
        complete: true,
        books: [
          {
            title: 'Missing Tome',
            subtitle: 'Book Two Subtitle',
            authors: ['Author One'],
            description: 'Guest visible synopsis.',
            cover_url: 'https://covers.example/missing.jpg',
            publish_date: '2020-03',
            series_index: 2,
            formats: [],
            state: 'Not in library',
          },
          {
            title: 'Future Tome',
            authors: ['Author One'],
            description: 'Scheduled synopsis.',
            publish_date: '2099-09',
            series_index: 3,
            formats: [],
            state: 'Scheduled',
            monitored: false,
          },
        ],
      });
    }
    if (url === '/api/books/resolve' && init?.method === 'POST') {
      return Response.json({
        id: 'req-unowned',
        title: 'Missing Tome',
        authorId: 'auth-1',
        authorName: 'Author One',
        state: 'Not in library',
        formats: [],
        editions: [],
      });
    }
    if (url === '/api/requests' && (!init?.method || init.method === 'GET')) {
      return Response.json([{ id: 55, book_id: 'req-unowned', format: 'epub' }]);
    }
    if (url === '/api/requests/55/candidates') {
      return Response.json([]);
    }
    throw new Error(`Unexpected fetch ${url} (${init?.method ?? 'GET'})`);
  });
  vi.stubGlobal('fetch', fetchMock);

  // Guest: read-only access to full details with zero write actions or /api/books/resolve writes
  const { rerender } = render(
    <SeriesDetailModal title="Sequence" canManage={false} canRequest={false} onClose={vi.fn()} />
  );
  const list = await screen.findByRole('list', { name: 'Series reading order' });
  const [missingItem, scheduledItem] = within(list).getAllByRole('listitem');
  expect(within(missingItem).queryByRole('button', { name: 'Download' })).not.toBeInTheDocument();
  expect(within(scheduledItem).queryByRole('button', { name: /Monitor/i })).not.toBeInTheDocument();

  await user.click(within(missingItem).getByRole('button', { name: 'Missing Tome' }));
  const guestDetail = await screen.findByRole('dialog', { name: 'Details for Missing Tome' });
  expect(within(guestDetail).getByText('Book Two Subtitle')).toBeInTheDocument();
  expect(within(guestDetail).getByText('Guest visible synopsis.')).toBeInTheDocument();
  expect(within(guestDetail).getByText('2020-03')).toBeInTheDocument();
  expect(within(guestDetail).getByText('No files available.')).toBeInTheDocument();
  expect(within(guestDetail).queryByRole('button', { name: 'Show Download Options' })).not.toBeInTheDocument();
  expect(within(guestDetail).queryByRole('button', { name: /Monitor eBook/i })).not.toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(0);
  await user.click(within(guestDetail).getByRole('button', { name: 'Close details' }));

  // Requester: sees Monitor Series, Download, Monitor on Scheduled books, and Monitor Book in details
  rerender(<SeriesDetailModal title="Sequence" canManage={false} canRequest onClose={vi.fn()} />);
  expect(screen.getByRole('button', { name: 'Monitor series eBooks' })).toBeInTheDocument();
  expect(within(missingItem).getByRole('button', { name: 'Download' })).toBeInTheDocument();
  expect(within(scheduledItem).getByRole('button', { name: 'Monitor eBook' })).toBeInTheDocument();

  await user.click(within(missingItem).getByRole('button', { name: 'Missing Tome' }));
  const reqDetail = await screen.findByRole('dialog', { name: 'Details for Missing Tome' });
  expect(within(reqDetail).getByRole('button', { name: 'Monitor eBook' })).toBeInTheDocument();
  expect(within(reqDetail).getByRole('button', { name: 'Monitor audiobook' })).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(0);

  await user.click(within(reqDetail).getByRole('button', { name: 'Show Download Options' }));
  expect(await screen.findByRole('dialog', { name: 'Download Options' })).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([u]) => u === '/api/books/resolve')).toHaveLength(1);
});


