import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, it, expect, vi } from 'vitest';
import { Layout } from '../components/Layout';

const props = {
  currentView: 'catalog' as const,
  onNavigate: vi.fn(),
  onOpenPalette: vi.fn(),
  user: null,
  onLogout: vi.fn(),
  children: null,
};

describe('Layout build version badge', () => {
  const user = { id: '1', username: 'reader', role: 'guest' as const, email: 'reader@example.com' };

  afterEach(() => vi.restoreAllMocks());

  it.each(['v0.7.0', 'v1.2.3-rc.1+build.001', 'abcdef01', 'dev'])(
    'displays %s from the health API in the sidebar',
    async (displayVersion) => {
      const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
        Response.json({ status: 'ok', displayVersion }),
      );
      render(<Layout {...props} user={user} activeDownloadsCount={0} convertingJobsCount={0} />);
      const sidebar = within(screen.getByRole('complementary'));
      expect(await sidebar.findByText(displayVersion)).toHaveAttribute('title', displayVersion);
      expect(fetchMock).toHaveBeenCalledExactlyOnceWith('/api/health', { signal: expect.any(AbortSignal) });
      expect(sidebar.queryByText('v0.1')).not.toBeInTheDocument();
    },
  );

  it.each([
    ['unavailable', () => new Response(null, { status: 503 })],
    ['missing version metadata', () => Response.json({ status: 'ok' })],
  ] as const)('keeps the sidebar usable when health is %s', async (_, response) => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(response());
    await act(async () => {
      render(<Layout {...props} user={user} activeDownloadsCount={0} convertingJobsCount={0} />);
    });
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(screen.getByText('Callimachus')).toBeInTheDocument();
    expect(screen.queryByText(/^(v0\.1|dev)$/)).not.toBeInTheDocument();
  });
});

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

describe('Layout Settings nav visibility', () => {
  it('shows Settings nav link for admin users', () => {
    render(
      <Layout
        {...props}
        user={{ id: '1', username: 'admin', role: 'admin', email: 'admin@example.com' }}
        activeDownloadsCount={0}
        convertingJobsCount={0}
      />
    );
    expect(screen.getByTestId('nav-catalog')).toBeInTheDocument();
    expect(screen.getByTestId('nav-activity')).toBeInTheDocument();
    expect(screen.getByTestId('nav-settings')).toBeInTheDocument();
  });

  it.each(['curator', 'requester', 'guest'] as const)(
    'hides Settings nav link for non-admin role %s',
    (role) => {
      render(
        <Layout
          {...props}
          user={{ id: '2', username: `${role}_user`, role, email: `${role}@example.com` }}
          activeDownloadsCount={0}
          convertingJobsCount={0}
        />
      );
      expect(screen.getByTestId('nav-catalog')).toBeInTheDocument();
      expect(screen.getByTestId('nav-activity')).toBeInTheDocument();
      expect(screen.queryByTestId('nav-settings')).not.toBeInTheDocument();
    }
  );

  it('hides Settings nav link when user is null', () => {
    render(<Layout {...props} user={null} activeDownloadsCount={0} convertingJobsCount={0} />);
    expect(screen.queryByTestId('nav-settings')).not.toBeInTheDocument();
  });
});
