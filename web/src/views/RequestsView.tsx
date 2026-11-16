import React, { useState, useMemo } from 'react';
import {
  Send,
  Check,
  X,
  Clock,
  BookOpen,
  Headphones,
  Search,
  Filter,
  CheckCircle2,
  AlertCircle,
  DownloadCloud,
} from 'lucide-react';
import { BookRequest, RequestType, RequestStatus } from '../types';

interface RequestsViewProps {
  requests: BookRequest[];
  onApproveRequest: (id: string) => void;
  onRejectRequest: (id: string, reason: string) => void;
  onSubmitRequest: (title: string, author: string, type: RequestType, notes?: string) => void;
  isAdminOrCurator?: boolean;
}

export const RequestsView: React.FC<RequestsViewProps> = ({
  requests,
  onApproveRequest,
  onRejectRequest,
  onSubmitRequest,
  isAdminOrCurator = true,
}) => {
  // New request form state
  const [newTitle, setNewTitle] = useState('');
  const [newAuthor, setNewAuthor] = useState('');
  const [newType, setNewType] = useState<RequestType>('both');
  const [newNotes, setNewNotes] = useState('');
  const [showSuccessToast, setShowSuccessToast] = useState(false);

  // Filter state
  const [statusFilter, setStatusFilter] = useState<'all' | RequestStatus>('all');
  const [searchFilter, setSearchFilter] = useState('');

  // Rejection modal state
  const [rejectingRequestId, setRejectingRequestId] = useState<string | null>(null);
  const [rejectReason, setRejectReason] = useState('');

  // Autocomplete mock suggestions
  const suggestions = [
    { title: 'The Chronos Paradigm', author: 'Alden Thorne' },
    { title: 'Citadel of Ashes', author: 'Sarah Vance' },
    { title: 'Orbit of the Leviathan', author: 'Elena Rostova' },
    { title: 'Hyperion Vector', author: 'Marcus Sterling' },
  ];

  const filteredSuggestions = useMemo(() => {
    if (!newTitle.trim() || newTitle.length < 2) return [];
    return suggestions.filter(
      (s) =>
        s.title.toLowerCase().includes(newTitle.toLowerCase()) ||
        s.author.toLowerCase().includes(newTitle.toLowerCase())
    );
  }, [newTitle]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newTitle.trim() || !newAuthor.trim()) return;

    onSubmitRequest(newTitle.trim(), newAuthor.trim(), newType, newNotes.trim() || undefined);
    setNewTitle('');
    setNewAuthor('');
    setNewNotes('');
    setShowSuccessToast(true);
    setTimeout(() => setShowSuccessToast(false), 3000);
  };

  const handleConfirmReject = () => {
    if (rejectingRequestId) {
      onRejectRequest(rejectingRequestId, rejectReason.trim() || 'No reason provided.');
      setRejectingRequestId(null);
      setRejectReason('');
    }
  };

  const filteredRequests = useMemo(() => {
    return requests.filter((req) => {
      const matchesStatus = statusFilter === 'all' || req.status === statusFilter;
      const matchesSearch =
        req.title.toLowerCase().includes(searchFilter.toLowerCase()) ||
        req.author.toLowerCase().includes(searchFilter.toLowerCase()) ||
        req.requestedBy.toLowerCase().includes(searchFilter.toLowerCase());
      return matchesStatus && matchesSearch;
    });
  }, [requests, statusFilter, searchFilter]);

  const getStatusBadge = (status: RequestStatus) => {
    switch (status) {
      case 'pending':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-mono px-2 py-0.5 rounded bg-amber-950/60 text-amber-300 border border-amber-800/60">
            <Clock className="w-3 h-3" /> Pending
          </span>
        );
      case 'approved':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-mono px-2 py-0.5 rounded bg-blue-950/60 text-blue-300 border border-blue-800/60">
            <Check className="w-3 h-3" /> Approved
          </span>
        );
      case 'downloading':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-mono px-2 py-0.5 rounded bg-cyan-950/60 text-cyan-300 border border-cyan-800/60">
            <DownloadCloud className="w-3 h-3" /> Downloading
          </span>
        );
      case 'available':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-mono px-2 py-0.5 rounded bg-emerald-950/60 text-emerald-300 border border-emerald-800/60">
            <CheckCircle2 className="w-3 h-3" /> In Library
          </span>
        );
      case 'rejected':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-mono px-2 py-0.5 rounded bg-rose-950/60 text-rose-300 border border-rose-800/60">
            <AlertCircle className="w-3 h-3" /> Rejected
          </span>
        );
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="border-b border-zinc-800 pb-4">
        <h1 className="text-xl font-bold tracking-tight text-white">Book Request Portal</h1>
        <p className="text-xs text-zinc-400 mt-0.5">
          Request books or audiobooks to be indexed, downloaded, and cataloged into Callimachus
        </p>
      </div>

      {/* New Request Creation Card */}
      <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-5 shadow-sm space-y-4">
        <h2 className="text-sm font-semibold text-zinc-200 flex items-center gap-2">
          <Send className="w-4 h-4 text-blue-400" />
          Submit a New Request
        </h2>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {/* Book Title with Autocomplete */}
            <div className="relative">
              <label className="block text-xs font-medium text-zinc-400 mb-1">
                Book Title *
              </label>
              <input
                type="text"
                data-testid="request-title-input"
                placeholder="e.g. The Chronos Paradigm"
                value={newTitle}
                onChange={(e) => setNewTitle(e.target.value)}
                required
                className="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-blue-500"
              />

              {/* Autocomplete dropdown */}
              {filteredSuggestions.length > 0 && (
                <div className="absolute left-0 right-0 top-full mt-1 bg-zinc-900 border border-zinc-800 rounded-md shadow-xl z-20 overflow-hidden divide-y divide-zinc-800">
                  {filteredSuggestions.map((item, idx) => (
                    <div
                      key={idx}
                      onClick={() => {
                        setNewTitle(item.title);
                        setNewAuthor(item.author);
                      }}
                      className="px-3 py-2 text-xs hover:bg-zinc-800 cursor-pointer flex justify-between items-center"
                    >
                      <span className="font-medium text-zinc-200">{item.title}</span>
                      <span className="text-zinc-500">{item.author}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Author */}
            <div>
              <label className="block text-xs font-medium text-zinc-400 mb-1">
                Author *
              </label>
              <input
                type="text"
                data-testid="request-author-input"
                placeholder="e.g. Alden Thorne"
                value={newAuthor}
                onChange={(e) => setNewAuthor(e.target.value)}
                required
                className="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-blue-500"
              />
            </div>
          </div>

          {/* Format Selection & Notes */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4 items-end">
            <div>
              <label className="block text-xs font-medium text-zinc-400 mb-1.5">
                Requested Format
              </label>
              <div className="flex bg-zinc-950 border border-zinc-800 rounded-md p-1 gap-1">
                <button
                  type="button"
                  data-testid="request-format-ebook"
                  onClick={() => setNewType('ebook')}
                  className={`flex-1 py-1.5 px-3 rounded text-xs font-medium flex items-center justify-center gap-1.5 transition-colors ${
                    newType === 'ebook'
                      ? 'bg-blue-600 text-white'
                      : 'text-zinc-400 hover:text-zinc-200'
                  }`}
                >
                  <BookOpen className="w-3.5 h-3.5" />
                  eBook Only
                </button>
                <button
                  type="button"
                  data-testid="request-format-audiobook"
                  onClick={() => setNewType('audiobook')}
                  className={`flex-1 py-1.5 px-3 rounded text-xs font-medium flex items-center justify-center gap-1.5 transition-colors ${
                    newType === 'audiobook'
                      ? 'bg-purple-600 text-white'
                      : 'text-zinc-400 hover:text-zinc-200'
                  }`}
                >
                  <Headphones className="w-3.5 h-3.5" />
                  Audiobook Only
                </button>
                <button
                  type="button"
                  data-testid="request-format-both"
                  onClick={() => setNewType('both')}
                  className={`flex-1 py-1.5 px-3 rounded text-xs font-medium flex items-center justify-center gap-1.5 transition-colors ${
                    newType === 'both'
                      ? 'bg-indigo-600 text-white'
                      : 'text-zinc-400 hover:text-zinc-200'
                  }`}
                >
                  Both Formats
                </button>
              </div>
            </div>

            <div>
              <label className="block text-xs font-medium text-zinc-400 mb-1">
                Optional Notes / ASIN / Specific Narrator
              </label>
              <div className="flex gap-2">
                <input
                  type="text"
                  placeholder="e.g. prefer chapterized M4B"
                  value={newNotes}
                  onChange={(e) => setNewNotes(e.target.value)}
                  className="flex-1 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-blue-500"
                />
                <button
                  type="submit"
                  data-testid="submit-request-btn"
                  className="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white font-medium text-xs rounded-md transition-colors shrink-0 flex items-center gap-1.5 shadow"
                >
                  <Send className="w-3.5 h-3.5" />
                  Submit Request
                </button>
              </div>
            </div>
          </div>
        </form>

        {showSuccessToast && (
          <div className="p-2.5 rounded bg-emerald-950/80 border border-emerald-800 text-emerald-300 text-xs flex items-center gap-2">
            <CheckCircle2 className="w-4 h-4 text-emerald-400" />
            Request recorded successfully! Queued for curator review.
          </div>
        )}
      </div>

      {/* Requests Queue Table */}
      <div className="space-y-4">
        {/* Filter bar */}
        <div className="flex flex-col sm:flex-row items-center justify-between gap-3">
          <div className="flex items-center gap-2 overflow-x-auto w-full sm:w-auto pb-1 sm:pb-0">
            <span className="text-xs text-zinc-500 flex items-center gap-1">
              <Filter className="w-3.5 h-3.5" /> Status:
            </span>
            <div className="flex bg-zinc-900 border border-zinc-800 rounded-lg p-0.5 text-xs font-medium">
              {(['all', 'pending', 'approved', 'downloading', 'available', 'rejected'] as const).map(
                (st) => (
                  <button
                    key={st}
                    onClick={() => setStatusFilter(st)}
                    data-testid={`filter-status-${st}`}
                    className={`px-2.5 py-1 rounded-md capitalize transition-colors ${
                      statusFilter === st
                        ? 'bg-zinc-800 text-white'
                        : 'text-zinc-400 hover:text-zinc-200'
                    }`}
                  >
                    {st}
                  </button>
                )
              )}
            </div>
          </div>

          <div className="relative w-full sm:w-64">
            <Search className="w-3.5 h-3.5 text-zinc-500 absolute left-3 top-2.5" />
            <input
              type="text"
              placeholder="Search requests..."
              value={searchFilter}
              onChange={(e) => setSearchFilter(e.target.value)}
              className="w-full pl-8 pr-3 py-1.5 bg-zinc-900 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-zinc-700"
            />
          </div>
        </div>

        {/* Requests Table */}
        <div className="bg-zinc-900 border border-zinc-800 rounded-xl overflow-hidden shadow-sm">
          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="border-b border-zinc-800 bg-zinc-950/50 text-zinc-400 font-medium">
                <th className="py-3 px-4">Title & Author</th>
                <th className="py-3 px-4">Format</th>
                <th className="py-3 px-4">Requested By</th>
                <th className="py-3 px-4">Status</th>
                <th className="py-3 px-4">Requested Date</th>
                {isAdminOrCurator && <th className="py-3 px-4 text-right">Review Actions</th>}
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800/60">
              {filteredRequests.length === 0 ? (
                <tr>
                  <td colSpan={6} className="py-12 text-center text-zinc-500">
                    No requests found matching current filters.
                  </td>
                </tr>
              ) : (
                filteredRequests.map((req) => (
                  <tr key={req.id} data-testid={`request-row-${req.id}`} className="hover:bg-zinc-800/40 text-zinc-300">
                    <td className="py-3 px-4">
                      <div className="font-semibold text-zinc-100">{req.title}</div>
                      <div className="text-zinc-400 text-[11px]">{req.author}</div>
                      {req.rejectionReason && (
                        <div className="text-[11px] text-rose-400 mt-1">
                          Reason: {req.rejectionReason}
                        </div>
                      )}
                    </td>
                    <td className="py-3 px-4">
                      <span className="text-[10px] font-mono uppercase px-2 py-0.5 rounded bg-zinc-800 text-zinc-300 border border-zinc-700">
                        {req.requestType}
                      </span>
                    </td>
                    <td className="py-3 px-4 font-mono text-[11px] text-zinc-400">
                      {req.requestedBy}
                    </td>
                    <td className="py-3 px-4">{getStatusBadge(req.status)}</td>
                    <td className="py-3 px-4 text-zinc-500 text-[11px]">
                      {new Date(req.requestedAt).toLocaleDateString([], {
                        month: 'short',
                        day: 'numeric',
                      })}
                    </td>
                    {isAdminOrCurator && (
                      <td className="py-3 px-4 text-right">
                        {req.status === 'pending' ? (
                          <div className="flex items-center justify-end gap-2">
                            <button
                              onClick={() => onApproveRequest(req.id)}
                              data-testid={`approve-btn-${req.id}`}
                              className="px-2.5 py-1 bg-emerald-600/20 hover:bg-emerald-600/30 text-emerald-400 border border-emerald-600/40 rounded text-xs font-medium transition-colors flex items-center gap-1"
                            >
                              <Check className="w-3.5 h-3.5" />
                              Approve
                            </button>
                            <button
                              onClick={() => {
                                setRejectingRequestId(req.id);
                                setRejectReason('');
                              }}
                              data-testid={`reject-btn-${req.id}`}
                              className="px-2.5 py-1 bg-rose-600/20 hover:bg-rose-600/30 text-rose-400 border border-rose-600/40 rounded text-xs font-medium transition-colors flex items-center gap-1"
                            >
                              <X className="w-3.5 h-3.5" />
                              Reject
                            </button>
                          </div>
                        ) : (
                          <span className="text-xs text-zinc-600">—</span>
                        )}
                      </td>
                    )}
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Reject Modal */}
      {rejectingRequestId && (
        <div
          role="dialog"
          aria-modal="true"
          aria-label="Reject Request"
          className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-sm animate-in fade-in duration-150"
        >
          <div className="w-full max-w-md bg-zinc-900 border border-zinc-800 rounded-xl p-5 shadow-2xl space-y-4">
            <div className="flex items-center justify-between border-b border-zinc-800 pb-3">
              <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
                <AlertCircle className="w-4 h-4 text-rose-400" />
                Reject Book Request
              </h3>
              <button
                onClick={() => setRejectingRequestId(null)}
                className="text-zinc-500 hover:text-zinc-300"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            <div>
              <label className="block text-xs font-medium text-zinc-400 mb-1.5">
                Reason for Rejection *
              </label>
              <textarea
                data-testid="reject-reason-input"
                rows={3}
                placeholder="e.g. Out of scope, unreleased edition, or invalid author metadata..."
                value={rejectReason}
                onChange={(e) => setRejectReason(e.target.value)}
                className="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-md text-xs text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-rose-500"
              />
            </div>

            <div className="flex justify-end gap-2 pt-2">
              <button
                onClick={() => setRejectingRequestId(null)}
                className="px-3 py-1.5 text-xs text-zinc-400 hover:text-zinc-200 bg-zinc-800 rounded-md transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleConfirmReject}
                data-testid="confirm-reject-btn"
                className="px-3 py-1.5 text-xs font-medium text-white bg-rose-600 hover:bg-rose-500 rounded-md transition-colors"
              >
                Confirm Rejection
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
