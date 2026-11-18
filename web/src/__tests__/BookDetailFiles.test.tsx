import { render, screen, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { testBooks } from './testFixtures';
import type { Book } from '../types';

const longName = `${'AnUnusuallyLongBookTitle'.repeat(12)}(2026) - Author.epub`;
const book: Book = {
  ...testBooks[0],
  editions: [
    { id: 'ebook', bookId: 'b', format: 'EPUB', language: 'en', assets: [
      { id: 'e1', editionId: 'ebook', filename: longName, format: 'EPUB', fileSizeBytes: 1536 },
      { id: 'e2', editionId: 'ebook', filename: 'Book.pdf', format: 'PDF', fileSizeBytes: 1048576 },
    ] },
    { id: 'audio', bookId: 'b', format: 'M4B', language: 'fr', assets: [
      { id: 'a1', editionId: 'audio', filename: 'Book.m4b', format: 'M4B', fileSizeBytes: 1073741824 },
    ] },
  ],
};

it('lists every ebook and audiobook file with its format, size and language', () => {
  render(<BookDetailModal book={book} onClose={vi.fn()} />);
  const items = screen.getAllByRole('listitem');
  expect(items).toHaveLength(3);
  for (const [index, filename, format, size, language] of [
    [0, longName, 'EPUB', '1.5 KiB', 'en'],
    [1, 'Book.pdf', 'PDF', '1 MiB', 'en'],
    [2, 'Book.m4b', 'M4B', '1 GiB', 'fr'],
  ] as const) {
    const row = within(items[index]);
    expect(row.getByText(filename)).toBeVisible();
    expect(row.getByText(format)).toBeVisible();
    expect(row.getByText(size)).toBeVisible();
    expect(row.getByText(language)).toBeVisible();
  }
  expect(screen.queryByText(/Edition$/)).not.toBeInTheDocument();
  // Full names wrap even when a filename contains no natural break points.
  expect(screen.getByText(longName)).toHaveClass('min-w-0', '[overflow-wrap:anywhere]');
});

it('handles absent filenames, languages, zero and invalid sizes without broken placeholders', () => {
  const missing: Book = { ...book, editions: [{ ...book.editions[0], language: '', assets:
    [undefined, 0, -1, Number.NaN, Number.POSITIVE_INFINITY].map((size, i) => ({
      id: String(i), editionId: 'ebook', format: 'EPUB', fileSizeBytes: size,
    })),
  }] };
  render(<BookDetailModal book={missing} onClose={vi.fn()} />);
  expect(screen.getAllByText('Filename unknown')).toHaveLength(5);
  expect(screen.getAllByText('Size unknown')).toHaveLength(5);
  expect(screen.queryByText(/0 B|NaN|undefined|Infinity/)).not.toBeInTheDocument();
  expect(screen.queryByText('en')).not.toBeInTheDocument();
});

it('explains when an edition has no files', () => {
  render(<BookDetailModal book={{ ...book, editions: [{ ...book.editions[0], assets: [] }] }} onClose={vi.fn()} />);
  expect(screen.getByText('No files available for this edition.')).toBeVisible();
});

it('explains when the book has no editions', () => {
  render(<BookDetailModal book={{ ...book, editions: [] }} onClose={vi.fn()} />);
  expect(screen.getByText('No files available.')).toBeVisible();
});
