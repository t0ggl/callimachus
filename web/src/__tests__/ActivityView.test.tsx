import { render, screen, within } from '@testing-library/react';
import { expect, it } from 'vitest';
import { ActivityView } from '../views/ActivityView';
import { testConversionJobs, testDownloads } from './testFixtures';

const base = { conversionJobs: [], downloads: [], logs: [], connectionStatus: 'connected' as const };
it('renders downloads, converting editions and import history in one list without legacy clutter', () => {
  render(<ActivityView {...base} conversionJobs={[{ ...testConversionJobs[0], stage: 'preparing' }]}
    downloads={testDownloads} logs={[{ id: 'log', timestamp: '', action: 'import', itemName: 'Imported book', status: 'success', details: 'Organized and tagged' }]} />);
  const list = within(screen.getByRole('list', { name: 'Book activity' }));
  expect(list.getAllByRole('listitem')).toHaveLength(3);
  expect(list.getByText(/Converting · Preparing/)).toBeInTheDocument();
  expect(list.getByText(/Downloading/)).toBeInTheDocument();
  expect(list.getByText(/Completed/)).toBeInTheDocument();
  expect(screen.queryByRole('tab')).not.toBeInTheDocument();
  expect(screen.queryByText(/SSE Live|Transcode|Activity & Queues|probing|encoding/i)).not.toBeInTheDocument();
});
it.each(['preparing', 'moving', 'completed'] as const)('keeps one row for an edition through %s', stage => {
  render(<ActivityView {...base}
    conversionJobs={[{ ...testConversionJobs[0], editionId: 'edition', stage, status: stage === 'completed' ? 'completed' : 'processing' }]}
    downloads={[{ ...testDownloads[0], editionId: 'edition', status: 'completed', requestId: 'request' }]}
    requests={[{ id: 'request', title: 'Same book', author: '', requestedBy: '', requestedAt: '', requestType: 'audiobook', status: 'downloading' }]} />);
  expect(screen.getAllByRole('listitem')).toHaveLength(1);
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', stage === 'completed' ? '100' : '89.2');
  expect(screen.getByText(new RegExp(stage === 'moving' ? 'Importing · Organizing' : stage === 'completed' ? 'Completed' : 'Converting · Preparing'))).toBeInTheDocument();
});
it('keeps different editions separate and shows errors and unknown ETA honestly', () => {
  render(<ActivityView {...base} conversionJobs={[{ ...testConversionJobs[0], editionId: 'audio', status: 'failed', errorMessage: 'Unreadable file', etaSeconds: 0 }]}
    downloads={[{ ...testDownloads[0], editionId: 'ebook', category: 'callimachus-ebooks' }]} />);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  expect(screen.getByRole('alert')).toHaveTextContent('Unreadable file');
  expect(screen.getByText(/ETA: Unavailable/)).toBeInTheDocument();
});
it('shows one useful empty state', () => {
  render(<ActivityView {...base} />);
  expect(screen.getByText(/No activity yet/)).toBeInTheDocument();
});
