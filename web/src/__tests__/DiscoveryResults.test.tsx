import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { DiscoveryResults } from '../components/DiscoveryResults';
afterEach(() => vi.unstubAllGlobals());
describe('book discovery', () => {
  it('confirms metadata with a single Add action', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          { title: 'Dune', authors: ['Frank Herbert'], year: 1965 },
        ],
      }),
    );
    render(<DiscoveryResults query="Dune" onSubmitRequest={add} />);
    await user.click(await screen.findByRole('button', { name: 'Add Dune' }));
    expect(add).toHaveBeenCalledWith('Dune', 'Frank Herbert', 'both', 1965);
    expect(await screen.findByRole('status')).toHaveTextContent('Added Dune');
  });
  it('opens an existing library book instead of adding a duplicate', async () => {
    const user = userEvent.setup({ delay: null });
    const add = vi.fn(); const open = vi.fn();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => [{ title: 'Dune', authors: ['Frank Herbert'], year: 1965, library_book_id: 'local-dune' }] }));
    render(<DiscoveryResults query="Dune" onSubmitRequest={add} onOpenBook={open} />);
    await user.click(await screen.findByRole('button', { name: /In Library/ }));
    expect(screen.queryByRole('button', { name: 'Add Dune' })).not.toBeInTheDocument();
    expect(open).toHaveBeenCalledWith('local-dune'); expect(add).not.toHaveBeenCalled();
  });
  it('shows source failures and permits recovery', async () => {
    const user = userEvent.setup({ delay: null });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce({ ok: false }).mockResolvedValue({ ok: true, json: async () => [] }));
    render(<DiscoveryResults query="Dune" onSubmitRequest={vi.fn()} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Try again');
    await user.click(screen.getByRole('button', { name: 'Retry search' }));
    expect(await screen.findByText(/No matches/)).toBeInTheDocument();
  });
});
