import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { CatalogView } from '../views/CatalogView';
import { ActivityRefreshContext, BookActivityContext } from '../utils/activity';
import { testBooks, testSeries } from './testFixtures';

const standalone = {
  ...testBooks[0],
  id: 'standalone',
  title: 'Winter Road',
  seriesId: undefined,
  seriesTitle: undefined,
  seriesIndex: undefined,
};

it.each([
  ['card', 'ring-purple-500', 'ring-blue-500'],
  ['row', 'bg-purple-600/15', 'bg-blue-600/15'],
])('focuses and scrolls the selected series and book %s', async (layout, seriesHighlight, bookHighlight) => {
  const user = userEvent.setup({ delay: null });
  const onSelectBook = vi.fn();
  const onSelectSeries = vi.fn();
  render(
    <CatalogView
      books={[...testBooks, standalone]}
      authors={[]}
      series={testSeries}
      onSelectBook={onSelectBook}
      onSelectSeries={onSelectSeries}
    />
  );

  if (layout === 'row') await user.click(screen.getByRole('button', { name: 'List View' }));
  const series = screen.getByTestId(`series-${layout}-ser_1`);
  const book = screen.getByTestId(`book-${layout}-standalone`);
  const scrollSeries = (series.scrollIntoView = vi.fn());
  const scrollBook = (book.scrollIntoView = vi.fn());
  expect(series).not.toHaveClass(seriesHighlight);
  expect(book).not.toHaveClass(bookHighlight);
  expect(scrollSeries).not.toHaveBeenCalled();

  await user.keyboard('{ArrowDown}');
  expect(series).toHaveClass(seriesHighlight);
  expect(within(series).getByRole('button', { name: 'The Obsidian Archive' })).toHaveFocus();
  expect(scrollSeries).toHaveBeenCalledExactlyOnceWith({ block: 'nearest' });
  await user.keyboard('{Enter}');
  expect(onSelectSeries).toHaveBeenCalledExactlyOnceWith({ title: 'The Obsidian Archive', id: 'ser_1' });

  await user.keyboard('j');
  expect(series).not.toHaveClass(seriesHighlight);
  expect(book).toHaveClass(bookHighlight);
  expect(within(book).getByRole('button', { name: 'Winter Road' })).toHaveFocus();
  expect(scrollBook).toHaveBeenCalledExactlyOnceWith({ block: 'nearest' });
  await user.keyboard(' ');
  expect(onSelectBook).toHaveBeenCalledExactlyOnceWith(standalone);

  await user.keyboard('{Escape}');
  expect(book).not.toHaveClass(bookHighlight);

  const filter = screen.getByPlaceholderText('Filter books...');
  await user.type(filter, 'Winter');
  await user.keyboard('{ArrowDown}');
  expect(filter).toHaveFocus();
  expect(book).not.toHaveClass(bookHighlight);
  await user.tab();
  await user.keyboard('{ArrowDown}{Enter}');
  expect(book).toHaveClass(bookHighlight);
  expect(onSelectBook.mock.calls).toEqual([[standalone], [standalone]]);
});

afterEach(() => vi.unstubAllGlobals());

