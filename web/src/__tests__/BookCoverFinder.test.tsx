import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { testBooks } from './testFixtures';
import type { Book } from '../types';

const ownedBook: Book = {
  ...testBooks[0],
  id: 'book-1',
  title: 'Dune',
  authorName: 'Frank Herbert',
  coverUrl: '/api/books/book-1/cover',
  editions: [
    {
      id: 'ed-1',
      bookId: 'book-1',
      format: 'EPUB',
      language: 'en',
      assets: [
        { id: 'a1', editionId: 'ed-1', filename: 'Dune.epub', format: 'EPUB', fileSizeBytes: 2048 },
      ],
    },
  ],
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('BookDetailModal cover finder', () => {
  it('shows Find cover next to Upload only for curators/admins on owned books with hover, keyboard, and touch visibility', () => {
    const { rerender } = render(<BookDetailModal book={ownedBook} onClose={vi.fn()} />);
    expect(screen.queryByRole('button', { name: /find cover/i })).toBeNull();

    // Unowned book hides Find cover even for admins
    rerender(
      <BookDetailModal
        book={{ ...ownedBook, editions: [{ ...ownedBook.editions[0], assets: [] }] }}
        canManage
        isAdmin
        onClose={vi.fn()}
      />
    );
    expect(screen.queryByRole('button', { name: /find cover/i })).toBeNull();

    // Curator sees Upload and Find cover side-by-side in the same accessible overlay
    rerender(<BookDetailModal book={ownedBook} canManage onClose={vi.fn()} />);
    const uploadBtn = screen.getByRole('button', { name: /upload cover/i });
    const findBtn = screen.getByRole('button', { name: /find cover/i });
    expect(findBtn).toBeInTheDocument();
    expect(findBtn.parentElement).toBe(uploadBtn.parentElement);
    const overlay = findBtn.parentElement!;
    expect(overlay.className).toContain('group-hover:opacity-100');
    expect(overlay.className).toContain('group-focus-within:opacity-100');
    expect(overlay.className).toContain('[@media(hover:none)]:opacity-100');
    expect(overlay.className).toContain('[@media(pointer:coarse)]:opacity-100');
  });

  it('opens Find Cover via keyboard, searches page 1, loads next page, and applies selected cover', async () => {
    const user = userEvent.setup({ delay: null });
    const onBookUpdated = vi.fn();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.startsWith('/api/books/book-1/cover/search')) {
        const parsed = new URL(url, 'http://localhost');
        const page = Number(parsed.searchParams.get('page') || '1');
        const q = parsed.searchParams.get('q') || '';
        if (page === 1) {
          return Response.json({
            query: q,
            page: 1,
            hasMore: true,
            nextPage: 2,
            candidates: [
              {
                url: 'https://assets.hardcover.app/dune-1.jpg',
                thumbnailUrl: 'https://assets.hardcover.app/dune-1.jpg',
                width: 600,
                height: 900,
                source: 'Hardcover',
                title: 'Dune',
              },
              {
                url: 'https://covers.openlibrary.org/b/id/101-L.jpg?default=false',
                thumbnailUrl: 'https://covers.openlibrary.org/b/id/101-M.jpg?default=false',
                width: 400,
                height: 600,
                source: 'OpenLibrary',
                title: 'Dune (1965)',
              },
            ],
          });
        }
        return Response.json({
          query: q,
          page: 2,
          hasMore: false,
          candidates: [
            // Duplicate of page 1 item should be deduplicated cleanly
            {
              url: 'https://assets.hardcover.app/dune-1.jpg',
              thumbnailUrl: 'https://assets.hardcover.app/dune-1.jpg',
              width: 600,
              height: 900,
              source: 'Hardcover',
              title: 'Dune',
            },
            {
              url: 'https://is1-ssl.mzstatic.com/image/thumb/dune-2/1000x1000bb.jpg',
              thumbnailUrl: 'https://is1-ssl.mzstatic.com/image/thumb/dune-2/400x400bb.jpg',
              width: 667,
              height: 1000,
              source: 'Apple Books',
              title: 'Dune Deluxe Edition',
            },
          ],
        });
      }
      if (url === '/api/books/book-1/cover/select' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toEqual({
          url: 'https://is1-ssl.mzstatic.com/image/thumb/dune-2/1000x1000bb.jpg',
        });
        return Response.json({
          coverUrl: '/api/books/book-1/cover?v=feedface1234',
          width: 667,
          height: 1000,
          epubUpdated: 1,
        });
      }
      throw new Error(`Unexpected fetch: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<BookDetailModal book={ownedBook} canManage onBookUpdated={onBookUpdated} onClose={vi.fn()} />);

    const findBtn = screen.getByRole('button', { name: /find cover/i });
    findBtn.focus();
    await user.keyboard('{Enter}');

    // Heading & pre-populated search query
    expect(screen.getByRole('heading', { name: 'Find Cover' })).toBeInTheDocument();
    const searchInput = screen.getByLabelText(/search for cover art/i) as HTMLInputElement;
    expect(searchInput.value).toBe('Dune Frank Herbert');

    // Page 1 results render with dimensions and source
    expect(await screen.findByText('600 × 900')).toBeInTheDocument();
    expect(screen.getByText('Hardcover')).toBeInTheDocument();
    expect(screen.getByText('400 × 600')).toBeInTheDocument();
    expect(screen.getByText('Showing 2 covers · Page 1')).toBeInTheDocument();

    // Load next page appends unique page 2 candidate and hides next-page button when hasMore=false
    const loadNextBtn = screen.getByRole('button', { name: /load next page/i });
    await user.click(loadNextBtn);

    expect(await screen.findByText('667 × 1000')).toBeInTheDocument();
    expect(screen.getByText('Apple Books')).toBeInTheDocument();
    expect(screen.getByText('Showing 3 covers · Page 2')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /load next page/i })).toBeNull();

    // Select page 2 cover and apply it
    const appleCard = screen.getByRole('button', { name: /select cover: dune deluxe edition/i });
    await user.click(appleCard);
    expect(appleCard).toHaveAttribute('aria-pressed', 'true');

    const applyBtn = screen.getByRole('button', { name: /apply cover/i });
    expect(applyBtn).toBeEnabled();
    await user.click(applyBtn);

    // Returns to book details with updated cover URL and status
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'Dune' })).toHaveAttribute(
        'src',
        '/api/books/book-1/cover?v=feedface1234'
      );
    });
    expect(screen.getByRole('status')).toHaveTextContent('Cover updated and embedded in EPUB.');
    expect(onBookUpdated).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'book-1', coverUrl: '/api/books/book-1/cover?v=feedface1234' })
    );
  });

  it('allows refining search query and surfaces apply errors while preserving the current cover', async () => {
    const user = userEvent.setup({ delay: null });
    const onBookUpdated = vi.fn();
    const queries: string[] = [];

    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.startsWith('/api/books/book-1/cover/search')) {
        const parsed = new URL(url, 'http://localhost');
        const q = parsed.searchParams.get('q') || '';
        queries.push(q);
        if (q === 'Dune Frank Herbert') {
          return Response.json({ query: q, page: 1, hasMore: false, candidates: [] });
        }
        return Response.json({
          query: q,
          page: 1,
          hasMore: false,
          candidates: [
            {
              url: 'https://covers.example.test/dune-alt.jpg',
              thumbnailUrl: 'https://covers.example.test/dune-alt.jpg',
              width: 500,
              height: 750,
              source: 'OpenLibrary',
              title: 'Dune Alternate',
            },
          ],
        });
      }
      if (url === '/api/books/book-1/cover/select' && init?.method === 'POST') {
        return new Response('Unable to download the selected cover image. Choose another cover or upload one directly.', {
          status: 502,
          headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        });
      }
      throw new Error(`Unexpected fetch: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<BookDetailModal book={ownedBook} canManage onBookUpdated={onBookUpdated} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: /find cover/i }));

    // Empty state on initial query
    expect(await screen.findByText(/no matching covers found/i)).toBeInTheDocument();

    // Refine query and submit
    const searchInput = screen.getByLabelText(/search for cover art/i);
    await user.clear(searchInput);
    await user.type(searchInput, 'Dune 1965');
    await user.click(screen.getByRole('button', { name: /^search$/i }));

    expect(await screen.findByText('500 × 750')).toBeInTheDocument();
    expect(queries).toEqual(['Dune Frank Herbert', 'Dune 1965']);

    // Attempt apply -> shows error, stays in picker
    await user.click(screen.getByRole('button', { name: /select cover: dune alternate/i }));
    await user.click(screen.getByRole('button', { name: /apply cover/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Unable to download the selected cover image.'
    );
    expect(onBookUpdated).not.toHaveBeenCalled();

    // Cancel returns to book details with original cover preserved
    await user.click(screen.getByRole('button', { name: /^cancel$/i }));
    expect(screen.getByRole('img', { name: 'Dune' })).toHaveAttribute('src', '/api/books/book-1/cover');
  });
});
