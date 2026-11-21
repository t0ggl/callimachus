import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { CatalogView } from '../views/CatalogView';
import type { Book, BulkActionReport, Series } from '../types';
import { testBooks, testSeries } from './testFixtures';

afterEach(() => {
  vi.unstubAllGlobals();
});

const extraBooks: Book[] = [
  ...testBooks,
  {
    ...testBooks[0],
    id: 'book_standalone_1',
    title: 'Winter Road',
    seriesId: undefined,
    seriesTitle: undefined,
    seriesIndex: undefined,
    monitored: false,
  },
  {
    ...testBooks[0],
    id: 'book_standalone_2',
    title: 'Amber Coast',
    seriesId: undefined,
    seriesTitle: undefined,
    seriesIndex: undefined,
    monitored: true,
  },
];

const extraSeries: Series[] = [
  ...testSeries,
  {
    id: 'ser_2',
    title: 'Shattered Spire',
    status: 'completed',
    monitored: true,
  },
];

it('hides bulk selection controls for non-managers and restricts Delete to admins', async () => {
  const user = userEvent.setup({ delay: null });

  const { rerender } = render(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={false}
      isAdmin={false}
      onSelectBook={vi.fn()}
    />
  );

  expect(screen.queryByTestId('toggle-select-mode')).not.toBeInTheDocument();

  // Curator sees Select and monitoring controls, but not Delete.
  rerender(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={true}
      isAdmin={false}
      onSelectBook={vi.fn()}
    />
  );

  await user.click(screen.getByTestId('toggle-select-mode'));
  await user.click(screen.getByRole('checkbox', { name: 'Select book Winter Road' }));
  expect(screen.getByRole('button', { name: 'Monitor' })).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Unmonitor' })).toBeEnabled();
  expect(screen.queryByRole('button', { name: /Delete/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Refresh' })).not.toBeInTheDocument();

  // Admin sees Delete as well.
  rerender(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={true}
      isAdmin={true}
      onSelectBook={vi.fn()}
    />
  );
  expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled();
});

it('selects books and series in grid, list, and series tab, reports partial failures, and keeps failed items selected', async () => {
  const user = userEvent.setup({ delay: null });
  const onScanComplete = vi.fn().mockResolvedValue(undefined);
  const fetchMock = vi.fn().mockResolvedValue(
    Response.json({
      action: 'monitor',
      total: 2,
      succeeded: 1,
      failed: 1,
      results: [
        { kind: 'book', id: 'book_standalone_1', title: 'Winter Road', ok: true, monitored: true },
        { kind: 'series', id: 'ser_1', title: 'The Obsidian Archive', ok: false, error: 'Series not found.' },
      ],
    } satisfies BulkActionReport)
  );
  vi.stubGlobal('fetch', fetchMock);

  render(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={true}
      onSelectBook={vi.fn()}
      onScanComplete={onScanComplete}
    />
  );

  await user.click(screen.getByTestId('toggle-select-mode'));
  await user.click(screen.getByRole('checkbox', { name: 'Select book Winter Road' }));
  await user.click(screen.getByRole('checkbox', { name: 'Select series The Obsidian Archive' }));

  expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent('2 selected (1 book, 1 series)');
  const bulkBar = screen.getByRole('region', { name: 'Bulk library actions' });
  expect(within(bulkBar).getByText('Winter Road')).toBeInTheDocument();
  expect(within(bulkBar).getByText('The Obsidian Archive')).toBeInTheDocument();

  // Filtering hides one selected item and surfaces the count outside current view.
  await user.type(screen.getByPlaceholderText('Filter books...'), 'Winter');
  expect(within(bulkBar).getByText('1 outside current view')).toBeInTheDocument();
  await user.clear(screen.getByPlaceholderText('Filter books...'));

  await user.click(screen.getByRole('button', { name: 'Monitor' }));

  expect(fetchMock).toHaveBeenCalledWith(
    '/api/library/bulk',
    expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    })
  );
  const sentBody = JSON.parse(fetchMock.mock.calls[0][1].body as string);
  expect(sentBody).toEqual({
    action: 'monitor',
    items: [
      { kind: 'book', id: 'book_standalone_1', title: 'Winter Road' },
      { kind: 'series', id: 'ser_1', title: 'The Obsidian Archive' },
    ],
  });

  const report = await screen.findByTestId('bulk-action-report');
  expect(report).toHaveAttribute('role', 'alert');
  expect(report).toHaveTextContent('Monitoring enabled for 1 of 2 items. 1 item failed.');
  expect(report).toHaveTextContent('The Obsidian Archive');
  expect(report).toHaveTextContent('Series not found.');
  expect(onScanComplete).toHaveBeenCalledOnce();

  // Succeeded item is deselected; failed item remains selected for retry.
  expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent('1 selected (1 series)');
  expect(screen.getByRole('checkbox', { name: 'Select book Winter Road' })).not.toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'Select series The Obsidian Archive' })).toBeChecked();
});

