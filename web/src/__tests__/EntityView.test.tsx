import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { EntityView } from '../views/EntityView';

afterEach(() => vi.unstubAllGlobals());
it('saves monitoring and queues both manual checks with feedback', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ monitored: false, books: [{ id: 'b', title: 'Next Book', releaseDate: '2028-05-10', state: 'Scheduled' }] }) });
  vi.stubGlobal('fetch', fetchMock);
  render(<EntityView kind="authors" name="An Author" canManage onOpenBook={vi.fn()} />);
  expect(await screen.findByText('Next Book')).toBeInTheDocument();
  expect(screen.getByText('Scheduled')).toBeInTheDocument();
  await user.click(screen.getByLabelText('Monitor Author'));
  expect(fetchMock).toHaveBeenCalledWith('/api/catalog/entity?kind=authors&name=An%20Author', expect.objectContaining({ method: 'PUT', body: '{"monitored":true}' }));
  expect(screen.getByLabelText('Monitor Author')).toBeChecked();
  await user.click(screen.getByRole('button', { name: 'Check New Releases' }));
  expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('action=discover'), expect.objectContaining({ method: 'POST' }));
  expect(screen.getByRole('status')).toHaveTextContent('queued');
  await user.click(screen.getByRole('button', { name: 'Search Downloads' }));
  expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('action=downloads'), expect.objectContaining({ method: 'POST' }));
});
it('reports failed actions and keeps the saved toggle state', async () => {
  const user = userEvent.setup({ delay: null });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce({ ok: true, json: async () => ({ monitored: false, books: [] }) }).mockResolvedValue({ ok: false }));
  render(<EntityView kind="series" name="Sequence" canManage onOpenBook={vi.fn()} />);
  await waitFor(() => expect(screen.getByLabelText('Monitor Series')).toBeEnabled());
  await user.click(screen.getByLabelText('Monitor Series'));
  expect(await screen.findByRole('alert')).toHaveTextContent('retry');
  expect(screen.getByLabelText('Monitor Series')).not.toBeChecked();
});
it('allows guests to browse without management controls', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ monitored: false, books: [] }) }));
  render(<EntityView kind="series" name="Sequence" canManage={false} onOpenBook={vi.fn()} />);
  expect(await screen.findByText(/No known books/)).toBeInTheDocument();
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
});
