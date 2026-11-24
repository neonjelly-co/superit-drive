# Storage and the index

## Layout on the storage

Under `DRIVE_STORAGE_FOLDER`:

```
My files/
    reka@example.com/          one top folder per member's "My files", named after their email address
    bence@example.com/         when they first open the Drive; the index ties it to their user id
        Tax notes.pdf          their files and folders, with their real names, at any depth
        Clients/Bakery Ltd/
Shared folders/
    Clients/                   one top folder per shared folder, named after it
        Bakery Ltd/Offer.pdf
    Clients (2)/               a second shared folder called "Clients" (see below)
.superit-drive/                the Drive's own folders, hidden from WebDAV programs that hide dot-names
    trash/
        My files/reka@example.com/          each place's trash, beside the place's own path
            2026-11-19 7f3a9c1e/Offer.docx  one folder per deleted item: the day it was deleted and a random
                                            part, holding the item (a whole folder, for a folder) by its real name
        Shared folders/Payroll/
            2026-11-19 0c41d2aa/Payroll     a shared folder deleted whole: its top folder, beside what was
                                            deleted from it
    uploads/
        3f9c0b…e1                           one file per upload while its bytes arrive (a file sent through an
                                            upload-only link too), named after a random id; moved into its folder
                                            when whole
    index/
        1a5ff6fe…73/                        the copies of the index whose storage id this is (the mark's)
            copy-000000000042.sqlite        the latest copy of the index (see "The copy of the index")
            refused-…, newer-version-…,     copies the Drive set aside, and keeps (see "Starting from a copy")
            higher-numbered-…
    storage-id                              the Drive's mark: the id of the storage, which the index keeps too
```

Files and folders keep their real names, up to 255 bytes, exactly as given; the interface sends names in Unicode's
composed form (NFC), so a name from Safari on macOS matches the same name from another browser. Making a folder,
renaming and moving are each one change on the storage (`MKCOL`, or `MOVE` with `Overwrite: F`), after a look that
the name is free, and nothing ever replaces what is there.

**Names the storage refuses.** A storage can have names of its own that it will not hold: Apache, and so a Storage
Box, answers 403 to anything about a name that starts with `.ht` (`.htaccess`, `.htpasswd`). When the storage answers
403 where the Drive looks for, or puts, something under a name a person chose (an upload, a new folder, a rename, a
new shared folder, a file sent through an upload-only link), the person is told that the storage does not take this
name and to choose another (400, as for any name that cannot be used), not that the storage failed (502). The storage
does not say why it refused, so a storage that refuses every change (its folder made read-only, say) is told to people
the same way: the log says `the storage refused a name`, with what it answered, at most once a minute (`level=WARN`,
with a count of those left out), and never as an error for each.

