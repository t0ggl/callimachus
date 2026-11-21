import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { App } from '../App';
import { testBooks } from './testFixtures';
import type { Book, BulkActionReport } from '../types';

afterEach(() => {
  vi.unstubAllGlobals();
  window.location.hash = '';
});

const baseBook: Book = {
  ...testBooks[0],
  id: 'book-refresh-1',
  title: 'Dune',
  authorName: 'Frank Herbert',
  synopsis: undefined,
  subtitle: undefined,
  releaseDate: '1965',
};

it('displays persisted synopses for newly imported books with paragraph formatting', () => {
  const importedBook: Book = {
    ...baseBook,
    synopsis: 'First paragraph about Arrakis.\nSecond paragraph about Paul.',
  };
  render(<BookDetailModal book={importedBook} onClose={vi.fn()} />);

  expect(screen.getByText('Synopsis')).toBeVisible();
  const synopsisEl = screen.getByText(/First paragraph about Arrakis\./);
  expect(synopsisEl).toBeVisible();
  expect(synopsisEl).toHaveClass('whitespace-pre-line');
});

it('shows Refresh Metadata only to curators/admins on persisted books and updates synopsis on refresh', async () => {
  const user = userEvent.setup({ delay: null });

  const { rerender } = render(
    <BookDetailModal book={baseBook} canManage={false} canRequest={true} isAdmin={false} onClose={vi.fn()} />
  );
  expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();
  expect(screen.queryByRole('menuitem', { name: 'Refresh Metadata' })).not.toBeInTheDocument();

  // Hidden on unpersisted books (id === '')
  rerender(<BookDetailModal book={{ ...baseBook, id: '' }} canManage={true} isAdmin={true} onClose={vi.fn()} />);
  expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();
  expect(screen.queryByRole('menuitem', { name: 'Refresh Metadata' })).not.toBeInTheDocument();

  let resolveFetch!: (value: Response) => void;
  const fetchMock = vi.fn(
    () =>
      new Promise<Response>((resolve) => {
        resolveFetch = resolve;
      })
  );
  vi.stubGlobal('fetch', fetchMock);

  const onBookUpdated = vi.fn();
  const onMetadataApplied = vi.fn();
  rerender(
    <BookDetailModal
      book={baseBook}
      canManage={true}
      isAdmin={false}
      onBookUpdated={onBookUpdated}
      onMetadataApplied={onMetadataApplied}
      onClose={vi.fn()}
    />
  );

  await user.click(screen.getByRole('button', { name: 'More actions' }));
  const refreshItem = screen.getByRole('menuitem', { name: 'Refresh Metadata' });
  expect(refreshItem).toBeEnabled();
  await user.click(refreshItem);

  expect(fetchMock).toHaveBeenCalledWith('/api/books/book-refresh-1/refresh', { method: 'POST' });
  expect(screen.getByRole('status')).toHaveTextContent('Refreshing metadata…');

  // Reopening the menu while in-flight shows Refreshing… disabled.
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  expect(screen.getByRole('menuitem', { name: 'Refreshing…' })).toBeDisabled();
  await user.keyboard('{Escape}');

  const refreshedBook: Book = {
    ...baseBook,
    subtitle: 'Book One of Dune',
    releaseDate: '1965-08-01',
    synopsis: 'Set on the desert planet Arrakis.',
  };
  resolveFetch(Response.json(refreshedBook));

  expect(await screen.findByText('Set on the desert planet Arrakis.')).toBeVisible();
  expect(screen.getByText('Book One of Dune')).toBeVisible();
  expect(screen.getByText('1965-08-01')).toBeVisible();
  expect(screen.getByRole('status')).toHaveTextContent('Metadata refreshed. Synopsis updated.');
  expect(onBookUpdated).toHaveBeenCalledWith(expect.objectContaining({
    id: 'book-refresh-1',
    synopsis: 'Set on the desert planet Arrakis.',
  }));
  expect(onMetadataApplied).toHaveBeenCalledOnce();
});

it('shows an accessible alert if Refresh Metadata fails', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn().mockResolvedValue(
    new Response('No matching metadata found for this book.', {
      status: 422,
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    })
  );
  vi.stubGlobal('fetch', fetchMock);

  render(<BookDetailModal book={baseBook} canManage={true} onClose={vi.fn()} />);
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  await user.click(screen.getByRole('menuitem', { name: 'Refresh Metadata' }));

  expect(await screen.findByRole('alert')).toHaveTextContent('No matching metadata found for this book.');
  await user.click(screen.getByRole('button', { name: 'More actions' }));
  expect(screen.getByRole('menuitem', { name: 'Refresh Metadata' })).toBeEnabled();
});

it('wires bulk metadata refresh in App library multi-select and displays refreshed descriptions in book view', async () => {
  const user = userEvent.setup({ delay: null });
  let refreshed = false;

  const initialBook: Book = {
    ...baseBook,
    id: 'b-app-1',
    title: 'Piranesi',
    authorName: 'Susanna Clarke',
    seriesId: undefined,
    seriesTitle: undefined,
    seriesIndex: undefined,
    synopsis: undefined,
  };
  const updatedBook: Book = {
    ...initialBook,
    synopsis: 'Piranesi lives in the House of infinite halls.',
  };

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === '/api/auth/me') {
      return Response.json({
        id: 'curator-1',
        username: 'curator',
        email: 'curator@example.test',
        role: 'curator',
        preferences: { bundleSeriesInLibrary: false, defaultSearchFormat: 'ebook' },
      });
    }
    if (url === '/api/books') {
      return Response.json([refreshed ? updatedBook : initialBook]);
    }
    if (url === '/api/authors') return Response.json([]);
    if (url === '/api/series') return Response.json([]);
    if (url === '/api/requests') return Response.json([]);
    if (url === '/api/activity') {
      return Response.json({ requests: [], conversionJobs: [], downloads: [], logs: [] });
    }
    if (url === '/api/library/bulk' && init?.method === 'POST') {
      refreshed = true;
      return Response.json({
        action: 'refresh',
        total: 1,
        succeeded: 1,
        failed: 0,
        results: [{ kind: 'book', id: 'b-app-1', title: 'Piranesi', ok: true, affectedBookCount: 1 }],
      } satisfies BulkActionReport);
    }
    throw new Error(`Unexpected fetch: ${init?.method ?? 'GET'} ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  render(<App />);

  // Enter selection mode, select the book, and trigger bulk Refresh.
  const selectModeBtn = await screen.findByTestId('toggle-select-mode');
  await user.click(selectModeBtn);
  await user.click(screen.getByRole('checkbox', { name: 'Select book Piranesi' }));

  const bulkRefreshBtn = screen.getByRole('button', { name: 'Refresh' });
  expect(bulkRefreshBtn).toBeEnabled();
  await user.click(bulkRefreshBtn);

  expect(await screen.findByTestId('bulk-action-report')).toHaveTextContent('Metadata refreshed for 1 item.');

  // Exit selection mode and open the book detail modal to verify refreshed synopsis renders.
  await user.click(screen.getByTestId('toggle-select-mode'));
  await user.click(screen.getByRole('heading', { name: 'Piranesi' }));

  await waitFor(() => {
    expect(screen.getByText('Piranesi lives in the House of infinite halls.')).toBeVisible();
  });
});
