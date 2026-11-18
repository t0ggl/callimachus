-- Preserve the old generic cap as the audiobook-only limit. An explicitly saved
-- new key wins if both exist; the ebook limit is unchanged.
INSERT INTO settings (key, value, updated_at)
SELECT 'max_audiobook_size_mb', value, updated_at
FROM settings WHERE key = 'max_file_size_mb'
ON CONFLICT(key) DO NOTHING;
DELETE FROM settings WHERE key = 'max_file_size_mb';
