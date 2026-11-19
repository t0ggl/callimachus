import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { CatalogView } from '../views/CatalogView';
import { testBooks } from './testFixtures';
import type { Book } from '../types';

const longName = `${'AnUnusuallyLongBookTitle'.repeat(12)}(2026) - Author.epub`;
const book: Book = {
  ...testBooks[0],
  id: 'book/with spaces',
  editions: [
    { id: 'ebook', bookId: 'b', format: 'EPUB', language: 'en', assets: [
      { id: 'e1', editionId: 'ebook', filename: longName, format: 'EPUB', fileSizeBytes: 1536 },
      { id: 'e2#pdf', editionId: 'ebook', filename: 'Book.pdf', format: 'PDF', fileSizeBytes: 1048576 },
    ] },
    { id: 'audio', bookId: 'b', format: 'M4B', language: 'fr', assets: [
      { id: 'a1', editionId: 'audio', filename: 'Book.m4b', format: 'M4B', fileSizeBytes: 1073741824 },
    ] },
  ],
};

afterEach(() => vi.unstubAllGlobals());

it('offers metadata repair only to admins with an EPUB asset', () => {
  const { rerender } = render(<BookDetailModal book={book} onClose={vi.fn()} />);
  expect(screen.queryByRole('button', { name: 'Fix Metadata' })).toBeNull();
  rerender(<BookDetailModal book={{ ...book, editions: [book.editions[1]] }} onClose={vi.fn()} isAdmin />);
  expect(screen.queryByRole('button', { name: 'Fix Metadata' })).toBeNull();
  rerender(<BookDetailModal book={book} onClose={vi.fn()} isAdmin />);
  expect(screen.getByRole('button', { name: 'Fix Metadata' })).toBeVisible();
});

it('searches, previews the selected candidate and applies only on confirmation', async () => {
  const user = userEvent.setup({ delay: null });
  const candidate = {
    title: 'Different Title', authors: ['New Author'], publisher: 'Example Press',
    publish_date: '2025', language: 'en', isbn13: '9781234567890',
  };
  const base = '/api/books/book%2Fwith%20spaces';
  let applyAttempts = 0;
  const fetchMock = vi.fn(async (url: string, options?: RequestInit) => {
    if (url.endsWith('/metadata-candidates')) {
      return Response.json([{ title: 'Suggested title', authors: ['Suggested author'] }]);
    }
    if (url.endsWith('?q=Missing')) return Response.json([]);
    if (url.endsWith('?q=Unavailable')) return new Response('Metadata sources are unavailable.', { status: 502 });
    if (url.endsWith('?q=Different%20Title')) return Response.json([candidate]);
    if (url.endsWith('/metadata-diff')) {
      const selected = options?.method === 'POST';
      return Response.json({
        preview: selected ? 'candidate-preview' : 'library-preview',
        ...(selected ? { bookDiffs: [
          { field: 'title', fileValue: 'Library title', libraryValue: candidate.title, changed: true },
        ] } : {}),
        files: [{
          id: 'e1', filename: 'Book.epub',
          diffs: [{ field: 'title', fileValue: 'Old EPUB title', libraryValue: selected ? candidate.title : 'Library title', changed: true }],
        }],
      });
    }
    if (url.endsWith('/fix-metadata')) {
      applyAttempts++;
      if (applyAttempts === 1) return new Response('Metadata changed. Review a fresh preview.', { status: 409 });
      return Response.json({ updated: 1 });
    }
    throw new Error(`Unexpected request ${url}`);
  });
  const applied = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
  render(<BookDetailModal book={book} onClose={vi.fn()} isAdmin onMetadataApplied={applied} />);
  await user.click(screen.getByRole('button', { name: 'Fix Metadata' }));
  expect(await screen.findByText('Old EPUB title')).toBeVisible();
  expect(screen.getByText('Library title')).toBeVisible();
  expect(await screen.findByRole('radio', { name: /Suggested title/ })).toBeVisible();
  const input = screen.getByRole('searchbox', { name: 'Search metadata by title' });
  await user.type(input, 'Missing');
  await user.click(screen.getByRole('button', { name: 'Search' }));
  expect(await screen.findByText('No matches found. Try another title.')).toBeVisible();
  await user.clear(input);
  await user.type(input, 'Unavailable{enter}');
  expect(await screen.findByRole('alert')).toHaveTextContent('Metadata sources are unavailable.');
  await user.clear(input);
  await user.type(input, 'Different Title{enter}');
  const option = await screen.findByRole('radio', { name: /Different Title.*Example Press.*9781234567890/ });
  await user.click(option);
  expect(await screen.findByRole('columnheader', { name: 'Current library' })).toBeVisible();
  expect(screen.getAllByRole('cell', { name: 'Different Title' })).toHaveLength(2);
  expect(fetchMock.mock.calls.filter(([url]) => url.endsWith('/fix-metadata'))).toHaveLength(0);

  await user.click(screen.getByRole('button', { name: 'Apply Metadata' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Metadata changed. Review a fresh preview.');
  expect(applied).not.toHaveBeenCalled();
  expect(await screen.findByRole('columnheader', { name: 'Current library' })).toBeVisible();
  await user.click(screen.getByRole('button', { name: 'Apply Metadata' }));
  expect(await screen.findByText('Metadata applied to the library and EPUB files.')).toBeVisible();
  expect(fetchMock).toHaveBeenCalledWith(base + '/fix-metadata', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ candidate, preview: 'candidate-preview' }),
  });
  expect(applied).toHaveBeenCalledOnce();
});

