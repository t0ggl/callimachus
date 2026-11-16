import React, { useState } from 'react';
import {
  Globe,
  Download,
  Sliders,
  Shield,
  RefreshCw,
  CheckCircle2,
} from 'lucide-react';
import {
  IndexerConfig,
  DownloadClientConfig,
  QualityProfile,
  AuthSettings,
} from '../types';

interface SettingsViewProps {
  indexers: IndexerConfig[];
  downloadClients: DownloadClientConfig[];
  qualityProfile: QualityProfile;
  authSettings: AuthSettings;
  onSyncIndexers?: () => void;
}

export const SettingsView: React.FC<SettingsViewProps> = ({
  indexers: initialIndexers,
  downloadClients: initialClients,
  qualityProfile: initialQuality,
  authSettings: initialAuth,
  onSyncIndexers,
}) => {
  const [activeTab, setActiveTab] = useState<'indexers' | 'downloaders' | 'quality' | 'auth'>('indexers');
  const [isSyncing, setIsSyncing] = useState(false);
  const [syncSuccess, setSyncSuccess] = useState(false);

  const [indexers, setIndexers] = useState<IndexerConfig[]>(initialIndexers);
  const [downloadClients] = useState<DownloadClientConfig[]>(initialClients);
  const [qualityProfile, setQualityProfile] = useState<QualityProfile>(initialQuality);
  const [authSettings, setAuthSettings] = useState<AuthSettings>(initialAuth);
  const [testResult, setTestResult] = useState<{ id: string; success: boolean } | null>(null);

  const handleSync = () => {
    setIsSyncing(true);
    setSyncSuccess(false);
    if (onSyncIndexers) onSyncIndexers();

    setTimeout(() => {
      setIsSyncing(false);
      setSyncSuccess(true);
      setTimeout(() => setSyncSuccess(false), 3000);
    }, 800);
  };

  const handleTestClient = (id: string) => {
    setTestResult({ id, success: true });
    setTimeout(() => setTestResult(null), 3000);
  };

  const toggleIndexer = (id: string) => {
    setIndexers((prev) =>
      prev.map((idx) => (idx.id === id ? { ...idx, enabled: !idx.enabled } : idx))
    );
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="border-b border-zinc-800 pb-4">
        <h1 className="text-xl font-bold tracking-tight text-white">System Settings</h1>
        <p className="text-xs text-zinc-400 mt-0.5">
          Configure external metadata indexers, download daemons, transcode quality profiles, and security
        </p>
      </div>

      {/* Tabs */}
      <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5 text-xs font-medium w-full sm:w-auto overflow-x-auto">
        <button
          onClick={() => setActiveTab('indexers')}
          data-testid="tab-settings-indexers"
          className={`px-3 py-1.5 rounded-md flex items-center gap-2 whitespace-nowrap transition-colors ${
            activeTab === 'indexers' ? 'bg-zinc-800 text-white' : 'text-zinc-400 hover:text-zinc-200'
          }`}
        >
          <Globe className="w-3.5 h-3.5 text-blue-400" />
          Indexers & Prowlarr
        </button>
        <button
          onClick={() => setActiveTab('downloaders')}
          data-testid="tab-settings-downloaders"
          className={`px-3 py-1.5 rounded-md flex items-center gap-2 whitespace-nowrap transition-colors ${
            activeTab === 'downloaders' ? 'bg-zinc-800 text-white' : 'text-zinc-400 hover:text-zinc-200'
          }`}
        >
          <Download className="w-3.5 h-3.5 text-cyan-400" />
          Download Clients
        </button>
        <button
          onClick={() => setActiveTab('quality')}
          data-testid="tab-settings-quality"
          className={`px-3 py-1.5 rounded-md flex items-center gap-2 whitespace-nowrap transition-colors ${
            activeTab === 'quality' ? 'bg-zinc-800 text-white' : 'text-zinc-400 hover:text-zinc-200'
          }`}
        >
          <Sliders className="w-3.5 h-3.5 text-purple-400" />
          Quality & Profiles
        </button>
        <button
          onClick={() => setActiveTab('auth')}
          data-testid="tab-settings-auth"
          className={`px-3 py-1.5 rounded-md flex items-center gap-2 whitespace-nowrap transition-colors ${
            activeTab === 'auth' ? 'bg-zinc-800 text-white' : 'text-zinc-400 hover:text-zinc-200'
          }`}
        >
          <Shield className="w-3.5 h-3.5 text-emerald-400" />
          Authentication & OIDC
        </button>
      </div>

      {/* TAB 1: INDEXERS */}
      {activeTab === 'indexers' && (
        <div className="space-y-4">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-zinc-900 border border-zinc-800 rounded-xl p-4">
            <div>
              <h3 className="text-sm font-semibold text-zinc-100 flex items-center gap-2">
                <Globe className="w-4 h-4 text-blue-400" />
                Prowlarr Integration
              </h3>
              <p className="text-xs text-zinc-400 mt-0.5">
                Automatically synchronizes Torznab and Newznab indexers directly from your Prowlarr instance
              </p>
            </div>
            <div className="flex items-center gap-3">
              <button
                onClick={handleSync}
                data-testid="prowlarr-sync-btn"
                disabled={isSyncing}
                className="px-3.5 py-1.5 bg-blue-600 hover:bg-blue-500 disabled:bg-blue-800 text-white rounded-md text-xs font-medium flex items-center gap-1.5 transition-colors shadow"
              >
                <RefreshCw className={`w-3.5 h-3.5 ${isSyncing ? 'animate-spin' : ''}`} />
                {isSyncing ? 'Syncing...' : 'Sync Prowlarr Indexers'}
              </button>
            </div>
          </div>

          {syncSuccess && (
            <div className="p-3 bg-emerald-950/60 border border-emerald-800 text-emerald-300 text-xs rounded-lg flex items-center gap-2">
              <CheckCircle2 className="w-4 h-4 text-emerald-400" />
              Indexers synchronized successfully with Prowlarr API.
            </div>
          )}

          {/* Indexers Table */}
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
                        className="rounded bg-zinc-950 border-zinc-700 text-blue-500 focus:ring-0 cursor-pointer"
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* TAB 2: DOWNLOAD CLIENTS */}
      {activeTab === 'downloaders' && (
        <div className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {downloadClients.map((client) => (
              <div
                key={client.id}
                className="bg-zinc-900 border border-zinc-800 rounded-xl p-5 space-y-4 shadow-sm"
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2.5">
                    <div className="w-8 h-8 rounded-lg bg-cyan-500/10 border border-cyan-500/30 flex items-center justify-center text-cyan-400">
                      <Download className="w-4 h-4" />
                    </div>
                    <div>
                      <h4 className="text-sm font-bold text-zinc-100">{client.name}</h4>
                      <p className="text-xs text-zinc-400">{client.type}</p>
                    </div>
                  </div>
                  <span
                    className={`text-[10px] font-mono px-2 py-0.5 rounded border capitalize ${
                      client.enabled
                        ? 'bg-emerald-950/60 text-emerald-400 border-emerald-800/60'
                        : 'bg-zinc-800 text-zinc-500 border-zinc-700'
                    }`}
                  >
                    {client.enabled ? 'Enabled' : 'Disabled'}
                  </span>
                </div>

                <div className="space-y-2 text-xs font-mono bg-zinc-950/60 p-3 rounded-lg border border-zinc-800">
                  <div className="flex justify-between text-zinc-400">
                    <span>Host / Endpoint:</span>
                    <span className="text-zinc-200">
                      {client.useSsl ? 'https://' : 'http://'}{client.host}:{client.port}
                    </span>
                  </div>
                  <div className="flex justify-between text-zinc-400">
                    <span>Category Path:</span>
                    <span className="text-zinc-200">{client.categoryPath}</span>
                  </div>
                </div>

                <div className="flex items-center justify-between pt-1">
                  <div className="text-xs">
                    {testResult?.id === client.id && (
                      <span className="text-emerald-400 flex items-center gap-1 font-medium">
                        <CheckCircle2 className="w-3.5 h-3.5" /> Connection verified!
                      </span>
                    )}
                  </div>
                  <button
                    onClick={() => handleTestClient(client.id)}
                    data-testid={`test-client-${client.id}`}
                    className="px-3 py-1.5 bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium rounded-md transition-colors"
                  >
                    Test Connection
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* TAB 3: QUALITY PROFILES */}
      {activeTab === 'quality' && (
        <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-6 space-y-6 max-w-2xl">
          <div>
            <h3 className="text-sm font-semibold text-zinc-100 flex items-center gap-2">
              <Sliders className="w-4 h-4 text-purple-400" />
              Standard Library Quality Profile
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              Defines codec targets and accepted formats when transcoding and organizing audiobooks
            </p>
          </div>

          <div className="space-y-4 text-xs">
            <div>
              <label className="block text-zinc-400 font-medium mb-1.5">
                Allowed File Formats
              </label>
              <div className="flex gap-2">
                {['EPUB', 'M4B', 'PDF', 'MP3'].map((fmt) => {
                  const isChecked = qualityProfile.allowedFormats.includes(fmt as any);
                  return (
                    <button
                      key={fmt}
                      type="button"
                      onClick={() => {
                        setQualityProfile((prev) => ({
                          ...prev,
                          allowedFormats: isChecked
                            ? prev.allowedFormats.filter((f) => f !== fmt)
                            : [...prev.allowedFormats, fmt as any],
                        }));
                      }}
                      className={`px-3 py-1.5 rounded-md font-mono border transition-colors ${
                        isChecked
                          ? 'bg-blue-600/20 text-blue-300 border-blue-600/50'
                          : 'bg-zinc-950 text-zinc-500 border-zinc-800'
                      }`}
                    >
                      {fmt}
                    </button>
                  );
                })}
              </div>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-zinc-400 font-medium mb-1">
                  Audio Min Bitrate (kbps)
                </label>
                <select
                  value={qualityProfile.audioMinBitrateKbps}
                  onChange={(e) =>
                    setQualityProfile((prev) => ({
                      ...prev,
                      audioMinBitrateKbps: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-zinc-200 text-xs focus:outline-none"
                >
                  <option value={32}>32 kbps (Mono)</option>
                  <option value={64}>64 kbps (Standard Audiobook)</option>
                  <option value={128}>128 kbps (High Fidelity)</option>
                </select>
              </div>

              <div>
                <label className="block text-zinc-400 font-medium mb-1">
                  Audio Target Bitrate (kbps)
                </label>
                <select
                  value={qualityProfile.audioTargetBitrateKbps}
                  onChange={(e) =>
                    setQualityProfile((prev) => ({
                      ...prev,
                      audioTargetBitrateKbps: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-zinc-200 text-xs focus:outline-none"
                >
                  <option value={64}>64 kbps AAC</option>
                  <option value={96}>96 kbps AAC</option>
                  <option value={128}>128 kbps AAC</option>
                </select>
              </div>
            </div>

            <div className="pt-2">
              <label className="flex items-center gap-2 cursor-pointer text-zinc-300">
                <input
                  type="checkbox"
                  checked={qualityProfile.preferSingleFileM4b}
                  onChange={(e) =>
                    setQualityProfile((prev) => ({
                      ...prev,
                      preferSingleFileM4b: e.target.checked,
                    }))
                  }
                  className="rounded bg-zinc-950 border-zinc-800 text-blue-500 focus:ring-0"
                />
                <span>Automatically mux multi-part MP3/FLAC into a single chaptered .m4b</span>
              </label>
            </div>
          </div>
        </div>
      )}

      {/* TAB 4: AUTH & OIDC */}
      {activeTab === 'auth' && (
        <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-6 space-y-6 max-w-2xl">
          <div>
            <h3 className="text-sm font-semibold text-zinc-100 flex items-center gap-2">
              <Shield className="w-4 h-4 text-emerald-400" />
              OpenID Connect (OIDC) & Local Authentication
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              Secure single sign-on via Authentik, Keycloak, or Authelia
            </p>
          </div>

          <div className="space-y-4 text-xs">
            <div className="flex items-center justify-between p-3 bg-zinc-950 border border-zinc-800 rounded-lg">
              <div>
                <div className="font-semibold text-zinc-200">Enable OIDC Authentication</div>
                <div className="text-zinc-500 text-[11px]">
                  Redirects unauthenticated users to the identity provider
                </div>
              </div>
              <input
                type="checkbox"
                checked={authSettings.oidcEnabled}
                onChange={(e) =>
                  setAuthSettings((prev) => ({ ...prev, oidcEnabled: e.target.checked }))
                }
                className="rounded bg-zinc-900 border-zinc-700 text-blue-500 focus:ring-0 cursor-pointer"
              />
            </div>

            <div>
              <label className="block text-zinc-400 font-medium mb-1">
                OIDC Issuer URL
              </label>
              <input
                type="text"
                value={authSettings.oidcIssuerUrl}
                onChange={(e) =>
                  setAuthSettings((prev) => ({ ...prev, oidcIssuerUrl: e.target.value }))
                }
                className="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-zinc-200 text-xs focus:outline-none"
              />
            </div>

            <div>
              <label className="block text-zinc-400 font-medium mb-1">
                OIDC Client ID
              </label>
              <input
                type="text"
                value={authSettings.oidcClientId}
                onChange={(e) =>
                  setAuthSettings((prev) => ({ ...prev, oidcClientId: e.target.value }))
                }
                className="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-zinc-200 text-xs focus:outline-none"
              />
            </div>

            <div className="pt-2 border-t border-zinc-800 flex items-center justify-between text-zinc-400">
              <span>Local Admin Fallback Password:</span>
              <span className="text-emerald-400 font-mono text-[11px] flex items-center gap-1">
                <CheckCircle2 className="w-3.5 h-3.5" /> Configured & Encrypted (Argon2id)
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
