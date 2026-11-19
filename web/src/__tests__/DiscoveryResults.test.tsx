import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { DiscoveryResults } from '../components/DiscoveryResults';

afterEach(() => vi.unstubAllGlobals());

describe('book discovery', () => {
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
    expect(add).toHaveBeenCalledWith('Dune', 'Frank Herbert', 'both', 1965, {
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
});
