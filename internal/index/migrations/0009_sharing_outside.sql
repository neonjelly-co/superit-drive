-- Sharing outside the project (public links and guests) switched off: for the whole project, by a project admin, or for
-- one shared folder, by its managers. A row is a switch that is off: place_id NULL for the whole project, or the shared
-- folder's id; off_at is when it was switched off (unix milliseconds) and off_by who did it (a member's user id).
-- Switching it back on removes the row. internal/access decides what it means (access.Place.NoSharingOutside): while
-- off, the links and invitations there stay as they are, open nothing and let nobody in, and work again once it is on.
-- A shared folder's own switch stays with it in the trash and goes when it is removed for good.
CREATE TABLE outside_off (
    place_id INTEGER REFERENCES places (id) ON DELETE CASCADE,
    off_at   INTEGER NOT NULL,
    off_by   TEXT    NOT NULL CHECK (off_by != '')
);
CREATE UNIQUE INDEX outside_off_one_per_place ON outside_off (IFNULL(place_id, 0));

-- What a project admin oversees: every public link and every guest's invitation, a page at a time by id.
CREATE INDEX grants_invitations ON grants (id) WHERE grantee = 'guest';
