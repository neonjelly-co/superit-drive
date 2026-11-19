-- Sharing with members: access given on a file or folder, or on a place as a whole, to a member or to everyone in the
-- project.
--
-- grants is rebuilt (SQLite cannot change a table's key or checks in place). Each row is access given to one grantee
-- on one item of a place (item_id) or on the place's top folder (item_id NULL): to a member by their stable user id
-- (grantee 'member'), or to everyone in the project (grantee 'everyone', member NULL), as viewer, editor or manager.
-- One row per grantee and item: giving again replaces the role. Manager is given only on a place as a whole, to a
-- member. internal/access decides what each row is worth (access.Counts); the index only holds them.
--
-- A grant on an item hangs on the item's id, which lasts through rename, move, the trash and restore and is never
-- given to another item. It goes with the item when the item is removed for good, or when the storage no longer has
-- it. The foreign key on (item_id, place_id) also keeps a grant in the place its item is in.
--
-- given_by is the user id of the member who gave it (NULL for the managers given before this migration).
--
-- items.trashed_by is, for an item in the trash, the user id of the member who put it there (by deleting it, or by
-- replacing it with an upload): someone given only part of a place restores only what they put there. NULL for what
-- was in the trash before this migration, and while an item is not in the trash.
--
-- The migration runs with foreign keys off, so dropping the old table takes nothing with it.
ALTER TABLE items ADD COLUMN trashed_by TEXT;

CREATE UNIQUE INDEX items_id_and_place ON items (id, place_id);

CREATE TABLE grants_new (
    place_id INTEGER NOT NULL REFERENCES places (id) ON DELETE CASCADE,
    item_id  INTEGER,                   -- NULL: the place's top folder
    grantee  TEXT    NOT NULL CHECK (grantee IN ('member', 'everyone')),
    member   TEXT,                      -- the member's stable user id; NULL for everyone
    role     TEXT    NOT NULL CHECK (role IN ('viewer', 'editor', 'manager')),
    given_at INTEGER NOT NULL,          -- unix milliseconds
    given_by TEXT,                      -- the user id of the member who gave it
    FOREIGN KEY (item_id, place_id) REFERENCES items (id, place_id) ON DELETE CASCADE ON UPDATE CASCADE,
    CHECK ((grantee = 'member') = (member IS NOT NULL)),
    CHECK (role != 'manager' OR (item_id IS NULL AND grantee = 'member'))
);

INSERT INTO grants_new (place_id, item_id, grantee, member, role, given_at)
    SELECT place_id, NULL, 'member', member, role, given_at FROM grants;
DROP TABLE grants;
ALTER TABLE grants_new RENAME TO grants;

CREATE UNIQUE INDEX grants_one_per_grantee ON grants (place_id, IFNULL(item_id, 0), grantee, IFNULL(member, ''));
CREATE INDEX grants_by_member ON grants (member);
CREATE INDEX grants_by_item ON grants (item_id, place_id);
