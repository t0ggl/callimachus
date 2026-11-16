import React, { useState, useEffect } from 'react';
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
  mockCurrentUser,
  mockAuthors,
  mockSeries,
  mockBooks,
  mockTranscodeJobs,
  mockDownloads,
  mockImportLogs,
  mockRequests,
  mockIndexers,
  mockDownloadClients,
  mockQualityProfile,
  mockAuthSettings,
} from './data/mockData';
import { Book, BookRequest, RequestType } from './types';

export const App: React.FC = () => {
  const [currentView, setCurrentView] = useState<NavView>('catalog');
  const [isPaletteOpen, setIsPaletteOpen] = useState(false);
  const [selectedBook, setSelectedBook] = useState<Book | null>(null);

  // Dynamic state
  const [books] = useState<Book[]>(mockBooks);
  const [authors] = useState(mockAuthors);
  const [series] = useState(mockSeries);
  const [requests, setRequests] = useState<BookRequest[]>(mockRequests);
  const [logs] = useState(mockImportLogs);

  // Real-time SSE hook for live pipeline updates
  const {
    transcodeJobs,
    setTranscodeJobs,
    downloads,
    setDownloads,
    connectionStatus,
  } = useSSE({
    initialTranscodeJobs: mockTranscodeJobs,
    initialDownloads: mockDownloads,
    enableMockFallback: true,
  });

  // Vim navigation for catalog
  const { selectedIndex, setSelectedIndex } = useKeyboardNav({
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

  // Request actions
  const handleApproveRequest = (id: string) => {
    setRequests((prev) =>
      prev.map((r) =>
        r.id === id
          ? {
              ...r,
              status: 'approved',
              reviewedAt: new Date().toISOString(),
            }
          : r
      )
    );
  };

  const handleRejectRequest = (id: string, reason: string) => {
    setRequests((prev) =>
      prev.map((r) =>
        r.id === id
          ? {
              ...r,
              status: 'rejected',
              rejectionReason: reason,
              reviewedAt: new Date().toISOString(),
            }
          : r
      )
    );
  };

  const handleSubmitRequest = (
    title: string,
    author: string,
    type: RequestType,
    notes?: string
  ) => {
    const newReq: BookRequest = {
      id: `req_${Date.now()}`,
      title,
      author,
      requestedBy: mockCurrentUser.username,
      requestType: type,
      status: 'pending',
      requestedAt: new Date().toISOString(),
      notes,
    };
    setRequests((prev) => [newReq, ...prev]);
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

  return (
    <>
      <Layout
        currentView={currentView}
        onNavigate={(v) => setCurrentView(v)}
        onOpenPalette={() => setIsPaletteOpen(true)}
        user={mockCurrentUser}
        activeDownloadsCount={activeDownloadsCount}
        transcodingJobsCount={transcodingJobsCount}
        onLogout={() => {
          alert('Signed out from Callimachus');
        }}
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
            isAdminOrCurator={mockCurrentUser.role === 'admin' || mockCurrentUser.role === 'curator'}
          />
        )}

        {currentView === 'settings' && (
          <SettingsView
            indexers={mockIndexers}
            downloadClients={mockDownloadClients}
            qualityProfile={mockQualityProfile}
            authSettings={mockAuthSettings}
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
        onNavigate={(view) => setCurrentView(view as NavView)}
        onSelectBook={(book) => {
          setSelectedBook(book);
          setSelectedIndex(books.findIndex((b) => b.id === book.id));
        }}
      />

      {/* Book Detail Modal */}
      <BookDetailModal
        book={selectedBook}
        onClose={() => setSelectedBook(null)}
      />
    </>
  );
};

export default App;