**Uploads** stream straight through the Drive to a file of their own in the hidden uploads folder, never onto the
server's disk, and are moved into their folder (`MOVE` with `Overwrite: F`) only when whole: a half-finished file
never shows in a folder, whatever the storage does with a cut-off write. A file of up to 32 MiB arrives in one request,
stored with one `PUT` with its `Content-Length`, which writes the whole file or nothing. A larger one is a resumable
upload (see [Large transfers](operations.md#large-transfers-and-the-memory-figure)): its parts are written into its file one after
another, each from where the file ends (`PUT` with `Content-Range`), and one cut off goes on from where it stopped.
When the name is taken, the interface asks first, before the file travels: **replace** stores the upload, then moves
the file that is there into the trash and the upload into its place (the file comes back if that fails), and the file
keeps its place, its id and so its sharing; **keep both** stores the upload as `Name (2).ext`, `Name (3).ext` and so
on. A file never replaces a folder. The name is looked at again when the upload is moved into place.

An upload in one request holds nothing in its place while its bytes arrive, however slowly they come: neither the
place's lock nor its name. So the folder it goes into, and those above it, can be renamed, moved or deleted meanwhile,
and the shared folder deleted or the held My files handed over. Once the file is whole, the Drive holds the place,
decides again whether the person may upload into the folder (on everything read again, as a file sent through an
upload-only link is decided again), and only then claims the name and moves the file in, where the folder is now.
Someone who lost the right to upload there while their file arrived (a member's access taken away, a guest's
invitation taken away or ended, sharing outside the project switched off, the folder or the place gone) stores nothing
and is answered as they would be now. Of two uploads of one name at once, the first to be whole is stored, and the
other finds the name taken (409 with `exists`, as for any taken name).

**The trash.** Deleting moves the item, with everything in it, into the place's trash on the storage; restoring
moves it back to the folder it came from (or the place's top folder when that folder is gone), adding ` (2)` to its
name if the name is taken there. Items keep their ids throughout. The Drive removes an item for good 30 days after it
was deleted: when it starts, every hour, and before showing the Trash view. An entry in a trash that the index does
not know (left by a change the Drive did not finish, put back by a rollback, or put there directly), named as an entry
and holding exactly one file or folder, is shown in the Trash as deleted on the day its name says, by nobody the Drive
knows, going back to the place's top folder (after a rollback, one holding exactly an item restored since is that item:
see [After a rollback](#after-a-rollback)); any other is removed once the day in its name is 30 days past.

**Space used** is the Drive's own sum of the sizes of the files its index knows, in every place, the trash included,
shown in the sidebar as "about" against `DRIVE_CAPACITY` (or alone, without it). Files put on the storage directly
count once the Drive has looked at their folder (opened, or walked through); files gone from it count until they are
dropped from the index; uploads in progress count once they are in their folder.

An upload nothing has arrived for in a day is removed, with its file, when the Drive starts and every hour; so is a
file in the uploads folder the index does not know (left by an upload the Drive did not finish) once the storage says
it has not changed for a day. The Drive works through the abandoned uploads 100 at a time, so removing them costs the
same little memory however many there are. A person (a member, or a guest) has at most 50 resumable uploads in
progress at once, in every place together: one more is refused (429, with a message that says so) until one of
theirs is finished, cancelled, or removed after its day. The copies of the index have a hidden folder of their own, `index/` (see [The copy of the
index](#the-copy-of-the-index)); the looks and the walk never read `.superit-drive/`, and no route reaches it.

## Keeping in step with the storage

The storage wins for which files exist and what is in them; the index alone holds access. People and programs with the
storage login can change files behind the Drive's back, and the storage can be put back to an earlier state, so the
Drive looks at it again (`internal/drive/reread.go`, `walk.go`):

- **When a folder is opened.** Its list answers from the index at once, and the folder is looked at on the storage in
  the background: `"checking": true` says a look is under way. The interface then asks once more with `?wait`, which
  waits (at most ten seconds) until the look is done, starts no other, and answers the list as the index has it then
  (`checking` is still true only when the look is not done; a folder the look found gone answers 404). The interface
  shows that list at once, unless the person is acting on the list (an item selected, a dialog open, a change under
  way): then a line above it says "This folder changed on the Drive storage", with "Show the changes", and the list
  changes when they are done. At most four looks run in the background at once and 64 folders wait for one; a folder
  opened again while it is looked at gets one more look after, never more.

  ```
  GET    /api/places/{place}/items[?wait]                 200 {"items", "path", "fromTop", "role", "can", "checking"}
  GET    /api/places/{place}/items/{folder}/items[?wait]
  ```
- **The walk.** Now and then the Drive looks at every folder of every place, and at every place's trash, one at a time:
  a minute after it starts, then six hours after each walk ends (at once after a [rollback](#after-a-rollback), and
  again eleven minutes after that walk ends). After each folder it rests half a second, or three
  times as long as the folder took when that is longer (at most a minute), so a slow storage is asked less often. Its
  requests are listings (`PROPFIND` with `Depth: 1`, a few hundred bytes per entry) and, rarely, a look at one address;
  never a file's bytes, so it takes no transfer slot and holds one connection to the storage at a time. A walk of
  100,000 files in 5,000 folders takes at least 42 minutes. Where it is (the last folder it finished) is kept in the
  index, so a walk a restart cut short goes on from there. It logs when it starts (or goes on), every 1,000 folders,
  and when it ends, with what it found: `folders`, `trashes`, `added`, `changed`, `back`, `hidden`, `dropped`, `unread` (folders the storage
  did not answer for), `tooLarge` and `untrusted` (folders not looked at for want of the mark, below).
- **Zips** look at each folder they put in, as they go.

What one look does, in one folder:

- **New on the storage**: a file or folder the index does not know is added, with nothing given on it, so it has the
  access of the folder it is in and nothing else. Access is never read from the storage.
- **Changed on the storage**: a file whose size or time changed keeps its id, as when an upload through the Drive
  replaces it: a file written over in place keeps its sharing and its public link, which serves the new content.
- **Gone from the storage**: what the index knows and the look does not find is hidden at once (not listed, opened,
  found, nor reached through a share or a link) and kept, with its id, what was given on it and its link. It is dropped
  from the index (with everything in it, what was given on it, its public link and uploads in progress into it; what
  in the trash would go back into it goes back to the top folder) only when a look at least ten minutes later still
  misses it, and a request for its own address finds nothing there (or another item now has its name there). Nothing
  is ever dropped on an error, a listing cut short, or a single look.
- **Back on the storage**: a hidden item that the storage shows again exactly as the Drive last knew it (the same name,
  kind, size and time of its last change, as a storage that answered wrong for a while gives it back) is the same item
  again, with its sharing and its link. Anything else that takes its name (a file put there anew, even with the same
  content; a folder made anew) is a new item, so a public link never serves a file that merely took the name. A file
  renamed or moved on the storage is one item gone and another new: its sharing and its link do not follow it.
- **The Drive's own changes win**: a look reads the storage without holding the place's lock, then takes it to compare
  and apply. What the Drive changed in the folder since the look began, or is changing right now (a name in it being
  uploaded, renamed, moved, deleted or restored, or the folder or one above it being moved), is left as it is. An
  upload's bytes arrive in the hidden uploads folder, so a look never sees a half-finished file.
- **Names the Drive would refuse** (empty, `.` or `..`, holding `/`, a line break or another control character, not
  text, longer than 255 bytes) are left out: never listed, opened, zipped, found or counted, and left on the storage.
- **Bounds**: a look reads a folder's entries as they arrive and keeps at most 10,000 of them; a folder holding more is
  not looked at (nothing in it changes in the index, and the walk logs it). Folders are taken in at most 255 deep below
  a place's top folder. A place's top folder gone from the storage is a place emptied; a look only reads, and the top
  folder is made again when something is put into it.
- **Only on the Drive's own storage**: the storage holds the Drive's mark, `.superit-drive/storage-id`, written when the
  first place is made, holding the id the index keeps. A look that would change anything checks it first; without it,
  or with another id (a storage that is empty, mounted wrongly, someone else's, or put back to before the Drive marked
  it), nothing is added, hidden or dropped, and the log says so every ten minutes, with the id the file should hold:
  written back, looks go on. Most looks change nothing, and then ask and write nothing more. The Drive writes the mark
  back itself only after a [rollback](#after-a-rollback) to a snapshot taken before it last wrote it.

The trash and the hidden folders are on the storage too. The Trash view looks at each place's trash on the storage
before it answers, and the walk looks at every place's trash. An item of a trash whose entry is gone, or no longer
holds it as the Drive knew it, is hidden from the Trash (restoring it answers as for something not there) and dropped
as above; back as it was, it is back. An entry the index does not know is shown in the Trash as [above](#layout-on-the-storage).
What is put into the uploads folder directly is never shown, and removed once a day old. Nothing put on the storage
makes a place or gives anyone access: a folder in `Shared folders/` or `My files/` that belongs to no place is seen by
nobody, and the walk logs how many there are (and the names of those in `Shared folders/`; those in `My files/` are
named after email addresses); a folder called `.superit-drive` inside a place is a folder like any other there. A folder
`My files/<address>` that is there before that member first opens the Drive becomes their My files, as when an index
is built anew from a storage that holds files: only they see it.

Search and "Shared with me" read only the index: what appears on the storage is found once a look took it in (its
folder opened, or the walk); what goes is not found from the first look that missed it.

**After the storage is put back to an earlier state without the Drive being told** (a self-hoster's own WebDAV put back;
the Drive is stopped meanwhile, and the walk goes on a minute after it starts): files that were added or changed since
are gone or as they were; the looks hide what the index knows that is gone, and drop it with its sharing ten minutes
later; files that are back (deleted or moved since) are new items with their folder's access only; files changed since
keep their ids with their older content; trash entries made since are gone (their items hidden, then dropped), and
entries that are back (restored or removed for good since) show in the Trash as entries put there directly; a shared
folder deleted since stays in the trash, and restoring it takes its top folder as it is back at its own path; an upload
in progress whose file went starts again from its beginning. Sharing itself is never put back. The mark is in every
snapshot taken after the Drive's first place, so looks go on after a rollback; one to before that changes nothing until
the mark is written back. Told about it in its settings, the Drive does more: see [After a rollback](#after-a-rollback).

## After a rollback

A project admin can put Superit's Drive storage back to one of its daily snapshots, from the dashboard: a rollback. The
storage then holds the files as they were when the snapshot was taken, and everything written since is gone, the
Drive's trash and uploads in progress included. The index on the server is not put back: who has access to what stays
as it was just before, except on what no longer exists. Whoever runs the Drive tells it in three
settings, and the Drive does the rest (`internal/drive/rollback.go`). A self-hoster who puts their own WebDAV back can
use them the same way.

**What the platform passes.** The daemon stops the Drive, the storage is put back, and the daemon starts the Drive
again, only once the storage is back, with these environment variables:

| Variable | Value |
|---|---|
| `DRIVE_ROLLBACK_AT` | When the rollback was done (when the platform ran it), as RFC 3339 with its offset: `2026-11-20T09:41:00Z`. Every rollback has its own: it is how the Drive tells one from another, to the second. |
| `DRIVE_ROLLBACK_TO` | When the snapshot it went back to was taken (the snapshot's `created` from Hetzner), as RFC 3339: `2026-11-19T03:00:00Z`. Earlier than `DRIVE_ROLLBACK_AT`. |
| `DRIVE_ROLLBACK_BY` | The name of the project admin who did it, as members should read it: their display name (their email address when they have none). Plain text, never markup: the Drive turns control characters into spaces, takes out the characters that turn the direction of text, and cuts it to 100 characters, the last one "…". |

- Pass all three, or none. With only some, a time the Drive cannot read (a date alone, a time without its offset, a
  number), `DRIVE_ROLLBACK_TO` not before `DRIVE_ROLLBACK_AT`, or a name with nothing left in it, the Drive does not
  start: it exits with 1 and its log names every variable that is wrong, as for any setting.
  `superit-drive check-storage` does not read them.
- Keep passing the same values on every later start (a new version, a new capacity), until the next rollback replaces
  them. The Drive takes a rollback in once and keeps it in its index: the same `DRIVE_ROLLBACK_AT` again is old news (it
  logs `the rollback of the storage in the settings was taken in already`), and one older than the last it took in is
  left aside (a warning). Leaving them out later changes nothing: the notice runs its seven days all the same.
- An install that is given them for a rollback it never saw (a new server, an index made anew) takes it in as new: it
  costs one walk through the storage, and shows the notice for what is left of the seven days.

**What the Drive does with a new one.** It logs `the storage was put back to a snapshot`, with the three times, and walks
through the storage at once, from the top (a walk the stop cut short is begun again): first every shared folder in the
trash and every place's trash, then every folder. The looks then do what they always do ([Keeping in step with the
storage](#keeping-in-step-with-the-storage)), with the rules below on shared folders, the trash and the mark until that
walk has ended with the mark there throughout and every shared folder in the trash looked at (a restart before then goes
on with them; a walk that could not, the next one, six hours later, begins with them again):

- **Added since the snapshot**: gone. Hidden at the first look, so its sharing and its link stop at once; dropped with
  them when a look at least ten minutes later confirms it. The walk after the one that takes the rollback in comes eleven
  minutes after it ends, not six hours, so that happens within about two walks, and the space used is right again.
- **Changed since**: the same item, with the content it had in the snapshot, its sharing and its link.
- **Deleted, moved or renamed since**: back where it was in the snapshot, as a new item with the access of its folder
  and nothing of its own: the Drive cannot tell it for the item it was, and a share or a link that stopped when it was
  deleted does not start again by itself. It can be shared again; the notice says so.
- **Sharing is never put back, and nothing is given from the storage**: access taken away since, and a link removed
  since, stay away; nothing that was removed comes back.
- **Sharing made after the snapshot applies to the files as they were then.** The index keeps today's links, shares and
  invitations, and the storage holds the snapshot's files, so a link or a share made on a folder after the snapshot
  reaches what the folder held at the snapshot: a file deleted from it before the link was made is in it again (as a
  new item, with the access of its folder), and is served through that link; a file replaced before it was shared
  serves its older bytes. Visitors and guests see no notice. After a rollback, whoever shared a folder outside the
  project since the snapshot should look at what is in it now; "Sharing and space" lists what is shared outside.
- **A shared folder deleted since** has its top folder back at its own path, while the index holds it in the trash. Its
  entry in the trash no longer holds it, so it is out of the trash again, as if a manager had restored it: with its
  managers, the roles given on it, what was given inside it and its links, as the index kept them. (A manager restoring
  such a shared folder by hand gets the same, with or without a rollback.)
- **Restored from the trash since**: back in its entry of the trash. An entry the index does not know that holds exactly
  an item the place has outside the trash (the same name, kind, size and time of its last change), which is no longer
  where the index has it, is that item: it goes back into the trash as itself, with what was given on it and its link,
  which work again when it is restored. Any other entry that is back (an item removed for good since, or changed after it
  was restored) shows in the Trash as an entry put there directly.
- **The mark**: a snapshot taken after the Drive first marked the storage holds the mark, so looks go on. One taken
  before that does not, and the rollback says why: when the snapshot was taken before the Drive last wrote its mark (or,
  for an index made anew against a storage that held the mark, before the time the storage gives the mark) and the
  storage holds none, the Drive writes it back with the id its index keeps, logs it, and looks go on (what the Drive had
  made since is then gone, as the admin was told). A mark with another id is never replaced, and a mark missing from a
  snapshot taken after the Drive wrote it still stops every look, as without a rollback.
- **Places made since** (a shared folder, a member's first My files) stay, emptied: a place is never removed for want of
  its top folder. A shared folder's managers can delete it.
- **The copy of the index on the storage** is the snapshot's again, older than the index. The Drive never adopts it (it
  starts with its own index), and copies its index as it starts, which replaces it; after a rollback to before the mark,
  once the walk has written the mark back. A Drive started later from that copy (a server lost) has the sharing as it is
  now, and knows the rollback was taken in.

**The notice.** For seven days from `DRIVE_ROLLBACK_AT` (from when the Drive took it in, when that time is still to come
by the Drive's clock), `GET /api/me` answers members with

```
"rollback": {"by": "Réka Kovács", "to": "2026-11-19T03:00:00Z", "at": "2026-11-20T09:41:00Z", "until": "2026-11-27T09:41:00Z"}
```

and the interface shows it above the list, in every view, in the page's language (see [Languages](using-the-drive.md#languages)), as "The
files were put back to 19 November at 04:00" (the time in the browser's time zone): "Réka Kovács put the Drive's files back to how they were then, on 20 November. Files
added after that are gone, changes made after that are undone, and files deleted after that are back. Files that came
back may need to be shared again." The name is shown as text. A member can close it: it stays closed in that browser,
for that rollback, and nowhere else; a new rollback shows again. Visitors through a public link are told nothing: their pages say nothing
about the project but who shared the item. Guests are not told either.

**Connections to the storage.** A server closes a connection kept open between requests after a while, and can do so
just as the Drive sends on it; the Drive then cannot tell whether the server acted. So every request that changes the
storage (`PUT`, `MKCOL`, `MOVE`, `DELETE`) goes on a new connection of its own, which a server never closes for being
idle, and is never sent twice. Reads (`GET`, `PROPFIND`) use kept connections, closed by the Drive after four seconds
idle, and are sent once more on a new connection when the server closed the kept one first.

## The copy of the index

The index alone holds who has access to what, and it lives on one server. So the Drive keeps copies of it
(`internal/drive/copies.go`):

- **in its data folder**, at `backup/index.sqlite`: the whole index as one SQLite file, made while the Drive runs with
  SQLite's `VACUUM INTO` (which reads the index in one read transaction, so the copy is the index as it was at one
  moment) and renamed over the one before, so whatever reads it, at any moment, reads one whole copy. It is the file a
  backup of the server archives (see [Backing up the Drive](#backing-up-the-drive));
- **on the storage**, at `.superit-drive/index/<storage id>/copy-<number>.sqlite`: the same copy without what lets
  guests in now or belongs to one server (below), for a Drive on a new server to start from (see [Starting from a
  copy](#starting-from-a-copy)).

**When.** Both together: when the Drive starts; soon after access changes; every night at 03:00 UTC, whatever
changed; and as the Drive stops (after the last request is answered), when access changed since the last copy, so that
a snapshot of the data folder taken once it has stopped, as before an upgrade, holds every change.

An access change is anything that changes who may reach what: a share given, changed or taken away; a public link made,
replaced or removed, or its kind changed (view or upload-only); a guest invited, their invitation changed, or gone with
it; a shared folder made, deleted, restored or removed for good, and its managers; a switch of sharing outside the
project; a held My files handed over or deleted; and an item renamed, moved, deleted or restored (what was given on it,
and on what is in it, hangs on its id, which an older copy would not find where the storage now has it). The index
counts them itself, in the transaction that makes them (migration `0011`: triggers on its tables), so none is missed,
whatever makes it: a look at the storage that drops something shared counts too. A burst of them makes one copy: it is
written once nothing changed for 30 seconds, and no change waits more than five minutes for its copy, however much
changes meanwhile. Two copies for access changes are at least five minutes apart: renaming a file is an access change
anyone who may edit can make, and each copy is the whole index, so the first change after a quiet while has its copy
in 30 seconds, and one that follows that copy closely has its own up to five minutes after it, with those since. Files added, folders made and how often links and invitations were used are not access changes: a
copy older than them finds the files on the storage, with the access of their folder, and the counts are a night behind
at most.

**How fresh.** At any moment both copies hold every access change made more than five minutes before (and every one
made more than 30 seconds before a pause in them, when no copy was written for an access change in the five minutes
before), and everything else as it was at the last copy: the last access
change's, or last night's at the latest. The log says each copy, with why it was made, its number and its size. When the
storage does not take a copy (it does not answer, or does not hold the Drive's mark), the data folder's copy is
written all the same, and the storage is asked again every five minutes: once it answers and holds the mark, a new copy
goes there.

**On the storage, safely.** A copy is written under a name of its own (`upload-<random>.part`, one `PUT`), then moved to
its name (`MOVE`, never replacing anything), and only then is the copy before it removed: a whole copy is there
throughout, and a copy cut off midway never has a copy's name. Each copy has the number after the last, kept in the
index; the folder holds one copy, two for a moment, besides the copies the Drive set aside (see [Starting from a
copy](#starting-from-a-copy)), which it never removes. A copy goes on the storage only while the storage holds the
Drive's mark with the id its index keeps: never onto a storage that is empty, mounted wrongly or someone else's, and the
folder is named after that id, so the copies of another index are never removed. Making a copy streams from disk to
disk, and putting it on the storage streams from the file, over one connection with the capped buffers every connection
to the storage has: it takes no transfer slot (as the walk takes none), so it never makes anyone wait their turn, and
its memory does not grow with the index ([measured](operations.md#large-transfers-and-the-memory-figure)). The copy for the storage
is copied, put on the storage, and (when a Drive starts from it) read back four MiB at a time, each part synced to disk
and let go of by the page cache before the next: the kernel's page cache counts against the container's memory too, and
a copy of 33 MB written in one go stopped the whole Drive for minutes in the memory test. Its temporary files are made
in the data folder, which is on disk, never in `/tmp`, which can be memory.

**What the copy on the storage leaves out.** Whoever has the storage login can read it, so it holds nothing that lets
anyone in by itself, and nothing that only one server can honour:

| Left out | Why |
|---|---|
| Guests' sessions (`guest_sessions`: each token's SHA-256) | A session is one browser's on one server. A hash cannot be turned back into a token, but a planted session would sign someone in; a Drive on a new server starts with none, and guests sign in again with a new link. |
| Codes of the sign-in links mailed to guests (`guest_codes`: HMACs) | They last thirty minutes, and are keyed from `DRIVE_HEADER_SECRET`, which a new server does not have. |
| `guest_limits` | Counters an earlier version kept of wrong codes; nothing is counted there now. |
| Uploads in progress (`uploads`) | Their uploader's browser on the old server; their files in `.superit-drive/uploads/` are removed a day after they were last written to, as any the index does not know. |
| Where the walk is (`walk_cursor`, `walk_started`, `walk_finished`) | By one server's clock; a Drive started from a copy walks the storage at once, from the top. |

What is left out is overwritten with zeros in the copy's file (SQLite's `secure_delete`), not only unlisted.
Everything else is kept: the places, the folder tree, the trash, every grant (to members, to everyone, and guests'
invitations with their end dates), public links (by their token's SHA-256: the address is in no copy, so a copy opens no
link by itself, and the same address works again on a new server), with their kind and what an upload-only link took in
(as of the copy: taking files in is no access change), guests by address, the switches of sharing outside
the project, the record of acts on held My files (and an act under way, which a Drive started from the copy finishes),
how often links and invitations were used, the storage's id, what the Drive knows of rollbacks (`rollback`,
`rollback_walk`, `storage_marked`): a Drive started from the copy knows a rollback it was told about is old news, and
goes on with the walk after one that was under way; and the copies made by a newer version that it has still to move
aside (`newer_copies`, below), which a Drive started from the copy moves aside in its turn.

## Starting from a copy

A Drive whose data folder holds no `index.sqlite` makes its index from a copy before it starts (`internal/drive/adopt.go`),
when there is one:

- the data folder's own copy, `backup/index.sqlite` (a backup of the server put back: see [Backing up the
  Drive](#backing-up-the-drive));
- the latest copy on the storage, in the folder of the id in the storage's mark, and only one whose index keeps that
  same id. When the data folder's copy belongs to that storage too, the newer of the two (by number) is taken, the data
  folder's when they are the same copy. The number decides whichever versions made them, as long as this Drive knows
  both schemas: after an upgrade to a version of the same schema that failed and was undone, the newer version's copy
  on the storage is taken (see [An upgrade that failed](#an-upgrade-that-failed)).

A copy becomes the index only once it passes a check: SQLite's integrity and foreign key checks; a schema exactly what
this Drive's migrations make for its version (so no table, index, trigger or view of its own); every place's top
folder, and every held act's, one name in `My files/` or `Shared folders/` (never the Drive's hidden folder, nor outside
its folder); a number that copies reach. One made by an older Drive is brought up to date, as any index is. A copy from
the storage is then cleared of what a copy leaves out (in case someone put it there), and the Drive walks the storage at
once, every trash first, as after a rollback: what changed on the storage since the copy was made comes in as the looks
always bring it (a file put there since has the access of its folder; what is gone is hidden at once, its sharing and
its link with it, and dropped ten minutes later), and an item deleted since, which the copy has where it was, is found
in its entry of the trash as itself, with what was given on it and its link, which work again when it is restored. The
log says which copy it was, when it was copied, and how many places, items, shares, managers, links, guests and acts it
brought; guests sign in again with a new link.

- **A copy made by a newer Drive** (its schema is newer than this one's): the Drive does not start, and says so. An
  index built without it would soon put a copy without the shares in its place. Start a version as new as the one that
  made it. The one exception is a newer copy on the storage when the data folder holds a copy that passes the check,
  belongs to that storage and is of this Drive's own schema, as after an upgrade that failed and was undone (see [An
  upgrade that failed](#an-upgrade-that-failed)): the Drive then makes its index from the data folder's copy and starts,
  and says so loudly (`level=ERROR`, `THE COPY OF THE INDEX ON THE STORAGE IS LEFT ALONE`): what changed in the index
  under the newer version is not in its index. The newer copy is neither removed nor replaced. The Drive numbers its own
  copies after it, so they are the ones adopted from then on, by this version or a newer one; once its first copy is on
  the storage, it moves the newer copy aside, to `newer-version-<when>-<its name>`, and keeps it. A Drive that dies
  before that, or one started from its copy on a new server, moves it aside in its turn (the index, and its copies, keep
  which copies are still to be moved, as `newer_copies`); a move the storage refuses is tried again after the next copy,
  and the newer copy stays where it is meanwhile. One whose number is past any a copy reaches (2⁵², by its name) is not
  left alone: the Drive does not start.
- A copy numbered after the Drive's own, which the index does not know of, is never removed. It is there when the Drive
  once started with no index while it could not see its mark on the storage (a wrong `DRIVE_STORAGE_FOLDER`, a disk not
  mounted yet): it built an empty index, and took the mark when the storage came back. (Or when its data folder was put
  back to an older state than the storage's copy.) When its first copy is on the
  storage, it moves the higher-numbered one aside, to `higher-numbered-<when>-<its name>`, keeps it, and says so loudly
  (`level=ERROR`, `THE STORAGE HELD A COPY OF THE INDEX NUMBERED AFTER THIS DRIVE'S OWN`). To get its shares, links and
  guests back: stop the Drive, give the copy back its name, and start the Drive with an empty data folder.
- **A damaged copy**, or **one of another storage's index**: not adopted. It is moved aside on the storage, to
  `refused-<when>-<its name>` (or beside the data folder's copy, as `index.sqlite.refused-<when>`), and kept; the Drive
  builds its index from the files, and the log says so loudly (`level=ERROR`, in capitals). One larger than 4 GiB (no
  index comes near it) is not downloaded, and is left where it is.
- **No copy**: the Drive builds its index from the files on the storage: each member's My files is the folder of their
  address when they first open the Drive, with what is in it; there are no shares, links or guests, and folders in
  `Shared folders/` belong to no shared folder (the walk logs them).
- **Copies, but no mark on the storage**: none is adopted (whose they are cannot be told), the log says so loudly, and
  the copies stay. If the storage is this Drive's, stop it, write the id of the copies to adopt (the name of their
  folder) into `.superit-drive/storage-id`, and start it again with an empty data folder.
- **The storage cannot be read** (three tries, a few seconds apart): the Drive does not start, and says why. An
  index made without knowing whether there is a copy would replace it. A container started again by its supervisor
  (Docker's `--restart`) tries again.

A Drive that starts with its index never adopts a copy: a running Drive, or one started again (a new version, a new
capacity, a rollback). After a rollback the storage's copy is the snapshot's, older than the index (see [After a
rollback](#after-a-rollback)).

**What someone with write access to the storage can do with a copy.** Anyone with the storage login reads and changes
every file, and so the copies. A running Drive never reads a copy; a Drive started with an empty data folder adopts what
is there. Through a copy they planted or changed, they can give more than the files they already hold:

- access to any file in the Drive to any member (a grant), or a member's My files to another member, or the manager role
  on shared folders;
- public links whose address they chose (anyone with it reaches those files through the Drive, until the link is
  removed), and invitations to addresses they control (to sign in, a guest still needs the link mailed to that address);
- sharing outside the project switched back on, where it was off;
- an act under way that hands over or deletes a held My files (it changes only what is in the Drive's folder), and a
  record of acts that never happened, or without those that did;
- a copy that keeps a Drive from starting (one that claims a newer schema), or one that is refused (the Drive starts
  with no shares); a copy that claims a newer schema makes a Drive whose data folder holds its own copy (a backup put
  back) start from that copy, not from a later one on the storage, as removing the later one would. Such a copy is moved
  aside and kept, never adopted.

They cannot: add members or project admins (the member list comes from the platform); sign anyone in as a guest
(sessions and codes are cleared); reach outside the Drive's folder, or into its hidden folder (the check); put code into
the index (no trigger or view of its own); or, through the copies a copy says are still to be moved aside, have the
Drive move anything but a copy numbered before its own latest, which it would otherwise remove. What bounds it: a copy
is adopted only with an empty data folder; the check; the log line that counts what it brought; and project admins see
every public link and every guest's invitation in "Shared outside the project". Nothing prevents it: a copy signed with
a key made from `DRIVE_HEADER_SECRET` would not open on a new server, whose daemon makes a new secret. The storage login
opens the sharing as well as the files; Superit gives customers no login to the Storage Box.

## Backing up the Drive

The data folder holds:

| File | What it is | Archive it? |
|---|---|---|
| `index.sqlite`, `index.sqlite-wal`, `index.sqlite-shm` | The live index and SQLite's write-ahead log. | **No.** SQLite writes the log and the file at different moments: copies of the two read one after the other, while the Drive runs, can be a damaged index, or lose changes. |
| `backup/index.sqlite` | The copy of the index: whole and consistent at every moment, renamed over the one before. It holds every access change but those of the last five minutes (see [The copy of the index](#the-copy-of-the-index)). | **Yes**, while the Drive runs; nothing needs to be stopped. |
| `.copy-<random>.sqlite` | A copy being made, cleared or adopted; removed when the Drive starts. | No. |

**What the Superit agent must do.**

- **Back up, and snapshot before an upgrade**: archive the data folder (`/mnt/data/superit-drive`) without `index.sqlite`,
  `index.sqlite-wal` and `index.sqlite-shm`, with the Drive running. With `tar -C /mnt/data`:
  `--exclude=superit-drive/index.sqlite --exclude=superit-drive/index.sqlite-wal --exclude=superit-drive/index.sqlite-shm superit-drive`.
  Everything else in the folder (`backup/index.sqlite`, and the agent's own `.superit-storage.json`) goes in the archive.
- **Restore** (the same server, or a new one): stop the Drive; replace the data folder with the archive's, so that it
  holds `backup/index.sqlite` and no `index.sqlite` (owner `65532:65532`, mode `700`, as for any start); start the Drive
  with the same storage settings (the header secret may be new). Starting, it makes its index from `backup/index.sqlite`,
  or from the copy on the storage when that one is newer (the storage's copy is usually fresher than a nightly backup,
  unless the storage was rolled back since); the log says which. From the data folder's copy, guests stay signed in.
  Never put an archived `index.sqlite` back, nor a copy at `index.sqlite` while an `index.sqlite-wal` is there.
- **A new server without a backup**: start the Drive with an empty data folder and the same storage settings: it makes
  its index from the copy on the storage.
- **An upgrade that fails**: stop the newer version, put the snapshot back as for a restore, and start the older version
  with the same settings. What it then keeps and loses depends on the newer version's schema: see [An upgrade that
  failed](#an-upgrade-that-failed).
- **The upgrade test** (snapshot, wipe, restore, check that a share is there and a file downloads) passes with the above
  as it is: the share is in `backup/index.sqlite` once the copy after it has been written, 30 seconds after the share (or
  at once when the Drive is stopped and started again before the snapshot). After an upgrade that failed, it can check
  that the older version starts, that what was shared before the upgrade is there, and that a file put on the storage
  under the newer version is still there (the storage is not put back). A share or a link made under the newer version
  is gone only when the newer version has a newer schema; with the same schema, it stays.

Running the Drive yourself, back up `backup/index.sqlite` the same way, and restore it as above.

## An upgrade that failed

The Superit agent upgrades the Drive by stopping it (it copies its index as it stops), taking a snapshot of its data
folder (`backup/index.sqlite`, never the live index), and starting the newer version with the data folder as it is. The
newer version brings the index up to its schema and copies it, into the data folder and onto the storage, as it starts
(numbered after the snapshot's copy) and after access changes. When a step fails, the agent stops the newer version,
puts the snapshot back as the data folder and starts the older version. Neither the files on the storage nor the copies
there are put back: the older Drive starts with no index, its own copy in the data folder, and a later copy on the
storage, made by the newer version (see [Starting from a copy](#starting-from-a-copy)).

| The newer version's schema | The older Drive starts from | Kept | Lost |
|---|---|---|---|
| Newer (a migration the older version lacks) | The data folder's copy: the snapshot, the index as the older version left it when it stopped for the upgrade. The log says `THE COPY OF THE INDEX ON THE STORAGE IS LEFT ALONE` (`level=ERROR`). | Everything in the index from before the upgrade, guests' sessions included. The files on the storage as the newer version left them, taken in by the walk as after any start from a copy: a file added under it has the access of its folder, and one deleted under it is in the trash as itself. | What changed in the index under the newer version: shares, links, guests' invitations and sessions, shared folders and their managers, switches, acts on held My files. A file moved or renamed under it loses what was given on it: it is a new item at its new place, with the access of its folder. All of it but guests' sessions is in the newer copy, which the Drive moves aside on the storage, as `newer-version-<when>-copy-<number>.sqlite`, and keeps. |
| The same | The newer version's last copy on the storage, numbered after the snapshot's. The log says the Drive made its index from the copy on the storage. | What changed in the index under the newer version, up to its last copy (the copy it makes as it stops holds every change, unless it was killed). The files on the storage, as above. | Guests' sessions (a copy on the storage holds none: guests sign in again, with a new link), and, when the newer version was killed rather than stopped, what changed after its last copy. |

**Access removed under the newer version is back.** In the first row, "what changed in the index under the newer
version" is lost both ways: what was given under it is gone, and what was *taken away* under it works again. A public
link removed then opens again at its old address; a share or an invitation taken away then lets its member or guest in
again; a guest signed out then (their sessions are in the snapshot) is signed in again. The Drive does not take the
removals over from the newer copy: it does not know that copy's schema, and cannot tell a removal that was meant from
the rest of what changed. It compares instead, as it starts, the sharing its index holds with the newer copy's, and
logs `REMOVED UNDER THE NEWER VERSION, AND BACK` (`level=ERROR`) with how many public links, shares and invitations are
back, and a line for each link (`a public link removed under the newer version works again`, with its place, item and
name; the first hundred). Sharing outside the project that was switched off under the newer version is on again too,
for the whole project or for a shared folder: the Drive logs `SWITCHED OFF UNDER THE NEWER VERSION, AND ON AGAIN`
(`level=ERROR`) for each, with the shared folder's place. (An end date brought forward under the newer version is back
where it was, and is not named.) When nothing is back it says so, and when the newer copy's sharing cannot be read (a
later version changed those tables) it says that it cannot tell. After an upgrade that was undone, read those lines,
and remove again what should not be back: the links they name, the switches, and for shares and invitations whatever
"Sharing and space" and each shared folder's access show that should no longer be there.

With the same schema the later copy wins, not the snapshot's, because the files on the storage were not put back
either: the later copy's sharing is on the files as they are.

The upgrade tried again starts the newer version with the older version's index: it adopts nothing, numbers its copies
after the older version's, and leaves a copy set aside where it is.

An index of a newer schema in the data folder itself (the image put back to an older version, the data folder not)
stops the older version from starting, with `the index was made by a newer version of the Drive` and both schema
versions: it would skip the migrations it does not know and work on tables whose rules it does not keep. Put the data
folder back as it was before the upgrade, as above, or start the newer version.

Versions up to 0.2.0 do not leave a newer copy alone: after an upgrade from one of them to a newer schema fails, the
older version does not start. Starting the newer version again on the restored data folder does: it makes its index
from its own copy on the storage, which holds what changed under it.
