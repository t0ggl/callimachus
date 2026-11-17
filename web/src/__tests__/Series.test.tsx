import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { SeriesBadge } from '../components/SeriesBadge';
import { SeriesDetailModal } from '../components/SeriesDetailModal';
import { RequestsView } from '../views/RequestsView';

afterEach(() => vi.restoreAllMocks());
it.each([[1, 3, 'Book 1 of 3 in'], [2.5, 4, 'Book 2.5 of 4 in'], [0, undefined, 'Book 0 in'], [0.5, 0, 'Book 0.5 in'], [undefined, 3, 'In']])('formats index %s with total %s', async (index, total, text) => {
  const select = vi.fn();
  render(<SeriesBadge title="Broken Earth" index={index} total={total} onSelect={select} />);
  expect(screen.getByRole("button").parentElement).toHaveTextContent(text);
  await userEvent.setup({ delay: null }).click(screen.getByRole('button', { name: 'Broken Earth' }));
  expect(select).toHaveBeenCalledWith('Broken Earth');
});
it('opens reading order from discovery with release dates and acquisition status', async () => {
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true; });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce({ ok: true, json: async () => [{ title: 'Novella', authors: ['Author'], series: 'Sequence', series_index: 2.5, series_total: 4 }] }).mockResolvedValueOnce({ ok: true, json: async () => ({ complete: true, books: [{ title: 'Novella', series_index: 2.5, publish_date: '2020-01-02', monitored: true, library_book_id: 'book' }] }) }));
  const user = userEvent.setup({ delay: null });
  render(<RequestsView requests={[]} onSubmitRequest={vi.fn()} />);
  await user.type(screen.getByRole('textbox'), 'Novella');
  await user.click(screen.getByRole('button', { name: 'Search' }));
  await user.click(await screen.findByRole('button', { name: 'Sequence' }));
  expect(await screen.findByText('Book 2.5: Novella')).toBeInTheDocument();
  expect(screen.getByText('2020-01-02')).toBeInTheDocument();
  expect(screen.getByText('In library · Monitored')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  vi.unstubAllGlobals();
});
it('offers retry on failure and explains incomplete order', async () => {
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true; });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce({ ok: false }).mockResolvedValueOnce({ ok: true, json: async () => ({ complete: false, books: [] }) }));
  render(<SeriesDetailModal title="Sequence" onClose={vi.fn()} />);
  await screen.findByRole('alert');
  await userEvent.setup({ delay: null }).click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByText(/Showing known catalog/)).toBeInTheDocument());
  vi.unstubAllGlobals();
});
