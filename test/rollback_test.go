package test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// After a rollback: a project admin put the Drive storage back to a snapshot. The daemon stops the Drive, the storage is
// put back, and the Drive starts again with settings that say so (by whom, when, to when), which it keeps being given
// until the next rollback. The files are as they were in the snapshot; sharing is as it was just before the rollback,
// but on what no longer exists; members see a notice for seven days. These tests take real snapshots of the stand-in
// storage's disk (harness.Storage.Snapshot) and put the storage back to them, and look only at what the Drive answers
// and at the storage.

// rollbackSettings declares a rollback at at, to a snapshot taken at to, by by.
func rollbackSettings(to, at time.Time, by string) map[string]string {
	return map[string]string{
		"DRIVE_ROLLBACK_AT": at.UTC().Format(time.RFC3339),
		"DRIVE_ROLLBACK_TO": to.UTC().Format(time.RFC3339),
		"DRIVE_ROLLBACK_BY": by,
	}
}

// rolledBack stops the Drive, puts its storage back to snap, and starts it again with the settings that declare the
// rollback, by Réka, now; it returns when the rollback says it was.
func rolledBack(t *testing.T, d *harness.Drive, snap harness.Snapshot) (at time.Time) {
	t.Helper()
	d.Stop(t)
	d.Storage.RollBack(t, snap)
	// The settings say when to the second: the rollback comes in a second after the snapshot's, as it always does.
	at = time.Now().Truncate(time.Second)
	if !snap.Taken.Truncate(time.Second).Before(at) {
		at = at.Add(time.Second)
		time.Sleep(time.Until(at))
	}
	d.Restart(t, rollbackSettings(snap.Taken, at, reka.DisplayName))
	return at
}

// meOf is what /api/me answers the member.
func meOf(t *testing.T, d *harness.Drive, m harness.Member) meJSON {
	t.Helper()
	res := d.As(m).Get(t, "/api/me")
	if res.Status != http.StatusOK {
		t.Fatalf("/api/me for %s: %d %s", m.Email, res.Status, res.Body)
	}
	var me meJSON
	res.JSON(t, &me)
	return me
}

// contentOf is the content of a file the member downloads, or "" with the status when it does not download.
func contentOf(t *testing.T, c *harness.Client, placeID, itemID int64) string {
	t.Helper()
	res := c.Get(t, downloadPath(placeID, itemID))
	if res.Status != http.StatusOK {
		return fmt.Sprint(res.Status)
	}
	return string(res.Body)
}

