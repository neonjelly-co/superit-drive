-- An upload-only link stops after a number of files as well as a number of bytes: empty files never filled it, and
-- nothing bounded how many it took.
--
-- taken_files is how many files the link has taken in since it was made or last reset, counted and reset with taken_in.
-- Like taken_in it is not in the column list of the trigger that counts access changes (0012), so a file sent in does
-- not make a copy of the index.
ALTER TABLE links ADD COLUMN taken_files INTEGER NOT NULL DEFAULT 0 CHECK (taken_files >= 0);
