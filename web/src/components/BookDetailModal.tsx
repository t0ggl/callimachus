import { EntityLink } from './EntityLink';
import React, { useEffect, useState } from 'react';
import { X, BookOpen, Headphones, FileText, HardDrive, Calendar, User, Mic, Layers, Download, Bell, Trash2, Upload, Loader2 } from 'lucide-react';
import { ManualSearch } from './ManualSearch';
import { MetadataRepairPanel } from './MetadataRepairPanel';
import { validateCoverFile } from '../utils/coverValidation';
import { Book } from '../types';

export interface ResolveBookInput {
  bookId?: string;
  title: string;
  subtitle?: string;
  author: string;
  series?: string;
  seriesId?: string;
  hardcoverSeriesId?: number;
  seriesIndex?: number | null;
  releaseDate?: string;
  synopsis?: string;
  coverUrl?: string;
}

export async function resolveCatalogBook(input: ResolveBookInput): Promise<Book> {
  if (input.bookId) {
    const res = await fetch(`/api/books?id=${encodeURIComponent(input.bookId)}`);
    if (!res.ok) throw new Error('Unable to load book details. Try again.');
    const list: Book[] = await res.json();
    const found = list.find((item) => item.id === input.bookId);
    if (found) {
      return {
        ...found,
        coverUrl: found.coverUrl || input.coverUrl,
        synopsis: found.synopsis || input.synopsis,
        subtitle: found.subtitle || input.subtitle,
        releaseDate: found.releaseDate || input.releaseDate,
        seriesId: found.seriesId || input.seriesId,
        seriesTitle: found.seriesTitle || input.series,
        seriesIndex: found.seriesIndex ?? input.seriesIndex ?? undefined,
      };
    }
  }
  const res = await fetch('/api/books/resolve', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      book_id: input.bookId || undefined,
      title: input.title,
      subtitle: input.subtitle,
      author: input.author || 'Unknown',
      series: input.series,
      series_id: input.seriesId || undefined,
      hardcover_series_id: input.hardcoverSeriesId || undefined,
      series_index: input.seriesIndex ?? undefined,
      release_date: input.releaseDate,
      synopsis: input.synopsis,
      cover_url: input.coverUrl,
    }),
  });
  if (!res.ok) {
    throw new Error('Unable to save book details. Check permissions and retry.');
  }
  const resolved: Book = await res.json();
  return {
    ...resolved,
    coverUrl: resolved.coverUrl || input.coverUrl,
    synopsis: resolved.synopsis || input.synopsis,
    subtitle: resolved.subtitle || input.subtitle,
    releaseDate: resolved.releaseDate || input.releaseDate,
    seriesTitle: resolved.seriesTitle || input.series,
    seriesIndex: resolved.seriesIndex ?? input.seriesIndex ?? undefined,
  };
}

interface BookDetailModalProps {
  book: Book | null;
  onClose: () => void;
  activity?: React.ReactNode;
  isAdmin?: boolean;
  canManage?: boolean;
  canRequest?: boolean;
  onMetadataApplied?: () => void;
  onBookUpdated?: (book: Book) => void;
  onBookDeleted?: (bookId: string) => void;
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

export const BookDetailModal: React.FC<BookDetailModalProps> = ({
  book,
  onClose,
  activity,
  isAdmin,
  canManage,
  canRequest,
  onMetadataApplied,
  onBookUpdated,
  onBookDeleted,
}) => {
  const dialog = React.useRef<HTMLDialogElement>(null);
  const fixButton = React.useRef<HTMLButtonElement>(null);
  const deleteTrigger = React.useRef<HTMLButtonElement>(null);
  const cancelDeleteButton = React.useRef<HTMLButtonElement>(null);
  const coverInputRef = React.useRef<HTMLInputElement>(null);
  const [prevBook, setPrevBook] = useState<Book | null>(book);
  const [activeBook, setActiveBook] = useState<Book | null>(book);
  const [repairBookID, setRepairBookID] = useState<string>();
  const [showDownloadOptions, setShowDownloadOptions] = useState(false);
  const [monitored, setMonitored] = useState(Boolean(book?.monitored));
  const [actionBusy, setActionBusy] = useState(false);
  const [actionError, setActionError] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const [coverUploading, setCoverUploading] = useState(false);
  const [coverError, setCoverError] = useState('');
  const [coverStatus, setCoverStatus] = useState('');

  if (book !== prevBook) {
    const sameResolvingBook = Boolean(
      book &&
        prevBook &&
        prevBook.id === '' &&
        book.title === prevBook.title &&
        book.authorName === prevBook.authorName
    );
    const sameBook = Boolean(book && prevBook && book.id !== '' && book.id === prevBook.id);
    setPrevBook(book);
    setActiveBook(book);
    setMonitored(Boolean(book?.monitored));
    setActionError('');
    setDeleteError('');
    if (!sameResolvingBook) {
      setShowDownloadOptions(false);
      setRepairBookID(undefined);
      setConfirmDelete(false);
      setDeleteFiles(false);
    }
    if (!sameResolvingBook && !sameBook) {
      setCoverUploading(false);
      setCoverError('');
      setCoverStatus('');
    }
  }

  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, [Boolean(book)]);

