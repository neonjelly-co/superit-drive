-- Public links: anyone with a link's address may see the file or folder it is on, and everything inside it, and
-- download them, without signing in.
--
-- One link per item at most (item_id is unique), never on a place's top folder. It hangs on the item's id, which lasts
-- through rename, move (into another place too), the trash and restore, and is never given to another item: the link
-- covers the item wherever it is now, and goes with it when it is removed for good or the storage no longer has it
-- (ON DELETE CASCADE). Its place is the item's.
--
-- The address holds a random token (at least 128 bits) that the index never holds: only its SHA-256 (token_hash), so
-- that a copy of the index, or a backup of it, hands out no working link. A link removed is a row deleted: its address
-- is dead for good, and a new link gets a new token. AUTOINCREMENT, so that a link's id is never given to another.
--
-- made_by is the user id of the member who made it. uses counts how often it was used and used_at says when it last
-- was (unix milliseconds, NULL before its first use): nothing else about visitors is kept.
CREATE TABLE links (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id    INTEGER NOT NULL UNIQUE REFERENCES items (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    kind       TEXT    NOT NULL CHECK (kind IN ('view')),
    made_at    INTEGER NOT NULL, -- unix milliseconds
    made_by    TEXT    NOT NULL,
    uses       INTEGER NOT NULL DEFAULT 0 CHECK (uses >= 0),
    used_at    INTEGER
);
