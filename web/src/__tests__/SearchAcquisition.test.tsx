import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../App';
import { DiscoveryResults } from '../components/DiscoveryResults';

const match = { title: 'Dune', authors: ['Frank Herbert'], year: 1965, cover_url: '/cover.jpg', series: 'Dune series', series_index: 1 };
afterEach(() => vi.unstubAllGlobals());

it.each(['Frank Herbert', 'Dune', 'Herbert desert planet'])('discovers and queues from the top search: %s', async (query) => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn(async (url: string, options?: RequestInit) => {
    if (url === '/api/auth/me') return { ok: true, json: async () => ({ username: 'admin', role: 'admin' }) };
    if (url.startsWith('/api/discover?')) return { ok: true, json: async () => [match] };
    if (url === '/api/requests' && options?.method === 'POST') return { ok: true, json: async () => ({}) };
    return { ok: true, json: async () => [] };
  });
  vi.stubGlobal('fetch', fetchMock);
  render(<App />);
  await user.click(screen.getByTestId('global-search-trigger'));
  const input = screen.getByRole('textbox', { name: 'Search books, authors, or keywords' });
  await user.type(input, query);
  const add = await screen.findByRole('button', { name: 'Add Dune' });
  expect(fetchMock).toHaveBeenCalledWith(`/api/discover?q=${encodeURIComponent(query)}`, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  expect(screen.getByRole('img', { name: 'Cover of Dune' })).toHaveAttribute('src', '/cover.jpg');
  expect(screen.getByRole('button', { name: 'Dune series' })).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText('Format'), 'ebook');
  // Keyboard activation must not run the palette's navigation action.
  add.focus();
  await user.keyboard('{Enter}');
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/requests', expect.objectContaining({ method: 'POST', body: JSON.stringify({ title: 'Dune', author: 'Frank Herbert', format: 'epub', year: 1965 }) })));
  expect(await screen.findByRole('button', { name: 'Added Dune' })).toBeDisabled();
  expect(screen.getByRole('dialog', { name: 'Search books and commands' })).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: /Library/ })).toBeInTheDocument();
});

it('opens on focus, traps focus and closes with Escape', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }));
  render(<App />);
  fireEvent.focus(screen.getByTestId('global-search-trigger'));
  const input = screen.getByTestId('command-palette-input');
  expect(input).toHaveFocus();
  await userEvent.setup({ delay: null }).keyboard('{Shift>}{Tab}{/Shift}');
  expect(screen.getByRole('button', { name: 'Close search' })).toHaveFocus();
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  const user = userEvent.setup({ delay: null });
  const trigger = screen.getByTestId('global-search-trigger');
  await user.click(trigger);
  await user.click(screen.getByRole('button', { name: 'Close search' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});

it('discards obsolete responses after query changes', async () => {
  let resolveOld!: (value: unknown) => void;
  const fetchMock = vi.fn().mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve; })).mockResolvedValue({ ok: true, json: async () => [match] });
  vi.stubGlobal('fetch', fetchMock);
  const props = { onSubmitRequest: vi.fn() };
  const { rerender } = render(<DiscoveryResults query="old" {...props} />);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  rerender(<DiscoveryResults query="new" {...props} />);
  expect(await screen.findByRole('button', { name: 'Add Dune' })).toBeInTheDocument();
  await act(async () => { resolveOld({ ok: true, json: async () => [{ ...match, title: 'Obsolete' }] }); });
  await waitFor(() => expect(screen.queryByText('Obsolete')).not.toBeInTheDocument());
});

it('shows acquisition errors and allows retry', async () => {
  const user = userEvent.setup({ delay: null });
  const add = vi.fn().mockRejectedValueOnce(new Error('Unable to add. Try again.')).mockResolvedValue(undefined);
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => [match] }));
  render(<DiscoveryResults query="Dune" onSubmitRequest={add} />);
  await user.click(await screen.findByRole('button', { name: 'Add Dune' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Try again');
  await user.click(screen.getByRole('button', { name: 'Add Dune' }));
  expect(await screen.findByRole('button', { name: 'Added Dune' })).toBeDisabled();
});
