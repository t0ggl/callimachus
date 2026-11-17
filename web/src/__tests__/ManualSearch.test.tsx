import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { it, expect, vi, afterEach } from 'vitest';
import { ManualSearch } from '../components/ManualSearch';
afterEach(() => vi.unstubAllGlobals());
it('shows cached score breakdown, refreshes, and manually retries a penalized release', async () => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  const candidate = {
    key: '1:g',
    release: { title: 'Dune EPUB', size: 1048576, seeders: 60 },
    format: 'epub',
    score: -50,
    penalty: -100,
    breakdown: { format: 30, seeders: 20 },
  };
  const fetcher = vi
    .fn()
    .mockResolvedValue({ ok: true, json: async () => [candidate] });
  vi.stubGlobal('fetch', fetcher);
  const user = userEvent.setup({ delay: null });
  const close = vi.fn();
  render(<ManualSearch requestId="7" onClose={close} />);
  expect(await screen.findByText('Dune EPUB')).toBeInTheDocument();
  expect(screen.getByText(/failure: -100/)).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Refresh' }));
  expect(fetcher).toHaveBeenCalledWith(
    '/api/requests/7/refresh',
    expect.objectContaining({ method: 'POST' }),
  );
  await user.click(await screen.findByRole('button', { name: 'Download' }));
  expect(fetcher).toHaveBeenCalledWith(
    '/api/requests/7/download',
    expect.objectContaining({ body: JSON.stringify({ key: '1:g' }) }),
  );
  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(close).toHaveBeenCalled();
});
