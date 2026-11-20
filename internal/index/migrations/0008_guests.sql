-- Guests: someone outside the project whom a member invited to a file or folder by email address, and who signs in on
-- the public prefix with a code mailed to that address.
--
-- guests holds one row per address that something was given to, as members.Address writes it (no two rows for one
-- address; dots and "+" tags make other addresses). AUTOINCREMENT, so that an id is never given to another guest: a
-- session or an upload in progress can never pass to someone else. A guest is removed with their last invitation (the
-- trigger below), and with them their codes, their sessions and their uploads in progress.
CREATE TABLE guests (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    address  TEXT    NOT NULL UNIQUE CHECK (address = lower(trim(address)) AND address != ''),
    added_at INTEGER NOT NULL -- unix milliseconds
);

-- grants is rebuilt (SQLite cannot change a table's checks in place), with:
--
--   - an id of its own (AUTOINCREMENT, so that the uses of an invitation counted in memory never land on another);
--   - a third kind of grantee, 'guest': an invitation, to guest_id, on an item (never a place as a whole), as viewer or
--     editor, given by a member (given_by is never NULL for one). It goes when the guest goes;
--   - ends_at: when it stops counting (unix milliseconds), NULL for never. An invitation may have one; a guest who joins
--     the project keeps theirs on the grant to them as a member. Nothing to everyone in the project ends;
--   - uses and used_at, for an invitation: how often the guest used it and when last (NULL before the first use).
--     Nothing else about what a guest does is kept.
CREATE TABLE grants_new (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    place_id INTEGER NOT NULL REFERENCES places (id) ON DELETE CASCADE,
    item_id  INTEGER,                   -- NULL: the place's top folder
    grantee  TEXT    NOT NULL CHECK (grantee IN ('member', 'everyone', 'guest')),
    member   TEXT,                      -- the member's stable user id; NULL for everyone and for a guest
    guest_id INTEGER REFERENCES guests (id) ON DELETE CASCADE,
    role     TEXT    NOT NULL CHECK (role IN ('viewer', 'editor', 'manager')),
    given_at INTEGER NOT NULL,          -- unix milliseconds
    given_by TEXT,                      -- the user id of the member who gave it
    ends_at  INTEGER,                   -- unix milliseconds; NULL: it does not end
    uses     INTEGER NOT NULL DEFAULT 0 CHECK (uses >= 0),
    used_at  INTEGER,                   -- unix milliseconds
    FOREIGN KEY (item_id, place_id) REFERENCES items (id, place_id) ON DELETE CASCADE ON UPDATE CASCADE,
    CHECK ((grantee = 'member') = (member IS NOT NULL)),
    CHECK ((grantee = 'guest') = (guest_id IS NOT NULL)),
    CHECK (role != 'manager' OR (item_id IS NULL AND grantee = 'member')),
    CHECK (grantee != 'guest' OR (item_id IS NOT NULL AND given_by IS NOT NULL)),
    CHECK (ends_at IS NULL OR grantee != 'everyone')
);

INSERT INTO grants_new (place_id, item_id, grantee, member, role, given_at, given_by)
    SELECT place_id, item_id, grantee, member, role, given_at, given_by FROM grants ORDER BY rowid;
DROP TABLE grants;
ALTER TABLE grants_new RENAME TO grants;

CREATE UNIQUE INDEX grants_one_per_grantee ON grants (place_id, IFNULL(item_id, 0), grantee, IFNULL(member, ''), IFNULL(guest_id, 0));
CREATE INDEX grants_by_member ON grants (member);
CREATE INDEX grants_by_item ON grants (item_id, place_id);
CREATE INDEX grants_by_guest ON grants (guest_id) WHERE guest_id IS NOT NULL;

-- A guest goes with their last invitation, however it goes (taken away, its item removed for good or dropped from the
-- index, its place removed), and their codes and sessions with them: a session ends at once when the last access is.
CREATE TRIGGER guests_go_with_their_last_invitation AFTER DELETE ON grants
WHEN OLD.guest_id IS NOT NULL
BEGIN
    DELETE FROM guests WHERE id = OLD.guest_id AND NOT EXISTS (SELECT 1 FROM grants WHERE guest_id = OLD.guest_id);
END;

-- The codes mailed to guests to sign in with: six digits, of which the index keeps only an HMAC-SHA-256 keyed with a
-- key the index does not hold (derived from DRIVE_HEADER_SECRET), so that a copy of the index tells no code. Only the
-- newest of a guest's codes works: asking for another spends the others. A code works once (spent), for ten minutes
-- after it was sent, and not after five wrong tries. Rows are kept an hour, to count the codes sent: at most five an
-- hour per address.
CREATE TABLE guest_codes (
    id        INTEGER PRIMARY KEY,
    guest_id  INTEGER NOT NULL REFERENCES guests (id) ON DELETE CASCADE,
    code_hash BLOB    NOT NULL CHECK (length(code_hash) = 32),
    sent_at   INTEGER NOT NULL, -- unix milliseconds
    tries     INTEGER NOT NULL DEFAULT 0 CHECK (tries >= 0),
    spent     INTEGER NOT NULL DEFAULT 0 CHECK (spent IN (0, 1))
);
CREATE INDEX guest_codes_by_guest ON guest_codes (guest_id, sent_at);
CREATE INDEX guest_codes_by_sent ON guest_codes (sent_at);

-- A guest's sessions, one per browser they signed in on: the session token's SHA-256 (the token is only in the
-- browser's cookie), whose it is, and when it ends (30 days after it began).
CREATE TABLE guest_sessions (
    token_hash BLOB    PRIMARY KEY CHECK (length(token_hash) = 32),
    guest_id   INTEGER NOT NULL REFERENCES guests (id) ON DELETE CASCADE,
    started_at INTEGER NOT NULL, -- unix milliseconds
    ends_at    INTEGER NOT NULL  -- unix milliseconds
) WITHOUT ROWID;
CREATE INDEX guest_sessions_by_guest ON guest_sessions (guest_id);
CREATE INDEX guest_sessions_by_end ON guest_sessions (ends_at);

-- What the Drive keeps across all guests to bound tries: the wrong codes tried in the current hour, in one row.
CREATE TABLE guest_limits (
    name         TEXT    PRIMARY KEY,
    window_start INTEGER NOT NULL, -- unix milliseconds
    count        INTEGER NOT NULL CHECK (count >= 0)
) WITHOUT ROWID;

-- A file or folder a guest added (uploaded, or made) is labelled with their address, as they had it then. It stays
-- with the item until someone uploads over it (a member's upload clears it) or it is removed.
ALTER TABLE items ADD COLUMN added_by_guest TEXT;

-- An upload in progress by a guest is theirs: member is '' and guest_id says whose. It goes when the guest goes.
ALTER TABLE uploads ADD COLUMN guest_id INTEGER REFERENCES guests (id) ON DELETE CASCADE;
