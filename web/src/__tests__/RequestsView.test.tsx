import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { RequestsView } from '../views/RequestsView';
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
    render(<RequestsView requests={[]} onSubmitRequest={add} />);
    await user.type(screen.getByLabelText('Book search'), 'Herbert dune');
    await user.click(screen.getByRole('button', { name: 'Search' }));
    await user.click(await screen.findByRole('button', { name: 'Add Dune' }));
    expect(add).toHaveBeenCalledWith('Dune', 'Frank Herbert', 'both', 1965);
    expect(await screen.findByRole('status')).toHaveTextContent('Added Dune');
  });
  it('shows source failures and permits recovery', async () => {
    const user = userEvent.setup({ delay: null });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }));
    render(<RequestsView requests={[]} onSubmitRequest={vi.fn()} />);
    await user.type(screen.getByLabelText('Book search'), 'Dune');
    await user.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Try again');
  });
});
