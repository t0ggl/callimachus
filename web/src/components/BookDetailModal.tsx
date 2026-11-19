import { EntityLink } from './EntityLink';
import React, { useEffect, useState } from 'react';
import { X, BookOpen, Headphones, FileText, HardDrive, Calendar, User, Mic, Layers, Download } from 'lucide-react';
import { ManualSearch } from './ManualSearch';
import { Book } from '../types';

interface BookDetailModalProps {
  book: Book | null;
  onClose: () => void;
  activity?: React.ReactNode;
}

function formatBytes(bytes?: number): string {
  if (!bytes || !Number.isFinite(bytes) || bytes < 0) return 'Size unknown';
  const k = 1024;
  const sizes = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const i = Math.max(0, Math.min(sizes.length - 1, Math.floor(Math.log(bytes) / Math.log(k))));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

function formatDuration(seconds?: number): string {
  if (!seconds) return 'Unknown';
  const hrs = Math.floor(seconds / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  if (hrs > 0) {
    return `${hrs}h ${mins}m`;
  }
  return `${mins}m`;
}

export const BookDetailModal: React.FC<BookDetailModalProps> = ({ book, onClose, activity }) => {
  const dialog = React.useRef<HTMLDialogElement>(null);
  const [format, setFormat] = useState('epub');
  const [requestId, setRequestId] = useState<string | null>(null);
  const [manualError, setManualError] = useState('');
  const [manualBusy, setManualBusy] = useState(false);
  const openManual = async () => {
    if (!book) return;
    setManualBusy(true);
    setManualError('');
    try {
      const res = await fetch('/api/requests');
      if (!res.ok) throw new Error('Unable to load acquisitions');
      const list = await res.json();
      const existing = list.find((r: any) => r.book_id === book.id && (r.format === format || r.format === 'both'));
      if (existing) {
        setRequestId(String(existing.id));
        return;
      }
      const created = await fetch('/api/requests', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          book_id: book.id,
          title: book.title,
          author: book.authorName,
          format,
          manual_only: true,
        }),
      });
      if (!created.ok) throw new Error('Unable to open manual search');
      setRequestId(String((await created.json()).id));
    } catch (e) {
      setManualError((e as Error).message);
    } finally {
      setManualBusy(false);
    }
  };
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, [Boolean(book)]);

  if (!book) return null;

  return (
    <>
    <dialog
      ref={dialog}
      aria-label={`Details for ${book.title}`}
      className="m-auto w-[calc(100%-2rem)] max-w-3xl max-h-[90dvh] bg-zinc-900 text-zinc-100 border border-zinc-700 rounded-xl overflow-hidden backdrop:bg-black/75"
      onCancel={onClose}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className="max-h-[90dvh] flex flex-col">
        {!activity && <div className="px-6 pt-4 flex flex-wrap items-center gap-3">
          <label className="text-sm text-zinc-300">
            Format
            <select value={format} onChange={e => setFormat(e.target.value)} className="ml-2 rounded border border-zinc-700 bg-zinc-950 px-2 py-1">
              <option value="epub">eBook</option>
              <option value="m4b">Audiobook</option>
            </select>
          </label>
          <button disabled={manualBusy} onClick={() => void openManual()} className="text-blue-300 disabled:opacity-50">
            {manualBusy ? 'Loading…' : 'Search Download Options'}
          </button>
          {manualError && <p role="alert" className="text-red-300">{manualError}</p>}
        </div>}
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-zinc-800 bg-zinc-950/40">
          <div className="flex items-center gap-3">
            <h2 className="text-lg font-semibold text-zinc-100">{book.title}</h2>
            <div className="flex gap-1.5">
              {book.formats.map((fmt) => (
                <span
                  key={fmt}
                  className={`text-[11px] font-mono px-2 py-0.5 rounded-sm font-medium ${
                    fmt === 'M4B'
                      ? 'bg-purple-950/80 text-purple-300 border border-purple-800/60'
                      : 'bg-blue-950/80 text-blue-300 border border-blue-800/60'
                  }`}
                >
                  {fmt}
                </span>
              ))}
            </div>
          </div>
          <button
            onClick={onClose}
            data-testid="close-book-detail"
            aria-label="Close details"
            autoFocus
            className="p-1.5 text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800 rounded-lg transition-colors"
            title="Close modal (Esc)"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content body */}
        <div className="flex-1 overflow-y-auto p-6 space-y-6">
          <div className="flex flex-col sm:flex-row gap-6">
            {/* Cover art */}
            <div className="w-40 sm:w-48 shrink-0">
              <div className="aspect-[2/3] rounded-lg overflow-hidden bg-zinc-800 border border-zinc-700/60 shadow-md">
                {book.coverUrl ? (
                  <img
                    src={book.coverUrl}
                    referrerPolicy="no-referrer"
                    alt={book.title}
                    className="w-full h-full object-cover"
                  />
                ) : (
                  <div className="w-full h-full flex flex-col items-center justify-center text-zinc-600">
                    <BookOpen className="w-12 h-12 mb-2 stroke-1" />
                    <span className="text-xs">No Cover Art</span>
                  </div>
                )}
              </div>
            </div>

            {/* Metadata overview */}
            <div className="flex-1 min-w-0 space-y-4">
              <div>
                <h3 className="text-xl font-bold text-zinc-100">{book.title}</h3>
                {book.subtitle && (
                  <p className="text-sm text-zinc-400 mt-0.5">{book.subtitle}</p>
                )}
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-xs">
                <div className="flex items-center gap-2 text-zinc-300">
                  <User className="w-4 h-4 text-zinc-500 shrink-0" />
                  <span className="text-zinc-500">Author:</span>
                  <span className="font-medium text-zinc-200"><EntityLink kind="authors" name={book.authorName} /></span>
                </div>

                {book.seriesTitle && (
                  <div className="flex items-center gap-2 text-zinc-300">
                    <Layers className="w-4 h-4 text-purple-400 shrink-0" />
                    <span className="text-zinc-500">Series:</span>
                    <span className="font-medium text-zinc-200">
                      <EntityLink kind="series" name={book.seriesTitle} />{book.seriesIndex != null && ` (${book.seriesIndexDisplay || `#${book.seriesIndex}`})`}
                    </span>
                  </div>
                )}

                {book.releaseDate && (
                  <div className="flex items-center gap-2 text-zinc-300">
                    <Calendar className="w-4 h-4 text-zinc-500 shrink-0" />
                    <span className="text-zinc-500">Released:</span>
                    <span className="font-medium text-zinc-200">{book.releaseDate}</span>
                  </div>
                )}
              </div>

              {/* Synopsis */}
              {book.synopsis && (
                <div className="bg-zinc-950/50 p-3.5 rounded-lg border border-zinc-800/80 text-xs text-zinc-300 leading-relaxed">
                  <div className="text-[11px] font-semibold text-zinc-400 uppercase tracking-wider mb-1">
                    Synopsis
                  </div>
                  {book.synopsis}
                </div>
              )}
            </div>
          </div>

          {activity}

          {/* Editions & File Assets */}
          <div className="space-y-4 border-t border-zinc-800 pt-5">
            <h4 className="text-sm font-semibold text-zinc-200 flex items-center gap-2">
              <HardDrive className="w-4 h-4 text-zinc-400" />
              Available files
            </h4>

            <div className="space-y-3">
              {book.editions.length === 0 && <p className="text-xs text-zinc-400">No files available.</p>}
              {book.editions.map((edition) => (
                <div
                  key={edition.id}
                  className="bg-zinc-950/60 border border-zinc-800 rounded-lg p-4 space-y-3"
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      {edition.format.toUpperCase() === 'M4B' ? (
                        <Headphones className="w-4 h-4 text-purple-400" />
                      ) : (
                        <FileText className="w-4 h-4 text-blue-400" />
                      )}
                      <span className="text-xs font-semibold text-zinc-200">
                        {edition.format.toUpperCase()}
                      </span>
                      {edition.language && (
                        <span className="text-[10px] bg-zinc-800 px-1.5 py-0.5 rounded-sm text-zinc-400">
                          {edition.language}
                        </span>
                      )}
                    </div>

                    <div className="flex flex-wrap items-center gap-3 text-xs text-zinc-400 font-mono">
                      {edition.publisher && <span className="font-sans text-zinc-300">{edition.publisher}</span>}
                      {edition.isbn13 && <span>ISBN: {edition.isbn13}</span>}
                      {edition.asin && <span>ASIN: {edition.asin}</span>}
                    </div>
                  </div>

                  {edition.narrators && edition.narrators.length > 0 && (
                    <div className="flex items-center gap-2 text-xs text-purple-300">
                      <Mic className="w-3.5 h-3.5 text-purple-400 shrink-0" />
                      <span className="text-zinc-500">Narrator:</span>
                      <span>{edition.narrators.map((n) => n.name).join(', ')}</span>
                    </div>
                  )}

                  {/* Asset files */}
                  <div className="space-y-1.5 pt-1" role="list" aria-label={`${edition.format.toUpperCase()} files`}>
                    {edition.assets.length === 0 && <p className="text-xs text-zinc-400">No files available for this edition.</p>}
                    {edition.assets.map((asset) => (
                      <div
                        key={asset.id}
                        role="listitem"
                        className="flex flex-col sm:flex-row sm:items-start justify-between p-2 rounded-sm bg-zinc-900/80 border border-zinc-800/60 text-xs gap-2"
                      >
                        <a
                          href={`/api/books/${encodeURIComponent(book.id)}/files/${encodeURIComponent(asset.id)}`}
                          download={asset.filename || true}
                          aria-label={asset.filename ? `Download ${asset.filename}` : 'Download file'}
                          className="flex min-w-0 flex-1 items-start gap-2 rounded-sm text-xs text-blue-300 underline decoration-blue-300/50 underline-offset-2 hover:text-blue-100 hover:decoration-current focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300"
                        >
                          <Download aria-hidden="true" className="h-4 w-4 shrink-0" />
                          <span className="min-w-0 [overflow-wrap:anywhere]">{asset.filename || 'Filename unknown'}</span>
                        </a>
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 sm:shrink-0 text-zinc-400 text-xs">
                          <span>{asset.format?.toUpperCase() || edition.format.toUpperCase()}</span>
                          {edition.language && <span>{edition.language}</span>}
                          {!!asset.durationSeconds && (
                            <span>{formatDuration(asset.durationSeconds)}</span>
                          )}
                          {asset.codec && <span>{asset.codec}</span>}
                          {!!asset.bitrateKbps && <span>{asset.bitrateKbps} kbps</span>}
                          <span className="whitespace-nowrap font-semibold text-zinc-200">
                            {formatBytes(asset.fileSizeBytes)}
                          </span>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="px-6 py-3 border-t border-zinc-800 bg-zinc-950/60 flex justify-end">
          <button
            onClick={onClose}
            className="px-4 py-1.5 text-xs font-medium text-zinc-300 hover:text-white bg-zinc-800 hover:bg-zinc-700 rounded-md transition-colors"
          >
            Close (Esc)
          </button>
        </div>
      </div>
    </dialog>
    {requestId && <ManualSearch requestId={requestId} onClose={() => setRequestId(null)} />}
    </>
  );
};
