ALTER TABLE books RENAME COLUMN monitored TO ebook_monitored;
ALTER TABLE book_editions DROP COLUMN monitored;