  useEffect(() => {
    if (confirmDelete) {
      cancelDeleteButton.current?.focus();
    }
  }, [confirmDelete]);

  const currentBook = activeBook ?? book;
  if (!currentBook) return null;
  const hasAssets = currentBook.editions.some((edition) => edition.assets.length > 0);
  const canDelete = Boolean(isAdmin && currentBook.id);
  const canUploadCover = Boolean((canManage || isAdmin) && currentBook.id && hasAssets);
  const canRepair = Boolean(
    isAdmin &&
      currentBook.id &&
      currentBook.editions.some((edition) =>
        edition.assets.some((asset) => asset.filename?.toLowerCase().endsWith('.epub'))
      )
  );
  const repairing = canRepair && repairBookID === currentBook.id;
  const isOwned =
    currentBook.state === 'In library' ||
    (currentBook.state === undefined && (hasAssets || currentBook.formats.length > 0));
  const allowRequest = Boolean(canRequest ?? canManage);
  const showMonitorControl = !isOwned && Boolean(canManage);

  const ensurePersistedBook = async (): Promise<Book> => {
    if (currentBook.id) return currentBook;
    const resolved = await resolveCatalogBook({
      title: currentBook.title,
      subtitle: currentBook.subtitle,
      author: currentBook.authorName,
      series: currentBook.seriesTitle,
      seriesId: currentBook.seriesId,
      seriesIndex: currentBook.seriesIndex,
      releaseDate: currentBook.releaseDate,
      synopsis: currentBook.synopsis,
      coverUrl: currentBook.coverUrl,
    });
    const merged: Book = {
      ...currentBook,
      ...resolved,
      coverUrl: resolved.coverUrl || currentBook.coverUrl,
      synopsis: resolved.synopsis || currentBook.synopsis,
      subtitle: resolved.subtitle || currentBook.subtitle,
      releaseDate: resolved.releaseDate || currentBook.releaseDate,
      seriesId: resolved.seriesId || currentBook.seriesId,
      seriesTitle: resolved.seriesTitle || currentBook.seriesTitle,
      seriesIndex: resolved.seriesIndex ?? currentBook.seriesIndex,
      seriesIndexDisplay: resolved.seriesIndexDisplay || currentBook.seriesIndexDisplay,
    };
    setActiveBook(merged);
    onBookUpdated?.(merged);
    return merged;
  };

  const handleShowDownloadOptions = async () => {
    if (currentBook.id) {
      setActionError('');
      setShowDownloadOptions(true);
      return;
    }
    setActionBusy(true);
    setActionError('');
    try {
      await ensurePersistedBook();
      setShowDownloadOptions(true);
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setActionBusy(false);
    }
  };

