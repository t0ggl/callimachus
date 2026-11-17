import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { Layout } from '../components/Layout';

const props = {
  currentView: 'catalog' as const,
  onNavigate: vi.fn(),
  onOpenPalette: vi.fn(),
  user: null,
  onLogout: vi.fn(),
  children: null,
};

describe('Layout active downloads metric', () => {
  it.each([[0, 0], [3, 0], [0, 2], [3, 2]])(
    'combines %i downloads and %i conversions into one header counter',
    (downloads, conversions) => {
      render(<Layout {...props} activeDownloadsCount={downloads} convertingJobsCount={conversions} />);
      const header = within(screen.getByRole('banner'));
      const metric = header.getByRole('button', { name: `Active Downloads: ${downloads + conversions}` });
      expect(metric).toHaveTextContent(String(downloads + conversions));
      expect(header.queryByTitle('Active Conversions')).not.toBeInTheDocument();
      expect(header.getAllByTitle('Active Downloads')).toHaveLength(1);
    },
  );

  it.each(['click', 'Enter', ' '])('opens Activity using %s', async (action) => {
    const user = userEvent.setup({ delay: null });
    const onNavigate = vi.fn();
    render(<Layout {...props} onNavigate={onNavigate} activeDownloadsCount={1} convertingJobsCount={2} />);
    const metric = screen.getByRole('button', { name: 'Active Downloads: 3' });
    if (action === 'click') await user.click(metric);
    else {
      metric.focus();
      await user.keyboard(action === 'Enter' ? '{Enter}' : ' ');
    }
    expect(onNavigate).toHaveBeenCalledExactlyOnceWith('activity');
  });
});

it('removes logo subtitle, mode badge and legacy labels', () => {
  render(<Layout {...props} activeDownloadsCount={0} convertingJobsCount={0} />);
  expect(screen.queryByText(/Audiobooks & eBooks|OPERATE MODE|Library & Catalog|Activity & Queues|Transcode Queue/)).not.toBeInTheDocument();
});
