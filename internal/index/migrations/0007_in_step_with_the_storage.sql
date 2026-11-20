-- Keeping in step with the storage: items the storage no longer shows, and the Drive's own state.
--
-- items.missing_at is when a look at the storage last found an item gone (unix milliseconds), NULL while the storage
-- shows it. A missing item is hidden everywhere, as if it were not there, but keeps its row, its id and what was given
-- on it: it is dropped only when a later look, at least ten minutes on, still finds it gone (and the storage is the
-- Drive's own: see drive_state.storage_id), and it is back, the same item, if the storage shows it again before that.
-- It is set only on the item that was seen gone (a folder), not on what is inside it, as trashed_at is.
--
-- The unique name of a live item no longer includes missing ones: a file the Drive itself writes where a missing item
-- was is a new item, with a new id, and never takes over the missing one's sharing or public link.
--
-- drive_state holds what the Drive keeps about itself between starts, one value per key: the id of the storage it
-- belongs to (storage_id, also written in .superit-drive/storage-id on the storage), and where the slow walk of the
-- storage is (walk_cursor, walk_started, walk_finished), so a walk interrupted by a restart goes on where it was.
ALTER TABLE items ADD COLUMN missing_at INTEGER;

DROP INDEX items_unique_name;
CREATE UNIQUE INDEX items_unique_name ON items (place_id, IFNULL(parent_id, 0), name) WHERE trashed_at IS NULL AND missing_at IS NULL;
CREATE INDEX items_missing ON items (place_id, IFNULL(parent_id, 0), name) WHERE missing_at IS NOT NULL;
-- The walk finds a place's next folder by id; a folder's list is read a page at a time, in its order.
CREATE INDEX items_folders_by_place ON items (place_id, id) WHERE is_folder = 1;
CREATE INDEX items_in_list_order ON items (place_id, IFNULL(parent_id, 0), is_folder DESC, name)
    WHERE trashed_at IS NULL AND missing_at IS NULL;

CREATE TABLE drive_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;
