import React, { useState } from 'react';
import {
  Zap,
  DownloadCloud,
  FileCheck2,
  Clock,
  Gauge,
  Radio,
  Pause,
  Play,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  FileAudio,
} from 'lucide-react';
import { TranscodeJob, DownloadItem, ImportLog, TranscodeStage } from '../types';

interface ActivityViewProps {
  transcodeJobs: TranscodeJob[];
  downloads: DownloadItem[];
  logs: ImportLog[];
  connectionStatus: 'connected' | 'connecting' | 'disconnected';
  onUpdateJobStatus?: (jobId: string, status: TranscodeJob['status']) => void;
  onUpdateDownloadStatus?: (downloadId: string, status: DownloadItem['status']) => void;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

function formatDuration(seconds: number): string {
  if (seconds <= 0) return '0s';
  const hrs = Math.floor(seconds / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  if (hrs > 0) return `${hrs}h ${mins}m`;
  if (mins > 0) return `${mins}m ${secs}s`;
  return `${secs}s`;
}

const STAGE_ORDER: TranscodeStage[] = ['probing', 'encoding', 'muxing', 'moving'];

export const ActivityView: React.FC<ActivityViewProps> = ({
  transcodeJobs,
  downloads,
  logs,
  connectionStatus,
  onUpdateJobStatus,
  onUpdateDownloadStatus,
}) => {
  const [activeSection, setActiveSection] = useState<'transcode' | 'downloads' | 'history'>('transcode');

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-zinc-800 pb-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-white flex items-center gap-2.5">
            Activity & Queues
            {/* Real-time SSE indicator */}
            <span
              className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] font-mono border ${
                connectionStatus === 'connected'
                  ? 'bg-emerald-950/60 text-emerald-400 border-emerald-800/60'
                  : connectionStatus === 'connecting'
                  ? 'bg-amber-950/60 text-amber-400 border-amber-800/60 animate-pulse'
                  : 'bg-zinc-800 text-zinc-400 border-zinc-700'
              }`}
            >
              <Radio className="w-3 h-3" />
              {connectionStatus === 'connected' ? 'SSE Live' : connectionStatus}
            </span>
          </h1>
          <p className="text-xs text-zinc-400 mt-0.5">
            Live audio transcoding pipelines, active indexer downloads, and file ingestion logs
          </p>
        </div>

        {/* Section Navigation Tabs */}
        <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5 text-xs font-medium">
          <button
            onClick={() => setActiveSection('transcode')}
            data-testid="tab-activity-transcode"
            className={`px-3 py-1.5 rounded-md flex items-center gap-2 transition-colors ${
              activeSection === 'transcode'
                ? 'bg-zinc-800 text-white shadow-sm'
                : 'text-zinc-400 hover:text-zinc-200'
            }`}
          >
            <Zap className="w-3.5 h-3.5 text-amber-400" />
            Transcode Queue ({transcodeJobs.length})
          </button>
          <button
            onClick={() => setActiveSection('downloads')}
            data-testid="tab-activity-downloads"
            className={`px-3 py-1.5 rounded-md flex items-center gap-2 transition-colors ${
              activeSection === 'downloads'
                ? 'bg-zinc-800 text-white shadow-sm'
                : 'text-zinc-400 hover:text-zinc-200'
            }`}
          >
            <DownloadCloud className="w-3.5 h-3.5 text-cyan-400" />
            Downloads ({downloads.length})
          </button>
          <button
            onClick={() => setActiveSection('history')}
            data-testid="tab-activity-history"
            className={`px-3 py-1.5 rounded-md flex items-center gap-2 transition-colors ${
              activeSection === 'history'
                ? 'bg-zinc-800 text-white shadow-sm'
                : 'text-zinc-400 hover:text-zinc-200'
            }`}
          >
            <FileCheck2 className="w-3.5 h-3.5 text-emerald-400" />
            Import History
          </button>
        </div>
      </div>

      {/* SECTION 1: TRANSCODE QUEUE */}
      {activeSection === 'transcode' && (
        <div className="space-y-4">
          {transcodeJobs.length === 0 ? (
            <div className="py-16 text-center border border-dashed border-zinc-800 rounded-xl">
              <Zap className="w-8 h-8 text-zinc-600 mx-auto mb-2" />
              <div className="text-sm font-medium text-zinc-400">Transcode queue is idle</div>
              <div className="text-xs text-zinc-500 mt-1">
                New multi-part audiobook jobs will appear here automatically.
              </div>
            </div>
          ) : (
            transcodeJobs.map((job) => {
              const currentStageIndex = STAGE_ORDER.indexOf(job.stage);

              return (
                <div
                  key={job.id}
                  data-testid={`transcode-job-${job.id}`}
                  className="bg-zinc-900 border border-zinc-800 rounded-xl p-5 space-y-4 shadow-sm"
                >
                  <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                    <div className="flex items-center gap-3">
                      <div className="w-9 h-9 rounded-lg bg-amber-500/10 border border-amber-500/20 flex items-center justify-center text-amber-400">
                        <FileAudio className="w-5 h-5" />
                      </div>
                      <div>
                        <div className="flex items-center gap-2">
                          <h3 className="text-sm font-bold text-zinc-100">{job.bookTitle}</h3>
                          <span className="text-[10px] font-mono font-medium px-2 py-0.5 rounded bg-zinc-800 border border-zinc-700 text-zinc-300">
                            {job.targetFormat}
                          </span>
                        </div>
                        <p className="text-xs text-zinc-400 mt-0.5">
                          {job.authorName} • {job.sourceFilesCount} source audio files
                        </p>
                      </div>
                    </div>

                    <div className="flex items-center gap-2">
                      <span className="text-xs font-mono text-zinc-400">
                        Status: <span className="text-zinc-200 capitalize font-semibold">{job.status}</span>
                      </span>
                      {onUpdateJobStatus && (
                        <button
                          onClick={() =>
                            onUpdateJobStatus(
                              job.id,
                              job.status === 'processing' ? 'paused' : 'processing'
                            )
                          }
                          className="p-1.5 bg-zinc-800 hover:bg-zinc-700 rounded text-zinc-300 transition-colors"
                          title={job.status === 'processing' ? 'Pause job' : 'Resume job'}
                        >
                          {job.status === 'processing' ? (
                            <Pause className="w-3.5 h-3.5" />
                          ) : (
                            <Play className="w-3.5 h-3.5" />
                          )}
                        </button>
                      )}
                    </div>
                  </div>

                  {/* Stage Progress Indicators */}
                  <div className="grid grid-cols-4 gap-2 pt-1">
                    {STAGE_ORDER.map((stageName, sIdx) => {
                      const isPast = sIdx < currentStageIndex;
                      const isCurrent = sIdx === currentStageIndex;
                      return (
                        <div
                          key={stageName}
                          className={`text-center py-1.5 px-2 rounded border text-xs font-mono font-medium uppercase tracking-wider transition-colors ${
                            isPast
                              ? 'bg-emerald-950/40 text-emerald-400 border-emerald-800/50'
                              : isCurrent
                              ? 'bg-amber-950/60 text-amber-300 border-amber-600/70 font-bold'
                              : 'bg-zinc-950 text-zinc-500 border-zinc-800/80'
                          }`}
                        >
                          {stageName}
                        </div>
                      );
                    })}
                  </div>

                  {/* Progress Bar */}
                  <div className="space-y-1.5">
                    <div className="flex items-center justify-between text-xs font-mono">
                      <span className="text-zinc-400">
                        {job.stage.toUpperCase()}: {job.progressPercent.toFixed(1)}%
                      </span>
                      <div className="flex items-center gap-4 text-zinc-400">
                        <span className="flex items-center gap-1">
                          <Gauge className="w-3.5 h-3.5 text-zinc-500" />
                          {job.bitrateKbps} kbps
                        </span>
                        <span className="flex items-center gap-1">
                          <Clock className="w-3.5 h-3.5 text-zinc-500" />
                          ETA: {formatDuration(job.etaSeconds)}
                        </span>
                      </div>
                    </div>

                    <div className="w-full bg-zinc-950 h-2.5 rounded-full overflow-hidden border border-zinc-800">
                      <div
                        data-testid={`transcode-progress-bar-${job.id}`}
                        className="bg-amber-500 h-full transition-all duration-300 rounded-full"
                        style={{ width: `${job.progressPercent}%` }}
                      />
                    </div>
                  </div>
                </div>
              );
            })
          )}
        </div>
      )}

      {/* SECTION 2: DOWNLOAD QUEUE */}
      {activeSection === 'downloads' && (
        <div className="space-y-4">
          {downloads.length === 0 ? (
            <div className="py-16 text-center border border-dashed border-zinc-800 rounded-xl">
              <DownloadCloud className="w-8 h-8 text-zinc-600 mx-auto mb-2" />
              <div className="text-sm font-medium text-zinc-400">No active downloads</div>
            </div>
          ) : (
            downloads.map((dl) => (
              <div
                key={dl.id}
                data-testid={`download-item-${dl.id}`}
                className="bg-zinc-900 border border-zinc-800 rounded-xl p-5 space-y-3"
              >
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                  <div>
                    <h3 className="text-sm font-bold text-zinc-100">{dl.title}</h3>
                    <div className="flex items-center gap-2 mt-1 text-xs text-zinc-400">
                      <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-zinc-800 border border-zinc-700 text-cyan-300">
                        {dl.category}
                      </span>
                      <span>via {dl.clientType}</span>
                    </div>
                  </div>

                  <div className="flex items-center gap-3">
                    <span
                      className={`text-xs font-mono font-medium px-2 py-0.5 rounded border capitalize ${
                        dl.status === 'downloading'
                          ? 'bg-cyan-950/60 text-cyan-400 border-cyan-800/60'
                          : dl.status === 'paused'
                          ? 'bg-amber-950/60 text-amber-400 border-amber-800/60'
                          : 'bg-emerald-950/60 text-emerald-400 border-emerald-800/60'
                      }`}
                    >
                      {dl.status}
                    </span>
                    {onUpdateDownloadStatus && dl.status !== 'completed' && (
                      <button
                        onClick={() =>
                          onUpdateDownloadStatus(
                            dl.id,
                            dl.status === 'downloading' ? 'paused' : 'downloading'
                          )
                        }
                        className="p-1.5 bg-zinc-800 hover:bg-zinc-700 rounded text-zinc-300 transition-colors"
                      >
                        {dl.status === 'downloading' ? (
                          <Pause className="w-3.5 h-3.5" />
                        ) : (
                          <Play className="w-3.5 h-3.5" />
                        )}
                      </button>
                    )}
                  </div>
                </div>

                {/* Progress bar */}
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between text-xs font-mono text-zinc-400">
                    <span>
                      {dl.progressPercent.toFixed(1)}% • {formatBytes(dl.downloadedSizeBytes)} / {formatBytes(dl.totalSizeBytes)}
                    </span>
                    <div className="flex items-center gap-4">
                      <span>{formatBytes(dl.downloadSpeedBps)}/s</span>
                      <span>ETA: {formatDuration(dl.etaSeconds)}</span>
                    </div>
                  </div>
                  <div className="w-full bg-zinc-950 h-2 rounded-full overflow-hidden border border-zinc-800">
                    <div
                      className="bg-cyan-500 h-full transition-all duration-300 rounded-full"
                      style={{ width: `${dl.progressPercent}%` }}
                    />
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      )}

      {/* SECTION 3: IMPORT HISTORY LOG */}
      {activeSection === 'history' && (
        <div className="bg-zinc-900 border border-zinc-800 rounded-xl overflow-hidden">
          <div className="px-4 py-3 border-b border-zinc-800 bg-zinc-950/50 flex items-center justify-between">
            <h3 className="text-xs font-bold text-zinc-200 uppercase tracking-wider">
              Post-Processing & Ingestion Audit Log
            </h3>
            <span className="text-[11px] font-mono text-zinc-500">{logs.length} entries</span>
          </div>

          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="border-b border-zinc-800 bg-zinc-950/30 text-zinc-400 font-medium">
                <th className="py-2.5 px-4">Status</th>
                <th className="py-2.5 px-4">Action</th>
                <th className="py-2.5 px-4">Target Item</th>
                <th className="py-2.5 px-4">Details</th>
                <th className="py-2.5 px-4 text-right">Timestamp</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800/60 font-mono text-[11px]">
              {logs.map((log) => (
                <tr key={log.id} className="hover:bg-zinc-800/40 text-zinc-300">
                  <td className="py-3 px-4">
                    {log.status === 'success' && (
                      <span className="inline-flex items-center gap-1 text-emerald-400">
                        <CheckCircle2 className="w-3.5 h-3.5" /> OK
                      </span>
                    )}
                    {log.status === 'warning' && (
                      <span className="inline-flex items-center gap-1 text-amber-400">
                        <AlertTriangle className="w-3.5 h-3.5" /> WARN
                      </span>
                    )}
                    {log.status === 'error' && (
                      <span className="inline-flex items-center gap-1 text-rose-400">
                        <XCircle className="w-3.5 h-3.5" /> ERR
                      </span>
                    )}
                  </td>
                  <td className="py-3 px-4 uppercase text-zinc-400">{log.action}</td>
                  <td className="py-3 px-4 font-sans font-medium text-zinc-100">{log.itemName}</td>
                  <td className="py-3 px-4 text-zinc-400 font-sans">{log.details}</td>
                  <td className="py-3 px-4 text-right text-zinc-500">
                    {new Date(log.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};