  const handleToggleMonitor = async () => {
    setActionBusy(true);
    setActionError('');
    const next = !monitored;
    try {
      const persisted = await ensurePersistedBook();
      const res = await fetch(`/api/books/${encodeURIComponent(persisted.id)}/monitor`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ monitored: next }),
      });
      if (!res.ok) {
        throw new Error('Unable to save monitoring state. Check permissions and retry.');
      }
      const updated: Book = { ...persisted, monitored: next };
      setMonitored(next);
      setActiveBook(updated);
      onBookUpdated?.(updated);
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setActionBusy(false);
    }
  };

  const handleCoverFileChange = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file || !currentBook.id || coverUploading) return;

    setCoverError('');
    setCoverStatus('');

    const validationError = await validateCoverFile(file);
    if (validationError) {
      setCoverError(validationError);
      return;
    }

    setCoverUploading(true);
    try {
      const formData = new FormData();
      formData.append('cover', file);
      const res = await fetch(`/api/books/${encodeURIComponent(currentBook.id)}/cover`, {
        method: 'POST',
        body: formData,
      });
      if (!res.ok) {
        const contentType = res.headers.get('Content-Type') ?? '';
        let message = '';
        if (contentType.startsWith('text/plain')) {
          message = (await res.text()).trim();
        } else if (contentType.includes('application/json')) {
          const payload = (await res.json().catch(() => null)) as { error?: string } | null;
          message = payload?.error?.trim() ?? '';
        }
        throw new Error(message || 'Unable to update book cover. Check permissions and try again.');
      }
      const data = (await res.json()) as { coverUrl?: string; epubUpdated?: number };
      const nextCoverUrl = data.coverUrl || `/api/books/${encodeURIComponent(currentBook.id)}/cover?v=${Date.now()}`;
      const updated: Book = { ...currentBook, coverUrl: nextCoverUrl };
      setActiveBook(updated);
      onBookUpdated?.(updated);
      setCoverStatus(
        data.epubUpdated && data.epubUpdated > 0
          ? 'Cover updated and embedded in EPUB.'
          : 'Cover updated.'
      );
    } catch (e) {
      setCoverError(e instanceof Error ? e.message : 'Unable to update book cover.');
    } finally {
      setCoverUploading(false);
    }
  };

  const handleDeleteBook = async () => {
    if (!currentBook.id || deleting) return;
    setDeleting(true);
    setDeleteError('');
    try {
      const params = new URLSearchParams();
      if (deleteFiles) {
        params.set('deleteFiles', 'true');
      }
      const query = params.toString();
      const res = await fetch(
        `/api/books/${encodeURIComponent(currentBook.id)}${query ? `?${query}` : ''}`,
        { method: 'DELETE' }
      );
      if (!res.ok) {
        const message = res.headers.get('Content-Type')?.startsWith('text/plain')
          ? (await res.text()).trim()
          : '';
        throw new Error(message || 'Unable to delete book. Check permissions and try again.');
      }
      if (onBookDeleted) {
        onBookDeleted(currentBook.id);
      } else {
        onMetadataApplied?.();
        onClose();
      }
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : 'Unable to delete book.');
    } finally {
      setDeleting(false);
    }
  };

  return (
    <>
    <dialog
      ref={dialog}
      aria-label={`Details for ${currentBook.title}`}
      className="m-auto w-[calc(100%-2rem)] max-w-3xl max-h-[90dvh] bg-zinc-900 text-zinc-100 border border-zinc-700 rounded-xl overflow-hidden backdrop:bg-black/75"
      onCancel={onClose}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className="max-h-[90dvh] flex flex-col">
        {!activity && !repairing && (allowRequest || showMonitorControl) && <div className="px-6 pt-4 flex flex-wrap items-center gap-3">
          {allowRequest && (
            <button type="button" disabled={actionBusy} onClick={() => void handleShowDownloadOptions()}
              className="inline-flex items-center gap-2 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 text-sm font-medium text-zinc-100 transition-colors hover:bg-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50">
              <Download size={16} aria-hidden="true" />
              Show Download Options
            </button>
          )}
          {showMonitorControl && (
            <button
              type="button"
              disabled={actionBusy}
              aria-pressed={monitored}
              onClick={() => void handleToggleMonitor()}
              className={`inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50 ${
                monitored
                  ? 'border-blue-700/80 bg-blue-950/70 text-blue-200 hover:bg-blue-900/70'
                  : 'border-zinc-700 bg-zinc-800 text-zinc-100 hover:bg-zinc-700'
              }`}
            >
              <Bell size={16} aria-hidden="true" />
              {monitored ? 'Monitoring Book' : 'Monitor Book'}
            </button>
          )}
          {actionError && (
            <p role="alert" className="text-xs text-red-300">{actionError}</p>
          )}
        </div>}
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-zinc-800 bg-zinc-950/40">
          <div className="flex flex-wrap items-center gap-3">
            <h2 className="text-lg font-semibold text-zinc-100">{currentBook.title}</h2>
            <div className="flex gap-1.5">
              {currentBook.state && currentBook.state !== 'In library' && (
                <span className="rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-0.5 text-xs text-zinc-200">
                  {currentBook.state}
                </span>
              )}
              {currentBook.formats.map((fmt) => (
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
        {repairing ? (
          <MetadataRepairPanel key={currentBook.id} bookID={currentBook.id} onApplied={onMetadataApplied}
            onBack={() => {
              setRepairBookID(undefined);
              requestAnimationFrame(() => fixButton.current?.focus());
            }} />
        ) : <div className="flex-1 overflow-y-auto p-6 space-y-6">
          <div className="flex flex-col sm:flex-row gap-6">
            {/* Cover art */}
            <div className="w-40 sm:w-48 shrink-0">
              <div className="group relative aspect-[2/3] rounded-lg overflow-hidden bg-zinc-800 border border-zinc-700/60 shadow-md">
                {currentBook.coverUrl ? (
                  <img
                    src={currentBook.coverUrl}
                    referrerPolicy="no-referrer"
                    alt={currentBook.title}
                    className="w-full h-full object-cover"
                  />
                ) : (
                  <div className="w-full h-full flex flex-col items-center justify-center text-zinc-500">
                    <BookOpen className="w-12 h-12 mb-2 stroke-1" aria-hidden="true" />
                    <span className="text-xs text-zinc-400">No Cover Art</span>
                  </div>
                )}
                {canUploadCover && (
                  <>
                    <input
                      ref={coverInputRef}
                      type="file"
                      accept="image/jpeg,image/png,image/webp,.jpg,.jpeg,.png,.webp"
                      tabIndex={-1}
                      aria-hidden="true"
                      data-testid="cover-upload-input"
                      onChange={(e) => void handleCoverFileChange(e)}
                      className="sr-only"
                    />
                    <div
                      className={`absolute inset-x-0 bottom-0 flex justify-center bg-gradient-to-t from-zinc-950/95 via-zinc-950/70 to-transparent p-2.5 pt-8 transition-opacity duration-150 ${
                        coverUploading
                          ? 'opacity-100'
                          : 'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100 [@media(pointer:coarse)]:opacity-100'
                      }`}
                    >
                      <button
                        type="button"
                        disabled={coverUploading}
                        onClick={() => coverInputRef.current?.click()}
                        aria-label={coverUploading ? 'Uploading cover' : 'Upload cover'}
                        className="inline-flex items-center justify-center gap-1.5 rounded-md border border-zinc-600/80 bg-zinc-900/95 px-2.5 py-1.5 text-xs font-medium text-zinc-100 shadow-sm transition-colors hover:bg-zinc-800 hover:text-white focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-70"
                      >
                        {coverUploading ? (
                          <>
                            <Loader2 className="w-3.5 h-3.5 animate-spin shrink-0" aria-hidden="true" />
                            Uploading…
                          </>
                        ) : (
                          <>
                            <Upload className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
                            Upload Cover
                          </>
                        )}
                      </button>
                    </div>
                  </>
                )}
              </div>
              {coverError && (
                <p role="alert" className="mt-2 text-xs text-red-300 leading-snug">
                  {coverError}
                </p>
              )}
              {coverStatus && !coverError && (
                <p role="status" className="mt-2 text-xs text-emerald-300 leading-snug">
                  {coverStatus}
                </p>
              )}
            </div>

            {/* Metadata overview */}
            <div className="flex-1 min-w-0 space-y-4">
              <div>
                <h3 className="text-xl font-bold text-zinc-100">{currentBook.title}</h3>
                {currentBook.subtitle && (
                  <p className="text-sm text-zinc-400 mt-0.5">{currentBook.subtitle}</p>
                )}
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-xs">
                <div className="flex items-center gap-2 text-zinc-300">
                  <User className="w-4 h-4 text-zinc-500 shrink-0" />
                  <span className="text-zinc-500">Author:</span>
                  <span className="font-medium text-zinc-200"><EntityLink kind="authors" name={currentBook.authorName} /></span>
                </div>

                {currentBook.seriesTitle && (
                  <div className="flex items-center gap-2 text-zinc-300">
                    <Layers className="w-4 h-4 text-purple-400 shrink-0" />
                    <span className="text-zinc-500">Series:</span>
                    <span className="font-medium text-zinc-200">
                      <EntityLink kind="series" name={currentBook.seriesTitle} id={currentBook.seriesId} />{currentBook.seriesIndex != null && ` (Book ${currentBook.seriesIndex})`}
                    </span>
                  </div>
                )}

                {currentBook.releaseDate && (
                  <div className="flex items-center gap-2 text-zinc-300">
                    <Calendar className="w-4 h-4 text-zinc-500 shrink-0" />
                    <span className="text-zinc-500">Released:</span>
                    <span className="font-medium text-zinc-200">{currentBook.releaseDate}</span>
                  </div>
                )}
              </div>

              {/* Synopsis */}
              {currentBook.synopsis && (
                <div className="bg-zinc-950/50 p-3.5 rounded-lg border border-zinc-800/80 text-xs text-zinc-300 leading-relaxed">
                  <div className="text-[11px] font-semibold text-zinc-400 uppercase tracking-wider mb-1">
                    Synopsis
                  </div>
                  {currentBook.synopsis}
                </div>
              )}
            </div>
          </div>

          {activity}

          {/* Editions & File Assets */}
          <div className="space-y-4 border-t border-zinc-800 pt-5">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h4 className="text-sm font-semibold text-zinc-200 flex items-center gap-2">
                <HardDrive className="w-4 h-4 text-zinc-400" />
                Available files
              </h4>
              {canRepair && (
                <button ref={fixButton} type="button" onClick={() => setRepairBookID(currentBook.id)}
                  className="rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 text-sm font-medium text-zinc-100 transition-colors hover:bg-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300">
                  Fix Metadata
                </button>
              )}
            </div>

            <div className="space-y-3">
              {currentBook.editions.length === 0 && <p className="text-xs text-zinc-400">No files available.</p>}
              {currentBook.editions.map((edition) => (
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
                          href={`/api/books/${encodeURIComponent(currentBook.id)}/files/${encodeURIComponent(asset.id)}`}
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
        </div>}

        {/* Footer */}
        {!repairing && confirmDelete && canDelete && (
          <div
            role="region"
            aria-label="Confirm book deletion"
            className="border-t border-zinc-800 bg-zinc-950/80 px-6 py-4 space-y-3"
          >
            <p className="text-sm font-medium text-zinc-100">
              Delete <span className="font-semibold">{currentBook.title}</span> from the library?
            </p>
            <label className="flex items-start gap-2.5 cursor-pointer select-none text-xs text-zinc-200">
              <input
                type="checkbox"
                checked={deleteFiles}
                disabled={deleting}
                onChange={(e) => setDeleteFiles(e.target.checked)}
                className="mt-0.5 size-4 rounded-sm accent-red-500 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300"
              />
              <span>
                <span className="font-medium text-zinc-100">Also delete files from disk</span>
                <span className="block text-zinc-400 mt-0.5">
                  {deleteFiles
                    ? 'Media files, cover art, and sidecar metadata on disk will be permanently deleted.'
                    : 'Only the library catalog entry will be removed; existing files on disk will be kept.'}
                </span>
              </span>
            </label>
            {deleteError && (
              <p role="alert" className="text-xs text-red-300">{deleteError}</p>
            )}
            <div className="flex flex-wrap items-center justify-end gap-2">
              <button
                ref={cancelDeleteButton}
                type="button"
                disabled={deleting}
                onClick={() => {
                  setConfirmDelete(false);
                  setDeleteFiles(false);
                  setDeleteError('');
                  requestAnimationFrame(() => deleteTrigger.current?.focus());
                }}
                className="rounded-md border border-zinc-700 bg-zinc-800 px-3 py-1.5 text-xs font-medium text-zinc-200 transition-colors hover:bg-zinc-700 hover:text-white focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={deleting}
                onClick={() => void handleDeleteBook()}
                className="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-semibold text-white transition-colors hover:bg-red-500 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300 disabled:opacity-50"
              >
                <Trash2 className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
                {deleting ? 'Deleting…' : 'Confirm Delete'}
              </button>
            </div>
          </div>
        )}
        {!repairing && !confirmDelete && (
          <div className="px-6 py-3 border-t border-zinc-800 bg-zinc-950/60 flex flex-wrap items-center justify-between gap-3">
            {canDelete ? (
              <button
                ref={deleteTrigger}
                type="button"
                onClick={() => {
                  setDeleteFiles(false);
                  setDeleteError('');
                  setConfirmDelete(true);
                }}
                className="inline-flex items-center gap-1.5 rounded-md border border-red-900/60 bg-red-950/30 px-3 py-1.5 text-xs font-medium text-red-300 transition-colors hover:bg-red-950/60 hover:text-red-200 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300"
              >
                <Trash2 className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
                Delete Book
              </button>
            ) : (
              <span />
            )}
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-1.5 text-xs font-medium text-zinc-300 hover:text-white bg-zinc-800 hover:bg-zinc-700 rounded-md transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300"
            >
              Close (Esc)
            </button>
          </div>
        )}
      </div>
    </dialog>
    {showDownloadOptions && currentBook.id && <ManualSearch key={currentBook.id}
      book={{ id: currentBook.id, title: currentBook.title, author: currentBook.authorName }}
      onClose={() => setShowDownloadOptions(false)} />}
    </>
  );
};
