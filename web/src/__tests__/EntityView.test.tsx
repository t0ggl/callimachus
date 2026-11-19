import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { EntityView } from '../views/EntityView';
import type { Book } from '../types';

afterEach(() => vi.unstubAllGlobals());
it.each([
  { kind: 'authors', name: 'An Author', label: 'Author', list: 'Books' },
  { kind: 'series', name: 'Sequence', label: 'Series', list: 'Reading Order' },
] as const)('renders $label books, saves monitoring, queues checks and opens book details', async ({ kind, name, label, list }) => {
  const user = userEvent.setup({ delay: null });
  const book: Book = {
    id: 'b', title: 'Collected Stories', authorId: 'a', authorName: 'An Author', seriesTitle: 'Sequence',
    seriesIndex: 0.5, state: 'In library', coverUrl: '/api/books/b/cover', formats: ['EPUB', 'M4B'], editions: [],
  };
  const upcoming = { ...book, id: 'next', title: 'Next Book', seriesIndex: 1, releaseDate: '2099-05-10', state: 'Scheduled', formats: [], coverUrl: undefined };
  const url = `/api/catalog/entity?kind=${kind}&name=${encodeURIComponent(name)}`;
  const fetchMock = vi.fn().mockResolvedValueOnce({
    ok: true,
    json: async () => ({ monitored: false, photoUrl: '/author.jpg', books: [book, upcoming] }),
  }).mockResolvedValue({ ok: true, json: async () => [upcoming] });
  vi.stubGlobal('fetch', fetchMock);
  const { container } = render(<EntityView kind={kind} name={name} canManage />);
  expect(screen.getByRole('heading', { name, level: 1 })).toBeInTheDocument();
  const books = await screen.findByRole('list', { name: list });
  const [owned, scheduled] = within(books).getAllByRole('listitem');
  expect(owned).toHaveTextContent('Book 0.5');
  expect(owned).toHaveTextContent('In library');
  expect(within(owned).getByText('EPUB')).toBeInTheDocument();
  expect(within(owned).getByText('M4B')).toBeInTheDocument();
  expect(within(owned).getByRole('img')).toHaveAttribute('src', '/api/books/b/cover');
  expect(scheduled).toHaveTextContent('Scheduled');
  expect(scheduled).toHaveTextContent('No files');
  expect(books).not.toHaveTextContent(/monitored/i);
  expect(screen.getByText('1 / 2 in library')).toBeInTheDocument();
  expect(container.querySelector('header img')).toHaveAttribute('src', kind === 'authors' ? '/author.jpg' : '/api/books/b/cover');
  const toggle = screen.getByRole('checkbox', { name: `Monitor ${label}` });
  await user.click(toggle);
  expect(fetchMock).toHaveBeenCalledWith(url, expect.objectContaining({ method: 'PUT', body: '{"monitored":true}' }));
  expect(toggle).toBeChecked();
  await user.click(screen.getByRole('button', { name: 'Check New Releases' }));
  expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('action=discover'), expect.objectContaining({ method: 'POST' }));
  expect(screen.getByRole('status')).toHaveTextContent('queued');
  await user.click(screen.getByRole('button', { name: 'Search Downloads' }));
  expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('action=downloads'), expect.objectContaining({ method: 'POST' }));
  await user.click(within(scheduled).getByRole('button', { name: 'Next Book' }));
  expect(fetchMock).toHaveBeenLastCalledWith('/api/books?id=next', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  const dialog = await screen.findByRole('dialog', { name: 'Details for Next Book' });
  expect(within(dialog).getByText('Scheduled')).toBeInTheDocument();
  await user.click(within(dialog).getByRole('button', { name: 'Close details' }));
  expect(dialog).not.toBeInTheDocument();
});
it('reports failed actions and keeps the saved toggle state', async () => {
  const user = userEvent.setup({ delay: null });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce({ ok: true, json: async () => ({ monitored: false, books: [] }) }).mockResolvedValue({ ok: false }));
  render(<EntityView kind="series" name="Sequence" canManage />);
  await waitFor(() => expect(screen.getByLabelText('Monitor Series')).toBeEnabled());
  await user.click(screen.getByLabelText('Monitor Series'));
  expect(await screen.findByRole('alert')).toHaveTextContent('retry');
  expect(screen.getByLabelText('Monitor Series')).not.toBeChecked();
});
it('allows guests to browse without management controls', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ monitored: false, books: [] }) }));
  render(<EntityView kind="series" name="Sequence" canManage={false} />);
  expect(await screen.findByText(/No known books/)).toBeInTheDocument();
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
});
