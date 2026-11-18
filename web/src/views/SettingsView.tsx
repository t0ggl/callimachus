import React, { useState, useEffect } from 'react';
import {
  ArrowUp,
  ArrowDown,
  Globe,
  Sliders,
  HardDrive,
  RefreshCw,
  CheckCircle2,
  Lock,
  Save,
  AlertCircle,
} from 'lucide-react';
import {
  IndexerConfig,
  LibrarySettings,
  ProwlarrSettings,
  QualityProfile,
  SettingsOverridesMap,
  FormatType,
  FORMAT_TYPES,
  DEFAULT_EBOOK_PREFERENCES,
} from '../types';

export interface SettingsUpdatePayload {
 metadata?: Record<string,string>;
  library?: {
    rootPath?: string;
    completedDownloadRoot?: string;
    stagingPollInterval?: string;
    indexerSyncInterval?: string;
  };
  prowlarr?: {
    url?: string;
    apiKey?: string;
    syncInterval?: string;
  };
  qualityProfile?: {
    name?: string;
    allowedLanguages?: string[];
    allowedFormats?: FormatType[];
    ebookFormatPreferences?: FormatType[];
    convertEbooks?: boolean;
    ebookTargetFormat?: 'EPUB' | 'AZW3';
    maxFileSizeMb?: number;
    maxEbookSizeBytes?: number;
  };
}

interface SettingsViewProps {
  indexers: IndexerConfig[];
  librarySettings?: LibrarySettings;
  prowlarrSettings?: ProwlarrSettings;
  qualityProfile: QualityProfile;
  overrides?: SettingsOverridesMap;
  onSyncIndexers?: () => void | Promise<void>;
  onSaveSettings?: (payload: SettingsUpdatePayload) => Promise<void>;
}

const DEFAULT_LIBRARY: LibrarySettings = {
  rootPath: './books',
  stagingPollInterval: '5s',
  indexerSyncInterval: '15m0s',
};

const DEFAULT_PROWLARR: ProwlarrSettings = {
  url: '',
  apiKeyConfigured: false,
  syncInterval: '1h0m0s',
};

