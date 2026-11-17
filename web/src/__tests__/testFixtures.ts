import {
  Author,
  Series,
  Book,
  ConversionJob,
  DownloadItem,
  BookRequest,
  UserProfile,
} from '../types';

export const testUser: UserProfile = {
  id: 'usr_test_1',
  username: 'test_user',
  role: 'admin',
  email: 'test@example.com',
};

export const testAuthors: Author[] = [
  {
    id: 'auth_1',
    name: 'Alden Thorne',
    sortName: 'Thorne, Alden',
    bookCount: 2,
  },
  {
    id: 'auth_2',
    name: 'Sarah Vance',
    sortName: 'Vance, Sarah',
    bookCount: 1,
  },
];

export const testSeries: Series[] = [
  {
    id: 'ser_1',
    title: 'The Obsidian Archive',
    status: 'ongoing',
  },
];

export const testBooks: Book[] = [
  {
    id: 'book_1',
    title: 'The Obsidian Path',
    subtitle: 'Book 1 of The Obsidian Archive',
    authorId: 'auth_1',
    authorName: 'Alden Thorne',
    seriesId: 'ser_1',
    seriesTitle: 'The Obsidian Archive',
    seriesIndex: 1.0,
    seriesIndexDisplay: 'Book 1',
    releaseDate: '2023-04-12',
    synopsis: 'A journey through an obsidian wasteland.',
    formats: ['EPUB', 'M4B'],
    editions: [
      {
        id: 'ed_1',
        bookId: 'book_1',
        format: 'EPUB',
        publisher: 'Apex Horizon Press',
        language: 'en',
        assets: [],
      },
      {
        id: 'ed_2',
        bookId: 'book_1',
        format: 'M4B',
        publisher: 'Apex Horizon Press',
        language: 'en',
        narrators: [{ id: 'nar_1', name: 'Jonathan Bell', sortName: 'Bell, Jonathan' }],
        assets: [],
      },
    ],
  },
];

export const testConversionJobs: ConversionJob[] = [
  {
    id: 'tc_101',
    bookId: 'book_1',
    bookTitle: 'The Obsidian Path',
    authorName: 'Alden Thorne',
    sourceFilesCount: 32,
    targetFormat: 'M4B',
    stage: 'converting',
    status: 'processing',
    progressPercent: 78.4,
    bitrateKbps: 64,
    elapsedSeconds: 840,
    etaSeconds: 195,
  },
];

export const testDownloads: DownloadItem[] = [
  {
    id: 'dl_201',
    title: 'The Obsidian Path - Audiobook M4B',
    category: 'callimachus-audiobooks',
    clientType: 'Prowlarr',
    status: 'downloading',
    progressPercent: 64.2,
    downloadSpeedBps: 8420000,
    totalSizeBytes: 524288000,
    downloadedSizeBytes: 336592896,
    etaSeconds: 22,
  },
];

export const testRequests: BookRequest[] = [
  {
    id: 'req_401',
    title: 'Iron Sigil',
    author: 'Elena Rostova',
    requestedBy: 'reader_jane',
    requestType: 'both',
    status: 'searching',
    requestedAt: '2023-05-01T12:00:00Z',
  },
  {
    id: '2',
    title: 'The Silent Star',
    author: 'Cyril Vance',
    requestedBy: 'reader_alex',
    requestType: 'ebook',
    status: 'searching',
    requestedAt: '2023-05-02T14:30:00Z',
  },
];
