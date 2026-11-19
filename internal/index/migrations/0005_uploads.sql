-- Uploads in progress: one row per resumable upload that has started and not yet finished or been cancelled. Its
-- bytes are on the storage, in the file named after its id in the hidden uploads folder; the row says where it goes and
-- whose it is.
--
-- id is random (it also names the file), and only the member who started the upload (member, a stable user id) may
-- continue, finish or cancel it. folder_id is the folder it goes into (NULL: the place's top folder): an item id, so it
-- follows the folder through a rename or move, and the row goes when the folder is removed for good, or the place is.
-- touched_at is when a part last arrived: an upload untouched for a day is removed, with its file.
CREATE TABLE uploads (
    id         TEXT    PRIMARY KEY CHECK (length(id) = 32),
    place_id   INTEGER NOT NULL REFERENCES places (id) ON DELETE CASCADE,
    folder_id  INTEGER,
    member     TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    size       INTEGER NOT NULL CHECK (size > 0),
    started_at INTEGER NOT NULL, -- unix milliseconds
    touched_at INTEGER NOT NULL, -- unix milliseconds
    FOREIGN KEY (folder_id, place_id) REFERENCES items (id, place_id) ON DELETE CASCADE
);

CREATE INDEX uploads_by_touched ON uploads (touched_at);
CREATE INDEX uploads_by_folder ON uploads (folder_id, place_id);
