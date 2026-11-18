import { EntityView } from './views/EntityView';
import React, { useState, useEffect, useCallback } from 'react';
import { Layout, NavView } from './components/Layout';
import { CatalogView } from './views/CatalogView';
import { ActivityView } from './views/ActivityView';
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
  IndexerConfig,
  LibrarySettings,
  ProwlarrSettings,
  QualityProfile,
  SettingsOverridesMap,
  UserProfile,
  RequestType,
  RequestStatus,
} from './types';

export const App: React.FC = () => {
  const [entityHash, setEntityHash] = useState(window.location.hash);
  useEffect(() => { const changed = () => { setEntityHash(window.location.hash); setSelectedBook(null); setIsPaletteOpen(false); }; window.addEventListener('hashchange', changed); return () => window.removeEventListener('hashchange', changed); }, []);
  const match = /^#\/(authors|series)\/(.+)$/.exec(entityHash);
  let entityName = '';
  try { entityName = match ? decodeURIComponent(match[2]) : ''; } catch { /* Invalid routes return to the library. */ }
  const [currentView, setCurrentView] = useState<NavView>('catalog');
  const [isPaletteOpen, setIsPaletteOpen] = useState(false);
  const [selectedBook, setSelectedBook] = useState<Book | null>(null);

  // Real backend state
  const [currentUser, setCurrentUser] = useState<UserProfile | null>(null);
  const [books, setBooks] = useState<Book[]>([]);
  const [authors, setAuthors] = useState<Author[]>([]);
  const [series, setSeries] = useState<Series[]>([]);
  const [requests, setRequests] = useState<BookRequest[]>([]);
  const [indexers, setIndexers] = useState<IndexerConfig[]>([]);
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
    preferSingleFileM4b: true,
    maxAudiobookSizeMb: 1536,
    maxEbookSizeBytes: 100 * 1024 * 1024,
  });
  const [settingsOverrides, setSettingsOverrides] = useState<SettingsOverridesMap>({});

  // Real-time activity stream for live pipeline updates
  const {
    logs, setLogs,
    conversionJobs,
    setConversionJobs,
    downloads,
    setDownloads,
    connectionStatus,
  } = useActivityStream({
    initialConversionJobs: [],
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
        if (data.conversionJobs) setConversionJobs(data.conversionJobs);
        if (data.downloads) setDownloads(data.downloads);
        if (data.logs) setLogs(data.logs);
      }
    } catch (err) {
      console.error('Failed to load activity', err);
    }
  }, [setConversionJobs, setDownloads, setLogs, redirectToOIDC]);

  const applySettingsResponse = useCallback((data: any) => {
    if (!data || typeof data !== 'object') return;
    if (Array.isArray(data.indexers)) setIndexers(data.indexers);
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
        ebookFormatPreferences: Array.isArray(data.qualityProfile.ebookFormatPreferences)
          ? data.qualityProfile.ebookFormatPreferences
          : prev.ebookFormatPreferences,
        allowedFormats: Array.isArray(data.qualityProfile.allowedFormats)
          ? data.qualityProfile.allowedFormats
          : prev.allowedFormats,
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
    const res = await fetch('/api/settings/prowlarr/sync', { method: 'POST' });
    if (res.status === 401) redirectToOIDC();
    if (!res.ok) throw new Error('Prowlarr sync failed. Check the URL and API key, then try again.');
    await fetchSettings();
  }, [redirectToOIDC, fetchSettings]);

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

      // View switching shortcuts 1, 2, 3
      if (!isInput && !isPaletteOpen && !selectedBook) {
        if (['1', '2', '3'].includes(e.key)) { window.location.hash = ''; setCurrentView(e.key === '1' ? 'catalog' : e.key === '2' ? 'activity' : 'settings'); }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isPaletteOpen, selectedBook]);

  // Request actions wired to real API
  const handleSubmitRequest = async (
    title: string,
    author: string,
    type: RequestType,
    year?: number
  ) => {
    try {
      const format = type === 'ebook' ? 'epub' : type === 'audiobook' ? 'm4b' : 'both';
      const res = await fetch('/api/requests', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, author, format, year }),
      });
      if (!res.ok) throw new Error('Unable to add this book. Please try again.');
      await fetchRequests();
    } catch (err) { throw err; }
  };

  const handleLogout = async () => {
    try {
      await fetch('/api/auth/logout', { method: 'POST' });
    } finally {
      window.location.href = '/api/auth/oidc/login';
    }
  };

  const activeDownloadsCount = downloads.filter((d) => d.status === 'downloading' || d.status === 'importing').length;
  const convertingJobsCount = conversionJobs.filter((j) => j.status === 'processing').length;

  return (
    <>
      <Layout
        currentView={currentView}
        onNavigate={(v) => { window.location.hash = ''; setCurrentView(v); }}
        onOpenPalette={() => setIsPaletteOpen(true)}
        user={currentUser}
        activeDownloadsCount={activeDownloadsCount}
        convertingJobsCount={convertingJobsCount}
        onLogout={handleLogout}
      >
        {match && entityName ? <EntityView key={entityHash} kind={match[1] as 'authors' | 'series'} name={entityName} canManage={currentUser?.role === 'admin' || currentUser?.role === 'curator'} onOpenBook={(id) => { void fetch('/api/books').then(async res => { if (res.ok) { const loaded: Book[] = await res.json(); setBooks(loaded); setSelectedBook(loaded.find(b => b.id === id) || null); } }).catch(console.error); }} /> : currentView === 'catalog' && (
          <CatalogView
            books={books}
            authors={authors}
            series={series}
            onSelectBook={(b) => setSelectedBook(b)}
            selectedIndex={selectedIndex}
            onScanComplete={fetchCatalog}
          />
        )}

        {!match && currentView === 'activity' && (
          <div className="space-y-6">
          <ActivityView
            requests={requests}
            conversionJobs={conversionJobs}
            downloads={downloads}
            logs={logs}
            connectionStatus={connectionStatus}
          />
          </div>
        )}

        {!match && currentView === 'settings' && (
          <SettingsView
            indexers={indexers}
            librarySettings={librarySettings}
            prowlarrSettings={prowlarrSettings}
            qualityProfile={qualityProfile}
            overrides={settingsOverrides}
            onSyncIndexers={handleSyncIndexers}
            onSaveSettings={handleSaveSettings}
          />
        )}
      </Layout>

      {/* Global Command Palette */}
      <CommandPalette
        onSubmitRequest={handleSubmitRequest}
        onOpenBook={(id) => { setSelectedBook(books.find((b) => b.id === id) ?? null); setIsPaletteOpen(false); }}
        isOpen={isPaletteOpen}
        onClose={() => setIsPaletteOpen(false)}
        books={books}
        authors={authors}
        series={series}
        onNavigate={(v) => {
          window.location.hash = '';
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
