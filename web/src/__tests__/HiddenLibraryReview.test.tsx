import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { HiddenLibraryReview } from '../components/HiddenLibraryReview';
import { SettingsView } from '../views/SettingsView';

const redRising = {
  id: 'red',
  title: 'Red Rising #1',
  author: '.agents',
  series: 'Red Rising',
  hiddenPath: '.agents/Red Rising #1/Red Rising #1.epub',
  removalBlock: '',
};
const requested = {
  id: 'requested',
  title: 'Requested Book',
  author: 'Requested Author',
  series: '',
  hiddenPath: '.agents/Requested.epub',
  removalBlock: 'This title has linked requests.',
};

afterEach(() => vi.unstubAllGlobals());

it('offers per-title decisions in Library & Storage and requires keyboard confirmation for removal', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(Response.json([redRising, requested]))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetchMock);
  const user = userEvent.setup({ delay: null });
  const onCatalogChanged = vi.fn();
  render(<SettingsView indexers={[]} qualityProfile={{
    id: 'default', name: 'Standard', allowedFormats: ['EPUB'], allowedLanguages: ['en'],
    preferSingleFileM4b: true, maxAudiobookSizeMb: 1536, maxEbookSizeBytes: 100 * 1024 * 1024,
  }} onCatalogChanged={onCatalogChanged} />);

  await user.click(screen.getByRole('button', { name: 'Library & Storage' }));
  const review = within(screen.getByRole('region', { name: 'Hidden import review' }));
  expect(fetchMock).not.toHaveBeenCalled();
  await user.click(review.getByRole('button', { name: 'Load review list' }));
  expect(await review.findByText(redRising.hiddenPath)).toBeVisible();

  const remove = review.getByRole('button', { name: 'Remove from catalog (Red Rising #1)' });
  expect(review.getByRole('button', { name: 'Remove from catalog (Requested Book)' })).toBeDisabled();
  expect(review.getByText(/linked requests/)).toBeVisible();

  remove.focus();
  await user.keyboard('{Enter}');
  expect(review.getByRole('button', { name: 'Cancel' })).toHaveFocus();
  await user.keyboard('{Enter}');
  expect(remove).toHaveFocus();
  expect(fetchMock).toHaveBeenCalledTimes(1);

  await user.keyboard('{Enter}{Tab}{Enter}');
  expect(await review.findByText('Removed Red Rising #1 from the catalog.')).toBeVisible();
  expect(fetchMock).toHaveBeenLastCalledWith('/api/library/review/red', { method: 'DELETE' });
  expect(onCatalogChanged).toHaveBeenCalledOnce();
  expect(review.getByRole('heading', { name: 'Hidden import review' })).toHaveFocus();

  await user.click(review.getByRole('button', { name: 'Keep Requested Book' }));
  expect(await review.findByText('No titles await review.')).toBeVisible();
  expect(review.getByRole('status')).toHaveTextContent('Kept Requested Book in the catalog.');
  expect(fetchMock).toHaveBeenLastCalledWith('/api/library/review/requested/keep', { method: 'PUT' });
  expect(onCatalogChanged).toHaveBeenCalledOnce();
});

it('recovers from a failed load and keeps a title reviewable if media arrives before removal', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(new Response(null, { status: 500 }))
    .mockResolvedValueOnce(Response.json([redRising]))
    .mockResolvedValueOnce(new Response('This title has indexed media.\n', { status: 409 }));
  vi.stubGlobal('fetch', fetchMock);
  const user = userEvent.setup({ delay: null });
  const onCatalogChanged = vi.fn();
  render(<HiddenLibraryReview onCatalogChanged={onCatalogChanged} />);

  await user.click(screen.getByRole('button', { name: 'Load review list' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load the review list');
  await user.click(screen.getByRole('button', { name: 'Load review list' }));

  await user.click(await screen.findByRole('button', { name: 'Remove from catalog (Red Rising #1)' }));
  await user.click(screen.getByRole('button', { name: 'Confirm removal of Red Rising #1' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('This title has indexed media.');
  expect(screen.getByRole('button', { name: 'Remove from catalog (Red Rising #1)' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Keep Red Rising #1' })).toBeEnabled();
  expect(onCatalogChanged).not.toHaveBeenCalled();
});
