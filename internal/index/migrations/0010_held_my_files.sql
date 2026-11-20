-- When a member is removed: their My files is held, unopened, until a project admin hands it to another member or
-- deletes it. Held is not a state the index keeps: a My files whose owner is not in the member list is held, and is
-- theirs again when they come back with the same user id. What the index keeps is the act that ends it.
--
-- held_acts holds one row per act: a hand-over ('hand-over') or a deletion ('delete') of one held My files, as a whole,
-- by a project admin. It is the record of who did it (by, a project admin's user id), when (at, unix milliseconds) and
-- what (whose files: owner, the removed member's user id, and folder, their My files' top folder on the storage; how
-- many files and how many bytes were in it; for a hand-over, to whom, to_member, and the folder it became in their My
-- files, to_name). It is kept for good: project admins read it.
--
-- An act is done in steps on the storage, then in the index, and can be cut short by the Drive stopping. While it is
-- under way (done_at NULL) place_id names the held My files, which stays held whatever the member list says, and the
-- Drive finishes the act when it starts again. Done, the place is gone (place_id NULL): handed over, its items are in
-- the receiver's My files (to_place) under a folder of their own; deleted, they are removed. AUTOINCREMENT, so that an
-- act's id is never given to another.
--
-- A folder named in an act is never given to a new My files: the removed member's address (or another's) never takes
-- over what may be left of it on the storage.
CREATE TABLE held_acts (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    act       TEXT    NOT NULL CHECK (act IN ('hand-over', 'delete')),
    place_id  INTEGER UNIQUE REFERENCES places (id) ON DELETE SET NULL,
    owner     TEXT    NOT NULL CHECK (owner != ''),
    folder    TEXT    NOT NULL CHECK (folder != ''),
    to_member TEXT,
    to_place  INTEGER REFERENCES places (id) ON DELETE SET NULL,
    to_name   TEXT,
    files     INTEGER NOT NULL DEFAULT 0 CHECK (files >= 0),
    size      INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
    by        TEXT    NOT NULL CHECK (by != ''),
    at        INTEGER NOT NULL,
    done_at   INTEGER,
    CHECK ((act = 'hand-over') = (to_member IS NOT NULL AND to_name IS NOT NULL)),
    CHECK (done_at IS NOT NULL OR place_id IS NOT NULL)
);
CREATE INDEX held_acts_by_folder ON held_acts (folder);
CREATE INDEX held_acts_by_receiver ON held_acts (to_place) WHERE done_at IS NULL;
