-- Upgrade failure records created by the initial acquisition implementation.
UPDATE failed_releases SET penalty = -100;
