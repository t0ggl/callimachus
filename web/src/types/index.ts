export const FORMAT_TYPES = ['EPUB', 'M4B', 'PDF', 'MP3', 'AZW3', 'MOBI', 'CBR', 'CBZ'] as const;
export const DEFAULT_EBOOK_PREFERENCES: FormatType[] = ['EPUB', 'AZW3', 'MOBI', 'PDF', 'CBR', 'CBZ'];
export type FormatType = typeof FORMAT_TYPES[number];

export interface Author {
  id: string;
  name: string;
  sortName: string;
  photoUrl?: string;
  biography?: string;
  bookCount: number;
}

export interface Narrator {
  id: string;
  name: string;
  sortName: string;
}

export interface Series {
  id: string;
  title: string;
  description?: string;
  totalBooks?: number;
  status?: string;
}

export interface MediaAsset {
  id: string;
  editionId: string;
  filename?: string;
  fileSizeBytes?: number;
  format: FormatType;
  codec?: string;
  bitrateKbps?: number;
  durationSeconds?: number;
  channels?: number;
}

export interface BookEdition {
  id: string;
  bookId: string;
  format: FormatType;
  isbn13?: string;
  asin?: string;
  publisher?: string;
  language: string;
  narrators?: Narrator[];
  assets: MediaAsset[];
}

export interface Book {
  id: string;
  title: string;
  subtitle?: string;
  authorId: string;
  authorName: string;
  seriesId?: string;
  seriesTitle?: string;
  seriesIndex?: number;
  seriesIndexDisplay?: string;
  releaseDate?: string;
  synopsis?: string;
  coverUrl?: string;
  formats: FormatType[];
  editions: BookEdition[];
}

export type ConversionStage = 'queued' | 'preparing' | 'converting' | 'muxing' | 'tagging' | 'moving' | 'completed';
export type JobStatus = 'queued' | 'processing' | 'completed' | 'failed' | 'paused';

export interface ConversionJob {
  requestId?: string;
  downloadId?: string;
  id: string;
  bookId: string;
  editionId?: string;
  bookTitle: string;
  authorName: string;
  sourceFilesCount: number;
  targetFormat: 'M4B' | 'EPUB' | 'AZW3';
  stage: ConversionStage;
  status: JobStatus;
  progressPercent: number;
  bitrateKbps: number;
  elapsedSeconds: number;
  etaSeconds: number;
  errorMessage?: string;
}

export type DownloadCategory = 'callimachus-ebooks' | 'callimachus-audiobooks';

export interface DownloadItem {
  bookId?: string;
  editionId?: string;
  requestId?: string;
  errorMessage?: string;
  id: string;
  title: string;
  category: DownloadCategory;
  clientType: string;
  status: 'downloading' | 'importing' | 'paused' | 'completed' | 'queued' | 'error';
  progressPercent: number;
  downloadSpeedBps: number;
  totalSizeBytes: number;
  downloadedSizeBytes: number;
  etaSeconds: number;
}

export interface ImportLog {
  editionId?: string;
  id: string;
  timestamp: string;
  action: 'import' | 'conversion' | 'organize' | 'metadata_enrich';
  itemName: string;
  status: 'success' | 'warning' | 'error';
  details: string;
}

export type RequestStatus = 'searching' | 'downloading' | 'available' | 'error';
export type RequestType = 'ebook' | 'audiobook' | 'both';

export type AcquisitionStage = 'queued' | 'searching' | 'evaluating' | 'downloading' | 'importing' | 'available';

export interface RequestEvent {
  id: number;
  kind: string;
  message: string;
  occurred_at: string;
}

export interface BookRequest {
  bookId?: string;
  coverUrl?: string;
  year?: number;
  stage?: AcquisitionStage;
  id: string;
  title: string;
  author: string;
  requestedBy: string;
  requestType: RequestType;
  status: RequestStatus;
  requestedAt: string;
}

export interface UIPreferences {
  bundleSeriesInLibrary: boolean;
  defaultSearchFormat?: RequestType;
}

export interface UserProfile {
  id: string;
  username: string;
  role: 'admin' | 'curator' | 'requester' | 'guest' | 'user';
  email: string;
  preferences?: UIPreferences;
}

export interface IndexerConfig {
  id: string;
  name: string;
  type: string;
  enabled: boolean;
  priority: number;
  responseTimeMs: number;
  lastSync: string;
  status: 'online' | 'degraded' | 'offline';
}

export interface SettingOverrideInfo {
  isOverridden: boolean;
  overriddenBy?: string;
  envVar: string;
}

export type SettingsOverridesMap = Record<string, SettingOverrideInfo>;

export interface LibrarySettings {
  completedDownloadRoot?: string;
  rootPath: string;
  stagingPollInterval: string;
  indexerSyncInterval: string;
  bundleSeriesInLibrary?: boolean;
}

export interface ProwlarrSettings {
  url: string;
  apiKey?: string;
  apiKeyConfigured: boolean;
  syncInterval: string;
}

export interface MetadataSettings {
  google_books_api_key?: string;
  hardcover_api_key?: string;
  openlibrary_enabled?: boolean;
  [key: string]: boolean | string | undefined;
}

export interface QualityProfile {
  convertEbooks?: boolean;
  ebookTargetFormat?: 'EPUB' | 'AZW3';
  id: string;
  name: string;
  allowedLanguages: string[];
  allowedFormats: FormatType[];
  ebookFormatPreferences?: FormatType[];
  preferSingleFileM4b: boolean;
  maxAudiobookSizeMb: number;
  maxEbookSizeBytes: number;
}


export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

export interface APIBookRequest {
  id: number;
  title: string;
  author: string;
  username: string;
  format: 'epub' | 'm4b' | 'both';
  status: RequestStatus;
  stage: AcquisitionStage;
  created_at: string;
  book_id?: string;
  cover_url?: string;
  year?: number;
}

export const mapBookRequest = (r: APIBookRequest): BookRequest => ({
  id: String(r.id),
  title: r.title,
  author: r.author,
  requestedBy: r.username || 'Anonymous',
  requestType: r.format === 'epub' ? 'ebook' : r.format === 'm4b' ? 'audiobook' : 'both',
  status: r.status,
  stage: r.stage,
  requestedAt: r.created_at,
  bookId: r.book_id,
  coverUrl: r.cover_url,
  year: r.year,
});

export interface RequestMetadata {
  book_id?: string;
  cover_url?: string;
  series?: string;
  series_index?: number;
  synopsis?: string;
}
