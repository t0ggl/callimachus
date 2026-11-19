import { EntityLink } from './EntityLink';
import React, { useState, useEffect, useRef, useMemo } from 'react';
import { Search, Book as BookIcon, User, Layers, ArrowRight, CornerDownLeft, X } from 'lucide-react';
import { Book, Author, Series, RequestMetadata, RequestType } from '../types';

import { DiscoveryResults } from './DiscoveryResults';
import { SeriesBadge } from './SeriesBadge';
import { SeriesDetailModal } from './SeriesDetailModal';

interface CommandPaletteProps {
  defaultSearchFormat?: RequestType;
  onSubmitRequest?: (title: string, author: string, type: RequestType, year?: number, metadata?: RequestMetadata) => Promise<void>;
  onOpenBook?: (id: string) => void;
  isOpen: boolean;
  onClose: () => void;
  books: Book[];
  authors: Author[];
  series: Series[];
  canAccessSettings?: boolean;
  onNavigate: (view: string) => void;
  onSelectBook: (book: Book) => void;
}

interface PaletteItem {
  id: string;
  type: 'navigation' | 'book' | 'author' | 'series';
  title: string;
  subtitle?: string;
  badge?: string;
  book?: Book;
  action: () => void;
}

export const CommandPalette: React.FC<CommandPaletteProps> = ({
  defaultSearchFormat,
  onSubmitRequest,
  onOpenBook,
  isOpen,
  onClose,
  books,
  authors,
  series,
  canAccessSettings = false,
  onNavigate,
  onSelectBook,
}) => {
  const [selectedSeries, setSelectedSeries] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const closePalette = () => {
    previousFocus.current?.focus({ preventScroll: true });
    onClose();
  };

  // Focus input when opened
  useEffect(() => {
    if (isOpen) {
      setQuery('');
      setSelectedIndex(0);
      previousFocus.current = document.activeElement as HTMLElement | null;
      inputRef.current?.focus();
    }
  }, [isOpen]);

  useEffect(() => {
    if (isOpen) {
      const handleGlobalKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape' && !selectedSeries && !(e.target as HTMLElement | null)?.closest?.('dialog')) {
          e.preventDefault();
          closePalette();
        }
      };

      window.addEventListener('keydown', handleGlobalKeyDown);
      return () => {
        window.removeEventListener('keydown', handleGlobalKeyDown);
      };
    }
  }, [isOpen, selectedSeries]);

  const items: PaletteItem[] = useMemo(() => {
    const rawItems: PaletteItem[] = [
      {
        id: 'nav_catalog',
        type: 'navigation',
        title: 'Library',
        subtitle: 'View all books, authors, and series',
        badge: 'View',
        action: () => {
          window.location.hash = ''; onNavigate('catalog');
          closePalette();
        },
      },
      {
        id: 'nav_activity',
        type: 'navigation',
        title: 'Activity',
        subtitle: 'Monitor conversion & download pipelines',
        badge: 'View',
        action: () => {
          onNavigate('activity');
          closePalette();
        },
      },
      ...(canAccessSettings
        ? [
            {
              id: 'nav_settings',
              type: 'navigation' as const,
              title: 'Settings',
              subtitle: 'Indexers, download clients, profiles & auth',
              badge: 'View',
              action: () => {
                onNavigate('settings');
                closePalette();
              },
            },
          ]
        : []),
    ];

    // Add books
    books.forEach((b) => {
      rawItems.push({
        id: `book_${b.id}`,
        type: 'book',
        title: b.title,
        subtitle: b.authorName,
        book: b,
        badge: b.formats.join('/'),
        action: () => {
          onSelectBook(b);
          closePalette();
        },
      });
    });

    // Add authors
    authors.forEach((a) => {
      rawItems.push({
        id: `auth_${a.id}`,
        type: 'author',
        title: a.name,
        subtitle: `${a.bookCount} books in collection`,
        badge: 'Author',
        action: () => {
          window.location.hash = `#/authors/${encodeURIComponent(a.name)}`;
          closePalette();
        },
      });
    });

    // Add series
    series.forEach((s) => {
      rawItems.push({
        id: `ser_${s.id}`,
        type: 'series',
        title: s.title,
        subtitle: `${s.totalBooks || 'Multiple'} volumes • ${s.status || 'Active'}`,
        badge: 'Series',
        action: () => {
          window.location.hash = `#/series/${encodeURIComponent(s.title)}`; closePalette();
        },
      });
    });

    if (!query.trim()) {
      return rawItems;
    }

    const q = query.toLowerCase();
    return rawItems.filter(
      (item) =>
        item.title.toLowerCase().includes(q) ||
        (item.subtitle && item.subtitle.toLowerCase().includes(q)) ||
        item.book?.seriesTitle?.toLowerCase().includes(q)
    );
  }, [books, authors, series, query, canAccessSettings, onNavigate, onSelectBook, onClose]);

  useEffect(() => {
    setSelectedIndex(0);
    if (resultsRef.current) resultsRef.current.scrollTop = 0;
  }, [query]);

  // Handle keyboard events inside command palette
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.target as HTMLElement).tagName !== 'INPUT') return;
    if (e.key === 'ArrowDown' || (e.ctrlKey && e.key === 'n')) {
      e.preventDefault();
      setSelectedIndex((prev) => (items.length > 0 ? (prev + 1) % items.length : 0));
    } else if (e.key === 'ArrowUp' || (e.ctrlKey && e.key === 'p')) {
      e.preventDefault();
      setSelectedIndex((prev) => (items.length > 0 ? (prev - 1 + items.length) % items.length : 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (items[selectedIndex]) {
        items[selectedIndex].action();
      }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closePalette();
    }
  };

  if (!isOpen) return null;

  return (
    <>
    {selectedSeries && (
      <SeriesDetailModal
        title={selectedSeries}
        onClose={() => setSelectedSeries(null)}
        onOpenBook={onOpenBook ? (id) => { setSelectedSeries(null); onOpenBook(id); } : undefined}
      />
    )}
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Search books and commands"
      className="fixed inset-0 z-50 flex items-start justify-center pt-6 sm:pt-20 bg-black/75 backdrop-blur-xs p-3 sm:p-4 animate-in fade-in duration-150"
      onClick={closePalette}
    >
      <div
        className="w-full max-w-2xl max-h-[calc(100vh-3rem)] sm:max-h-[82vh] bg-zinc-900 border border-zinc-800 rounded-xl shadow-2xl overflow-hidden flex flex-col"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          handleKeyDown(e);
          if (e.key === 'Tab') {
            const controls = Array.from(
              e.currentTarget.querySelectorAll<HTMLElement>(
                'input, button:not(:disabled), select, a[href], [tabindex="0"]'
              )
            );
            const first = controls[0];
            const last = controls[controls.length - 1];
            if (e.shiftKey && document.activeElement === first) {
              e.preventDefault();
              last?.focus();
            } else if (!e.shiftKey && document.activeElement === last) {
              e.preventDefault();
              first?.focus();
            }
          }
        }}
      >
        {/* Search Input Bar */}
        <div className="flex items-center px-4 py-3 border-b border-zinc-800 bg-zinc-950/40 gap-2.5 shrink-0">
          <Search className="w-4 h-4 sm:w-5 sm:h-5 text-zinc-400 shrink-0" aria-hidden="true" />
          <input
            ref={inputRef}
            type="text"
            aria-label="Search books, authors, or keywords"
            maxLength={500}
            data-testid="command-palette-input"
            placeholder="Search books, authors, series, or navigate (Ctrl+K, /)..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-full min-w-0 bg-transparent text-sm text-zinc-100 placeholder-zinc-500 focus:outline-hidden focus:ring-0"
          />
          {query && (
            <button
              type="button"
              onClick={() => {
                setQuery('');
                inputRef.current?.focus();
              }}
              className="px-2 py-1 text-[11px] font-medium text-zinc-300 hover:text-white hover:bg-zinc-800 rounded-md transition-colors shrink-0 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
              aria-label="Clear search"
              title="Clear search"
            >
              Clear
            </button>
          )}
          <kbd className="hidden sm:inline-block px-1.5 py-0.5 text-[10px] font-mono font-medium text-zinc-400 bg-zinc-800 border border-zinc-700 rounded-sm shrink-0">
            ESC
          </kbd>
          <button
            type="button"
            onClick={closePalette}
            aria-label="Close search"
            title="Close search (Esc)"
            className="p-1.5 text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800 rounded-lg transition-colors shrink-0 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400"
          >
            <X className="w-4 h-4 sm:w-5 sm:h-5" aria-hidden="true" />
          </button>
        </div>

        <div ref={resultsRef} className="flex-1 overflow-y-auto divide-y divide-zinc-800">
          {/* Results List */}
          <div
            data-testid="command-palette-list"
            className="p-2 space-y-1"
          >
            {onSubmitRequest && query.trim() && (
              <div className="px-3 pt-1 pb-1.5 text-[11px] font-semibold uppercase tracking-wider text-zinc-400">
                Library &amp; Commands
              </div>
            )}
            {items.length === 0 ? (
              <div className="px-3 py-2 text-sm text-zinc-400">
                No library items or commands match &ldquo;{query}&rdquo;.
              </div>
            ) : (
              items.map((item, index) => {
                const isSelected = index === selectedIndex;
                return (
                  <div
                    key={item.id}
                    data-testid={`palette-item-${item.id}`}
                    onClick={() => item.action()}
                    onMouseEnter={() => setSelectedIndex(index)}
                    className={`flex items-center justify-between px-3 py-2.5 rounded-md cursor-pointer text-sm transition-colors duration-150 ${
                      isSelected ? 'bg-blue-600/20 text-white' : 'text-zinc-300 hover:bg-zinc-800/50'
                    }`}
                  >
                    <div className="flex items-center gap-3 min-w-0">
                      <span className="shrink-0 text-zinc-400">
                        {item.type === 'book' && <BookIcon className="w-4 h-4 text-blue-400" />}
                        {item.type === 'author' && <User className="w-4 h-4 text-emerald-400" />}
                        {item.type === 'series' && <Layers className="w-4 h-4 text-purple-400" />}
                        {item.type === 'navigation' && <ArrowRight className="w-4 h-4 text-zinc-400" />}
                      </span>
                      <div className="truncate">
                        <div className="font-medium text-zinc-100 truncate">{item.title}</div>
                        {item.book && (
                          <SeriesBadge
                            title={item.book.seriesTitle}
                            index={item.book.seriesIndex}
                            total={series.find((s) => s.id === item.book?.seriesId)?.totalBooks}
                            onSelect={setSelectedSeries}
                          />
                        )}
                        {item.subtitle && (
                          <div className="text-xs text-zinc-400 truncate">
                            {item.book ? (
                              <EntityLink kind="authors" name={item.book.authorName} />
                            ) : (
                              item.subtitle
                            )}
                          </div>
                        )}
                      </div>
                    </div>

                    <div className="flex items-center gap-2 shrink-0 ml-3">
                      {item.badge && (
                        <span className="text-[11px] font-mono px-2 py-0.5 rounded-sm bg-zinc-800 border border-zinc-700/80 text-zinc-300">
                          {item.badge}
                        </span>
                      )}
                      {isSelected && <CornerDownLeft className="w-3.5 h-3.5 text-blue-400" />}
                    </div>
                  </div>
                );
              })
            )}
          </div>
          {onSubmitRequest && query.trim() && (
            <div className="p-4">
              <DiscoveryResults
                query={query}
                defaultSearchFormat={defaultSearchFormat}
                onSubmitRequest={onSubmitRequest}
                onOpenBook={onOpenBook}
              />
            </div>
          )}
        </div>

        {/* Footer shortcuts */}
        <div className="px-4 py-2 border-t border-zinc-800 bg-zinc-950/60 flex flex-wrap items-center justify-between gap-2 text-xs text-zinc-400 shrink-0">
          <div className="flex items-center gap-3">
            <span className="hidden sm:inline">
              <kbd className="px-1 py-0.5 bg-zinc-800 border border-zinc-700 rounded-sm text-[10px]">
                ↑
              </kbd>
              <kbd className="ml-1 px-1 py-0.5 bg-zinc-800 border border-zinc-700 rounded-sm text-[10px]">
                ↓
              </kbd>{' '}
              navigate
            </span>
            <span className="hidden sm:inline">
              <kbd className="px-1 py-0.5 bg-zinc-800 border border-zinc-700 rounded-sm text-[10px]">
                ↵
              </kbd>{' '}
              select
            </span>
            <span className="sm:hidden text-[11px]">Tap to select or add</span>
          </div>
          <span>Callimachus Fast Palette</span>
        </div>
      </div>
    </div>
    </>
  );
};