it('supports select all visible in list view and multi-select in the Series tab', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn().mockResolvedValue(
    Response.json({
      action: 'discover',
      total: 2,
      succeeded: 2,
      failed: 0,
      results: [
        { kind: 'series', id: 'ser_1', title: 'The Obsidian Archive', ok: true },
        { kind: 'series', id: 'ser_2', title: 'Shattered Spire', ok: true },
      ],
    } satisfies BulkActionReport)
  );
  vi.stubGlobal('fetch', fetchMock);

  render(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={true}
      onSelectBook={vi.fn()}
    />
  );

  // List view select-all header checkbox selects all visible books & bundled series.
  await user.click(screen.getByTestId('view-list-btn'));
  await user.click(screen.getByTestId('toggle-select-mode'));
  await user.click(screen.getByRole('checkbox', { name: 'Select all visible items' }));
  expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent('3 selected (2 books, 1 series)');

  // Clear selection and switch to Series tab.
  await user.click(screen.getByRole('button', { name: 'Clear' }));
  await user.click(screen.getByTestId('tab-series'));

  await user.click(screen.getByRole('checkbox', { name: 'Select series The Obsidian Archive' }));
  await user.click(screen.getByRole('checkbox', { name: 'Select series Shattered Spire' }));
  expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent('2 selected (2 series)');

  await user.click(screen.getByRole('button', { name: 'Check New Releases' }));
  const report = await screen.findByTestId('bulk-action-report');
  expect(report).toHaveAttribute('role', 'status');
  expect(report).toHaveTextContent('New release check queued for 2 items.');
  expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent('Select books or series to run bulk actions');
});

it('supports bulk refresh hook when available and reports results', async () => {
  const user = userEvent.setup({ delay: null });
  const onScanComplete = vi.fn().mockResolvedValue(undefined);
  const onBulkRefresh = vi.fn().mockResolvedValue({
    action: 'refresh',
    total: 2,
    succeeded: 2,
    failed: 0,
    results: [
      { kind: 'book', id: 'book_standalone_1', title: 'Winter Road', ok: true, affectedBookCount: 1 },
      { kind: 'series', id: 'ser_1', title: 'The Obsidian Archive', ok: true, affectedBookCount: 1 },
    ],
  } satisfies BulkActionReport);

  render(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={true}
      onBulkRefresh={onBulkRefresh}
      onScanComplete={onScanComplete}
      onSelectBook={vi.fn()}
    />
  );

  await user.click(screen.getByTestId('toggle-select-mode'));
  await user.click(screen.getByRole('checkbox', { name: 'Select book Winter Road' }));
  await user.click(screen.getByRole('checkbox', { name: 'Select series The Obsidian Archive' }));

  const refreshBtn = screen.getByRole('button', { name: 'Refresh' });
  expect(refreshBtn).toBeEnabled();
  await user.click(refreshBtn);

  expect(onBulkRefresh).toHaveBeenCalledWith([
    expect.objectContaining({ kind: 'book', id: 'book_standalone_1', title: 'Winter Road' }),
    expect.objectContaining({ kind: 'series', id: 'ser_1', title: 'The Obsidian Archive' }),
  ]);
  expect(await screen.findByTestId('bulk-action-report')).toHaveTextContent('Metadata refreshed for 2 items.');
  expect(onScanComplete).toHaveBeenCalledOnce();
});

