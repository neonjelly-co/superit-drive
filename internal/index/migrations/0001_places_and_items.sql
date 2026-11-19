-- Places are the Drive's top-level places. So far only a member's "My files", one per member, owned by that
-- member's stable user id. Its top folder on the storage is `folder`, relative to the Drive's folder.
CREATE TABLE places (
    id         INTEGER PRIMARY KEY,
    kind       TEXT    NOT NULL CHECK (kind IN ('my-files')),
    owner      TEXT    NOT NULL,
    folder     TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX places_one_my_files_per_member ON places (owner) WHERE kind = 'my-files';

-- Items are the files and folders inside places, as last seen on the storage. The storage wins for which files
-- exist; the index gives them stable ids that access (later) hangs on.
CREATE TABLE items (
    id         INTEGER PRIMARY KEY,
    place_id   INTEGER NOT NULL REFERENCES places (id) ON DELETE CASCADE,
    parent_id  INTEGER REFERENCES items (id) ON DELETE CASCADE, -- NULL: directly in the place's top folder
    name       TEXT    NOT NULL,
    is_folder  INTEGER NOT NULL CHECK (is_folder IN (0, 1)),
    size       INTEGER NOT NULL DEFAULT 0, -- bytes; 0 for folders
    changed_at INTEGER NOT NULL            -- unix seconds, from the storage
);
CREATE UNIQUE INDEX items_unique_name ON items (place_id, IFNULL(parent_id, 0), name);
