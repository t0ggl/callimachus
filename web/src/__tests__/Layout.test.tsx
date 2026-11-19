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

it('keeps the acquisition count and navigation on Activity while the top bar only offers search', async () => {
  const user = userEvent.setup({ delay: null });
  const onNavigate = vi.fn();
  render(<Layout {...props} onNavigate={onNavigate} activeDownloadsCount={1} convertingJobsCount={2} />);
  const header = within(screen.getByRole('banner'));
  expect(header.getAllByRole('button')).toEqual([header.getByTestId('global-search-trigger')]);
  const activity = screen.getByRole('button', { name: 'Activity 3' });
  activity.focus();
  await user.keyboard('{Enter}');
  expect(onNavigate).toHaveBeenCalledExactlyOnceWith('activity');
});

it('removes logo subtitle, mode badge and legacy labels', () => {
  render(<Layout {...props} activeDownloadsCount={0} convertingJobsCount={0} />);
  expect(screen.queryByText(/Audiobooks & eBooks|OPERATE MODE|Library & Catalog|Activity & Queues|Transcode Queue/)).not.toBeInTheDocument();
});

describe('Layout Settings nav visibility', () => {
  it('puts all three admin destinations in one mobile row and keeps desktop navigation stacked', () => {
    render(
      <Layout
        {...props}
        user={{ id: '1', username: 'admin', role: 'admin', email: 'admin@example.com' }}
        activeDownloadsCount={0}
        convertingJobsCount={0}
      />
    );
    const nav = within(screen.getByRole('navigation'));
    expect(screen.getByRole('navigation')).toHaveClass('flex', 'flex-nowrap', 'md:block');
    expect(nav.getAllByRole('button')).toEqual([
      nav.getByTestId('nav-catalog'),
      nav.getByTestId('nav-activity'),
      nav.getByTestId('nav-settings'),
    ]);
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
    expect(screen.getByTestId('auth-verifying')).toHaveClass('sr-only', 'md:not-sr-only');
    expect(screen.queryByTestId('logout-button')).not.toBeInTheDocument();
  });
});

it('keeps the profile desktop-only and provides one accessible sign-out control', async () => {
  const viewer = userEvent.setup({ delay: null });
  const onLogout = vi.fn();
  render(
    <Layout
      {...props}
      user={{ id: '1', username: 'reader', role: 'guest', email: 'reader@example.com' }}
      onLogout={onLogout}
      activeDownloadsCount={0}
      convertingJobsCount={0}
    />
  );
  const profile = screen.getByText('reader').closest('.md\\:flex');
  expect(profile).toHaveClass('hidden', 'md:flex');
  expect(profile).toContainElement(screen.getByText('guest'));
  const logout = screen.getByRole('button', { name: 'Sign Out' });
  expect(logout).toBe(screen.getByTestId('logout-button'));
  expect(profile).not.toContainElement(logout);
  expect(logout).toHaveClass('absolute', 'md:static');
  await viewer.click(logout);
  expect(onLogout).toHaveBeenCalledOnce();
});
