import React, { useState, useEffect, useCallback } from 'react';
import { Layout, NavView } from './components/Layout';
import { CatalogView } from './views/CatalogView';
import { ActivityView } from './views/ActivityView';
import { RequestsView } from './views/RequestsView';
import { SettingsView } from './views/SettingsView';
import { CommandPalette } from './components/CommandPalette';
import { BookDetailModal } from './components/BookDetailModal';
import { useKeyboardNav } from './hooks/useKeyboardNav';
import { useSSE } from './hooks/useSSE';
import {
  Book,
  Author,
  Series,
  BookRequest,
  ImportLog,
  IndexerConfig,
  DownloadClientConfig,
  QualityProfile,
  AuthSettings,
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
  const [qualityProfile, setQualityProfile] = useState<QualityProfile>({
    id: 'default',
    name: 'Standard Audiobook',
    allowedLanguages: ['en'],
    allowedFormats: ['EPUB', 'M4B'],
    audioMinBitrateKbps: 32,
    audioTargetBitrateKbps: 64,
    preferSingleFileM4b: true,
  });
  const [authSettings, setAuthSettings] = useState<AuthSettings>({
    oidcEnabled: false,
    oidcIssuerUrl: '',
    oidcClientId: '',
    localAuthEnabled: false,
    sessionTimeoutHours: 720,
  });

  // Real-time SSE hook for live pipeline updates
  const {
    transcodeJobs,
    setTranscodeJobs,
    downloads,
    setDownloads,
    connectionStatus,
  } = useSSE({
    initialTranscodeJobs: [],
    initialDownloads: [],
  });

  // Fetch functions for real backend data
  const fetchAuthUser = useCallback(async () => {
    try {
      const res = await fetch('/api/auth/me');
      if (res.ok) {
        const user = await res.json();
        setCurrentUser(user);
      } else {
        setCurrentUser(null);
      }
    } catch {
      setCurrentUser(null);
    }
  }, []);

  const fetchCatalog = useCallback(async () => {
    try {
      const [booksRes, authorsRes, seriesRes] = await Promise.all([
        fetch('/api/books'),
        fetch('/api/authors'),
        fetch('/api/series'),
      ]);
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
  }, []);

  const fetchRequests = useCallback(async () => {
    try {
      const res = await fetch('/api/requests');
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
  }, []);

  const fetchActivity = useCallback(async () => {
    try {
      const res = await fetch('/api/activity');
      if (res.ok) {
        const data = await res.json();
        if (data.transcodeJobs) setTranscodeJobs(data.transcodeJobs);
        if (data.downloads) setDownloads(data.downloads);
        if (data.logs) setLogs(data.logs);
      }
    } catch (err) {
      console.error('Failed to load activity', err);
    }
  }, [setTranscodeJobs, setDownloads]);

  const fetchSettings = useCallback(async () => {
    try {
      const res = await fetch('/api/settings');
      if (res.ok) {
        const data = await res.json();
        if (data.indexers) setIndexers(data.indexers);
        if (data.downloadClients) setDownloadClients(data.downloadClients);
        if (data.qualityProfile) setQualityProfile(data.qualityProfile);
        if (data.authSettings) setAuthSettings(data.authSettings);
      }
    } catch (err) {
      console.error('Failed to load settings', err);
    }
  }, []);

  useEffect(() => {
    fetchAuthUser();
    fetchCatalog();
    fetchRequests();
    fetchActivity();
    fetchSettings();
  }, [fetchAuthUser, fetchCatalog, fetchRequests, fetchActivity, fetchSettings]);

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
            qualityProfile={qualityProfile}
            authSettings={authSettings}
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
