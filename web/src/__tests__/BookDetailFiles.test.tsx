import { render, screen, waitFor, within } from '@testing-library/react';
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

it('offers metadata repair only to admins with an EPUB asset', async () => {
  const user = userEvent.setup({ delay: null });
  const { rerender } = render(<BookDetailModal book={book} onClose={vi.fn()} />);
  expect(screen.queryByRole('button', { name: 'More actions' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Fix Metadata' })).toBeNull();

  rerender(<BookDetailModal book={{ ...book, editions: [book.editions[1]] }} onClose={vi.fn()} isAdmin />);
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  expect(screen.queryByRole('menuitem', { name: 'Fix Metadata' })).toBeNull();
  await user.keyboard('{Escape}');

  rerender(<BookDetailModal book={book} onClose={vi.fn()} isAdmin />);
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  expect(screen.getByRole('menuitem', { name: 'Fix Metadata' })).toBeVisible();
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
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  await user.click(screen.getByRole('menuitem', { name: 'Fix Metadata' }));
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
    bundleSeriesInLibrary={false} onSelectBook={select} />);
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

it('shows and toggles Monitor Book only for unowned books when user can manage', async () => {
  const user = userEvent.setup({ delay: null });
  const unowned: Book = {
    ...book,
    id: 'unowned-1',
    state: 'Not in library',
    monitored: false,
    formats: [],
    editions: [],
  };
  const onBookUpdated = vi.fn();
  const fetchMock = vi.fn().mockResolvedValue(Response.json({ id: 'unowned-1', monitored: true }));
  vi.stubGlobal('fetch', fetchMock);

  const { rerender } = render(<BookDetailModal book={book} onClose={vi.fn()} canManage onBookUpdated={onBookUpdated} />);
  expect(screen.queryByRole('button', { name: /Monitor Book/i })).not.toBeInTheDocument();

  rerender(<BookDetailModal book={unowned} onClose={vi.fn()} canManage={false} onBookUpdated={onBookUpdated} />);
  expect(screen.queryByRole('button', { name: /Monitor Book/i })).not.toBeInTheDocument();

  rerender(<BookDetailModal book={unowned} onClose={vi.fn()} canManage onBookUpdated={onBookUpdated} />);
  const toggle = screen.getByRole('button', { name: 'Monitor Book' });
  expect(toggle).toHaveAttribute('aria-pressed', 'false');
  await user.click(toggle);
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/books/unowned-1/monitor',
    expect.objectContaining({ method: 'PUT', body: '{"monitored":true}' }),
  );
  expect(screen.getByRole('button', { name: 'Monitoring Book' })).toHaveAttribute('aria-pressed', 'true');
  expect(onBookUpdated).toHaveBeenCalledWith(expect.objectContaining({ id: 'unowned-1', monitored: true }));
});

it('hides write controls from guests and lazily resolves unpersisted books for requester and curator actions', async () => {
  const user = userEvent.setup({ delay: null });
  const unpersisted: Book = {
    id: '',
    title: 'Remote Chronicle',
    subtitle: 'Part Two',
    authorId: '',
    authorName: 'Jane Author',
    seriesTitle: 'Remote Saga',
    seriesIndex: 2,
    releaseDate: '2099-09',
    synopsis: 'Preserved remote blurb.',
    coverUrl: 'https://covers.example/remote.jpg',
    state: 'Scheduled',
    monitored: false,
    formats: [],
    editions: [],
  };

  // Guest / unprivileged sees metadata and "No files available." with zero write controls
  const { rerender } = render(
    <BookDetailModal book={unpersisted} canManage={false} canRequest={false} onClose={vi.fn()} />
  );
  expect(screen.getByText('Preserved remote blurb.')).toBeVisible();
  expect(screen.getByText('2099-09')).toBeVisible();
  expect(screen.getByText('No files available.')).toBeVisible();
  expect(screen.queryByRole('button', { name: 'Show Download Options' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Monitor Book/i })).not.toBeInTheDocument();

  // Requester sees Show Download Options (no Monitor Book) and clicking resolves once with full metadata
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/books/resolve' && init?.method === 'POST') {
      return Response.json({
        id: 'persisted-remote',
        title: 'Remote Chronicle',
        authorId: 'a-1',
        authorName: 'Jane Author',
        monitored: false,
        formats: [],
        editions: [],
      });
    }
    if (url === '/api/books/persisted-remote/monitor' && init?.method === 'PUT') {
      return Response.json({ id: 'persisted-remote', monitored: true });
    }
    if (url === '/api/requests') {
      return Response.json([{ id: 88, book_id: 'persisted-remote', format: 'epub' }]);
    }
    if (url === '/api/requests/88/candidates') {
      return Response.json([]);
    }
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  const onBookUpdated = vi.fn();
  rerender(
    <BookDetailModal book={unpersisted} canManage={false} canRequest onBookUpdated={onBookUpdated} onClose={vi.fn()} />
  );
  expect(screen.queryByRole('button', { name: /Monitor Book/i })).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Show Download Options' }));

  const resolveCall = fetchMock.mock.calls.find(([url]) => url === '/api/books/resolve');
  expect(resolveCall).toBeDefined();
  expect(JSON.parse(String(resolveCall?.[1]?.body))).toEqual({
    title: 'Remote Chronicle',
    subtitle: 'Part Two',
    author: 'Jane Author',
    series: 'Remote Saga',
    series_index: 2,
    release_date: '2099-09',
    synopsis: 'Preserved remote blurb.',
    cover_url: 'https://covers.example/remote.jpg',
  });
  expect(onBookUpdated).toHaveBeenCalledWith(expect.objectContaining({
    id: 'persisted-remote',
    coverUrl: 'https://covers.example/remote.jpg',
    synopsis: 'Preserved remote blurb.',
    subtitle: 'Part Two',
    releaseDate: '2099-09',
  }));
  expect(await screen.findByRole('dialog', { name: 'Download Options' })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Close' }));

  // Curator clicking Monitor Book on an unpersisted book resolves then PUTs /monitor
  onBookUpdated.mockClear();
  fetchMock.mockClear();
  rerender(
    <BookDetailModal book={{ ...unpersisted, title: 'Another Remote' }} canManage canRequest onBookUpdated={onBookUpdated} onClose={vi.fn()} />
  );
  await user.click(screen.getByRole('button', { name: 'Monitor Book' }));
  expect(await screen.findByRole('button', { name: 'Monitoring Book' })).toHaveAttribute('aria-pressed', 'true');
  expect(fetchMock.mock.calls.map(([u, opts]) => `${opts?.method ?? 'GET'} ${u}`)).toEqual([
    'POST /api/books/resolve',
    'PUT /api/books/persisted-remote/monitor',
  ]);
  expect(onBookUpdated).toHaveBeenLastCalledWith(expect.objectContaining({
    id: 'persisted-remote',
    monitored: true,
    synopsis: 'Preserved remote blurb.',
  }));
});

it('shows an error alert and keeps Download/Monitor closed if /api/books/resolve fails', async () => {
  const user = userEvent.setup({ delay: null });
  const unpersisted: Book = {
    id: '',
    title: 'Forbidden Chronicle',
    authorId: '',
    authorName: 'Jane Author',
    state: 'Not in library',
    formats: [],
    editions: [],
  };
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'forbidden' }), { status: 403 })));
  render(<BookDetailModal book={unpersisted} canManage canRequest onClose={vi.fn()} />);

  await user.click(screen.getByRole('button', { name: 'Show Download Options' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Check permissions and retry');
  expect(screen.queryByRole('dialog', { name: 'Download Options' })).not.toBeInTheDocument();
});

it('allows only admins to delete a persisted book with file deletion defaulting to off', async () => {
  const user = userEvent.setup({ delay: null });
  const onBookDeleted = vi.fn();
  const fetchMock = vi.fn(async (url: string, options?: RequestInit) => {
    if (options?.method === 'DELETE' && url === '/api/books/book%2Fwith%20spaces') {
      return Response.json({ deleted: true, deletedFiles: 0 });
    }
    if (options?.method === 'DELETE' && url === '/api/books/book%2Fwith%20spaces?deleteFiles=true') {
      return Response.json({ deleted: true, deletedFiles: 3 });
    }
    throw new Error(`Unexpected fetch ${options?.method ?? 'GET'} ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  const { rerender } = render(<BookDetailModal book={book} onClose={vi.fn()} onBookDeleted={onBookDeleted} />);
  expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();
  expect(screen.queryByRole('menuitem', { name: 'Delete Book' })).not.toBeInTheDocument();

  rerender(<BookDetailModal book={{ ...book, id: '' }} isAdmin onClose={vi.fn()} onBookDeleted={onBookDeleted} />);
  expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();
  expect(screen.queryByRole('menuitem', { name: 'Delete Book' })).not.toBeInTheDocument();

  rerender(<BookDetailModal book={book} isAdmin onClose={vi.fn()} onBookDeleted={onBookDeleted} />);
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  await user.click(screen.getByRole('menuitem', { name: 'Delete Book' }));

  const region = screen.getByRole('region', { name: 'Confirm book deletion' });
  const checkbox = within(region).getByRole('checkbox', { name: /Also delete files from disk/i });
  expect(checkbox).not.toBeChecked();

  // Check, then cancel: checkbox resets to unchecked and focus returns to overflow menu trigger.
  await user.click(checkbox);
  expect(checkbox).toBeChecked();
  await user.click(within(region).getByRole('button', { name: 'Cancel' }));
  expect(screen.queryByRole('region', { name: 'Confirm book deletion' })).not.toBeInTheDocument();
  const restoredTrigger = screen.getByRole('button', { name: 'More actions' });
  await waitFor(() => expect(restoredTrigger).toHaveFocus());

  // Reopen and confirm with default (off) file deletion.
  await user.click(restoredTrigger);
  await user.click(screen.getByRole('menuitem', { name: 'Delete Book' }));
  const reopened = screen.getByRole('region', { name: 'Confirm book deletion' });
  expect(within(reopened).getByRole('checkbox', { name: /Also delete files from disk/i })).not.toBeChecked();
  await user.click(within(reopened).getByRole('button', { name: 'Confirm Delete' }));

  expect(fetchMock).toHaveBeenCalledWith('/api/books/book%2Fwith%20spaces', { method: 'DELETE' });
  expect(onBookDeleted).toHaveBeenCalledWith('book/with spaces');

  // Open again, opt in to file deletion, and confirm.
  onBookDeleted.mockClear();
  rerender(<BookDetailModal book={{ ...book }} isAdmin onClose={vi.fn()} onBookDeleted={onBookDeleted} />);
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  await user.click(screen.getByRole('menuitem', { name: 'Delete Book' }));
  const secondConfirm = screen.getByRole('region', { name: 'Confirm book deletion' });
  await user.click(within(secondConfirm).getByRole('checkbox', { name: /Also delete files from disk/i }));
  await user.click(within(secondConfirm).getByRole('button', { name: 'Confirm Delete' }));

  expect(fetchMock).toHaveBeenCalledWith('/api/books/book%2Fwith%20spaces?deleteFiles=true', { method: 'DELETE' });
  expect(onBookDeleted).toHaveBeenCalledWith('book/with spaces');
});

it('shows an error alert when deleting a book fails', async () => {
  const user = userEvent.setup({ delay: null });
  const onBookDeleted = vi.fn();
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response('Cannot delete book while an import or conversion is finishing.', {
        status: 409,
        headers: { 'Content-Type': 'text/plain; charset=utf-8' },
      })
    )
  );
  render(<BookDetailModal book={book} isAdmin onClose={vi.fn()} onBookDeleted={onBookDeleted} />);

  await user.click(screen.getByRole('button', { name: 'More actions' }));
  await user.click(screen.getByRole('menuitem', { name: 'Delete Book' }));
  await user.click(screen.getByRole('button', { name: 'Confirm Delete' }));

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Cannot delete book while an import or conversion is finishing.'
  );
  expect(onBookDeleted).not.toHaveBeenCalled();
});

