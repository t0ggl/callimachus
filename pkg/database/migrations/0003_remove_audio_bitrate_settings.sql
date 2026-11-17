-- Bitrate is fixed at 128 kbps AAC; existing M4B audio is copied unchanged.
DELETE FROM settings WHERE key IN ('audio_min_bitrate_kbps', 'audio_target_bitrate_kbps', 'worker_bitrate');