// The Drive is told at start that the storage was put back, by whom and to when; it keeps its index and walks through
// the storage at once. What is gone (added since the snapshot) is hidden at once, its sharing and link with it, and
// dropped later; what is back (deleted since) is a new item with the access of its folder only; what changed since has
// its older content and keeps its sharing; and access taken away since, or a link removed since, stays away. Every
// member is told; a visitor through a link is told nothing.
func TestARollbackKeepsSharingAsItIs(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: fastWalk("1h")})
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	missing := v.Get(t, noSuchLink(t))
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Notes.txt with Dóra", share(t, b, x.place, x.notes.ID, dora, "viewer"))
	ok(t, "sharing Secret.txt with Dóra", share(t, b, x.place, x.secret.ID, dora, "viewer"))
	_, notesPage := madeLink(t, d, b, x.place, x.notes.ID, false)
	_, ledgerPage := madeLink(t, d, b, x.place, x.ledger.ID, false)
	gone := uploadedInto(t, b, x.place, 0, "Deleted since.txt", []byte("deleted since"))
	ok(t, "sharing Deleted since.txt with Dóra", share(t, b, x.place, gone.ID, dora, "viewer"))
	list(t, b, x.place, 0)
	nextSecond(t, d)
	snap := d.Storage.Snapshot(t)
	nextSecond(t, d)

	// After the snapshot: access taken away, a link removed, a file added and shared, files deleted and one changed.
	ok(t, "taking Secret.txt away from Dóra", unshare(t, b, x.place, x.secret.ID, dora))
	if res := removeLink(t, b, x.place, x.ledger.ID); res.Status != http.StatusNoContent {
		t.Fatalf("removing the link on Ledger.xlsx: %d %s", res.Status, res.Body)
	}
	added := uploadedInto(t, b, x.place, 0, "Added since.txt", []byte("added since"))
	ok(t, "sharing Added since.txt with Dóra", share(t, b, x.place, added.ID, dora, "viewer"))
	_, addedPage := madeLink(t, d, b, x.place, added.ID, false)
	deep := uploadedInto(t, b, x.place, x.y2026.ID, "Deep since.txt", []byte("added deep down since"))
	deleted(t, b, x.place, gone.ID)
	deleted(t, b, x.place, x.prices.ID)
	if res := uploadInto(t, b, x.place, x.private.ID, "Secret.txt", []byte("changed since"), "replace"); res.Status != http.StatusCreated {
		t.Fatalf("replacing Secret.txt: %d %s", res.Status, res.Body)
	}
	walks := strings.Count(d.Logs(), "the walk through the storage ended")

	at := rolledBack(t, d, snap)
	b, o, v = d.As(bence), d.As(dora), visitor(d)

	// Every member is told, by whom and to when.
	for _, m := range []harness.Member{reka, bence, dora} {
		r := meOf(t, d, m).Rollback
		if r == nil || r.By != reka.DisplayName || !r.To.Equal(snap.Taken.Truncate(time.Second)) || !r.At.Equal(at) ||
			!r.Until.Equal(at.Add(7*24*time.Hour)) {
			t.Fatalf("/api/me for %s says of the rollback: %+v", m.Email, r)
		}
	}
	// The walk goes through the storage at once (it would wait an hour otherwise): what was added deep down since, in
	// a folder nobody opens, is no longer found.
	harness.Eventually(t, "the walk after the rollback has ended", func() bool {
		return strings.Count(d.Logs(), "the walk through the storage ended") > walks
	})
	if got := foundNames(t, b, "since"); !slices.Equal(got, []string{"Deleted since.txt"}) {
		t.Fatalf("searching for \"since\" after the rollback: %q", got)
	}
	if res := b.Get(t, downloadPath(x.place, deep.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("Deep since.txt after the rollback: %d", res.Status)
	}

	// Gone: Added since.txt, its sharing and its link with it.
	top := list(t, b, x.place, 0)
	if slices.Contains(names(top.Items), "Added since.txt") {
		t.Fatalf("Bence's My files after the rollback: %q", names(top.Items))
	}
	sameAsNoLink(t, "the link on Added since.txt", v.Get(t, addedPage), missing)
	if got := sorted(sharedNames(t, o)); !slices.Equal(got, sorted([]string{entry("Clients", x.place), entry("Notes.txt", x.place)})) {
		t.Fatalf("Dóra's Shared with me after the rollback: %q", got)
	}
	// Kept: what was given before the snapshot and never taken away.
	if p := readPage(t, v, notesPage); p.name != "Notes.txt" {
		t.Fatalf("the link on Notes.txt after the rollback: %+v", p)
	}
	if got := contentOf(t, o, x.place, x.notes.ID); got != "notes" {
		t.Fatalf("Dóra downloading Notes.txt: %s", got)
	}
	// Taken away since: stays away.
	sameAsNoLink(t, "the link removed from Ledger.xlsx since the snapshot", v.Get(t, ledgerPage), missing)
	sameAsNoLink(t, "the removed link's file", v.Get(t, inLink(ledgerPage, x.ledger.ID, "/download")), missing)
	if got := contentOf(t, o, x.place, x.secret.ID); got != "404" {
		t.Fatalf("Dóra downloading Secret.txt, taken away from her since the snapshot: %s", got)
	}
	// Changed since: the same item, with its content as it was.
	if got := contentOf(t, b, x.place, x.secret.ID); got != "secret" {
		t.Fatalf("Bence downloading Secret.txt after the rollback: %s", got)
	}
	// Back (deleted since): a new item, with the access of its folder and nothing of its own.
	back := named(t, top.Items, "Deleted since.txt")
	if back.ID == gone.ID || !slices.Equal(itemAccess(t, b, x.place, back.ID), []string{bence.UserID + " owner (owner)"}) {
		t.Fatalf("Deleted since.txt is back as %+v, seen by %q", back, itemAccess(t, b, x.place, back.ID))
	}
	if got := contentOf(t, o, x.place, back.ID); got != "404" {
		t.Fatalf("Dóra downloading Deleted since.txt, back in Bence's own top folder: %s", got)
	}
	prices := named(t, list(t, b, x.place, x.clients.ID).Items, "Price list.pdf")
	if got, want := sorted(itemAccess(t, b, x.place, prices.ID)), sorted([]string{bence.UserID + " owner (owner)", dora.UserID + " viewer (from Clients)"}); prices.ID == x.prices.ID || !slices.Equal(got, want) {
		t.Fatalf("Price list.pdf is back as item %d (was %d), seen by %q", prices.ID, x.prices.ID, got)
	}
	if got := contentOf(t, o, x.place, prices.ID); got != "prices" {
		t.Fatalf("Dóra downloading Price list.pdf, back in Clients: %s", got)
	}
	// What was deleted since is no longer in the trash: it is where it was.
	for _, e := range trashOf(t, b, x.place) {
		if e.ID == gone.ID || e.ID == x.prices.ID || e.Name == "Secret.txt" {
			t.Fatalf("the trash after the rollback lists %+v", e)
		}
	}
	// A visitor is told nothing about it.
	if body := string(v.Get(t, notesPage).Body); strings.Contains(body, "Réka") || strings.Contains(body, "put back") || strings.Contains(body, "snapshot") {
		t.Fatalf("the link's page tells a visitor about the rollback: %s", body)
	}

	// Eleven minutes later, with the same settings (the daemon keeps passing them): the rollback was taken in already,
	// and what went is dropped with its sharing, for good. A file put under its name now is another one.
	d.Restart(t, map[string]string{harness.TestClockShift: "11m"})
	b, o, v = d.As(bence), d.As(dora), visitor(d)
	harness.Eventually(t, "the log says the rollback was taken in already", func() bool {
		return strings.Contains(d.Logs(), "was taken in already")
	})
	if n := strings.Count(d.Logs(), "the storage was put back to a snapshot:"); n != 1 {
		t.Fatalf("the rollback was taken in %d times", n)
	}
	if r := meOf(t, d, dora).Rollback; r == nil || !r.At.Equal(at) {
		t.Fatalf("/api/me eleven minutes on: %+v", r)
	}
	list(t, b, x.place, 0)
	nextSecond(t, d)
	d.Storage.Put(t, "My files/bence@example.com/Added since.txt", "added since")
	again := named(t, list(t, b, x.place, 0).Items, "Added since.txt")
	if again.ID == added.ID || !slices.Equal(itemAccess(t, b, x.place, again.ID), []string{bence.UserID + " owner (owner)"}) {
		t.Fatalf("a file put where Added since.txt was: %+v, seen by %q", again, itemAccess(t, b, x.place, again.ID))
	}
	sameAsNoLink(t, "the link on the dropped Added since.txt", v.Get(t, addedPage), missing)
	if got := sorted(sharedNames(t, o)); !slices.Equal(got, sorted([]string{entry("Clients", x.place), entry("Notes.txt", x.place)})) {
		t.Fatalf("Dóra's Shared with me eleven minutes on: %q", got)
	}
}

