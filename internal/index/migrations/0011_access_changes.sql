-- The copy of the index on the storage (internal/drive/copies.go) is written soon after anything changes who may reach
-- what. access_changes counts those changes, in one row, in the same transaction as the change itself, so that the
-- Drive tells "something to copy" by comparing a number, whatever wrote it: a member sharing, a link made or removed, a
-- guest invited or gone with their last invitation, a shared folder made, deleted or restored, a switch of sharing
-- outside the project, a held My files handed over or deleted, and an item renamed, moved, deleted or restored (what was
-- given on it, and on everything inside it, hangs on its id, which a copy older than the change would not find where
-- the storage has it). Writing how often links and invitations were used (uses, used_at) is not counted, nor anything a
-- copy leaves out (guests' sessions, codes and limits, uploads in progress, the walk's cursor), nor files added (a copy
-- older than them finds them on the storage, with the access of their folder).
--
-- A trigger goes with its table: a migration that rebuilds one of these tables must make its triggers again (a test in
-- copies_test.go fails when one is missing).
CREATE TABLE access_changes (
    n INTEGER NOT NULL
);
INSERT INTO access_changes (n) VALUES (0);

CREATE TRIGGER access_changes_grants_insert AFTER INSERT ON grants BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_grants_delete AFTER DELETE ON grants BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_grants_update
    AFTER UPDATE OF place_id, item_id, grantee, member, guest_id, role, given_at, given_by, ends_at ON grants
    BEGIN UPDATE access_changes SET n = n + 1; END;

CREATE TRIGGER access_changes_links_insert AFTER INSERT ON links BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_links_delete AFTER DELETE ON links BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_links_update AFTER UPDATE OF item_id, token_hash, kind, made_at, made_by ON links
    BEGIN UPDATE access_changes SET n = n + 1; END;

CREATE TRIGGER access_changes_guests_insert AFTER INSERT ON guests BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_guests_delete AFTER DELETE ON guests BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_guests_update AFTER UPDATE ON guests BEGIN UPDATE access_changes SET n = n + 1; END;

CREATE TRIGGER access_changes_places_insert AFTER INSERT ON places BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_places_delete AFTER DELETE ON places BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_places_update AFTER UPDATE ON places BEGIN UPDATE access_changes SET n = n + 1; END;

CREATE TRIGGER access_changes_outside_insert AFTER INSERT ON outside_off BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_outside_delete AFTER DELETE ON outside_off BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_outside_update AFTER UPDATE ON outside_off BEGIN UPDATE access_changes SET n = n + 1; END;

CREATE TRIGGER access_changes_acts_insert AFTER INSERT ON held_acts BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_acts_delete AFTER DELETE ON held_acts BEGIN UPDATE access_changes SET n = n + 1; END;
CREATE TRIGGER access_changes_acts_update AFTER UPDATE ON held_acts BEGIN UPDATE access_changes SET n = n + 1; END;

CREATE TRIGGER access_changes_items_update AFTER UPDATE OF name, parent_id, place_id, trashed_at ON items
    BEGIN UPDATE access_changes SET n = n + 1; END;
