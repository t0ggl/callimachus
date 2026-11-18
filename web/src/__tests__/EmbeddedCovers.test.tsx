import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { CatalogView } from '../views/CatalogView';
import { testBooks } from './testFixtures';

it('uses the catalog cover URL in the library and detail views', () => {
  const book = { ...testBooks[0], coverUrl: '/api/books/book_1/cover' };
  const { unmount } = render(<CatalogView books={[book]} authors={[]} series={[]} onSelectBook={vi.fn()} selectedIndex={-1} />);
  expect(screen.getByRole('img', { name: book.title })).toHaveAttribute('src', book.coverUrl);
  unmount();
  render(<BookDetailModal book={book} onClose={vi.fn()} />);
  expect(screen.getByRole('img', { name: book.title })).toHaveAttribute('src', book.coverUrl);
});
