import { act, fireEvent, render, screen, within } from '@testing-library/react';
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

  it('shows five ranked matches, reveals the rest by keyboard, and resets for a new query', async () => {
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
      const fetch = vi.fn().mockResolvedValueOnce(search('First', 8)).mockResolvedValueOnce(search('Second', 6));
      vi.stubGlobal('fetch', fetch);
      const onOpenBook = vi.fn();
      const props = { onSubmitRequest: vi.fn(), onOpenBook };
      const { rerender } = render(<DiscoveryResults query="First" {...props} />);
      await act(() => vi.advanceTimersByTimeAsync(250));
      vi.useRealTimers();

      const showMore = screen.getByRole('button', { name: 'Show more (3 more)' });
      expect(screen.getAllByRole('listitem')).toHaveLength(5);
      expect(screen.getByRole('heading', { name: 'First 5' })).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'First 6' })).not.toBeInTheDocument();

      showMore.focus();
      await user.keyboard('{Enter}');
      expect(screen.getAllByRole('listitem')).toHaveLength(8);
      expect(screen.queryByRole('button', { name: /Show more/ })).not.toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'First 6' }).closest('li')).toHaveFocus();
      await user.tab();
      expect(screen.getByRole('button', { name: 'In Library: First 6' })).toHaveFocus();
      await user.keyboard('{Enter}');
      expect(onOpenBook).toHaveBeenCalledWith('owned-book');
      expect(fetch).toHaveBeenCalledTimes(1);

      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
      rerender(<DiscoveryResults query="Second" {...props} />);
      await act(() => vi.advanceTimersByTimeAsync(250));
      expect(screen.getAllByRole('listitem')).toHaveLength(5);
      expect(screen.getByRole('button', { name: 'Show more (1 more)' })).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
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
});