it('links every ebook and audiobook file by ID and shows its format, size and language', () => {
  render(<BookDetailModal book={book} onClose={vi.fn()} />);
  const items = screen.getAllByRole('listitem');
  expect(items).toHaveLength(3);
  for (const [index, assetId, filename, format, size, language] of [
    [0, 'e1', longName, 'EPUB', '1.5 KiB', 'en'],
    [1, 'e2%23pdf', 'Book.pdf', 'PDF', '1 MiB', 'en'],
    [2, 'a1', 'Book.m4b', 'M4B', '1 GiB', 'fr'],
  ] as const) {
    const row = within(items[index]);
    const link = row.getByRole('link', { name: `Download ${filename}` });
    expect(link).toHaveAttribute('href', `/api/books/book%2Fwith%20spaces/files/${assetId}`);
    expect(link).toHaveAttribute('download', filename);
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
  for (const link of screen.getAllByRole('link', { name: 'Download file' })) {
    expect(link).toHaveAttribute('download', '');
  }
  expect(screen.getAllByText('Size unknown')).toHaveLength(5);
  expect(screen.queryByText(/0 B|NaN|undefined|Infinity/)).not.toBeInTheDocument();
  expect(screen.queryByText('en')).not.toBeInTheDocument();
});

it('explains when an edition has no files', () => {
  render(<BookDetailModal book={{ ...book, editions: [{ ...book.editions[0], assets: [] }] }} onClose={vi.fn()} />);
  expect(screen.getByText('No files available for this edition.')).toBeVisible();
  expect(screen.queryByRole('link', { name: /^Download/ })).not.toBeInTheDocument();
});

it('shows a pending book in the Library and explains the absence of files in its details', async () => {
  const pending: Book = { ...book, state: 'Downloading', coverUrl: undefined, formats: [], editions: [] };
  const select = vi.fn();
  const user = userEvent.setup({ delay: null });
  const { rerender } = render(<CatalogView books={[pending]} authors={[]} series={[]}
    bundleSeriesInLibrary={false} selectedIndex={-1} onSelectBook={select} />);
  expect(screen.getByText('Downloading')).toBeVisible();
  await user.click(screen.getByRole('heading', { name: pending.title }));
  expect(select).toHaveBeenCalledWith(pending);
  await user.click(screen.getByRole('button', { name: 'List View' }));
  expect(screen.getByText('Downloading')).toBeVisible();

  rerender(<BookDetailModal book={pending} onClose={vi.fn()} />);
  expect(screen.getByText('Downloading')).toBeVisible();
  expect(screen.getByText('No files available.')).toBeVisible();
  expect(screen.queryByRole('link', { name: /^Download/ })).not.toBeInTheDocument();
});
