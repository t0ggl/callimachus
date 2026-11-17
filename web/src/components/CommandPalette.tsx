import React, { useState, useEffect, useRef, useMemo } from 'react';
import { Search, Book as BookIcon, User, Layers, ArrowRight, CornerDownLeft, X } from 'lucide-react';
import { Book, Author, Series } from '../types';

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
  books: Book[];
  authors: Author[];
  series: Series[];
  onNavigate: (view: string) => void;
  onSelectBook: (book: Book) => void;
}

interface PaletteItem {
  id: string;
  type: 'navigation' | 'book' | 'author' | 'series';
  title: string;
  subtitle?: string;
  badge?: string;
  action: () => void;
}

export const CommandPalette: React.FC<CommandPaletteProps> = ({
  isOpen,
  onClose,
  books,
  authors,
  series,
  onNavigate,
  onSelectBook,
}) => {
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Focus input when opened and listen for global Escape
  useEffect(() => {
    if (isOpen) {
      setQuery('');
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);

      const handleGlobalKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          onClose();
        }
      };

      window.addEventListener('keydown', handleGlobalKeyDown);
      return () => window.removeEventListener('keydown', handleGlobalKeyDown);
    }
  }, [isOpen, onClose]);

  const items: PaletteItem[] = useMemo(() => {
    const rawItems: PaletteItem[] = [
      {
        id: 'nav_catalog',
        type: 'navigation',
        title: 'Library & Catalog',
        subtitle: 'View all books, authors, and series',
        badge: 'View',
        action: () => {
          onNavigate('catalog');
          onClose();
        },
      },
      {
        id: 'nav_activity',
        type: 'navigation',
        title: 'Activity & Queues',
        subtitle: 'Monitor transcode & download pipelines',
        badge: 'View',
        action: () => {
          onNavigate('activity');
          onClose();
        },
      },
      {
        id: 'nav_requests',
        type: 'navigation',
        title: 'Request Portal',
        subtitle: 'Find books and manage acquisitions',
        badge: 'View',
        action: () => {
          onNavigate('requests');
          onClose();
        },
      },
      {
        id: 'nav_settings',
        type: 'navigation',
        title: 'Settings',
        subtitle: 'Indexers, download clients, profiles & auth',
        badge: 'View',
        action: () => {
          onNavigate('settings');
          onClose();
        },
      },
    ];

    // Add books
    books.forEach((b) => {
      rawItems.push({
        id: `book_${b.id}`,
        type: 'book',
        title: b.title,
        subtitle: `${b.authorName}${b.seriesTitle ? ` • ${b.seriesTitle} (${b.seriesIndexDisplay || `#${b.seriesIndex}`})` : ''}`,
        badge: b.formats.join('/'),
        action: () => {
          onSelectBook(b);
          onClose();
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
          onNavigate('catalog');
          onClose();
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
          onNavigate('catalog');
          onClose();
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
        (item.subtitle && item.subtitle.toLowerCase().includes(q))
    );
  }, [books, authors, series, query, onNavigate, onSelectBook, onClose]);

  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  // Handle keyboard events inside command palette
  const handleKeyDown = (e: React.KeyboardEvent) => {
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
      onClose();
    }
  };

  if (!isOpen) return null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Command Palette"
      className="fixed inset-0 z-50 flex items-start justify-center pt-20 bg-black/70 backdrop-blur-sm p-4 animate-in fade-in duration-150"
      onClick={onClose}
    >
      <div
        className="w-full max-w-2xl bg-zinc-900 border border-zinc-800 rounded-lg shadow-2xl overflow-hidden flex flex-col"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        {/* Search Input Bar */}
        <div className="flex items-center px-4 py-3.5 border-b border-zinc-800 gap-3">
          <Search className="w-5 h-5 text-zinc-400 shrink-0" />
          <input
            ref={inputRef}
            type="text"
            data-testid="command-palette-input"
            placeholder="Search books, authors, series, or navigate (Ctrl+K, /)..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-full bg-transparent text-sm text-zinc-100 placeholder-zinc-500 focus:outline-none focus:ring-0"
          />
          {query && (
            <button
              onClick={() => setQuery('')}
              className="text-zinc-500 hover:text-zinc-300 p-1 rounded"
              title="Clear search"
            >
              <X className="w-4 h-4" />
            </button>
          )}
          <kbd className="hidden sm:inline-block px-1.5 py-0.5 text-[10px] font-mono font-medium text-zinc-400 bg-zinc-800 border border-zinc-700 rounded">
            ESC
          </kbd>
        </div>

        {/* Results List */}
        <div
          ref={listRef}
          data-testid="command-palette-list"
          className="max-h-96 overflow-y-auto divide-y divide-zinc-800/40 p-2 space-y-1"
        >
          {items.length === 0 ? (
            <div className="py-12 text-center text-sm text-zinc-500">
              No matching books, authors, or commands found for "{query}".
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
                      {item.subtitle && (
                        <div className="text-xs text-zinc-400 truncate">{item.subtitle}</div>
                      )}
                    </div>
                  </div>

                  <div className="flex items-center gap-2 shrink-0 ml-3">
                    {item.badge && (
                      <span className="text-[11px] font-mono px-2 py-0.5 rounded bg-zinc-800 border border-zinc-700/80 text-zinc-300">
                        {item.badge}
                      </span>
                    )}
                    {isSelected && (
                      <CornerDownLeft className="w-3.5 h-3.5 text-blue-400" />
                    )}
                  </div>
                </div>
              );
            })
          )}
        </div>

        {/* Footer shortcuts */}
        <div className="px-4 py-2 border-t border-zinc-800 bg-zinc-950/60 flex items-center justify-between text-xs text-zinc-500">
          <div className="flex items-center gap-3">
            <span>
              <kbd className="px-1 py-0.5 bg-zinc-800 border border-zinc-700 rounded text-[10px]">↑</kbd>
              <kbd className="ml-1 px-1 py-0.5 bg-zinc-800 border border-zinc-700 rounded text-[10px]">↓</kbd> navigate
            </span>
            <span>
              <kbd className="px-1 py-0.5 bg-zinc-800 border border-zinc-700 rounded text-[10px]">↵</kbd> select
            </span>
          </div>
          <span>Callimachus Fast Palette</span>
        </div>
      </div>
    </div>
  );
};
