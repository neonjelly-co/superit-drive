-- Folders at any depth, the trash, and ids that are never given to a second item.
--
-- The items table is rebuilt (SQLite cannot change a table's key in place) so that its ids come from AUTOINCREMENT: an
-- id is never reused, even after its item is removed for good. Access hangs on item ids (later tickets), and an
-- address a page still holds must never reach another item than the one it named.
--
-- An item deleted into the trash keeps its row and its id. trashed_at says when (unix milliseconds, so the trash lists
-- in the order things were deleted), trash_folder is its folder in the place's trash on the storage, trashed_why
-- whether a person deleted it or an upload replaced it, and restore_to the folder it was in (NULL: the place's top
-- folder). While it is in the trash its parent_id is NULL, so removing a folder for good never takes along an item
-- deleted out of it earlier. What is inside a deleted folder keeps its rows under it, unchanged, and goes and comes
-- back with it.
PRAGMA defer_foreign_keys = ON;

CREATE TABLE items_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    place_id     INTEGER NOT NULL REFERENCES places (id) ON DELETE CASCADE,
    parent_id    INTEGER REFERENCES items_new (id) ON DELETE CASCADE, -- NULL: in the place's top folder, or in the trash
    name         TEXT    NOT NULL,
    is_folder    INTEGER NOT NULL CHECK (is_folder IN (0, 1)),
    size         INTEGER NOT NULL DEFAULT 0, -- bytes; 0 for folders
    changed_at   INTEGER NOT NULL,           -- unix seconds, from the storage
    trashed_at   INTEGER,                    -- unix milliseconds; set only on the item that was deleted, not on what is in it
    trash_folder TEXT,
    trashed_why  TEXT CHECK (trashed_why IN ('deleted', 'replaced')),
    restore_to   INTEGER REFERENCES items_new (id) ON DELETE SET NULL,
    CHECK ((trashed_at IS NULL) = (trash_folder IS NULL) AND (trashed_at IS NULL) = (trashed_why IS NULL)),
    CHECK (trashed_at IS NULL OR parent_id IS NULL),
    CHECK (trashed_at IS NOT NULL OR restore_to IS NULL)
);

INSERT INTO items_new (id, place_id, parent_id, name, is_folder, size, changed_at)
    SELECT id, place_id, parent_id, name, is_folder, size, changed_at FROM items ORDER BY id;
DROP TABLE items;
ALTER TABLE items_new RENAME TO items;

-- One name once per folder, among the items that are not in the trash.
CREATE UNIQUE INDEX items_unique_name ON items (place_id, IFNULL(parent_id, 0), name) WHERE trashed_at IS NULL;
CREATE INDEX items_by_parent ON items (parent_id);
CREATE INDEX items_in_trash ON items (place_id, trashed_at) WHERE trashed_at IS NOT NULL;
CREATE INDEX items_by_restore_to ON items (restore_to) WHERE restore_to IS NOT NULL;
