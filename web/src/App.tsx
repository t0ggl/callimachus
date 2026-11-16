import React, { useState, useEffect, useCallback } from 'react';
import { Layout, NavView } from './components/Layout';
import { CatalogView } from './views/CatalogView';
import { ActivityView } from './views/ActivityView';
import { RequestsView } from './views/RequestsView';
import { SettingsView } from './views/SettingsView';
import { CommandPalette } from './components/CommandPalette';
import { BookDetailModal } from './components/BookDetailModal';
import { useKeyboardNav } from './hooks/useKeyboardNav';
import { useActivityStream } from './hooks/useActivityStream';
import {
  Book,
  Author,
  Series,
  BookRequest,
  ImportLog,
  IndexerConfig,
  DownloadClientConfig,
  LibrarySettings,
  ProwlarrSettings,
  QualityProfile,
  AuthSettings,
  SettingsOverridesMap,
  UserProfile,
  RequestType,
  RequestStatus,
} from './types';

export const App: React.FC = () => {
  const [currentView, setCurrentView] = useState<NavView>('catalog');
  const [isPaletteOpen, setIsPaletteOpen] = useState(false);
  const [selectedBook, setSelectedBook] = useState<Book | null>(null);

  // Real backend state
  const [currentUser, setCurrentUser] = useState<UserProfile | null>(null);
  const [books, setBooks] = useState<Book[]>([]);
  const [authors, setAuthors] = useState<Author[]>([]);
  const [series, setSeries] = useState<Series[]>([]);
  const [requests, setRequests] = useState<BookRequest[]>([]);
  const [logs, setLogs] = useState<ImportLog[]>([]);
  const [indexers, setIndexers] = useState<IndexerConfig[]>([]);
  const [downloadClients, setDownloadClients] = useState<DownloadClientConfig[]>([]);
  const [librarySettings, setLibrarySettings] = useState<LibrarySettings>({
    rootPath: './books',
    stagingPollInterval: '5s',
    indexerSyncInterval: '15m0s',
  });
  const [prowlarrSettings, setProwlarrSettings] = useState<ProwlarrSettings>({
    url: '',
    apiKeyConfigured: false,
    syncInterval: '1h0m0s',
  });
  const [qualityProfile, setQualityProfile] = useState<QualityProfile>({
    id: 'default',
    name: 'Standard Audiobook',
    allowedLanguages: ['en'],
    allowedFormats: ['EPUB', 'M4B'],
    audioMinBitrateKbps: 32,
    audioTargetBitrateKbps: 64,
    preferSingleFileM4b: true,
    maxFileSizeMb: 2048,
  });
  const [authSettings, setAuthSettings] = useState<AuthSettings>({
    oidcEnabled: false,
    oidcIssuerUrl: '',
    oidcClientId: '',
    oidcClientSecretConfigured: false,
    oidcRedirectUrl: '',
    oidcAdminGroups: [],
    oidcCuratorGroups: [],
    oidcDefaultRole: 'requester',
    localAuthEnabled: false,
    sessionTimeoutHours: 720,
  });
  const [settingsOverrides, setSettingsOverrides] = useState<SettingsOverridesMap>({});

  // Real-time activity stream for live pipeline updates
  const {
    transcodeJobs,
    setTranscodeJobs,
    downloads,
    setDownloads,
    connectionStatus,
  } = useActivityStream({
    initialTranscodeJobs: [],
    initialDownloads: [],
  });

  const redirectToOIDC = useCallback(() => {
    window.location.assign('/api/auth/oidc/login');
  }, []);

  // Fetch functions for real backend data
  const fetchAuthUser = useCallback(async () => {
    try {
      const res = await fetch('/api/auth/me');
      if (res.status === 401) {
        setCurrentUser(null);
        redirectToOIDC();
        return;
      }
      if (res.ok) {
        const user = await res.json();
        setCurrentUser(user);
      } else {
        setCurrentUser(null);
      }
    } catch {
      setCurrentUser(null);
    }
  }, [redirectToOIDC]);

  const fetchCatalog = useCallback(async () => {
    try {
      const [booksRes, authorsRes, seriesRes] = await Promise.all([
        fetch('/api/books'),
        fetch('/api/authors'),
        fetch('/api/series'),
      ]);
      if (booksRes.status === 401 || authorsRes.status === 401 || seriesRes.status === 401) {
        redirectToOIDC();
        return;
      }
      if (booksRes.ok) {
        const data = await booksRes.json();
        setBooks(Array.isArray(data) ? data : []);
      }
      if (authorsRes.ok) {
        const data = await authorsRes.json();
        setAuthors(Array.isArray(data) ? data : []);
      }
      if (seriesRes.ok) {
        const data = await seriesRes.json();
        setSeries(Array.isArray(data) ? data : []);
      }
    } catch (err) {
      console.error('Failed to load catalog', err);
    }
  }, [redirectToOIDC]);

  const fetchRequests = useCallback(async () => {
    try {
      const res = await fetch('/api/requests');
      if (res.status === 401) {
        redirectToOIDC();
        return;
      }
      if (res.ok) {
        const data = await res.json();
        if (Array.isArray(data)) {
          const mapped: BookRequest[] = data.map((r: any) => ({
            id: String(r.id),
            title: r.title,
            author: r.author,
            requestedBy: r.username || 'Anonymous',
            requestType: (r.format === 'epub' ? 'ebook' : r.format === 'm4b' ? 'audiobook' : 'both') as RequestType,
            status: r.status as RequestStatus,
            notes: r.notes,
            rejectionReason: r.rejection_reason,
            requestedAt: r.created_at,
          }));
          setRequests(mapped);
        }
      }
    } catch (err) {
      console.error('Failed to load requests', err);
    }
  }, [redirectToOIDC]);

  const fetchActivity = useCallback(async () => {
    try {
      const res = await fetch('/api/activity');
      if (res.status === 401) {
        redirectToOIDC();
        return;
      }
      if (res.ok) {
        const data = await res.json();
        if (data.transcodeJobs) setTranscodeJobs(data.transcodeJobs);
        if (data.downloads) setDownloads(data.downloads);
        if (data.logs) setLogs(data.logs);
      }
    } catch (err) {
      console.error('Failed to load activity', err);
    }
  }, [setTranscodeJobs, setDownloads, redirectToOIDC]);

  const applySettingsResponse = useCallback((data: any) => {
    if (!data || typeof data !== 'object') return;
    if (Array.isArray(data.indexers)) setIndexers(data.indexers);
    if (Array.isArray(data.downloadClients)) setDownloadClients(data.downloadClients);
    if (data.library) {
      setLibrarySettings((prev) => ({ ...prev, ...data.library }));
    }
    if (data.prowlarr) {
      setProwlarrSettings((prev) => ({ ...prev, ...data.prowlarr }));
    }
    if (data.qualityProfile) {
      setQualityProfile((prev) => ({
        ...prev,
        ...data.qualityProfile,
        allowedLanguages: Array.isArray(data.qualityProfile.allowedLanguages)
          ? data.qualityProfile.allowedLanguages
          : prev.allowedLanguages,
        allowedFormats: Array.isArray(data.qualityProfile.allowedFormats)
          ? data.qualityProfile.allowedFormats
          : prev.allowedFormats,
      }));
    }
    if (data.authSettings) {
      setAuthSettings((prev) => ({
        ...prev,
        ...data.authSettings,
        oidcAdminGroups: Array.isArray(data.authSettings.oidcAdminGroups)
          ? data.authSettings.oidcAdminGroups
          : prev.oidcAdminGroups,
        oidcCuratorGroups: Array.isArray(data.authSettings.oidcCuratorGroups)
          ? data.authSettings.oidcCuratorGroups
          : prev.oidcCuratorGroups,
      }));
    }
    if (data.overrides && typeof data.overrides === 'object') {
      setSettingsOverrides(data.overrides);
    }
  }, []);

  const fetchSettings = useCallback(async () => {
    try {
      const res = await fetch('/api/settings');
      if (res.status === 401) {
        redirectToOIDC();
        return;
      }
      if (res.ok) {
        const data = await res.json();
        applySettingsResponse(data);
      }
    } catch (err) {
      console.error('Failed to load settings', err);
    }
  }, [applySettingsResponse, redirectToOIDC]);

  const handleSaveSettings = useCallback(async (payload: any) => {
    const res = await fetch('/api/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (res.status === 401) {
      redirectToOIDC();
      return;
    }
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data?.error || 'Failed to save settings');
    }
    applySettingsResponse(data);
  }, [applySettingsResponse, redirectToOIDC]);

  const handleSyncIndexers = useCallback(async () => {
    try {
      const res = await fetch('/api/settings/prowlarr/sync', { method: 'POST' });
      if (res.status === 401) {
        redirectToOIDC();
      }
    } catch (err) {
      console.error('Failed to sync indexers', err);
    }
  }, [redirectToOIDC]);

  useEffect(() => {
    fetchAuthUser();
  }, [fetchAuthUser]);

  useEffect(() => {
    if (!currentUser) return;
    fetchCatalog();
    fetchRequests();
    fetchActivity();
    fetchSettings();
  }, [currentUser, fetchCatalog, fetchRequests, fetchActivity, fetchSettings]);

  // Vim navigation for catalog
  const { selectedIndex } = useKeyboardNav({
    itemsCount: books.length,
    enabled: currentView === 'catalog' && !isPaletteOpen && selectedBook === null,
    onSelect: (index) => {
      if (books[index]) {
        setSelectedBook(books[index]);
      }
    },
    onEscape: () => {
      if (selectedBook) setSelectedBook(null);
      if (isPaletteOpen) setIsPaletteOpen(false);
    },
  });

  // Global key listener for '/' and 'Ctrl+K' / 'Cmd+K'
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const isInput =
        target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.tagName === 'SELECT' ||
          target.isContentEditable);

      // Open palette on Ctrl+K / Cmd+K
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setIsPaletteOpen((prev) => !prev);
        return;
      }

      // Open palette on '/' if not typing in an input
      if (e.key === '/' && !isInput) {
        e.preventDefault();
        setIsPaletteOpen(true);
        return;
      }

      // View switching shortcuts 1, 2, 3, 4
      if (!isInput && !isPaletteOpen && !selectedBook) {
        if (e.key === '1') setCurrentView('catalog');
        else if (e.key === '2') setCurrentView('activity');
        else if (e.key === '3') setCurrentView('requests');
        else if (e.key === '4') setCurrentView('settings');
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isPaletteOpen, selectedBook]);

  // Request actions wired to real API
  const handleApproveRequest = async (id: string) => {
    try {
      const res = await fetch(`/api/requests/${id}/approve`, { method: 'POST' });
      if (res.ok) {
        await fetchRequests();
      }
    } catch (err) {
      console.error('Failed to approve request', err);
    }
  };

  const handleRejectRequest = async (id: string, reason: string) => {
    try {
      const res = await fetch(`/api/requests/${id}/reject`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason }),
      });
      if (res.ok) {
        await fetchRequests();
      }
    } catch (err) {
      console.error('Failed to reject request', err);
    }
  };

  const handleSubmitRequest = async (
    title: string,
    author: string,
    type: RequestType,
    notes?: string
  ) => {
    try {
      const format = type === 'ebook' ? 'epub' : type === 'audiobook' ? 'm4b' : 'both';
      const res = await fetch('/api/requests', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, author, format, notes }),
      });
      if (res.ok) {
        await fetchRequests();
      }
    } catch (err) {
      console.error('Failed to submit request', err);
    }
  };

  const handleLogout = async () => {
    try {
      await fetch('/api/auth/logout', { method: 'POST' });
    } finally {
      window.location.href = '/api/auth/oidc/login';
    }
  };

  const handleUpdateJobStatus = (jobId: string, status: any) => {
    setTranscodeJobs((prev) =>
      prev.map((j) => (j.id === jobId ? { ...j, status } : j))
    );
  };

  const handleUpdateDownloadStatus = (dlId: string, status: any) => {
    setDownloads((prev) =>
      prev.map((d) => (d.id === dlId ? { ...d, status } : d))
    );
  };

  const activeDownloadsCount = downloads.filter((d) => d.status === 'downloading').length;
  const transcodingJobsCount = transcodeJobs.filter((j) => j.status === 'processing').length;
  const isAdminOrCurator = currentUser?.role === 'admin' || currentUser?.role === 'curator';

  return (
    <>
      <Layout
        currentView={currentView}
        onNavigate={(v) => setCurrentView(v)}
        onOpenPalette={() => setIsPaletteOpen(true)}
        user={currentUser}
        activeDownloadsCount={activeDownloadsCount}
        transcodingJobsCount={transcodingJobsCount}
        onLogout={handleLogout}
      >
        {currentView === 'catalog' && (
          <CatalogView
            books={books}
            authors={authors}
            series={series}
            onSelectBook={(b) => setSelectedBook(b)}
            selectedIndex={selectedIndex}
          />
        )}

        {currentView === 'activity' && (
          <ActivityView
            transcodeJobs={transcodeJobs}
            downloads={downloads}
            logs={logs}
            connectionStatus={connectionStatus}
            onUpdateJobStatus={handleUpdateJobStatus}
            onUpdateDownloadStatus={handleUpdateDownloadStatus}
          />
        )}

        {currentView === 'requests' && (
          <RequestsView
            requests={requests}
            onApproveRequest={handleApproveRequest}
            onRejectRequest={handleRejectRequest}
            onSubmitRequest={handleSubmitRequest}
            isAdminOrCurator={isAdminOrCurator}
          />
        )}

        {currentView === 'settings' && (
          <SettingsView
            indexers={indexers}
            downloadClients={downloadClients}
            librarySettings={librarySettings}
            prowlarrSettings={prowlarrSettings}
            qualityProfile={qualityProfile}
            authSettings={authSettings}
            overrides={settingsOverrides}
            onSyncIndexers={handleSyncIndexers}
            onSaveSettings={handleSaveSettings}
          />
        )}
      </Layout>

      {/* Global Command Palette */}
      <CommandPalette
        isOpen={isPaletteOpen}
        onClose={() => setIsPaletteOpen(false)}
        books={books}
        authors={authors}
        series={series}
        onNavigate={(v) => {
          setCurrentView(v as NavView);
          setIsPaletteOpen(false);
        }}
        onSelectBook={(b) => {
          setSelectedBook(b);
          setIsPaletteOpen(false);
        }}
      />

      {/* Book Detail Modal */}
      {selectedBook && (
        <BookDetailModal
          book={selectedBook}
          onClose={() => setSelectedBook(null)}
        />
      )}
    </>
  );
};

export default App;