export const SettingsView: React.FC<SettingsViewProps> = ({
  indexers: initialIndexers,
  librarySettings: initialLibrary = DEFAULT_LIBRARY,
  prowlarrSettings: initialProwlarr = DEFAULT_PROWLARR,
  qualityProfile: initialQuality,
  overrides = {},
  onSyncIndexers,
  onSaveSettings,
}) => {
  const [activeTab, setActiveTab] = useState<'indexers' | 'library' | 'quality'>('indexers');
  const [isSyncing, setIsSyncing] = useState(false);
  const [syncSuccess, setSyncSuccess] = useState(false);

  const [isSaving, setIsSaving] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const [indexers, setIndexers] = useState<IndexerConfig[]>(initialIndexers);
  const [librarySettings, setLibrarySettings] = useState<LibrarySettings>(initialLibrary);
  const [prowlarrSettings, setProwlarrSettings] = useState<ProwlarrSettings>(initialProwlarr);
  const [prowlarrApiKeyDraft, setProwlarrApiKeyDraft] = useState('');
 const [metadataKeys,setMetadataKeys]=useState<Record<string,string>>({});
  const [connectionTests, setConnectionTests] = useState<Record<string, { pending: boolean; ok?: boolean; message?: string }>>({});

  const [qualityProfile, setQualityProfile] = useState<QualityProfile>({
    ...initialQuality,
    allowedLanguages: initialQuality.allowedLanguages ?? ['en'],
    allowedFormats: initialQuality.allowedFormats ?? ['EPUB', 'M4B'],
    maxFileSizeMb: initialQuality.maxFileSizeMb ?? 1536,
    maxEbookSizeBytes: initialQuality.maxEbookSizeBytes ?? 100 * 1024 * 1024,
    ebookFormatPreferences: initialQuality.ebookFormatPreferences ?? DEFAULT_EBOOK_PREFERENCES,
    convertEbooks: initialQuality.convertEbooks ?? false,
    ebookTargetFormat: initialQuality.ebookTargetFormat ?? 'EPUB',
  });
  const [languagesInput, setLanguagesInput] = useState(
    (initialQuality.allowedLanguages ?? ['en']).join(', ')
  );


  useEffect(() => {
    setIndexers(initialIndexers);
  }, [initialIndexers]);


  useEffect(() => {
    setLibrarySettings(initialLibrary);
  }, [initialLibrary]);

  useEffect(() => {
    setProwlarrSettings(initialProwlarr);
  }, [initialProwlarr]);

  useEffect(() => {
    const langs = initialQuality.allowedLanguages ?? ['en'];
    setQualityProfile({
      ...initialQuality,
      allowedLanguages: langs,
      allowedFormats: initialQuality.allowedFormats ?? ['EPUB', 'M4B'],
      maxFileSizeMb: initialQuality.maxFileSizeMb ?? 1536,
      maxEbookSizeBytes: initialQuality.maxEbookSizeBytes ?? 100 * 1024 * 1024,
      ebookFormatPreferences: initialQuality.ebookFormatPreferences ?? DEFAULT_EBOOK_PREFERENCES,
      convertEbooks: initialQuality.convertEbooks ?? false,
      ebookTargetFormat: initialQuality.ebookTargetFormat ?? 'EPUB',
    });
    setLanguagesInput(langs.join(', '));
  }, [initialQuality]);

  const isOverridden = (key: string): boolean => Boolean(overrides[key]?.isOverridden);

  const overriddenCount = Object.values(overrides).filter((o) => o?.isOverridden).length;

  const OverrideNote: React.FC<{ settingKey: string }> = ({ settingKey }) => {
    const info = overrides[settingKey];
    if (!info?.isOverridden) return null;
    return (
      <p
        data-testid={`override-note-${settingKey}`}
        className="mt-1.5 flex items-center gap-1.5 text-[11px] text-amber-300/90"
      >
        <Lock className="w-3 h-3 shrink-0 text-amber-400" />
        <span>
          Managed via environment variable <code className="font-mono font-semibold text-amber-200">{info.overriddenBy}</code> and cannot be modified here.
        </span>
      </p>
    );
  };

  const OverrideBadge: React.FC<{ settingKey: string }> = ({ settingKey }) => {
    const info = overrides[settingKey];
    if (!info?.isOverridden) return null;
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm bg-amber-950/60 border border-amber-800/60 text-[10px] font-mono text-amber-300">
        <Lock className="w-2.5 h-2.5" />
        {info.overriddenBy}
      </span>
    );
  };

  const inputClass = (locked: boolean) =>
    `w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-zinc-200 text-xs focus:outline-hidden focus:border-blue-500/70 transition-colors ${
      locked ? 'opacity-60 cursor-not-allowed bg-zinc-900/50 text-zinc-400' : ''
    }`;

  const splitList = (val: string) =>
    val
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);

  const handleSave = async () => {
    setIsSaving(true);
    setSaveSuccess(false);
    setSaveError(null);

    const payload: SettingsUpdatePayload = {
 metadata: Object.fromEntries(Object.entries(metadataKeys).filter(([key,value])=>!isOverridden(key)&&value.trim())),
      library: {
        ...(!isOverridden("completed_download_root") ? { completedDownloadRoot: librarySettings.completedDownloadRoot ?? "" } : {}),
        ...(!isOverridden('library_root') ? { rootPath: librarySettings.rootPath } : {}),
        ...(!isOverridden('staging_poll_interval')
          ? { stagingPollInterval: librarySettings.stagingPollInterval }
          : {}),
        ...(!isOverridden('indexer_sync_interval')
          ? { indexerSyncInterval: librarySettings.indexerSyncInterval }
          : {}),
      },
      prowlarr: {
        ...(!isOverridden('prowlarr_url') ? { url: prowlarrSettings.url } : {}),
        ...(!isOverridden('prowlarr_api_key') && prowlarrApiKeyDraft.trim() !== ''
          ? { apiKey: prowlarrApiKeyDraft.trim() }
          : {}),
        ...(!isOverridden('prowlarr_sync_interval')
          ? { syncInterval: prowlarrSettings.syncInterval }
          : {}),
      },
      qualityProfile: {
        ...(!isOverridden('convert_ebooks') ? { convertEbooks: qualityProfile.convertEbooks } : {}),
        ...(!isOverridden('ebook_target_format') ? { ebookTargetFormat: qualityProfile.ebookTargetFormat } : {}),
        ...(!isOverridden('allowed_languages')
          ? { allowedLanguages: splitList(languagesInput) }
          : {}),
        ...(!isOverridden('allowed_formats')
          ? { allowedFormats: qualityProfile.allowedFormats }
          : {}),
        ...(!isOverridden('ebook_format_preferences')
          ? { ebookFormatPreferences: qualityProfile.ebookFormatPreferences }
          : {}),
        ...(!isOverridden('max_ebook_size_mb')
          ? { maxEbookSizeBytes: qualityProfile.maxEbookSizeBytes }
          : {}),
        ...(!isOverridden('max_file_size_mb')
          ? { maxFileSizeMb: qualityProfile.maxFileSizeMb }
          : {}),
      },
    };

    try {
      if (onSaveSettings) {
        await onSaveSettings(payload);
      } else {
        const res = await fetch('/api/settings', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!res.ok) {
          const data = await res.json().catch(() => ({}));
          throw new Error(data.error || 'Failed to save settings');
        }
      }
      setProwlarrApiKeyDraft('');
      setSaveSuccess(true);
      setTimeout(() => setSaveSuccess(false), 3000);
    } catch (err: any) {
      setSaveError(err?.message || 'Failed to save settings');
    } finally {
      setIsSaving(false);
    }
  };

  const handleSync = async () => {
    setIsSyncing(true);
    setSyncSuccess(false);
    setSaveError(null);
    try {
      if (onSyncIndexers) await onSyncIndexers();
      else {
        const response = await fetch('/api/settings/prowlarr/sync', { method: 'POST' });
        if (!response.ok) throw new Error('Prowlarr sync failed. Check the URL and API key, then try again.');
      }
      setSyncSuccess(true);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Prowlarr sync failed');
    } finally {
      setIsSyncing(false);
    }
  };

  const testConnection = async (service: string) => {
    setConnectionTests(prev => ({ ...prev, [service]: { pending: true } }));
    const payload = service === 'prowlarr'
      ? { url: prowlarrSettings.url, apiKey: prowlarrApiKeyDraft }
      : { apiKey: metadataKeys[service === 'google-books' ? 'google_books_api_key' : 'hardcover_api_key'] || '' };
    try {
      const response = await fetch(`/api/settings/${service}/test`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
      });
      const data = await response.json();
      setConnectionTests(prev => ({ ...prev, [service]: { pending: false, ok: response.ok, message: data.message } }));
    } catch {
      setConnectionTests(prev => ({ ...prev, [service]: { pending: false, ok: false, message: 'Unable to test connection. Check your network and retry.' } }));
    }
  };

  const connectionControl = (service: string, label: string) => (
    <div className="space-y-2">
      <button type="button" aria-label={`Test ${label} connection`} disabled={connectionTests[service]?.pending}
        onClick={() => void testConnection(service)}
        className="px-3.5 py-2 bg-zinc-800 hover:bg-zinc-700 disabled:opacity-60 text-zinc-100 border border-zinc-700 rounded-md text-xs font-medium focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400">
        {connectionTests[service]?.pending ? 'Testing…' : 'Test connection'}
      </button>
      <p role="status" aria-live="polite" className={`text-xs ${connectionTests[service]?.ok ? 'text-emerald-300' : 'text-rose-300'}`}>
        {connectionTests[service]?.message}
      </p>
    </div>
  );

  const toggleIndexer = (id: string) => {
    setIndexers((prev) =>
      prev.map((idx) => (idx.id === id ? { ...idx, enabled: !idx.enabled } : idx))
    );
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="border-b border-zinc-800 pb-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-white">System Settings</h1>
          <p className="text-xs text-zinc-400 mt-0.5">
            Configure external metadata indexers, library storage, and conversion quality profiles
          </p>
        </div>

        <div className="flex items-center gap-3">
          {overriddenCount > 0 && (
            <span
              data-testid="env-override-summary"
              className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-amber-950/50 border border-amber-800/60 text-xs text-amber-300"
            >
              <Lock className="w-3.5 h-3.5 text-amber-400" />
              {overriddenCount} env {overriddenCount === 1 ? 'override' : 'overrides'} active
            </span>
          )}
          <button
            type="button"
            onClick={handleSave}
            disabled={isSaving}
            data-testid="save-settings-btn"
            className="px-3.5 py-1.5 bg-blue-600 hover:bg-blue-500 disabled:bg-blue-800 disabled:opacity-70 text-white rounded-md text-xs font-medium flex items-center gap-1.5 transition-colors shadow-sm"
          >
            <Save className="w-3.5 h-3.5" />
            {isSaving ? 'Saving...' : 'Save Settings'}
          </button>
        </div>
      </div>

      {saveSuccess && (
        <div
          data-testid="settings-save-success"
          className="p-3 bg-emerald-950/60 border border-emerald-800 text-emerald-300 text-xs rounded-lg flex items-center gap-2"
        >
          <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
          Settings saved successfully.
        </div>
      )}

      {saveError && (
        <div
          data-testid="settings-save-error"
          className="p-3 bg-rose-950/60 border border-rose-800 text-rose-300 text-xs rounded-lg flex items-center gap-2"
        >
          <AlertCircle className="w-4 h-4 text-rose-400 shrink-0" />
          {saveError}
        </div>
      )}

      {/* Tabs */}
      <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5 text-xs font-medium w-full sm:w-auto overflow-x-auto">
        <button
          type="button"
          onClick={() => setActiveTab('indexers')}
          data-testid="tab-settings-indexers"
          className={`px-3 py-1.5 rounded-md flex items-center gap-2 whitespace-nowrap transition-colors ${
            activeTab === 'indexers' ? 'bg-zinc-800 text-white' : 'text-zinc-400 hover:text-zinc-200'
          }`}
        >
          <Globe className="w-3.5 h-3.5 text-blue-400" />
          Indexers & Metadata
        </button>

        <button
          type="button"
          onClick={() => setActiveTab('library')}
          data-testid="tab-settings-library"
          className={`px-3 py-1.5 rounded-md flex items-center gap-2 whitespace-nowrap transition-colors ${
            activeTab === 'library' ? 'bg-zinc-800 text-white' : 'text-zinc-400 hover:text-zinc-200'
          }`}
        >
          <HardDrive className="w-3.5 h-3.5 text-amber-400" />
          Library & Storage
        </button>
        <button
          type="button"
          onClick={() => setActiveTab('quality')}
          data-testid="tab-settings-quality"
          className={`px-3 py-1.5 rounded-md flex items-center gap-2 whitespace-nowrap transition-colors ${
            activeTab === 'quality' ? 'bg-zinc-800 text-white' : 'text-zinc-400 hover:text-zinc-200'
          }`}
        >
          <Sliders className="w-3.5 h-3.5 text-purple-400" />
          Quality & Profiles
        </button>
      </div>

      {/* TAB 1: INDEXERS & PROWLARR */}
      {activeTab === 'indexers' && (
        <div className="space-y-4">
          <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-5 space-y-5">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
              <div>
                <h3 className="text-sm font-semibold text-zinc-100 flex items-center gap-2">
                  <Globe className="w-4 h-4 text-blue-400" />
                  Prowlarr Integration
                </h3>
                <p className="text-xs text-zinc-400 mt-0.5">
                  Automatically synchronizes Torznab and Newznab indexers directly from your Prowlarr instance
                </p>
              </div>
              <button
                type="button"
                onClick={handleSync}
                data-testid="prowlarr-sync-btn"
                disabled={isSyncing}
                className="px-3.5 py-1.5 bg-zinc-800 hover:bg-zinc-700 disabled:opacity-60 text-zinc-100 border border-zinc-700 rounded-md text-xs font-medium flex items-center gap-1.5 transition-colors"
              >
                <RefreshCw className={`w-3.5 h-3.5 ${isSyncing ? 'animate-spin' : ''}`} />
                {isSyncing ? 'Syncing...' : 'Sync Prowlarr Indexers'}
              </button>
            </div>

            {connectionControl('prowlarr', 'Prowlarr')}

            <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-xs pt-2 border-t border-zinc-800/80">
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label htmlFor="prowlarr-url" className="block text-zinc-400 font-medium">
                    Prowlarr URL
                  </label>
                  <OverrideBadge settingKey="prowlarr_url" />
                </div>
                <input
                  id="prowlarr-url"
                  type="url"
                  placeholder="http://prowlarr:9696"
                  value={prowlarrSettings.url}
                  disabled={isOverridden('prowlarr_url')}
                  onChange={(e) =>
                    setProwlarrSettings((prev) => ({ ...prev, url: e.target.value }))
                  }
                  className={inputClass(isOverridden('prowlarr_url'))}
                />
                <OverrideNote settingKey="prowlarr_url" />
              </div>

              <div>
                <div className="flex items-center justify-between mb-1">
                  <label htmlFor="prowlarr-api-key" className="block text-zinc-400 font-medium">
                    Prowlarr API Key
                  </label>
                  <OverrideBadge settingKey="prowlarr_api_key" />
                </div>
                <input
                  id="prowlarr-api-key"
                  type="password"
                  placeholder={prowlarrSettings.apiKeyConfigured ? '•••••••• (configured)' : 'Enter API key'}
                  value={prowlarrApiKeyDraft}
                  disabled={isOverridden('prowlarr_api_key')}
                  onChange={(e) => setProwlarrApiKeyDraft(e.target.value)}
                  className={inputClass(isOverridden('prowlarr_api_key'))}
                />
                <OverrideNote settingKey="prowlarr_api_key" />
              </div>

              <div>
                <div className="flex items-center justify-between mb-1">
                  <label htmlFor="prowlarr-sync-interval" className="block text-zinc-400 font-medium">
                    Prowlarr Sync Interval
                  </label>
                  <OverrideBadge settingKey="prowlarr_sync_interval" />
                </div>
                <input
                  id="prowlarr-sync-interval"
                  type="text"
                  placeholder="1h0m0s"
                  value={prowlarrSettings.syncInterval}
                  disabled={isOverridden('prowlarr_sync_interval')}
                  onChange={(e) =>
                    setProwlarrSettings((prev) => ({ ...prev, syncInterval: e.target.value }))
                  }
                  className={inputClass(isOverridden('prowlarr_sync_interval'))}
                />
                <OverrideNote settingKey="prowlarr_sync_interval" />
              </div>
            </div>
          </div>

          <section aria-label="Metadata providers" className="space-y-4">
            <h2 className="text-sm font-semibold text-zinc-100">Metadata providers</h2>
            {(['google_books_api_key', 'hardcover_api_key'] as const).map(key => {
              const google = key === 'google_books_api_key';
              const label = google ? 'Google Books' : 'Hardcover';
              return (
                <section key={key} aria-label={label} className="bg-zinc-900 border border-zinc-800 rounded-xl p-5 space-y-3">
                  <h3 className="text-sm font-semibold text-zinc-100">{label}</h3>
                  <div className="max-w-lg space-y-1">
                    <label htmlFor={key} className="block text-xs text-zinc-300">{label} API key{google ? ' (optional)' : ''}</label>
                    <OverrideBadge settingKey={key} />
                    <input id={key} type="password" autoComplete="off" value={metadataKeys[key] || ''}
                      onChange={e => setMetadataKeys(prev => ({ ...prev, [key]: e.target.value }))}
                      disabled={isOverridden(key)} placeholder="Leave blank to keep current key" className={inputClass(isOverridden(key))} />
                    <OverrideNote settingKey={key} />
                    <p className="text-xs text-zinc-400">{google ? 'An API key is optional. Without one, tests use public access unless a saved key exists.' : 'Leave blank to test with your saved API key.'}</p>
                  </div>
                  {connectionControl(google ? 'google-books' : 'hardcover', label)}
                </section>
              );
            })}
          </section>

          {syncSuccess && (
            <div className="p-3 bg-emerald-950/60 border border-emerald-800 text-emerald-300 text-xs rounded-lg flex items-center gap-2">
              <CheckCircle2 className="w-4 h-4 text-emerald-400" />
              Indexers synchronized successfully with Prowlarr API.
            </div>
          )}

          {/* Indexers Table */}
          {indexers.length === 0 ? (
            <div className="py-16 text-center border border-dashed border-zinc-800 rounded-xl">
              <Globe className="w-8 h-8 text-zinc-600 mx-auto mb-2" />
              <div className="text-sm font-medium text-zinc-400">No indexers configured</div>
              <div className="text-xs text-zinc-500 mt-1">
                Synchronize with Prowlarr to automatically populate Torznab and Newznab indexers.
              </div>
            </div>
          ) : (
            <div className="bg-zinc-900 border border-zinc-800 rounded-xl overflow-hidden">
              <table className="w-full text-left text-xs border-collapse">
                <thead>
                  <tr className="border-b border-zinc-800 bg-zinc-950/50 text-zinc-400 font-medium">
                    <th className="py-3 px-4">Indexer Name</th>
                    <th className="py-3 px-4">Protocol</th>
                    <th className="py-3 px-4">Priority</th>
                    <th className="py-3 px-4">Latency</th>
                    <th className="py-3 px-4">Last Sync</th>
                    <th className="py-3 px-4 text-right">Enabled</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800/60">
                  {indexers.map((idx) => (
                    <tr key={idx.id} className="hover:bg-zinc-800/40 text-zinc-300">
                      <td className="py-3 px-4 font-semibold text-zinc-100 flex items-center gap-2">
                        <span
                          className={`w-2 h-2 rounded-full ${
                            idx.status === 'online'
                              ? 'bg-emerald-400'
                              : idx.status === 'degraded'
                              ? 'bg-amber-400'
                              : 'bg-rose-400'
                          }`}
                        />
                        {idx.name}
                      </td>
                      <td className="py-3 px-4 font-mono text-zinc-400">{idx.type}</td>
                      <td className="py-3 px-4 font-mono">{idx.priority}</td>
                      <td className="py-3 px-4 font-mono text-zinc-400">{idx.responseTimeMs}ms</td>
                      <td className="py-3 px-4 text-zinc-500">{idx.lastSync}</td>
                      <td className="py-3 px-4 text-right">
                        <input
                          type="checkbox"
                          checked={idx.enabled}
                          onChange={() => toggleIndexer(idx.id)}
                          className="rounded-sm bg-zinc-950 border-zinc-700 text-blue-500 focus:ring-0 cursor-pointer"
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* TAB 3: LIBRARY & STORAGE */}
      {activeTab === 'library' && (
        <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-6 space-y-5 max-w-2xl">
          <div>
            <h3 className="text-sm font-semibold text-zinc-100 flex items-center gap-2">
              <HardDrive className="w-4 h-4 text-amber-400" />
              Library Storage & Background Polling
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              Controls the root filesystem directory for imported books and background monitor intervals
            </p>
          </div>

          <div className="space-y-4 text-xs">
            <div>
              <label htmlFor="completed-download-root" className="block text-zinc-400 font-medium mb-1">Completed Download Directory</label>
              <input id="completed-download-root" type="text" value={librarySettings.completedDownloadRoot ?? ''}
                disabled={isOverridden('completed_download_root')}
                onChange={(e) => setLibrarySettings((prev) => ({ ...prev, completedDownloadRoot: e.target.value }))}
                className={inputClass(isOverridden('completed_download_root'))} aria-describedby="completed-download-help" />
              <p id="completed-download-help" className="mt-1.5 text-xs text-zinc-400">Configure your download clients in Prowlarr. Move finished downloads into this shared directory only after completion. Leave empty to disable ingestion.</p>
              <OverrideNote settingKey="completed_download_root" />
            </div>

            <div>
              <div className="flex items-center justify-between mb-1">
                <label htmlFor="library-root" className="block text-zinc-400 font-medium">
                  Library Root Directory
                </label>
                <OverrideBadge settingKey="library_root" />
              </div>
              <input
                id="library-root"
                type="text"
                value={librarySettings.rootPath}
                disabled={isOverridden('library_root')}
                onChange={(e) =>
                  setLibrarySettings((prev) => ({ ...prev, rootPath: e.target.value }))
                }
                className={inputClass(isOverridden('library_root'))}
              />
              <OverrideNote settingKey="library_root" />
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label htmlFor="staging-poll-interval" className="block text-zinc-400 font-medium">
                    Staging Poll Interval
                  </label>
                  <OverrideBadge settingKey="staging_poll_interval" />
                </div>
                <input
                  id="staging-poll-interval"
                  type="text"
                  value={librarySettings.stagingPollInterval}
                  disabled={isOverridden('staging_poll_interval')}
                  onChange={(e) =>
                    setLibrarySettings((prev) => ({ ...prev, stagingPollInterval: e.target.value }))
                  }
                  className={inputClass(isOverridden('staging_poll_interval'))}
                />
                <OverrideNote settingKey="staging_poll_interval" />
              </div>

              <div>
                <div className="flex items-center justify-between mb-1">
                  <label htmlFor="indexer-sync-interval" className="block text-zinc-400 font-medium">
                    Indexer Sync Interval
                  </label>
                  <OverrideBadge settingKey="indexer_sync_interval" />
                </div>
                <input
                  id="indexer-sync-interval"
                  type="text"
                  value={librarySettings.indexerSyncInterval}
                  disabled={isOverridden('indexer_sync_interval')}
                  onChange={(e) =>
                    setLibrarySettings((prev) => ({ ...prev, indexerSyncInterval: e.target.value }))
                  }
                  className={inputClass(isOverridden('indexer_sync_interval'))}
                />
                <OverrideNote settingKey="indexer_sync_interval" />
              </div>
            </div>
          </div>
        </div>
      )}

      {/* TAB 4: QUALITY PROFILES */}
      {activeTab === 'quality' && (
        <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-6 space-y-6 max-w-2xl">
          <div>
            <h3 className="text-sm font-semibold text-zinc-100 flex items-center gap-2">
              <Sliders className="w-4 h-4 text-purple-400" />
              Standard Library Quality Profile
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              Defines codec targets, file size limits, and accepted formats when evaluating and converting releases
            </p>
          </div>

          <div className="space-y-4 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label htmlFor="allowed-languages" className="block text-zinc-400 font-medium">
                    Allowed Languages (comma-separated)
                  </label>
                  <OverrideBadge settingKey="allowed_languages" />
                </div>
                <input
                  id="allowed-languages"
                  type="text"
                  value={languagesInput}
                  disabled={isOverridden('allowed_languages')}
                  onChange={(e) => setLanguagesInput(e.target.value)}
                  className={inputClass(isOverridden('allowed_languages'))}
                />
                <OverrideNote settingKey="allowed_languages" />
              </div>
            </div>

            <div className="space-y-3">
              <label className="flex items-center gap-3 text-zinc-300">
                <input type="checkbox" checked={qualityProfile.convertEbooks ?? false}
                  disabled={isOverridden('convert_ebooks')}
                  onChange={(e) => setQualityProfile((prev) => ({ ...prev, convertEbooks: e.target.checked }))} />
                Automatically convert eBooks to target format
              </label>
              <OverrideNote settingKey="convert_ebooks" />
              <label htmlFor="ebook-target-format" className="block text-zinc-400 font-medium">Target eBook format</label>
              <select id="ebook-target-format" value={qualityProfile.ebookTargetFormat ?? 'EPUB'}
                disabled={!qualityProfile.convertEbooks || isOverridden('ebook_target_format')}
                className={inputClass(!qualityProfile.convertEbooks || isOverridden('ebook_target_format'))}
                onChange={(e) => setQualityProfile((prev) => ({ ...prev, ebookTargetFormat: e.target.value as 'EPUB' | 'AZW3' }))}>
                <option value="EPUB">EPUB</option><option value="AZW3">AZW3</option>
              </select>
              <OverrideNote settingKey="ebook_target_format" />
              <p className="text-zinc-400">Convert after downloading, before adding to the library.</p>
            </div>
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <span className="block text-zinc-400 font-medium">Allowed eBook Formats</span>
                <OverrideBadge settingKey="allowed_formats" />
              </div>
              <p className="text-zinc-400 mb-3">Enable eBook formats to allow downloads. Move formats up to prefer them; disabled eBook formats are never acquired.</p>
              <OverrideBadge settingKey="ebook_format_preferences" />
              <ol aria-label="eBook format priority" className="space-y-2 max-w-lg">
                {[...(qualityProfile.ebookFormatPreferences ?? []), ...FORMAT_TYPES.filter(
                  (fmt) => !qualityProfile.ebookFormatPreferences?.includes(fmt)
                )].filter((fmt) => fmt !== 'MP3' && fmt !== 'M4B').map((fmt, index) => {
                  const isChecked = qualityProfile.allowedFormats.includes(fmt);
                  const preferences = qualityProfile.ebookFormatPreferences ?? [];
                  const ranked = index < preferences.length;
                  const move = (direction: number) => {
                    const next = [...preferences];
                    [next[index], next[index + direction]] = [next[index + direction], next[index]];
                    setQualityProfile((prev) => ({ ...prev, ebookFormatPreferences: next }));
                  };
                  return (
                    <li key={fmt} className="flex items-center gap-3 rounded-md border border-zinc-800 px-3 py-2">
                      <span className="w-6 text-zinc-400 tabular-nums">{ranked ? index + 1 : ''}</span>
                      <button
                        type="button"
                        disabled={isOverridden('allowed_formats')}
                        aria-pressed={isChecked}
                        onClick={() => setQualityProfile((prev) => ({
                          ...prev,
                          allowedFormats: isChecked
                            ? prev.allowedFormats.filter((f) => f !== fmt)
                            : [...prev.allowedFormats, fmt],
                        }))}
                        className="flex-1 text-left text-zinc-200 rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-60"
                      >
                        {fmt}<span aria-hidden="true" className="ml-3 text-zinc-400">{isChecked ? 'Enabled' : 'Disabled'}</span>
                      </button>
                      {!ranked && DEFAULT_EBOOK_PREFERENCES.includes(fmt) && (
                        <button type="button" aria-label={`Add ${fmt} to priorities`}
                          disabled={isOverridden('ebook_format_preferences')}
                          onClick={() => setQualityProfile((prev) => ({ ...prev, ebookFormatPreferences: [...(prev.ebookFormatPreferences ?? []), fmt] }))}
                          className="p-2 rounded-sm text-zinc-300 hover:bg-zinc-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-40">
                          Prioritize
                        </button>
                      )}
                      {ranked && <>
                        <button type="button" aria-label={`Move ${fmt} up`}
                          disabled={isOverridden('ebook_format_preferences') || index === 0}
                          onClick={() => move(-1)} className="p-2 rounded-sm hover:bg-zinc-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-40">
                          <ArrowUp size={16} aria-hidden="true" />
                        </button>
                        <button type="button" aria-label={`Move ${fmt} down`}
                          disabled={isOverridden('ebook_format_preferences') || index === preferences.length - 1}
                          onClick={() => move(1)} className="p-2 rounded-sm hover:bg-zinc-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400 disabled:opacity-40">
                          <ArrowDown size={16} aria-hidden="true" />
                        </button>
                      </>}
                    </li>
                  );
                })}
              </ol>
              <OverrideNote settingKey="ebook_format_preferences" />
              <OverrideNote settingKey="allowed_formats" />
            </div>

            <div className="max-w-sm">
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label htmlFor="max-file-size-mb" className="block text-zinc-400 font-medium">
                    Max File Size (MB)
                  </label>
                  <OverrideBadge settingKey="max_file_size_mb" />
                </div>
                <input
                  id="max-file-size-mb"
                  type="number"
                  min={0}
                  value={qualityProfile.maxFileSizeMb}
                  disabled={isOverridden('max_file_size_mb')}
                  onChange={(e) =>
                    setQualityProfile((prev) => ({
                      ...prev,
                      maxFileSizeMb: Number(e.target.value),
                    }))
                  }
                  className={inputClass(isOverridden('max_file_size_mb'))}
                />
                <OverrideNote settingKey="max_file_size_mb" />
              </div>
            </div>

            <div className="max-w-sm">
              <div className="flex items-center justify-between mb-1">
                <label htmlFor="max-ebook-size-mb" className="block text-zinc-400 font-medium">
                  Maximum eBook File Size (MB)
                </label>
                <OverrideBadge settingKey="max_ebook_size_mb" />
              </div>
              <input
                id="max-ebook-size-mb"
                type="number"
                min={0}
                step={1}
                value={qualityProfile.maxEbookSizeBytes / (1024 * 1024)}
                disabled={isOverridden('max_ebook_size_mb')}
                aria-describedby="max-ebook-size-help"
                onChange={(e) => setQualityProfile((prev) => ({
                  ...prev,
                  maxEbookSizeBytes: Number(e.target.value) * 1024 * 1024,
                }))}
                className={inputClass(isOverridden('max_ebook_size_mb'))}
              />
              <p id="max-ebook-size-help" className="mt-1.5 text-xs text-zinc-400">
                Reject oversized eBook downloads such as large scans and omnibus archives. Audiobooks use the general file size limit above. 0 disables the eBook limit; 1 MB = 1,048,576 bytes.
              </p>
              <OverrideNote settingKey="max_ebook_size_mb" />
            </div>

            <p className="text-zinc-400">Audiobooks always prefer M4B. MP3 downloads are automatically converted to M4B.</p>
          </div>
        </div>
      )}
    </div>
  );
};