// The notice lasts seven days from when the storage was put back, however often the Drive starts meanwhile, and also
// when it is started without the settings; a newer rollback is told anew, and an older one is left aside. The name is
// text, cleaned and cut.
func TestTheRollbackNoticeLastsSevenDays(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	if r := meOf(t, d, bence).Rollback; r != nil {
		t.Fatalf("/api/me before any rollback: %+v", r)
	}
	at := time.Now().Add(-time.Hour).Truncate(time.Second) // the Drive was stopped for an hour
	to := at.Add(-30 * time.Hour)
	d.Restart(t, rollbackSettings(to, at, "  Réka <b>Kovács</b>\r\n"))
	r := meOf(t, d, bence).Rollback
	if r == nil || r.By != "Réka <b>Kovács</b>" || !r.To.Equal(to) || !r.At.Equal(at) || !r.Until.Equal(at.Add(7*24*time.Hour)) {
		t.Fatalf("/api/me after the rollback: %+v", r)
	}

	// Two hours before the seven days end, and after they end: the same settings, taken in already.
	d.Restart(t, map[string]string{harness.TestClockShift: (7*24*time.Hour - 2*time.Hour).String()})
	if r := meOf(t, d, bence).Rollback; r == nil || !r.At.Equal(at) {
		t.Fatalf("/api/me two hours before the seven days end: %+v", r)
	}
	d.Restart(t, map[string]string{harness.TestClockShift: (7 * 24 * time.Hour).String()})
	if r := meOf(t, d, bence).Rollback; r != nil {
		t.Fatalf("/api/me after the seven days: %+v", r)
	}

	// Started without the settings, a day on: the notice stays, from the index.
	d.Restart(t, map[string]string{harness.TestClockShift: "24h", "DRIVE_ROLLBACK_AT": "", "DRIVE_ROLLBACK_TO": "", "DRIVE_ROLLBACK_BY": ""})
	if r := meOf(t, d, reka).Rollback; r == nil || !r.At.Equal(at) {
		t.Fatalf("/api/me started without the settings: %+v", r)
	}

	// A newer rollback is told anew, with a name cut to 100 characters.
	newer := time.Now().Truncate(time.Second)
	long := strings.Repeat("Bence ", 30)
	d.Restart(t, map[string]string{harness.TestClockShift: "", "DRIVE_ROLLBACK_AT": newer.UTC().Format(time.RFC3339),
		"DRIVE_ROLLBACK_TO": at.Add(-time.Hour).UTC().Format(time.RFC3339), "DRIVE_ROLLBACK_BY": long})
	r = meOf(t, d, reka).Rollback
	if r == nil || !r.At.Equal(newer) || len([]rune(r.By)) != 100 || !strings.HasPrefix(r.By, "Bence Bence") || !strings.HasSuffix(r.By, "…") {
		t.Fatalf("/api/me after a newer rollback: %+v", r)
	}
	// One older than that is left aside, and the log says so.
	d.Restart(t, rollbackSettings(to, at, "Réka Kovács"))
	if r := meOf(t, d, reka).Rollback; r == nil || !r.At.Equal(newer) {
		t.Fatalf("/api/me after an older rollback was declared: %+v", r)
	}
	harness.Eventually(t, "the log says an older rollback is left aside", func() bool {
		return strings.Contains(d.Logs(), "older than the last one taken in")
	})
}

