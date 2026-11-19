import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { CatalogView } from '../views/CatalogView';
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
  expect(onSelectSeries).toHaveBeenCalledExactlyOnceWith('The Obsidian Archive');

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
