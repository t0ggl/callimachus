import React, { useState, useMemo } from 'react';
import {
  Grid,
  List,
  Search,
  BookOpen,
  User,
  Layers,
  Filter,
} from 'lucide-react';
import { Book, Author, Series, FormatType, FORMAT_TYPES } from '../types';

import { SeriesBadge } from '../components/SeriesBadge';
import { SeriesDetailModal } from '../components/SeriesDetailModal';

interface CatalogViewProps {
  books: Book[];
  authors: Author[];
  series: Series[];
  onSelectBook: (book: Book) => void;
  selectedIndex: number;
  onScanComplete?: () => Promise<void>;
}

export const CatalogView: React.FC<CatalogViewProps> = ({
  books,
  authors,
  series,
  onSelectBook,
  selectedIndex,
  onScanComplete,
}) => {
  const [selectedSeries, setSelectedSeries] = useState<string | null>(null);
  const [scanning, setScanning] = useState(false);
  const [scanMessage, setScanMessage] = useState('');
  const scan = async () => {
    setScanning(true); setScanMessage('');
    try {
      const res = await fetch('/api/library/scan', { method: 'POST' });
      if (!res.ok) throw new Error('Library scan failed. Check the library path and permissions, then retry.');
      const report = await res.json();
      await onScanComplete?.();
      setScanMessage(`Scan complete: ${report.books} books found.`);
    } catch (e) { setScanMessage((e as Error).message); }
    finally { setScanning(false); }
  };
  const [viewMode, setViewMode] = useState<'grid' | 'list'>('grid');
  const [activeTab, setActiveTab] = useState<'books' | 'authors' | 'series'>('books');
  const [searchQuery, setSearchQuery] = useState('');
  const [formatFilter, setFormatFilter] = useState<'ALL' | FormatType>('ALL');

  // Filtered books
  const filteredBooks = useMemo(() => {
    return books.filter((b) => {
      const matchesSearch =
        b.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
        b.authorName.toLowerCase().includes(searchQuery.toLowerCase()) ||
        (b.seriesTitle && b.seriesTitle.toLowerCase().includes(searchQuery.toLowerCase()));

      const matchesFormat =
        formatFilter === 'ALL' || b.formats.includes(formatFilter);

      return matchesSearch && matchesFormat;
    });
  }, [books, searchQuery, formatFilter]);

  // Filtered authors
  const filteredAuthors = useMemo(() => {
    return authors.filter(
      (a) =>
        a.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
        a.sortName.toLowerCase().includes(searchQuery.toLowerCase())
    );
  }, [authors, searchQuery]);

  // Filtered series
  const filteredSeries = useMemo(() => {
    return series.filter((s) =>
      s.title.toLowerCase().includes(searchQuery.toLowerCase())
    );
  }, [series, searchQuery]);

  return (
    <div className="space-y-6">
      {selectedSeries && <SeriesDetailModal title={selectedSeries} onClose={() => setSelectedSeries(null)} />}
      <div className="flex flex-wrap items-center gap-3">
        <button disabled={scanning} onClick={() => void scan()} className="px-3 py-2 bg-zinc-800 text-zinc-100 rounded-md disabled:opacity-50">{scanning ? 'Scanning…' : 'Scan Library'}</button>
        {scanMessage && <p role="status" className="text-sm text-zinc-300">{scanMessage}</p>}
      </div>
      {/* Top Header & Actions Bar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-zinc-800 pb-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-white">Library & Catalog</h1>
          <p className="text-xs text-zinc-400 mt-0.5">
            Organized book works, editions, series sequences, and media assets
          </p>
        </div>

        {/* View Switcher & Actions */}
        <div className="flex items-center gap-3">
          {/* Tabs */}
          <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5 text-xs font-medium">
            <button
              onClick={() => setActiveTab('books')}
              data-testid="tab-books"
              className={`px-3 py-1.5 rounded-md transition-colors ${
                activeTab === 'books'
                  ? 'bg-zinc-800 text-white shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Books ({books.length})
            </button>
            <button
              onClick={() => setActiveTab('authors')}
              data-testid="tab-authors"
              className={`px-3 py-1.5 rounded-md transition-colors ${
                activeTab === 'authors'
                  ? 'bg-zinc-800 text-white shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Authors ({authors.length})
            </button>
            <button
              onClick={() => setActiveTab('series')}
              data-testid="tab-series"
              className={`px-3 py-1.5 rounded-md transition-colors ${
                activeTab === 'series'
                  ? 'bg-zinc-800 text-white shadow-xs'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Series ({series.length})
            </button>
          </div>

          {/* Grid vs List toggle (active on books tab) */}
          {activeTab === 'books' && (
            <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5">
              <button
                onClick={() => setViewMode('grid')}
                data-testid="view-grid-btn"
                className={`p-1.5 rounded-md transition-colors ${
                  viewMode === 'grid'
                    ? 'bg-zinc-800 text-white'
                    : 'text-zinc-400 hover:text-zinc-200'
                }`}
                title="Grid View"
              >
                <Grid className="w-4 h-4" />
              </button>
              <button
                onClick={() => setViewMode('list')}
                data-testid="view-list-btn"
                className={`p-1.5 rounded-md transition-colors ${
                  viewMode === 'list'
                    ? 'bg-zinc-800 text-white'
                    : 'text-zinc-400 hover:text-zinc-200'
                }`}
                title="List View"
              >
                <List className="w-4 h-4" />
              </button>
            </div>
          )}
        </div>
      </div>

      {/* Filter Toolbar */}
      <div className="flex flex-col sm:flex-row items-center justify-between gap-3">
        <div className="relative w-full sm:w-80">
          <Search className="w-4 h-4 text-zinc-500 absolute left-3 top-2.5" />
          <input
            type="text"
            placeholder={`Filter ${activeTab}...`}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full pl-9 pr-3 py-1.5 bg-zinc-900 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-hidden focus:border-zinc-700"
          />
        </div>

        {activeTab === 'books' && (
          <div className="flex items-center gap-2 flex-wrap self-start sm:self-auto text-xs">
            <span className="text-zinc-500 flex items-center gap-1">
              <Filter className="w-3.5 h-3.5" /> Format:
            </span>
            <div className="flex flex-wrap bg-zinc-900 border border-zinc-800 rounded-md p-0.5 text-xs font-mono">
              {(['ALL', ...FORMAT_TYPES] as const).map((fmt) => (
                <button
                  key={fmt}
                  onClick={() => setFormatFilter(fmt)}
                  data-testid={`filter-format-${fmt.toLowerCase()}`}
                  className={`px-2.5 py-1 rounded-sm transition-colors ${
                    formatFilter === fmt
                      ? 'bg-zinc-800 text-white font-semibold'
                      : 'text-zinc-400 hover:text-zinc-200'
                  }`}
                >
                  {fmt}
                </button>
              ))}
            </div>
            <span className="text-[11px] text-zinc-500 ml-2">
              (Use <kbd className="px-1 py-0.5 bg-zinc-900 border border-zinc-800 rounded-sm">j</kbd>/<kbd className="px-1 py-0.5 bg-zinc-900 border border-zinc-800 rounded-sm">k</kbd> + <kbd className="px-1 py-0.5 bg-zinc-900 border border-zinc-800 rounded-sm">↵</kbd>)
            </span>
          </div>
        )}
      </div>

      {/* TAB CONTENT: BOOKS */}
      {activeTab === 'books' && (
        <>
          {filteredBooks.length === 0 ? (
            <div className="py-20 text-center border border-dashed border-zinc-800 rounded-xl">
              <BookOpen className="w-8 h-8 text-zinc-600 mx-auto mb-2" />
              <div className="text-sm font-medium text-zinc-400">No books found</div>
              <div className="text-xs text-zinc-500 mt-1">
                Try refining your search or format filters.
              </div>
            </div>
          ) : viewMode === 'grid' ? (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
              {filteredBooks.map((book, index) => {
                const isSelected = index === selectedIndex;
                return (
                  <div
                    key={book.id}
                    data-testid={`book-card-${book.id}`}
                    onClick={() => onSelectBook(book)}
                    className={`group relative bg-zinc-900/90 rounded-lg border overflow-hidden cursor-pointer transition-all duration-150 flex flex-col ${
                      isSelected
                        ? 'border-blue-500 ring-1 ring-blue-500 shadow-lg shadow-blue-500/10'
                        : 'border-zinc-800/80 hover:border-zinc-700 hover:bg-zinc-900'
                    }`}
                  >
                    {/* Cover Image */}
                    <div className="aspect-[2/3] w-full bg-zinc-950 relative overflow-hidden">
                      {book.coverUrl ? (
                        <img
                          src={book.coverUrl}
                          alt={book.title}
                          className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-200"
                        />
                      ) : (
                        <div className="w-full h-full flex flex-col items-center justify-center text-zinc-600">
                          <BookOpen className="w-8 h-8 mb-1" />
                        </div>
                      )}

                      {/* Format Badges Overlay */}
                      <div className="absolute top-2 right-2 flex flex-col gap-1 items-end">
                        {book.formats.map((fmt) => (
                          <span
                            key={fmt}
                            className={`text-[9px] font-mono px-1.5 py-0.5 rounded-sm shadow-sm font-semibold ${
                              fmt === 'M4B'
                                ? 'bg-purple-900/90 text-purple-200 border border-purple-700/60'
                                : 'bg-blue-900/90 text-blue-200 border border-blue-700/60'
                            }`}
                          >
                            {fmt}
                          </span>
                        ))}
                      </div>

                      {/* Series Index Badge */}
                      {book.seriesIndexDisplay && (
                        <div className="absolute bottom-2 left-2 bg-zinc-950/80 backdrop-blur-xs border border-zinc-800 px-1.5 py-0.5 rounded-sm text-[10px] font-mono text-zinc-300 font-medium">
                          {book.seriesIndexDisplay}
                        </div>
                      )}
                    </div>

                    {/* Meta info */}
                    <div className="p-3 flex-1 flex flex-col justify-between">
                      <div>
                        <h3 className="text-xs font-semibold text-zinc-100 group-hover:text-blue-400 transition-colors line-clamp-1">
                          {book.title}
                        </h3>
                        <p className="text-[11px] text-zinc-400 mt-0.5 truncate">
                          {book.authorName}
                        </p>
                      </div>

                      <SeriesBadge title={book.seriesTitle} index={book.seriesIndex} total={series.find(s => s.id === book.seriesId)?.totalBooks} onSelect={setSelectedSeries} />
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            /* Table / List View */
            <div className="bg-zinc-900/80 border border-zinc-800 rounded-lg overflow-hidden">
              <table className="w-full text-left border-collapse text-xs">
                <thead>
                  <tr className="border-b border-zinc-800 bg-zinc-950/50 text-zinc-400 font-medium">
                    <th className="py-2.5 px-4">Title</th>
                    <th className="py-2.5 px-4">Author</th>
                    <th className="py-2.5 px-4">Series & Order</th>
                    <th className="py-2.5 px-4">Formats</th>
                    <th className="py-2.5 px-4 text-right">Editions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800/60">
                  {filteredBooks.map((book, index) => {
                    const isSelected = index === selectedIndex;
                    return (
                      <tr
                        key={book.id}
                        data-testid={`book-row-${book.id}`}
                        onClick={() => onSelectBook(book)}
                        className={`cursor-pointer transition-colors duration-150 ${
                          isSelected
                            ? 'bg-blue-600/15 text-white'
                            : 'hover:bg-zinc-800/50 text-zinc-300'
                        }`}
                      >
                        <td className="py-3 px-4 font-medium text-zinc-100 flex items-center gap-2">
                          <BookOpen className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
                          <span className="truncate">{book.title}</span>
                        </td>
                        <td className="py-3 px-4 text-zinc-300">{book.authorName}</td>
                        <td className="py-3 px-4">
                          {book.seriesTitle ? (
                            <SeriesBadge title={book.seriesTitle} index={book.seriesIndex} total={series.find(s => s.id === book.seriesId)?.totalBooks} onSelect={setSelectedSeries} />
                          ) : (
                            <span className="text-zinc-600">—</span>
                          )}
                        </td>
                        <td className="py-3 px-4">
                          <div className="flex gap-1">
                            {book.formats.map((fmt) => (
                              <span
                                key={fmt}
                                className={`text-[10px] font-mono px-1.5 py-0.5 rounded-sm ${
                                  fmt === 'M4B'
                                    ? 'bg-purple-950 text-purple-300 border border-purple-800/60'
                                    : 'bg-blue-950 text-blue-300 border border-blue-800/60'
                                }`}
                              >
                                {fmt}
                              </span>
                            ))}
                          </div>
                        </td>
                        <td className="py-3 px-4 text-right font-mono text-zinc-400">
                          {book.editions.length}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}

      {/* TAB CONTENT: AUTHORS */}
      {activeTab === 'authors' && (
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-4">
          {filteredAuthors.map((author) => (
            <div
              key={author.id}
              className="bg-zinc-900 border border-zinc-800 rounded-lg p-4 space-y-3 hover:border-zinc-700 transition-colors"
            >
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-full bg-zinc-800 border border-zinc-700 flex items-center justify-center text-emerald-400 font-semibold text-sm">
                  <User className="w-5 h-5" />
                </div>
                <div>
                  <h3 className="text-sm font-semibold text-zinc-100">{author.name}</h3>
                  <p className="text-[11px] font-mono text-zinc-400">{author.sortName}</p>
                </div>
              </div>

              {author.biography && (
                <p className="text-xs text-zinc-400 line-clamp-2 leading-relaxed">
                  {author.biography}
                </p>
              )}

              <div className="pt-2 border-t border-zinc-800 flex items-center justify-between text-xs text-zinc-400">
                <span>Books in library:</span>
                <span className="font-mono font-semibold text-zinc-200">{author.bookCount}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* TAB CONTENT: SERIES */}
      {activeTab === 'series' && (
        <div className="space-y-4">
          {filteredSeries.map((s) => {
            const seriesBooks = books
              .filter((b) => b.seriesId === s.id)
              .sort((a, b) => (a.seriesIndex || 0) - (b.seriesIndex || 0));

            return (
              <div
                key={s.id}
                className="bg-zinc-900 border border-zinc-800 rounded-lg p-4 space-y-4"
              >
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <Layers className="w-4 h-4 text-purple-400" />
                    <h3 className="text-sm font-bold text-zinc-100"><button onClick={() => setSelectedSeries(s.title)} className="text-purple-300 underline underline-offset-2">{s.title}</button></h3>
                    <span className="text-[10px] font-mono px-2 py-0.5 rounded-sm bg-zinc-800 text-zinc-400 border border-zinc-700">
                      {s.status}
                    </span>
                  </div>
                  <span className="text-xs text-zinc-400">
                    {seriesBooks.length} / {s.totalBooks || '—'} books collected
                  </span>
                </div>

                {s.description && (
                  <p className="text-xs text-zinc-400">{s.description}</p>
                )}

                {/* Series Reading Order Sequence */}
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3 pt-2 border-t border-zinc-800/80">
                  {seriesBooks.map((book) => (
                    <div
                      key={book.id}
                      onClick={() => onSelectBook(book)}
                      className="cursor-pointer bg-zinc-950 p-2.5 rounded-sm border border-zinc-800 hover:border-purple-600/60 transition-colors flex items-center justify-between gap-2"
                    >
                      <div className="truncate">
                        <div className="text-xs font-medium text-zinc-200 truncate">
                          {book.title}
                        </div>
                        <div className="text-[11px] text-zinc-500 truncate">
                          {book.authorName}
                        </div>
                      </div>
                      <span className="font-mono text-[10px] font-bold px-1.5 py-0.5 rounded-sm bg-purple-950 text-purple-300 border border-purple-800 shrink-0">
                        {book.seriesIndexDisplay || `#${book.seriesIndex}`}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};