// A shared folder deleted since the snapshot has its top folder back at its own path, while the index held it in the
// trash: it is out of the trash again, as it was, for everyone who had it, and stays (30 days on, it is not removed).
// Nothing is given from the storage: the roles are those the index kept.
func TestASharedFolderDeletedSinceTheSnapshotIsBack(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: fastWalk("1h")})
	r, v := d.As(reka), visitor(d)
	payroll := madeSharedFolder(t, r, "Payroll")
	ok(t, "giving Dóra Payroll", sendJSON(t, r, http.MethodPut, accessPath(payroll.ID, dora), map[string]string{"role": "viewer"}))
	march := uploadedInto(t, r, payroll.ID, 0, "March.pdf", []byte("march"))
	_, page := madeLink(t, d, r, payroll.ID, march.ID, false)
	list(t, r, payroll.ID, 0)
	nextSecond(t, d)
	snap := d.Storage.Snapshot(t)
	ok(t, "deleting Payroll", deleteSharedFolder(t, r, payroll.ID))
	if _, sees := seesSharedFolder(t, d, dora, payroll.ID); sees {
		t.Fatal("Dóra sees Payroll in the trash")
	}

	rolledBack(t, d, snap)
	r, v = d.As(reka), visitor(d)
	harness.Eventually(t, "Payroll is back in Dóra's places", func() bool {
		_, sees := seesSharedFolder(t, d, dora, payroll.ID)
		return sees
	})
	if got := contentOf(t, d.As(dora), payroll.ID, march.ID); got != "march" {
		t.Fatalf("Dóra downloading March.pdf: %s", got)
	}
	if _, sees := seesSharedFolder(t, d, bence, payroll.ID); sees {
		t.Fatal("Bence, who had no role on Payroll, sees it")
	}
	if p := readPage(t, v, page); p.name != "March.pdf" {
		t.Fatalf("the link on March.pdf: %+v", p)
	}
	if got := deletedSharedFolders(t, r); len(got) != 0 {
		t.Fatalf("shared folders in the trash: %+v", got)
	}
	if res := restoreSharedFolder(t, r, payroll.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring Payroll, which is back: %d %s", res.Status, res.Body)
	}
	harness.Eventually(t, "the log says Payroll is back", func() bool {
		return strings.Contains(d.Logs(), "a shared folder in the trash is back")
	})

	d.Restart(t, map[string]string{harness.TestClockShift: "744h"})
	if got := contentOf(t, d.As(dora), payroll.ID, march.ID); got != "march" {
		t.Fatalf("Dóra downloading March.pdf 31 days on: %s", got)
	}
}

