import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { DiscoveryResults } from '../components/DiscoveryResults';

afterEach(() => vi.unstubAllGlobals());

describe('book discovery', () => {
  it.each(['ebook', 'audiobook', 'both'] as const)('initializes the format picker to %s when configured', (format) => {
    render(<DiscoveryResults query="" defaultSearchFormat={format} onSubmitRequest={vi.fn()} />);
    expect(screen.getByRole('combobox', { name: 'Format' })).toHaveValue(format);
  });

  it('uses a preference that arrives after opening and preserves an explicit selection', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn();
    const { rerender } = render(<DiscoveryResults query="" onSubmitRequest={add} />);
    const select = screen.getByRole('combobox', { name: 'Format' });
    expect(select).toHaveValue('ebook');
    rerender(<DiscoveryResults query="" defaultSearchFormat="audiobook" onSubmitRequest={add} />);
    expect(select).toHaveValue('audiobook');
    await user.selectOptions(select, 'both');
    rerender(<DiscoveryResults query="" defaultSearchFormat="ebook" onSubmitRequest={add} />);
    expect(select).toHaveValue('both');
  });

  it('shows five ranked matches, reveals 10 per View more press by keyboard until exhausted, and resets for a new query', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      const user = userEvent.setup({ delay: null });
      const search = (prefix: string, count: number) => ({
        ok: true,
        json: async () => Array.from({ length: count }, (_, index) => ({
          title: `${prefix} ${index + 1}`,
          year: 2000,
          library_book_id: index === 5 ? 'owned-book' : undefined,
        })),
      });
      const fetch = vi.fn().mockResolvedValueOnce(search('First', 26)).mockResolvedValueOnce(search('Second', 6));
      vi.stubGlobal('fetch', fetch);
      const onOpenBook = vi.fn();
      const props = { onSubmitRequest: vi.fn(), onOpenBook };
      const { rerender } = render(<DiscoveryResults query="First" {...props} />);
      await act(() => vi.advanceTimersByTimeAsync(250));
      vi.useRealTimers();

      const viewMore = screen.getByRole('button', { name: 'View more' });
      expect(screen.getAllByRole('listitem')).toHaveLength(5);
      expect(screen.getByRole('heading', { name: 'First 5' })).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'First 6' })).not.toBeInTheDocument();

      // First View more press reveals next 10 (5 -> 15) and focuses item 6
      viewMore.focus();
      await user.keyboard('{Enter}');
      expect(screen.getAllByRole('listitem')).toHaveLength(15);
      expect(screen.getByRole('heading', { name: 'First 15' })).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'First 16' })).not.toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'First 6' }).closest('li')).toHaveFocus();
      await user.tab();
      expect(screen.getByRole('button', { name: 'In Library: First 6' })).toHaveFocus();
      await user.keyboard('{Enter}');
      expect(onOpenBook).toHaveBeenCalledWith('owned-book');
      expect(fetch).toHaveBeenCalledTimes(1);

      // Second View more press reveals next 10 (15 -> 25) and focuses item 16
      await user.click(screen.getByRole('button', { name: 'View more' }));
      expect(screen.getAllByRole('listitem')).toHaveLength(25);
      expect(screen.getByRole('heading', { name: 'First 25' })).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'First 26' })).not.toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'First 16' }).closest('li')).toHaveFocus();

      // Third View more press reveals the final item (25 -> 26) and hides View more
      await user.click(screen.getByRole('button', { name: 'View more' }));
      expect(screen.getAllByRole('listitem')).toHaveLength(26);
      expect(screen.getByRole('heading', { name: 'First 26' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'View more' })).not.toBeInTheDocument();

      // Query change resets back to 5
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
      rerender(<DiscoveryResults query="Second" {...props} />);
      await act(() => vi.advanceTimersByTimeAsync(250));
      expect(screen.getAllByRole('listitem')).toHaveLength(5);
      expect(screen.getByRole('button', { name: 'View more' })).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('fetches paginated download results on demand, deduplicates across pages, handles load-more errors, and stops when exhausted', async () => {
    const user = userEvent.setup({ delay: null });
    const makeBatch = (start: number, count: number) =>
      Array.from({ length: count }, (_, idx) => ({
        title: `Author Work ${start + idx}`,
        authors: ['Prolific Author'],
        year: 2000 + start + idx,
        openlibrary_id: `OL${start + idx}W`,
      }));

    let page3Failures = 1;
    const fetchMock = vi.fn(async (url: string) => {
      const parsed = new URL(url, 'http://localhost');
      const page = Number(parsed.searchParams.get('page'));
      const limit = Number(parsed.searchParams.get('limit'));
      expect(limit).toBe(10);
      if (page === 1) {
        return {
          ok: true,
          json: async () => ({
            results: makeBatch(1, 10),
            page: 1,
            hasMore: true,
            nextPage: 2,
          }),
        };
      }
      if (page === 2) {
        return {
          ok: true,
          json: async () => ({
            // Include duplicate of item 10 from page 1 + 10 new items (11..20)
            results: [...makeBatch(10, 1), ...makeBatch(11, 10)],
            page: 2,
            hasMore: true,
            nextPage: 3,
          }),
        };
      }
      if (page === 3) {
        if (page3Failures > 0) {
          page3Failures--;
          return { ok: false };
        }
        return {
          ok: true,
          json: async () => ({
            results: makeBatch(21, 3),
            page: 3,
            hasMore: false,
          }),
        };
      }
      throw new Error(`Unexpected page ${page}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<DiscoveryResults query="Prolific Author" onSubmitRequest={vi.fn()} />);

    // Page 1 fetched (10 items loaded), only 5 shown initially
    expect(await screen.findByRole('heading', { name: 'Author Work 5' })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(5);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // 1st View more needs 15 total -> fetches page 2, deduplicates item 10, shows 15
    await user.click(screen.getByRole('button', { name: 'View more' }));
    expect(await screen.findByRole('heading', { name: 'Author Work 15' })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(15);
    expect(screen.getAllByRole('heading', { name: 'Author Work 10' })).toHaveLength(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);

    // 2nd View more needs 25 total -> fetches page 3 which fails first, keeping existing 15 intact
    await user.click(screen.getByRole('button', { name: 'View more' }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/Unable to load more results/i);
    expect(screen.getAllByRole('listitem')).toHaveLength(15);

    // Retrying loads page 3 (3 final items -> 23 unique total) and hides View more
    await user.click(within(alert).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('heading', { name: 'Author Work 23' })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(23);
    expect(screen.queryByRole('button', { name: 'View more' })).not.toBeInTheDocument();
  });

  it('confirms metadata with a single compact Add action and loading/added states', async () => {
    const user = userEvent.setup({ delay: null });
    let resolveAdd!: () => void;
    const add = vi.fn().mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveAdd = resolve;
        }),
    );
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          {
            title: 'Dune',
            authors: ['Frank Herbert'],
            year: 1965,
            description: 'A desert planet',
            cover_url: 'https://covers.example/dune.jpg',
            series: 'Dune Chronicles',
            series_index: 1,
            series_total: 6,
          },
        ],
      }),
    );

    render(<DiscoveryResults query="Dune" onSubmitRequest={add} />);

    const addBtn = await screen.findByRole('button', { name: 'Add Dune' });
    expect(addBtn).toHaveClass('shrink-0', 'self-center');
    expect(screen.getByRole('img', { name: 'Cover of Dune' })).toHaveAttribute(
      'src',
      'https://covers.example/dune.jpg',
    );
    expect(screen.getByRole('link', { name: 'Frank Herbert' })).toHaveAttribute(
      'href',
      '#/authors/Frank%20Herbert',
    );
    expect(screen.getByText('1965')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Dune Chronicles' })).toBeInTheDocument();

    await user.click(addBtn);
    expect(screen.getByRole('button', { name: 'Adding Dune' })).toBeDisabled();
    expect(screen.getByRole('status')).toHaveTextContent('Adding book to queue');

    resolveAdd();
    expect(add).toHaveBeenCalledWith('Dune', 'Frank Herbert', 'ebook', 1965, {
      book_id: undefined,
      cover_url: 'https://covers.example/dune.jpg',
      series: 'Dune Chronicles',
      series_index: 1,
      synopsis: 'A desert planet',
    });
    expect(await screen.findByText(/Added Dune/)).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Added Dune');
    expect(screen.getByRole('button', { name: 'Added Dune' })).toBeDisabled();
  });

  it('opens an existing library book instead of adding a duplicate', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn();
    const open = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          { title: 'Dune', authors: ['Frank Herbert'], year: 1965, library_book_id: 'local-dune' },
        ],
      }),
    );

    render(<DiscoveryResults query="Dune" onSubmitRequest={add} onOpenBook={open} />);

    const inLibBtn = await screen.findByRole('button', { name: /In Library/ });
    expect(inLibBtn).toHaveClass('shrink-0', 'self-center');
    await user.click(inLibBtn);
    expect(screen.queryByRole('button', { name: 'Add Dune' })).not.toBeInTheDocument();
    expect(open).toHaveBeenCalledWith('local-dune');
    expect(add).not.toHaveBeenCalled();
  });

  it('shows source failures and permits recovery', async () => {
    const user = userEvent.setup({ delay: null });
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce({ ok: false })
        .mockResolvedValue({ ok: true, json: async () => [] }),
    );

    render(<DiscoveryResults query="Dune" onSubmitRequest={vi.fn()} />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Try again');
    await user.click(within(alert).getByRole('button', { name: 'Retry search' }));
    expect(await screen.findByText(/No matches/)).toBeInTheDocument();
  });

  it('renders aspect-[2/3] cover thumbnails with broken-image and missing-cover placeholders', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          {
            title: 'Broken Cover Book',
            authors: ['Author A'],
            year: 2020,
            cover_url: 'https://covers.example/broken.jpg',
          },
          {
            title: 'No Cover Book',
            authors: [],
            year: 0,
          },
        ],
      }),
    );

    render(<DiscoveryResults query="Cover" onSubmitRequest={vi.fn()} />);

    const img = await screen.findByRole('img', { name: 'Cover of Broken Cover Book' });
    expect(img.parentElement).toHaveClass('aspect-[2/3]');
    expect(screen.getAllByLabelText('No cover available')).toHaveLength(1);
    expect(screen.getByText('Author unknown')).toBeInTheDocument();

    fireEvent.error(img);
    expect(await screen.findAllByLabelText('No cover available')).toHaveLength(2);
  });

  it('passes the selected format and surfaces add errors cleanly', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn().mockRejectedValueOnce(new Error('Request quota exceeded'));
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [{ title: 'Foundation', authors: ['Isaac Asimov'], year: 1951 }],
      }),
    );

    render(<DiscoveryResults query="Foundation" onSubmitRequest={add} />);

    const formatSelect = await screen.findByLabelText('Format');
    await user.selectOptions(formatSelect, 'audiobook');
    await user.click(await screen.findByRole('button', { name: 'Add Foundation' }));

    expect(add).toHaveBeenCalledWith('Foundation', 'Isaac Asimov', 'audiobook', 1951, expect.any(Object));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Request quota exceeded');
    expect(within(alert).queryByRole('button', { name: 'Retry search' })).not.toBeInTheDocument();
  });

  it('shows Added from server active requests across reopen/re-search and allows requesting other formats', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          {
            title: 'Dune',
            authors: ['Frank Herbert'],
            year: 1965,
            requested_formats: ['epub'],
            request_status: 'searching',
          },
        ],
      }),
    );

    const { unmount } = render(<DiscoveryResults query="Dune" defaultSearchFormat="ebook" onSubmitRequest={add} />);

    // Persisted server-reported eBook request shows Added (disabled) immediately
    expect(await screen.findByRole('button', { name: 'Added Dune' })).toBeDisabled();
    expect(screen.getByText('eBook searching')).toBeInTheDocument();

    // Switching format to Audiobook enables adding Audiobook
    const formatSelect = screen.getByRole('combobox', { name: 'Format' });
    await user.selectOptions(formatSelect, 'audiobook');
    const addAudioBtn = screen.getByRole('button', { name: 'Add Dune' });
    expect(addAudioBtn).toBeEnabled();

    await user.click(addAudioBtn);
    expect(add).toHaveBeenCalledWith('Dune', 'Frank Herbert', 'audiobook', 1965, expect.any(Object));
    expect(await screen.findByRole('button', { name: 'Added Dune' })).toBeDisabled();

    // Both format is now also Added because both epub and m4b are requested
    await user.selectOptions(formatSelect, 'both');
    expect(screen.getByRole('button', { name: 'Added Dune' })).toBeDisabled();

    // Unmount and remount (simulating closing/reopening palette) still reflects server state
    unmount();
    render(<DiscoveryResults query="Frank Herbert" defaultSearchFormat="ebook" onSubmitRequest={add} />);
    expect(await screen.findByRole('button', { name: 'Added Dune' })).toBeDisabled();
  });

  it('disables Add for all formats when server reports both epub and m4b active, and lets Both add missing format', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          {
            title: 'Hyperion',
            authors: ['Dan Simmons'],
            year: 1989,
            requested_formats: ['epub'],
            request_status: 'downloading',
          },
          {
            title: 'Good Omens',
            authors: ['Terry Pratchett', 'Neil Gaiman'],
            year: 1990,
            requested_formats: ['epub', 'm4b'],
            request_status: 'downloading',
          },
        ],
      }),
    );

    render(<DiscoveryResults query="Sci-fi" defaultSearchFormat="both" onSubmitRequest={add} />);

    // Good Omens has both formats active -> Added (disabled) in Both, eBook, and Audiobook
    expect(await screen.findByRole('button', { name: 'Added Good Omens' })).toBeDisabled();
    // Hyperion only has epub active -> Add enabled in Both so missing Audiobook can be requested
    const addHyperion = screen.getByRole('button', { name: 'Add Hyperion' });
    expect(addHyperion).toBeEnabled();
    expect(screen.getByText('eBook downloading')).toBeInTheDocument();

    await user.click(addHyperion);
    expect(add).toHaveBeenCalledWith('Hyperion', 'Dan Simmons', 'both', 1989, expect.any(Object));
    expect(await screen.findByRole('button', { name: 'Added Hyperion' })).toBeDisabled();
  });

  it('hides Format and Add controls for guests while keeping library and series reading-order navigation', async () => {
    const user = userEvent.setup({ delay: null });
    const open = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).startsWith('/api/discover?')) {
          return Response.json([
            { title: 'Owned Book', authors: ['Author A'], year: 2020, library_book_id: 'owned-1' },
            { title: 'Unowned Book', authors: ['Author A'], year: 2024, series: 'Guest Saga', series_index: 2 },
          ]);
        }
        if (String(url).startsWith('/api/series/books?')) {
          return Response.json({
            title: 'Guest Saga',
            complete: true,
            books: [
              { title: 'Unowned Book', authors: ['Author A'], series_index: 2, description: 'Guest readable blurb', state: 'Not in library', formats: [] },
            ],
          });
        }
        throw new Error(`Unexpected fetch ${url}`);
      }),
    );

    render(<DiscoveryResults query="Guest" canManage={false} canRequest={false} onSubmitRequest={vi.fn()} onOpenBook={open} />);

    expect(await screen.findByText('Unowned Book')).toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Format' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Add Unowned Book/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /In Library/ })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Guest Saga' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).queryByRole('button', { name: 'Download' })).not.toBeInTheDocument();
  });

  it('differentiates series from books in discovery results and opens SeriesDetailModal to monitor directly', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        if (String(url).startsWith('/api/discover?')) {
          return Response.json({
            series: [
              {
                title: 'His Dark Materials',
                authors: ['Philip Pullman'],
                books_count: 3,
                hardcover_series_id: 42,
              },
            ],
            results: [
              {
                title: 'The Golden Compass',
                authors: ['Philip Pullman'],
                year: 1995,
                series: 'His Dark Materials',
                series_index: 1,
                series_total: 3,
                hardcover_series_id: 42,
              },
            ],
            page: 1,
            hasMore: false,
          });
        }
        if (String(url).startsWith('/api/series/books?')) {
          return Response.json({
            title: 'His Dark Materials',
            authors: ['Philip Pullman'],
            monitored: false,
            total_books: 3,
            owned_count: 0,
            complete: true,
            books: [
              { title: 'The Golden Compass', authors: ['Philip Pullman'], series_index: 1, state: 'Not in library', formats: [] },
            ],
          });
        }
        if (String(url).startsWith('/api/catalog/entity?') && init?.method === 'PUT') {
          return new Response(null, { status: 204 });
        }
        throw new Error(`Unexpected fetch ${url}`);
      }),
    );

    render(<DiscoveryResults query="His Dark Materials" canManage canRequest onSubmitRequest={add} />);

    const seriesList = await screen.findByRole('list', { name: 'Series discovery results' });
    const bookList = screen.getByRole('list', { name: 'Book discovery results' });

    expect(within(seriesList).getByRole('heading', { name: 'His Dark Materials' })).toBeInTheDocument();
    expect(within(seriesList).getByText('Series')).toBeInTheDocument();
    expect(within(seriesList).getByText('3 books')).toBeInTheDocument();
    expect(within(seriesList).queryByRole('button', { name: /Add His Dark Materials/i })).not.toBeInTheDocument();

    expect(within(bookList).getByRole('heading', { name: 'The Golden Compass' })).toBeInTheDocument();
    expect(within(bookList).getByRole('button', { name: 'Add The Golden Compass' })).toBeInTheDocument();

    await user.click(within(seriesList).getByRole('button', { name: 'View series His Dark Materials' }));
    const dialog = await screen.findByRole('dialog');
    const monitorBtn = await within(dialog).findByRole('button', { name: 'Monitor series eBooks' });
    await user.click(monitorBtn);
    await waitFor(() => expect(monitorBtn).toHaveAttribute('aria-pressed', 'true'));

    await user.click(within(dialog).getByTestId('close-series-detail'));
    expect(within(seriesList).getByText('Monitored')).toBeInTheDocument();
    expect(add).not.toHaveBeenCalled();
  });
});
