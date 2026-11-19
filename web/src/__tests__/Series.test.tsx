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
              series_index_display: 'Book 2.5',
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

  expect(within(items[1]).getByText('Book 2.5')).toBeInTheDocument();
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
    seriesIndexDisplay: 'Book 1',
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