// Without a rollback, a shared folder in the trash whose top folder was put back at its own path on the storage
// directly, out of its entry, is restored by a manager as it is, rather than refused as taken.
func TestASharedFolderWhoseTopFolderIsBackIsRestoredAsItIs(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, dora)
	r := d.As(reka)
	payroll := madeSharedFolder(t, r, "Payroll")
	ok(t, "giving Dóra Payroll", sendJSON(t, r, http.MethodPut, accessPath(payroll.ID, dora), map[string]string{"role": "viewer"}))
	march := uploadedInto(t, r, payroll.ID, 0, "March.pdf", []byte("march"))
	ok(t, "deleting Payroll", deleteSharedFolder(t, r, payroll.ID))
	trash := ".superit-drive/trash/Shared folders/Payroll"
	entries := onStorage(t, d, trash)
	if len(entries) != 1 {
		t.Fatalf("Payroll's trash on the storage: %q", entries)
	}
	d.Storage.Move(t, trash+"/"+strings.TrimSuffix(entries[0], "/")+"/Payroll", "Shared folders/Payroll", true)

	ok(t, "restoring Payroll", restoreSharedFolder(t, r, payroll.ID))
	if got := contentOf(t, d.As(dora), payroll.ID, march.ID); got != "march" {
		t.Fatalf("Dóra downloading March.pdf: %s", got)
	}
	if got := onStorage(t, d, trash); len(got) != 0 {
		t.Fatalf("Payroll's trash on the storage after it was restored: %q", got)
	}

	// With its top folder both in its entry and at its own path, it is still refused: neither is taken.
	ok(t, "deleting Payroll again", deleteSharedFolder(t, r, payroll.ID))
	d.Storage.MakeFolder(t, "Shared folders/Payroll")
	if res := restoreSharedFolder(t, r, payroll.ID); res.Status != http.StatusConflict {
		t.Fatalf("restoring Payroll with something at its own path: %d %s", res.Status, res.Body)
	}
}

// What was restored from the trash since the snapshot is back in its entry in the trash: it goes back into the trash as
// itself, its sharing and link with it, as deleting it would, and comes back with them when restored. Access taken away
// since stays away.
func TestWhatWasRestoredSinceTheSnapshotIsBackInTheTrashAsItself(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence, dora}, Env: fastWalk("1h")})
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	missing := v.Get(t, noSuchLink(t))
	x := bencesFiles(t, d)
	ok(t, "sharing Notes.txt with Dóra", share(t, b, x.place, x.notes.ID, dora, "viewer"))
	ok(t, "sharing Offer.pdf with Dóra", share(t, b, x.place, x.offer.ID, dora, "viewer"))
	_, page := madeLink(t, d, b, x.place, x.notes.ID, false)
	deleted(t, b, x.place, x.notes.ID)
	deleted(t, b, x.place, x.offer.ID)
	trashOf(t, b, x.place)
	nextSecond(t, d)
	snap := d.Storage.Snapshot(t)
	restored(t, b, x.place, x.notes.ID)
	restored(t, b, x.place, x.offer.ID)
	ok(t, "taking Offer.pdf away from Dóra", unshare(t, b, x.place, x.offer.ID, dora))

	rolledBack(t, d, snap)
	b, o, v = d.As(bence), d.As(dora), visitor(d)
	harness.Eventually(t, "the trash holds Notes.txt and Offer.pdf as themselves", func() bool {
		var ids []int64
		for _, e := range trashOf(t, b, x.place) {
			ids = append(ids, e.ID)
		}
		slices.Sort(ids)
		return slices.Equal(ids, sorted64(x.notes.ID, x.offer.ID))
	})
	if l := list(t, b, x.place, 0); slices.Contains(names(l.Items), "Notes.txt") {
		t.Fatalf("Notes.txt is listed in Bence's My files, while it is in the trash: %q", names(l.Items))
	}
	if got := sharedNames(t, o); len(got) != 0 {
		t.Fatalf("Dóra's Shared with me while Notes.txt is in the trash: %q", got)
	}
	sameAsNoLink(t, "the link on Notes.txt in the trash", v.Get(t, page), missing)

	back := restored(t, b, x.place, x.notes.ID)
	if back.Item.ID != x.notes.ID || len(back.Path) != 0 {
		t.Fatalf("Notes.txt restored: %+v", back)
	}
	if got := contentOf(t, o, x.place, x.notes.ID); got != "notes" {
		t.Fatalf("Dóra downloading Notes.txt, restored: %s", got)
	}
	if p := readPage(t, v, page); p.name != "Notes.txt" {
		t.Fatalf("the link on Notes.txt, restored: %+v", p)
	}
	offer := restored(t, b, x.place, x.offer.ID)
	if offer.Item.ID != x.offer.ID || !slices.Equal(pathNames(offer.Path), []string{"Clients", "Bakery", "2026"}) {
		t.Fatalf("Offer.pdf restored: %+v", offer)
	}
	if got := contentOf(t, o, x.place, x.offer.ID); got != "404" {
		t.Fatalf("Dóra downloading Offer.pdf, taken away from her since the snapshot: %s", got)
	}
}

