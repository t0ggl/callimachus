import { EntityView } from './views/EntityView';
import React, { useState, useEffect, useCallback } from 'react';
import { Layout, NavView } from './components/Layout';
import { CatalogView } from './views/CatalogView';
import { ActivityView } from './views/ActivityView';
import { EpubMetadataMismatchesView } from './views/EpubMetadataMismatchesView';
import { SettingsView } from './views/SettingsView';
import { CommandPalette } from './components/CommandPalette';
import { BookDetailModal } from './components/BookDetailModal';
import { useActivityStream } from './hooks/useActivityStream';
import {
  Book,
  Author,
  Series,
  SeriesSelection,
  mapBookRequest,
  RequestMetadata,
  IndexerConfig,
  LibrarySettings,
  LogLevel,
  MetadataSettings,
  ProwlarrSettings,
  QualityProfile,
  SettingsOverridesMap,
  UserProfile,
  RequestType,
} from './types';

interface AppProps {
  initialView?: NavView;
}

export const App: React.FC<AppProps> = ({ initialView = 'catalog' }) => {
  const [entityHash, setEntityHash] = useState(window.location.hash);
  const [currentView, setCurrentView] = useState<NavView>(() => {
    if (window.location.hash === '#/settings' || window.location.pathname === '/settings') {
      return 'settings';
    }
    if (
      window.location.hash === '#/activity/metadata-mismatches' ||
      window.location.pathname === '/activity/metadata-mismatches'
    ) {
      return 'activity-mismatches';
    }
    return initialView;
  });
  useEffect(() => {
    const changed = () => {
      setEntityHash(window.location.hash);
      if (window.location.hash === '#/settings') {
        setCurrentView('settings');
      } else if (window.location.hash === '#/activity/metadata-mismatches') {
        setCurrentView('activity-mismatches');
      }
      setSelectedBook(null);
      setSelectedSeries(null);
      setIsPaletteOpen(false);
    };
    window.addEventListener('hashchange', changed);
    return () => window.removeEventListener('hashchange', changed);
  }, []);
  const [entityPath, entityQuery] = entityHash.split('?', 2);
  const match = /^#\/(authors|series)\/(.+)$/.exec(entityPath);
  const entityParams = new URLSearchParams(entityQuery);
  let entityName = '';
  try { entityName = match ? decodeURIComponent(match[2]) : ''; } catch { /* Invalid routes return to the library. */ }
  const [isPaletteOpen, setIsPaletteOpen] = useState(false);
  const [selectedBook, setSelectedBook] = useState<Book | null>(null);
  const [selectedSeries, setSelectedSeries] = useState<SeriesSelection | null>(null);

  // Real backend state
  const [currentUser, setCurrentUser] = useState<UserProfile | null>(null);
  const isAdmin = currentUser?.role === 'admin';
  const canManage = currentUser?.role === 'admin' || currentUser?.role === 'curator';
  const canRequest = canManage || currentUser?.role === 'requester';
  const [books, setBooks] = useState<Book[]>([]);
  const [authors, setAuthors] = useState<Author[]>([]);
  const [series, setSeries] = useState<Series[]>([]);
  const [indexers, setIndexers] = useState<IndexerConfig[]>([]);
  const [librarySettings, setLibrarySettings] = useState<LibrarySettings>({
    rootPath: './books',
    stagingPollInterval: '5s',
    indexerSyncInterval: '15m0s',
    bundleSeriesInLibrary: true,
  });
  const [prowlarrSettings, setProwlarrSettings] = useState<ProwlarrSettings>({
    url: '',
    apiKey: '',
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
  const [logLevel, setLogLevel] = useState<LogLevel>('info');
  const [defaultSearchFormat, setDefaultSearchFormat] = useState<RequestType>('ebook');
  const [metadataSettings, setMetadataSettings] = useState<MetadataSettings>({
    google_books_api_key: '',
    hardcover_api_key: '',
    openlibrary_enabled: true,
  });
  const [settingsOverrides, setSettingsOverrides] = useState<SettingsOverridesMap>({});

  // Real-time activity stream for live pipeline updates
  const {
    logs, setLogs, requests, setRequests,
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
        const user: UserProfile = await res.json();
        setCurrentUser(user);
        setDefaultSearchFormat(user?.preferences?.defaultSearchFormat ?? 'ebook');
        if (typeof user?.preferences?.bundleSeriesInLibrary === 'boolean') {
          const bundlePref = user.preferences.bundleSeriesInLibrary;
          setLibrarySettings((prev) => ({
            ...prev,
            bundleSeriesInLibrary: bundlePref,
          }));
        }
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
        const nextBooks: Book[] = Array.isArray(data) ? data : [];
        setBooks(nextBooks);
        setSelectedBook((current) => nextBooks.find((book) => book.id === current?.id) ?? current);
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
          setRequests(data.map(mapBookRequest));
        }
      }
    } catch (err) {
      console.error('Failed to load requests', err);
    }
  }, [redirectToOIDC, setRequests]);

  const [activityLoading, setActivityLoading] = useState(false);
  const [activityError, setActivityError] = useState('');
  const [metadataMismatchesCount, setMetadataMismatchesCount] = useState(0);

  const fetchActivity = useCallback(async () => {
    setActivityLoading(true);
    setActivityError('');
    try {
      const res = await fetch('/api/activity');
      if (res.status === 401) {
        redirectToOIDC();
        return;
      }
      if (!res.ok) {
        throw new Error('Unable to load activity. Try again.');
      }
      const data = await res.json();
      if (data.conversionJobs) setConversionJobs(data.conversionJobs);
      if (data.downloads) setDownloads(data.downloads);
      if (data.logs) setLogs(data.logs);
      if (Array.isArray(data.requests)) setRequests(data.requests.map(mapBookRequest));
    } catch (err) {
      console.error('Failed to load activity', err);
      setActivityError('Unable to load activity. Try again.');
    } finally {
      setActivityLoading(false);
    }
  }, [setConversionJobs, setDownloads, setLogs, setRequests, redirectToOIDC]);

  const applySettingsResponse = useCallback((data: any) => {
    if (!data || typeof data !== 'object') return;
    if (data.logLevel === 'debug' || data.logLevel === 'info' || data.logLevel === 'warn' || data.logLevel === 'error') {
      setLogLevel(data.logLevel);
    }
    if (data.defaultSearchFormat === 'ebook' || data.defaultSearchFormat === 'audiobook' || data.defaultSearchFormat === 'both') {
      setDefaultSearchFormat(data.defaultSearchFormat);
    }
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
    if (data.metadata && typeof data.metadata === 'object') {
      setMetadataSettings((prev) => ({ ...prev, ...data.metadata }));
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
      if (res.status === 403) {
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

  const navigateTo = useCallback((v: NavView) => {
    window.location.hash = '';
    if ((v === 'settings' || v === 'activity-mismatches') && !isAdmin) {
      setCurrentView(v === 'activity-mismatches' ? 'activity' : 'catalog');
      return;
    }
    setCurrentView(v);
    if (v === 'catalog') void fetchCatalog();
  }, [isAdmin, fetchCatalog]);

  useEffect(() => {
    fetchAuthUser();
  }, [fetchAuthUser]);

  useEffect(() => {
    if (!currentUser) return;
    fetchCatalog();
    fetchRequests();
    fetchActivity();
    if (currentUser.role === 'admin') {
      fetchSettings();
    }
  }, [currentUser, fetchCatalog, fetchRequests, fetchActivity, fetchSettings]);

  const bundleSeriesInLibrary = librarySettings.bundleSeriesInLibrary ?? true;

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
      if (!isInput && !isPaletteOpen && !selectedBook && !selectedSeries && !document.querySelector('dialog[open]')) {
        if (e.key === '1') {
          window.location.hash = '';
          setCurrentView('catalog');
        } else if (e.key === '2') {
          window.location.hash = '';
          setCurrentView('activity');
        } else if (e.key === '3' && isAdmin) {
          window.location.hash = '';
          setCurrentView('settings');
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isPaletteOpen, selectedBook, selectedSeries, isAdmin]);

  // Request actions wired to real API
  const handleSubmitRequest = async (
    title: string,
    author: string,
    type: RequestType,
    year?: number,
    metadata?: RequestMetadata
  ) => {
    try {
      const format = type === 'ebook' ? 'epub' : type === 'audiobook' ? 'm4b' : 'both';
      const res = await fetch('/api/requests', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, author, format, year, ...metadata }),
      });
      if (!res.ok) {
        if (res.status === 409) {
          await fetchRequests();
          throw new Error('This book has already been added for the selected format.');
        }
        throw new Error('Unable to add this book. Please try again.');
      }
      await Promise.all([fetchRequests(), fetchCatalog()]);
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
        onNavigate={navigateTo}
        onOpenPalette={() => setIsPaletteOpen(true)}
        user={currentUser}
        activeDownloadsCount={activeDownloadsCount}
        convertingJobsCount={convertingJobsCount}
        metadataMismatchesCount={metadataMismatchesCount}
        onLogout={handleLogout}
      >
        {match && entityName ? (
          <EntityView
            key={entityHash}
            kind={match[1] as 'authors' | 'series'}
            name={entityName}
            seriesID={entityParams.get('id') ?? undefined}
            seriesAuthor={entityParams.get('author') ?? undefined}
            hardcoverID={entityParams.get('hardcover_id') ?? undefined}
            canManage={canManage}
            canRequest={canRequest}
            isAdmin={isAdmin}
            onMetadataApplied={fetchCatalog}
          />
        ) : currentView === 'catalog' && (
          <CatalogView
            books={books}
            authors={authors}
            series={series}
            bundleSeriesInLibrary={bundleSeriesInLibrary}
            canManage={canManage}
            canRequest={canRequest}
            isAdmin={isAdmin}
            canRefresh={canManage}
            onSelectBook={(b) => setSelectedBook(b)}
            keyboardNavigationEnabled={!isPaletteOpen && selectedBook === null}
            selectedSeries={selectedSeries}
            onSelectSeries={setSelectedSeries}
            onScanComplete={fetchCatalog}
          />
        )}

        {!match && currentView === 'activity' && (
          <ActivityView
            requests={requests}
            canManageJobs={canManage}
            canRequest={canRequest}
            isAdmin={isAdmin}
            onMetadataApplied={fetchCatalog}
            conversionJobs={conversionJobs}
            downloads={downloads}
            logs={logs}
            connectionStatus={connectionStatus}
            loading={activityLoading}
            error={activityError}
            onRetry={() => void fetchActivity()}
          />
        )}

        {!match && currentView === 'activity-mismatches' && (
          isAdmin ? (
            <EpubMetadataMismatchesView
              canManage={canManage}
              canRequest={canRequest}
              onCatalogChanged={fetchCatalog}
              onCountChanged={setMetadataMismatchesCount}
            />
          ) : currentUser ? (
            <div data-testid="mismatches-access-denied" role="alert" className="py-16 text-center space-y-3">
              <h1 className="text-xl font-bold text-white">Access Denied</h1>
              <p className="text-sm text-zinc-400">
                Administrator privileges are required to review EPUB metadata mismatches.
              </p>
              <button
                type="button"
                onClick={() => navigateTo('activity')}
                className="px-4 py-2 rounded-md bg-zinc-800 hover:bg-zinc-700 text-xs font-medium text-zinc-100"
              >
                Return to Activity
              </button>
            </div>
          ) : (
            <div data-testid="mismatches-verifying" role="status" className="py-16 text-center text-sm text-zinc-400">
              Verifying permissions…
            </div>
          )
        )}

        {!match && currentView === 'settings' && (
          isAdmin ? (
            <SettingsView
              indexers={indexers}
              metadata={metadataSettings}
              librarySettings={librarySettings}
              prowlarrSettings={prowlarrSettings}
              qualityProfile={qualityProfile}
              logLevel={logLevel}
              defaultSearchFormat={defaultSearchFormat}
              overrides={settingsOverrides}
              onSyncIndexers={handleSyncIndexers}
              onSaveSettings={handleSaveSettings}
              onCatalogChanged={fetchCatalog}
            />
          ) : currentUser ? (
            <div data-testid="settings-access-denied" role="alert" className="py-16 text-center space-y-3">
              <h1 className="text-xl font-bold text-white">Access Denied</h1>
              <p className="text-sm text-zinc-400">
                Administrator privileges are required to view or modify system settings.
              </p>
              <button
                type="button"
                onClick={() => navigateTo('catalog')}
                className="px-4 py-2 rounded-md bg-zinc-800 hover:bg-zinc-700 text-xs font-medium text-zinc-100"
              >
                Return to Library
              </button>
            </div>
          ) : (
            <div data-testid="settings-verifying" role="status" className="py-16 text-center text-sm text-zinc-400">
              Verifying permissions…
            </div>
          )
        )}
      </Layout>

      {/* Global Command Palette */}
      <CommandPalette
        defaultSearchFormat={defaultSearchFormat}
        onSubmitRequest={handleSubmitRequest}
        onOpenBook={(id) => {
          setIsPaletteOpen(false);
          const local = books.find((b) => b.id === id);
          if (local) {
            setSelectedBook(local);
            return;
          }
          void fetch(`/api/books?id=${encodeURIComponent(id)}`)
            .then((res) => (res.ok ? res.json() : []))
            .then((list: Book[]) => {
              const fetched = Array.isArray(list) ? list.find((b) => b.id === id) : undefined;
              if (fetched) setSelectedBook(fetched);
            })
            .catch(() => {});
        }}
        isOpen={isPaletteOpen}
        onClose={() => setIsPaletteOpen(false)}
        books={books}
        authors={authors}
        series={series}
        canAccessSettings={isAdmin}
        canManage={canManage}
        canRequest={canRequest}
        isAdmin={isAdmin}
        onNavigate={(v) => {
          navigateTo(v as NavView);
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
          isAdmin={isAdmin}
          canManage={canManage}
          canRequest={canRequest}
          onBookUpdated={(updated) => {
            setSelectedBook(updated);
            setBooks((prev) => prev.map((b) => (b.id === updated.id ? updated : b)));
          }}
          onBookDeleted={(deletedId) => {
            setSelectedBook(null);
            setBooks((prev) => prev.filter((b) => b.id !== deletedId));
            void fetchCatalog();
          }}
          onMetadataApplied={fetchCatalog}
          onClose={() => setSelectedBook(null)}
        />
      )}
    </>
  );
};

export default App;
