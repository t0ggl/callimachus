import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { CatalogView } from '../views/CatalogView';
import { testBooks } from './testFixtures';

it('uses the catalog cover URL in the library and detail views', () => {
  const standaloneBook = {
    ...testBooks[0],
    seriesId: undefined,
    seriesTitle: undefined,
    coverUrl: '/api/books/book_1/cover',
  };
  const { unmount } = render(
    <CatalogView books={[standaloneBook]} authors={[]} series={[]} onSelectBook={vi.fn()} selectedIndex={-1} />
  );
  expect(screen.getByRole('img', { name: standaloneBook.title })).toHaveAttribute('src', standaloneBook.coverUrl);
  unmount();

  const seriesBook = { ...testBooks[0], coverUrl: '/api/books/book_1/cover' };
  const { unmount: unmountBundled } = render(
    <CatalogView books={[seriesBook]} authors={[]} series={[]} onSelectBook={vi.fn()} selectedIndex={-1} />
  );
  expect(screen.getByRole('img', { name: seriesBook.seriesTitle! })).toHaveAttribute('src', seriesBook.coverUrl);
  unmountBundled();

  render(<BookDetailModal book={seriesBook} onClose={vi.fn()} />);
  expect(screen.getByRole('img', { name: seriesBook.title })).toHaveAttribute('src', seriesBook.coverUrl);
});