it('cancels in-progress downloads in library grid, list, and series tab without opening details', async () => {
  const user = userEvent.setup({ delay: null });
  const onSelectBook = vi.fn();
  const onScanComplete = vi.fn().mockResolvedValue(undefined);
  const refreshActivity = vi.fn();

  const downloadingStandalone = {
    ...standalone,
    id: 'dl-solo',
    title: 'Downloading Solo',
    state: 'Downloading' as const,
    monitored: true,
    formats: [],
    editions: [],
  };
  const seriesDownloadingBook = {
    ...testBooks[0],
    id: 'dl-series-book',
    title: 'Downloading Series Entry',
    state: 'Searching' as const,
    monitored: true,
    formats: [],
    editions: [],
  };
  const seriesMonitoredBook = {
    ...testBooks[0],
    id: 'mon-series-book',
    title: 'Monitored Series Entry',
    seriesIndex: 2,
    state: 'Not in library' as const,
    monitored: true,
    formats: [],
    editions: [],
  };

  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/books/dl-solo/cancel' && init?.method === 'POST') {
      return Response.json({ id: 'dl-solo', monitored: false, audiobookMonitored: false, state: 'Not in library', cancelledRequests: 1, cancelledDownloads: 0, cancelledConversions: 0 });
    }
    if (url === '/api/books/dl-series-book/cancel' && init?.method === 'POST') {
      return Response.json({ id: 'dl-series-book', monitored: false, audiobookMonitored: false, state: 'Not in library', cancelledRequests: 1, cancelledDownloads: 0, cancelledConversions: 0 });
    }
    if (url === '/api/books/mon-series-book/cancel' && init?.method === 'POST') {
      return Response.json({ id: 'mon-series-book', monitored: false, audiobookMonitored: false, state: 'Not in library', cancelledRequests: 0, cancelledDownloads: 0, cancelledConversions: 0 });
    }
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  const books = [downloadingStandalone, seriesDownloadingBook, seriesMonitoredBook];
  const { rerender } = render(
    <ActivityRefreshContext.Provider value={refreshActivity}>
      <BookActivityContext.Provider value={[]}>
        <CatalogView
          books={books}
          authors={[]}
          series={testSeries}
          canRequest
          bundleSeriesInLibrary={false}
          onSelectBook={onSelectBook}
          onScanComplete={onScanComplete}
        />
      </BookActivityContext.Provider>
    </ActivityRefreshContext.Provider>
  );

  // 1. Grid view: Cancel & Unmonitor on downloading standalone card without triggering onSelectBook
  const card = screen.getByTestId('book-card-dl-solo');
  await user.click(within(card).getByRole('button', { name: 'Cancel download and unmonitor Downloading Solo' }));
  await waitFor(() => {
    expect(within(card).queryByRole('button', { name: /Cancel download and unmonitor/i })).not.toBeInTheDocument();
  });
  expect(within(card).getByText('Not in library')).toBeInTheDocument();
  expect(onSelectBook).not.toHaveBeenCalled();

  // 2. List view: Cancel & Unmonitor on searching book row without triggering onSelectBook
  await user.click(screen.getByTestId('view-list-btn'));
  const row = screen.getByTestId('book-row-dl-series-book');
  await user.click(within(row).getByRole('button', { name: 'Cancel download and unmonitor Downloading Series Entry' }));
  await waitFor(() => {
    expect(within(row).queryByRole('button', { name: /Cancel download and unmonitor/i })).not.toBeInTheDocument();
  });
  expect(onSelectBook).not.toHaveBeenCalled();

  // 3. Series tab: Unmonitor monitored book in series card without triggering onSelectBook
  await user.click(screen.getByTestId('tab-series'));
  const miniCard = screen.getByTestId('series-tab-book-mon-series-book');
  await user.click(within(miniCard).getByRole('button', { name: 'Unmonitor Monitored Series Entry' }));
  await waitFor(() => {
    expect(within(miniCard).queryByRole('button', { name: 'Unmonitor Monitored Series Entry' })).not.toBeInTheDocument();
  });
  expect(onSelectBook).not.toHaveBeenCalled();
  expect(onScanComplete).toHaveBeenCalledTimes(3);
  expect(refreshActivity).toHaveBeenCalledTimes(3);

  // 4. Guests do not see cancel/unmonitor buttons
  rerender(
    <CatalogView
      books={[{ ...downloadingStandalone }]}
      authors={[]}
      series={[]}
      canManage={false}
      canRequest={false}
      bundleSeriesInLibrary={false}
      onSelectBook={onSelectBook}
    />
  );
  await user.click(screen.getByTestId('tab-books'));
  expect(screen.queryByRole('button', { name: /Cancel download and unmonitor/i })).not.toBeInTheDocument();
});
