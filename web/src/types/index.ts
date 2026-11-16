export type FormatType = 'EPUB' | 'M4B' | 'PDF' | 'MP3';

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
  filePath: string;
  fileSizeBytes: number;
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

export type TranscodeStage = 'probing' | 'encoding' | 'muxing' | 'moving';
export type JobStatus = 'queued' | 'processing' | 'completed' | 'failed' | 'paused';

export interface TranscodeJob {
  id: string;
  bookId: string;
  bookTitle: string;
  authorName: string;
  sourceFilesCount: number;
  targetFormat: 'M4B';
  stage: TranscodeStage;
  status: JobStatus;
  progressPercent: number;
  bitrateKbps: number;
  elapsedSeconds: number;
  etaSeconds: number;
  errorMessage?: string;
}

export type DownloadCategory = 'callimachus-ebooks' | 'callimachus-audiobooks';
export type DownloadClientType = 'qBittorrent' | 'Transmission' | 'SABnzbd';

export interface DownloadItem {
  id: string;
  title: string;
  category: DownloadCategory;
  clientType: DownloadClientType;
  status: 'downloading' | 'paused' | 'completed' | 'queued' | 'error';
  progressPercent: number;
  downloadSpeedBps: number;
  totalSizeBytes: number;
  downloadedSizeBytes: number;
  etaSeconds: number;
}

export interface ImportLog {
  id: string;
  timestamp: string;
  action: 'import' | 'transcode' | 'organize' | 'metadata_enrich';
  itemName: string;
  status: 'success' | 'warning' | 'error';
  details: string;
}

export type RequestStatus = 'pending' | 'approved' | 'downloading' | 'available' | 'rejected';
export type RequestType = 'ebook' | 'audiobook' | 'both';

export interface BookRequest {
  id: string;
  title: string;
  author: string;
  requestedBy: string;
  requestType: RequestType;
  status: RequestStatus;
  requestedAt: string;
  reviewedAt?: string;
  rejectionReason?: string;
  notes?: string;
}

export interface UserProfile {
  id: string;
  username: string;
  role: 'admin' | 'curator' | 'user';
  email: string;
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

export interface DownloadClientConfig {
  id: string;
  name: string;
  type: DownloadClientType;
  enabled: boolean;
  host: string;
  port: number;
  useSsl: boolean;
  categoryPath: string;
  status: 'connected' | 'error';
}

export interface SettingOverrideInfo {
  isOverridden: boolean;
  overriddenBy?: string;
  envVar: string;
}

export type SettingsOverridesMap = Record<string, SettingOverrideInfo>;

export interface LibrarySettings {
  rootPath: string;
  stagingPollInterval: string;
  indexerSyncInterval: string;
}

export interface ProwlarrSettings {
  url: string;
  apiKeyConfigured: boolean;
  syncInterval: string;
}

export interface QualityProfile {
  id: string;
  name: string;
  allowedLanguages: string[];
  allowedFormats: FormatType[];
  audioMinBitrateKbps: number;
  audioTargetBitrateKbps: number;
  preferSingleFileM4b: boolean;
  maxFileSizeMb: number;
}

export interface AuthSettings {
  oidcEnabled: boolean;
  oidcIssuerUrl: string;
  oidcClientId: string;
  oidcClientSecretConfigured?: boolean;
  oidcRedirectUrl?: string;
  oidcAdminGroups?: string[];
  oidcCuratorGroups?: string[];
  oidcDefaultRole?: string;
  localAuthEnabled: boolean;
  sessionTimeoutHours: number;
}

