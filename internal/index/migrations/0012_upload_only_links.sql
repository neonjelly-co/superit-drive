-- Upload-only links: a folder's public link can let visitors put new files into it, and see nothing of what is there.
--
-- links is rebuilt (SQLite cannot change a table's checks in place), with:
--
--   - a second kind, 'upload-only', for a link on a folder (the Drive makes it on a folder only; internal/access lets
--     nothing through one on anything else);
--   - taken_in: how many bytes of files the link has taken in since it was made or last reset. It stops taking files
--     once that would pass its limit (10 GB), until whoever may manage it resets it to 0. It is left out of the column
--     list of the trigger that counts access changes, so a file sent in does not make a copy of the index; a copy carries
--     the count as it was then.
--
-- Its ids carry on from where the old table's left off (sqlite_sequence), so that a link's id, which the uses counted in
-- memory are kept by, is never given to another; and the triggers that count access changes on links go with the old
-- table, so they are made again, the same.
CREATE TABLE links_new (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id    INTEGER NOT NULL UNIQUE REFERENCES items (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    kind       TEXT    NOT NULL CHECK (kind IN ('view', 'upload-only')),
    made_at    INTEGER NOT NULL, -- unix milliseconds
    made_by    TEXT    NOT NULL,
    uses       INTEGER NOT NULL DEFAULT 0 CHECK (uses >= 0),
    used_at    INTEGER,
    taken_in   INTEGER NOT NULL DEFAULT 0 CHECK (taken_in >= 0) -- bytes
);

INSERT INTO links_new (id, item_id, token_hash, kind, made_at, made_by, uses, used_at)
    SELECT id, item_id, token_hash, kind, made_at, made_by, uses, used_at FROM links ORDER BY id;
-- The highest id the old table ever gave, also when the links that had it were removed since.
INSERT INTO sqlite_sequence (name, seq)
    SELECT 'links_new', 0 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'links_new');
UPDATE sqlite_sequence
    SET seq = MAX(seq, IFNULL((SELECT seq FROM sqlite_sequence WHERE name = 'links'), 0))
    WHERE name = 'links_new';
DROP TABLE links;
ALTER TABLE links_new RENAME TO links;

CREATE TRIGGER access_changes_links_insert AFTER INSERT ON links BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_links_delete AFTER DELETE ON links BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_links_update AFTER UPDATE OF item_id, token_hash, kind, made_at, made_by ON links
    BEGIN UPDATE access_changes SET n = n + 1; END;

-- A file that arrived through a public link is labelled so, as one a guest added is labelled with their address. Like
-- that label, it stays with the item until a member uploads over it, or it is removed.
ALTER TABLE items ADD COLUMN added_through_link INTEGER NOT NULL DEFAULT 0 CHECK (added_through_link IN (0, 1));
