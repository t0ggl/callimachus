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
  expect(select).toHaveBeenCalledWith('Broken Earth');
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
              monitored: false,
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
              monitored: true,
              library_book_id: 'book-25',
            },
            {
              title: 'Companion Appendix',
              authors: ['Co-Author'],
              series_index: null,
              formats: [],
              monitored: false,
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
  expect(within(items[0]).getByText('Not in library')).toBeInTheDocument();
  expect(within(items[0]).getByText('Not monitored')).toBeInTheDocument();

  expect(within(dialog).getAllByText('Book 2.5')).toHaveLength(1);
  expect(within(items[1]).getByText('Book 2.5')).toBeInTheDocument();
  expect(within(dialog).queryByText('#2.5')).not.toBeInTheDocument();
  expect(within(items[1]).getByText('EPUB')).toBeInTheDocument();
  expect(within(items[1]).getByText('M4B')).toBeInTheDocument();
  expect(within(items[1]).getByText('2020-01-02')).toBeInTheDocument();
  expect(within(items[1]).getByText('In library')).toBeInTheDocument();
  expect(within(items[1]).getByText('Monitored')).toBeInTheDocument();

  expect(within(items[2]).getByText('Unindexed')).toBeInTheDocument();
  expect(within(items[2]).getByText('Release date unknown')).toBeInTheDocument();
  expect(within(items[2]).getByLabelText('No cover available')).toBeInTheDocument();

  await user.click(within(items[1]).getByRole('button', { name: 'Novella' }));
  expect(openBook).toHaveBeenCalledWith('book-25');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
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
      selectedIndex={-1}
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
      selectedIndex={-1}
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
      selectedIndex={-1}
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

