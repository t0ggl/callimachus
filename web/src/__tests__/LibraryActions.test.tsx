import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { CatalogView } from '../views/CatalogView';
import { BookDetailModal } from '../components/BookDetailModal';
import { testBooks } from './testFixtures';

afterEach(() => vi.unstubAllGlobals());

it('scans the library and refreshes catalog data', async () => {
  const user = userEvent.setup({ delay: null });
  const refresh = vi.fn().mockResolvedValue(undefined);
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ books: 2 }) });
  vi.stubGlobal('fetch', fetch);
  render(<CatalogView books={[]} authors={[]} series={[]} selectedIndex={0} onSelectBook={vi.fn()} onScanComplete={refresh} />);
  await user.click(screen.getByRole('button', { name: 'Scan Library' }));
  expect(await screen.findByRole('status')).toHaveTextContent('2 books found');
  expect(fetch).toHaveBeenCalledWith('/api/library/scan', { method: 'POST' });
  expect(refresh).toHaveBeenCalledOnce();
});

it('offers recovery when a library scan fails', async () => {
  const user = userEvent.setup({ delay: null });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }));
  render(<CatalogView books={[]} authors={[]} series={[]} selectedIndex={0} onSelectBook={vi.fn()} />);
  await user.click(screen.getByRole('button', { name: 'Scan Library' }));
  expect(await screen.findByRole('status')).toHaveTextContent('permissions, then retry');
  expect(screen.getByRole('button', { name: 'Scan Library' })).toBeEnabled();
});

it('offers download options for an existing book', () => {
  render(<BookDetailModal book={testBooks[0]} onClose={vi.fn()} />);
  expect(screen.getByRole('button', { name: 'Search Download Options' })).toBeEnabled();
});
