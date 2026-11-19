-- Shared folders, their managers, and shared folders in the trash.
--
-- A shared folder is a place owned by the project: it has a name and no owner. Its top folder on the storage is
-- `folder`, "Shared folders/<name>", with " (2)" and so on added when another place already holds that folder; people
-- see `name`. A deleted shared folder keeps its row (and its id, its items and its grants) for 30 days: deleted_at says
-- when (unix milliseconds), deleted_entry is the entry in its own trash folder on the storage that holds its top
-- folder. Its `folder` stays held meanwhile, so restoring it always has its own folder to go back to.
--
-- places is rebuilt (SQLite cannot change a table's checks in place) with AUTOINCREMENT, so a place's id is never given
-- to a second place: a page can still hold the id of a shared folder that was removed for good. The migration runs with
-- foreign keys off, so dropping the old table does not take the items with it.
CREATE TABLE places_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT    NOT NULL CHECK (kind IN ('my-files', 'shared-folder')),
    owner         TEXT,             -- My files: the member's user id. NULL for a shared folder: the project owns it.
    name          TEXT,             -- a shared folder's name as people see it; NULL for My files
    folder        TEXT    NOT NULL UNIQUE,
    created_at    INTEGER NOT NULL,
    deleted_at    INTEGER,
    deleted_entry TEXT,
    CHECK ((kind = 'my-files') = (owner IS NOT NULL)),
    CHECK ((kind = 'shared-folder') = (name IS NOT NULL)),
    CHECK ((deleted_at IS NULL) = (deleted_entry IS NULL)),
    CHECK (deleted_at IS NULL OR kind = 'shared-folder')
);

INSERT INTO places_new (id, kind, owner, folder, created_at)
    SELECT id, kind, owner, folder, created_at FROM places ORDER BY id;
DROP TABLE places;
ALTER TABLE places_new RENAME TO places;

CREATE UNIQUE INDEX places_one_my_files_per_member ON places (owner) WHERE kind = 'my-files';

-- Roles given to members on a place, one per member and place. So far only on shared folders, and only "manager":
-- whoever makes a shared folder is its first manager, and a manager can make others managers. Holding the admin role on
-- the project makes someone a manager of every shared folder without a row here; access.RoleIn decides that.
CREATE TABLE grants (
    place_id INTEGER NOT NULL REFERENCES places (id) ON DELETE CASCADE,
    member   TEXT    NOT NULL,           -- the member's stable user id
    role     TEXT    NOT NULL CHECK (role IN ('manager')),
    given_at INTEGER NOT NULL,           -- unix milliseconds
    PRIMARY KEY (place_id, member)
);
CREATE INDEX grants_by_member ON grants (member);