it('requires inline confirmation for admin bulk delete and passes deleteFiles option', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn().mockResolvedValue(
    Response.json({
      action: 'delete',
      total: 1,
      succeeded: 1,
      failed: 0,
      results: [{ kind: 'book', id: 'book_standalone_1', title: 'Winter Road', ok: true, deletedFiles: 2 }],
    } satisfies BulkActionReport)
  );
  vi.stubGlobal('fetch', fetchMock);

  render(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={true}
      isAdmin={true}
      onSelectBook={vi.fn()}
    />
  );

  await user.click(screen.getByTestId('toggle-select-mode'));
  await user.click(screen.getByRole('checkbox', { name: 'Select book Winter Road' }));
  await user.click(screen.getByRole('checkbox', { name: 'Select series The Obsidian Archive' }));

  await user.click(screen.getByRole('button', { name: 'Delete (1 book)' }));
  const confirmRegion = screen.getByRole('region', { name: 'Confirm bulk delete' });
  expect(confirmRegion).toHaveTextContent('Delete 1 selected book from Callimachus?');
  expect(confirmRegion).toHaveTextContent('1 selected series will not be deleted.');

  await user.click(within(confirmRegion).getByRole('checkbox', { name: 'Also delete book files from disk' }));
  await user.click(within(confirmRegion).getByRole('button', { name: 'Confirm Delete' }));

  const sentBody = JSON.parse(fetchMock.mock.calls[0][1].body as string);
  expect(sentBody).toEqual({
    action: 'delete',
    deleteFiles: true,
    items: [{ kind: 'book', id: 'book_standalone_1', title: 'Winter Road' }],
  });
  expect(await screen.findByTestId('bulk-action-report')).toHaveTextContent(
    'Deleted from library for 1 item. Removed 2 files from disk.'
  );
});

it('supports removing individual affected chips and built-in canRefresh API calls', async () => {
  const user = userEvent.setup({ delay: null });
  const fetchMock = vi.fn().mockResolvedValue(
    Response.json({
      action: 'refresh',
      total: 1,
      succeeded: 1,
      failed: 0,
      results: [{ kind: 'book', id: 'book_standalone_2', title: 'Amber Coast', ok: true, affectedBookCount: 1 }],
    } satisfies BulkActionReport)
  );
  vi.stubGlobal('fetch', fetchMock);

  render(
    <CatalogView
      books={extraBooks}
      authors={[]}
      series={extraSeries}
      canManage={true}
      canRefresh={true}
      onSelectBook={vi.fn()}
    />
  );

  await user.click(screen.getByTestId('toggle-select-mode'));
  await user.click(screen.getByRole('checkbox', { name: 'Select book Amber Coast' }));
  await user.click(screen.getByRole('checkbox', { name: 'Select book Winter Road' }));
  expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent('2 selected (2 books)');

  await user.click(screen.getByRole('button', { name: 'Remove Winter Road from selection' }));
  expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent('1 selected (1 book)');

  await user.click(screen.getByRole('button', { name: 'Refresh' }));
  const sentBody = JSON.parse(fetchMock.mock.calls[0][1].body as string);
  expect(sentBody).toEqual({
    action: 'refresh',
    items: [{ kind: 'book', id: 'book_standalone_2', title: 'Amber Coast' }],
  });
  expect(await screen.findByTestId('bulk-action-report')).toHaveTextContent('Metadata refreshed for 1 item.');
});