func sorted64(ids ...int64) []int64 {
	slices.Sort(ids)
	return ids
}

// The storage put back to a snapshot taken before the Drive first marked it as its own holds no mark. The rollback
// explains why, so the Drive writes its mark back, with the same id, and the looks go on: what went is gone. A snapshot
// taken after the Drive marked the storage holds the mark: if it does not, the rollback explains nothing, and nothing
// changes until the mark is back.
func TestARollbackToBeforeTheDriveMarkedItsStorage(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: fastWalk("1h")})
	d.Storage.MakeFolder(t, "")
	before := d.Storage.Snapshot(t)
	nextSecond(t, d)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	uploaded(t, c, placeID, "a.txt", []byte("a"))
	mark, ok := d.Storage.Get(t, ".superit-drive/storage-id")
	if !ok {
		t.Fatal("the Drive did not mark its storage")
	}

	rolledBack(t, d, before)
	c = d.As(reka)
	harness.Eventually(t, "the mark is back on the storage", func() bool {
		got, ok := d.Storage.Get(t, ".superit-drive/storage-id")
		return ok && string(got) == string(mark)
	})
	harness.Eventually(t, "the log says the Drive wrote its mark back", func() bool {
		return strings.Contains(d.Logs(), "wrote its mark back")
	})
	if got := names(list(t, c, placeID, 0).Items); len(got) != 0 {
		t.Fatalf("Réka's My files after a rollback to before it was made: %q", got)
	}
	uploaded(t, c, placeID, "b.txt", []byte("b"))

	// A snapshot taken after the Drive marked the storage, without the mark in it (someone removed it): the rollback
	// does not explain that, and nothing changes.
	nextSecond(t, d)
	nextSecond(t, d)
	d.Storage.Delete(t, ".superit-drive/storage-id", false)
	after := d.Storage.Snapshot(t)
	d.Storage.Put(t, ".superit-drive/storage-id", string(mark))
	uploaded(t, c, placeID, "c.txt", []byte("c"))
	missingMark := strings.Count(d.Logs(), "does not hold the Drive's mark")
	rolledBack(t, d, after)
	c = d.As(reka)
	harness.Eventually(t, "the log says the mark is missing", func() bool {
		return strings.Count(d.Logs(), "does not hold the Drive's mark") > missingMark
	})
	if got := names(list(t, c, placeID, 0).Items); !slices.Equal(got, []string{"b.txt", "c.txt"}) {
		t.Fatalf("Réka's My files without the mark: %q", got)
	}
	if _, ok := d.Storage.Get(t, ".superit-drive/storage-id"); ok {
		t.Fatal("the Drive wrote its mark on a storage put back to after it marked it")
	}
}

// The rollback settings go together and must make sense: otherwise the Drive does not start, and names what is wrong.
func TestTheDriveDoesNotStartWithBadRollbackSettings(t *testing.T) {
	t.Parallel()
	good := rollbackSettings(time.Now().Add(-48*time.Hour), time.Now(), "Réka Kovács")
	cases := []struct {
		set  map[string]string
		name string
	}{
		{map[string]string{"DRIVE_ROLLBACK_BY": ""}, "DRIVE_ROLLBACK_BY"},
		{map[string]string{"DRIVE_ROLLBACK_AT": "2026-11-20"}, "DRIVE_ROLLBACK_AT"},
		{map[string]string{"DRIVE_ROLLBACK_TO": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "DRIVE_ROLLBACK_TO"},
	}
	for _, c := range cases {
		env := map[string]string{}
		for k, v := range good {
			env[k] = v
		}
		for k, v := range c.set {
			env[k] = v
		}
		if out := harness.StartFailing(t, harness.Options{Members: []harness.Member{reka}, Env: env}); !strings.Contains(out, c.name) {
			t.Errorf("%q: the Drive did not name %s:\n%s", c.set, c.name, out)
		}
	}
}
