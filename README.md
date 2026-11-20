# Superit Drive

Superit Drive ("the Drive") is where a project's people keep, open and share their files, in the browser. Each member
has "My files", private to them, and the project has shared folders, owned by the project and not by a person; files
live on WebDAV storage (a Hetzner Storage Box bought from Superit, or the customer's own WebDAV server). It runs as one
small container on the customer's own server.

Version 1 is being built. The spec is [neonjelly-co/superit#38](https://github.com/neonjelly-co/superit/issues/38);
the build tickets are this repository's issues. Words follow the `GLOSSARY.md` of neonjelly-co/superit (member,
guest, My files, shared folder, Drive storage, trash). The Drive also runs without Superit: see
[Running it yourself](#running-it-yourself).

## How it works

- One Go binary. The interface (TypeScript and React, in `web/`) is compiled into it as static files.
- Files live on the storage as a readable folder tree with real names. The index, one SQLite file in the data
  folder, holds the places, the folder tree, the access given on places and items and (as later tickets add them)
  links and guests. The storage wins for which files exist; the index alone holds access. Lists come from the index,
  and the Drive looks at the storage again when a folder is opened and in a slow walk now and then, so changes made on
  the storage directly reach the index (see [Keeping in step with the storage](#keeping-in-step-with-the-storage)).
- The index is copied into the data folder, for backups, and onto the storage, so that a Drive on a new server starts
  with the sharing the lost one had (see [The copy of the index](#the-copy-of-the-index)).
- Members do not sign in to the Drive. A reverse proxy in front of it signs them in and adds headers saying who they
  are (see below). Everyone else comes in under one public prefix, `/s/`, which the reverse proxy opens without sign-in:
  visitors with a public link (see [Public links](#public-links)), and guests, who sign in there with a code mailed to
  them (see [Guests](#guests)). Every route passes through one authorisation function, `internal/access`.

### Signing in: headers and the member list

A signed-in request must carry two headers, added by the reverse proxy (which removes any copies a browser sent):

| Header | Value |
|---|---|
| `Drive-Secret` | The value of `DRIVE_HEADER_SECRET`. Without it, or with any other value, the request is refused, whatever else it carries. |
| `Remote-User-Id` | The member's stable user id. |

The person must also be in the member list file (`DRIVE_MEMBERS_FILE`), which the Drive reads and never writes. It
re-reads the file within a second of it changing; replace it by writing a new file and renaming it over the old one.
The list is the authority for each member's email address, name and role. (Superit's gateway also sends
`Remote-Email`, `Remote-Name` and `Remote-Role`; the Drive does not need them.)

```json
{
  "members": [
    { "userId": "6f1c…", "email": "reka@example.com", "displayName": "Réka Kovács", "role": "admin" },
    { "userId": "a93b…", "email": "bence@example.com", "displayName": "Bence Tóth", "role": "member" }
  ]
}
```

`role` is `admin` or `member`. A list with a mistake in it is refused whole: at start the Drive does not start, and
later it keeps the previous list and logs why.

**The public prefix.** Everything someone outside the project reaches lives under `/s/`: the pages of public links and
the files and zips they download, and, under `/s/guest`, the guests' page, the interface's files for it and the guests'
API. The reverse proxy opens it without sign-in, and passes nothing about who anyone is there: it removes
`Drive-Secret`, the identity headers and its own sign-in cookie, as it removes them everywhere else from what a browser
sent. The Drive needs no secret there, and reads none of them: a request under `/s/` is a visitor's, through the link its
address names, or under `/s/guest` a guest's (the one whose session its own cookie names) or nobody's; never a member's,
whatever it carries. A signed-in member who opens a link is a visitor like anyone, and a forged `Remote-User-Id` there
does nothing. No request outside `/s/` is ever a visitor's or a guest's: without the secret it is refused with 403 as
before. In short, the reverse proxy must:

- send `/s/*` to the Drive without sign-in, removing `Drive-Secret`, `Remote-User-Id` (and the other `Remote-*`
  headers) and its own sign-in cookie, and nothing else: every method (guests upload, rename, move and delete there
  with `POST`, `PUT` and `DELETE`), the request body, `Origin`, `Range`, `If-Range`, and the guests' cookie
  (`__Secure-drive-guest`, or `drive-guest` when `DRIVE_ADDRESS` is plain HTTP) both ways, as for any other request;
- sign in everything else and add the two headers above, as before, and never add `Drive-Secret` to a request under
  `/s/` (the Drive would ignore it there, but the secret would travel where it is not needed);
- not cache answers under `/s/` (they say `Cache-Control: no-store`) nor log their addresses: an address there can be
  the secret that opens a link.

Paths are matched as the Drive's router cleans them: `/s/x/../../api/places` is redirected to `/api/places`, which
needs sign-in. Caddy's path matcher ignores case, so it also opens `/S/…` without sign-in; the Drive refuses that as
any request outside `/s/` without the secret.

**Changes come only from the Drive's own pages.** The reverse proxy signs members in with a cookie, which a browser
also sends when a page on another site makes it call the Drive; so does the Drive's own cookie for guests. So every
request other than `GET` and `HEAD`, a member's or a guest's (signing in included), must carry an `Origin` header equal
to `DRIVE_ADDRESS` (browsers add it themselves); any other origin, `null`, or none is refused with 403, and the Drive
logs both values. The reverse proxy must pass `Origin` through unchanged, and `DRIVE_ADDRESS` must be the address in the
browser's address bar.

### Settings

Every setting is an environment variable; there is no settings page. This table is the full list. The Drive refuses
to start without the required ones and names every one that is missing. (`internal/settings` holds the same list, and
a test keeps the two equal.)

| Variable | Required | Meaning |
|---|---|---|
| `DRIVE_STORAGE_URL` | yes | Address of the WebDAV server that holds the files (http or https). |
| `DRIVE_STORAGE_USER` | yes | Login name for the WebDAV server. |
| `DRIVE_STORAGE_PASSWORD` | yes | Password for the WebDAV server. |
| `DRIVE_STORAGE_FOLDER` | no, `/` | Folder on the WebDAV server that the Drive keeps everything in. Made if missing. |
| `DRIVE_CAPACITY` | no | Size of the storage, in bytes or with a unit (`500GB`, `1TB`, `1TiB`). Empty when not known. |
| `DRIVE_ADDRESS` | yes | The Drive's own address as people reach it, such as `https://files.example.com`. Changes are accepted only from pages at this address, and a public link's address starts with it. |
| `DRIVE_PROJECT_NAME` | yes | Name of the project (the organisation) the Drive belongs to. |
| `DRIVE_HEADER_SECRET` | yes | Secret the reverse proxy adds as `Drive-Secret`. At least 32 characters. The key that keeps guests' codes (see [Guests](#guests)) is made from it, so changing it ends the codes on their way (not the sessions). |
| `DRIVE_MEMBERS_FILE` | yes | Path of the member list file. |
| `DRIVE_DATA_DIR` | yes | Folder for the index, and its copy in `backup/` (see [Backing up the Drive](#backing-up-the-drive)). The container image sets it to `/data`. |
| `DRIVE_LISTEN` | no, `:8080` | Address the web server listens on. |
| `DRIVE_SMTP_HOST` | no | Mail server the Drive sends its own mail through (see [Mail](#mail)), such as `smtp.example.com`. Empty: the Drive sends no mail; sharing with members and public links work without it, and inviting guests is unavailable. |
| `DRIVE_SMTP_PORT` | no, `587` | Port of the mail server. |
| `DRIVE_SMTP_TLS` | no | How the connection to the mail server is encrypted: `starttls`, `implicit` (TLS from the first byte) or `none`. Empty: `implicit` on port 465, `starttls` on any other. `none` only without a login. |
| `DRIVE_SMTP_USER` | no | Login name for the mail server. Empty when it needs none. |
| `DRIVE_SMTP_PASSWORD` | no | Password for the mail server, with `DRIVE_SMTP_USER`. |
| `DRIVE_MAIL_FROM` | no | Address the Drive's mail comes from, such as `drive@files.example.com` or `Drive <drive@files.example.com>`. Needed with `DRIVE_SMTP_HOST`. |
| `DRIVE_ROLLBACK_AT` | no | When the storage was last put back to a snapshot, as a time with its offset (RFC 3339), such as `2026-11-20T09:41:00Z`. It tells one rollback from another. Set with the next two, or none of the three; see [After a rollback](#after-a-rollback). |
| `DRIVE_ROLLBACK_TO` | no | When the snapshot it was put back to was taken (RFC 3339), before `DRIVE_ROLLBACK_AT`. |
| `DRIVE_ROLLBACK_BY` | no | Who put it back, as members should read it: their name. Shown as plain text; control characters are taken out, and it is cut to 100 characters. |

### Layout on the storage

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
        3f9c0b…e1                           one file per upload while its bytes arrive, named after a random id;
                                            moved into its folder when whole
    index/
        1a5ff6fe…73/                        the copies of the index whose storage id this is (the mark's)
            copy-000000000042.sqlite        the latest copy of the index (see "The copy of the index")
    storage-id                              the Drive's mark: the id of the storage, which the index keeps too
```

Files and folders keep their real names, up to 255 bytes, exactly as given; the interface sends names in Unicode's
composed form (NFC), so a name from Safari on macOS matches the same name from another browser. Making a folder,
renaming and moving are each one change on the storage (`MKCOL`, or `MOVE` with `Overwrite: F`), after a look that
the name is free, and nothing ever replaces what is there.

**Uploads** stream straight through the Drive to a file of their own in the hidden uploads folder, never onto the
server's disk, and are moved into their folder (`MOVE` with `Overwrite: F`) only when whole: a half-finished file
never shows in a folder, whatever the storage does with a cut-off write. A file of up to 32 MiB arrives in one request,
stored with one `PUT` with its `Content-Length`, which writes the whole file or nothing. A larger one is a resumable
upload (see [Large transfers](#large-transfers-and-the-memory-figure)): its parts are written into its file one after
another, each from where the file ends (`PUT` with `Content-Range`), and one cut off goes on from where it stopped.
When the name is taken, the interface asks first, before the file travels: **replace** stores the upload, then moves
the file that is there into the trash and the upload into its place (the file comes back if that fails), and the file
keeps its place, its id and so its sharing; **keep both** stores the upload as `Name (2).ext`, `Name (3).ext` and so
on. A file never replaces a folder. The name is looked at again when the upload is moved into place.

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
it has not changed for a day. The copies of the index have a hidden folder of their own, `index/` (see [The copy of the
index](#the-copy-of-the-index)); the looks and the walk never read `.superit-drive/`, and no route reaches it.

### Keeping in step with the storage

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

### After a rollback

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

and the interface shows it above the list, in every view, as "The files were put back to 19 November at 04:00" (the
time in the browser's time zone): "Réka Kovács put the Drive's files back to how they were then, on 20 November. Files
added after that are gone, changes made after that are undone, and files deleted after that are back. Files that came
back may need to be shared again." The name is shown as text. A member can close it: it stays closed in that browser,
for that rollback, and nowhere else; a new rollback shows again. Visitors through a public link are told nothing: their pages say nothing
about the project but who shared the item. Guests are not told either.

**Connections to the storage.** A server closes a connection kept open between requests after a while, and can do so
just as the Drive sends on it; the Drive then cannot tell whether the server acted. So every request that changes the
storage (`PUT`, `MKCOL`, `MOVE`, `DELETE`) goes on a new connection of its own, which a server never closes for being
idle, and is never sent twice. Reads (`GET`, `PROPFIND`) use kept connections, closed by the Drive after four seconds
idle, and are sent once more on a new connection when the server closed the kept one first.

### The copy of the index

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
replaced or removed; a guest invited, their invitation changed, or gone with it; a shared folder made, deleted,
restored or removed for good, and its managers; a switch of sharing outside the project; a held My files handed over
or deleted; and an item renamed, moved, deleted or restored (what was given on it, and on what is in it, hangs on its
id, which an older copy would not find where the storage now has it). The index counts them itself, in the transaction
that makes them (migration `0011`: triggers on its tables), so none is missed, whatever makes it: a look at the storage
that drops something shared counts too. A burst of them makes one copy: it is written once nothing changed for 30
seconds, and no change waits more than five minutes for its copy, however much changes meanwhile. Files added, folders
made and how often links and invitations were used are not access changes: a copy older than them finds the files on
the storage, with the access of their folder, and the counts are a night behind at most.

**How fresh.** At any moment both copies hold every access change made more than five minutes before (and every one
made more than 30 seconds before a pause in them), and everything else as it was at the last copy: the last access
change's, or last night's at the latest. The log says each copy, with why it was made, its number and its size. When the
storage does not take a copy (it does not answer, or does not hold the Drive's mark), the data folder's copy is
written all the same, and the storage is asked again every five minutes: once it answers and holds the mark, a new copy
goes there.

**On the storage, safely.** A copy is written under a name of its own (`upload-<random>.part`, one `PUT`), then moved
to its name (`MOVE`, never replacing anything), and only then is the copy before it removed: a whole copy is there
throughout, and a copy cut off midway never has a copy's name. Each copy has the number after the last, kept in the
index; the folder holds one copy, two for a moment. A copy goes on the storage only while the storage holds the Drive's
mark with the id its index keeps: never onto a storage that is empty, mounted wrongly or someone else's, and the folder
is named after that id, so the copies of another index are never removed. Making a copy streams from disk to disk, and
putting it on the storage streams from the file, over one connection with the capped buffers every connection to the
storage has: it takes no transfer slot (as the walk takes none), so it never makes anyone wait their turn, and its
memory does not grow with the index ([measured](#large-transfers-and-the-memory-figure)). The copy for the storage is
copied, put on the storage, and (when a Drive starts from it) read back four MiB at a time, each part synced to disk and
let go of by the page cache before the next: the kernel's page cache counts against the container's memory too, and a
copy of 33 MB written in one go stopped the whole Drive for minutes in the memory test. Its temporary files are made in
the data folder, which is on disk, never in `/tmp`, which can be memory.

**What the copy on the storage leaves out.** Whoever has the storage login can read it, so it holds nothing that lets
anyone in by itself, and nothing that only one server can honour:

| Left out | Why |
|---|---|
| Guests' sessions (`guest_sessions`: each token's SHA-256) | A session is one browser's on one server. A hash cannot be turned back into a token, but a planted session would sign someone in; a Drive on a new server starts with none, and guests sign in again with a new code. |
| Codes mailed to guests (`guest_codes`: HMACs) | They last ten minutes, and are keyed from `DRIVE_HEADER_SECRET`, which a new server does not have. |
| The limits on trying codes (`guest_limits`) | Counters of the last hour on one server. |
| Uploads in progress (`uploads`) | Their uploader's browser on the old server; their files in `.superit-drive/uploads/` are removed a day after they were last written to, as any the index does not know. |
| Where the walk is (`walk_cursor`, `walk_started`, `walk_finished`) | By one server's clock; a Drive started from a copy walks the storage at once, from the top. |

What is left out is overwritten with zeros in the copy's file (SQLite's `secure_delete`), not only unlisted.
Everything else is kept: the places, the folder tree, the trash, every grant (to members, to everyone, and guests'
invitations with their end dates), public links (by their token's SHA-256: the address is in no copy, so a copy opens no
link by itself, and the same address works again on a new server), guests by address, the switches of sharing outside
the project, the record of acts on held My files (and an act under way, which a Drive started from the copy finishes),
how often links and invitations were used, the storage's id, and what the Drive knows of rollbacks (`rollback`,
`rollback_walk`, `storage_marked`): a Drive started from the copy knows a rollback it was told about is old news, and
goes on with the walk after one that was under way.

### Starting from a copy

A Drive whose data folder holds no `index.sqlite` makes its index from a copy before it starts (`internal/drive/adopt.go`),
when there is one:

- the data folder's own copy, `backup/index.sqlite` (a backup of the server put back: see [Backing up the
  Drive](#backing-up-the-drive));
- the latest copy on the storage, in the folder of the id in the storage's mark, and only one whose index keeps that
  same id. When the data folder's copy belongs to that storage too, the newer of the two (by number) is taken, the data
  folder's when they are the same copy.

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
brought; guests sign in again with a new code.

- **A copy made by a newer Drive** (its schema is newer than this one's): the Drive does not start, and says so. An
  index built without it would soon put a copy without the shares in its place. Start a version as new as the one that
  made it.
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
  removed), and invitations to addresses they control (to sign in, a guest still needs the code mailed to that address);
- sharing outside the project switched back on, where it was off;
- an act under way that hands over or deletes a held My files (it changes only what is in the Drive's folder), and a
  record of acts that never happened, or without those that did;
- a copy that keeps a Drive from starting (one that claims a newer schema), or one that is refused (the Drive starts with
  no shares).

They cannot: add members or project admins (the member list comes from the platform); sign anyone in as a guest
(sessions and codes are cleared); reach outside the Drive's folder, or into its hidden folder (the check); or put code
into the index (no trigger or view of its own). What bounds it: a copy is adopted only with an empty data folder; the
check; the log line that counts what it brought; and project admins see every public link and every guest's invitation
in "Shared outside the project". Nothing prevents it: a copy signed with a key made from `DRIVE_HEADER_SECRET` would
not open on a new server, whose daemon makes a new secret. The storage login opens the sharing as well as the files;
Superit gives customers no login to the Storage Box.

### Backing up the Drive

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
- **The upgrade test** (snapshot, wipe, restore, check that a share is there and a file downloads) passes with the above
  as it is: the share is in `backup/index.sqlite` once the copy after it has been written, 30 seconds after the share (or
  at once when the Drive is stopped and started again before the snapshot).

Running the Drive yourself, back up `backup/index.sqlite` the same way, and restore it as above.

### Shared folders, sharing and who may do what

A shared folder is a top-level place owned by the project. Any member can make one; its maker becomes its first
manager, as a role given to them like any other, and nothing else about it depends on their account: it stays, with
its files, when they leave. Everyone holding the admin role on the project (in the member list) is a manager of every
shared folder, for as long as they hold it. A member with no role on a shared folder does not see it in their places.

**Sharing.** Access is given on a file or a folder, in "My files" or in a shared folder, to a named member (anyone in
the member list, including people who have never opened the Drive), to everyone in the project (every member, as
long as they are one, new members included), or to a guest by email address (see [Guests](#guests)), as viewer or
editor. A shared folder as a whole can also be given, by
its managers: viewer, editor or manager to a member, viewer or editor to everyone. "My files" as a whole is never
shared: only items in it.

Access given on a folder applies to everything in it, at any depth. Deeper levels can only add access, never take it
away: someone's role at an item is the highest of what was given to them (or to everyone) on the item, on every folder
above it and on the shared folder as a whole, and of what the place gives them by itself (the owner of a "My files";
a project admin in a shared folder). `access.RoleAt` works it out, in one place, from what the guard hands it: the
place, the item with the folders above it, and every grant on them. Access on one item reaches neither its parent nor
its siblings. Taking access away, or giving less, counts from the next request, for everything below too.

`internal/access` decides everything. Roles go up a ladder, each doing what the one before it does:

| At an item (a file or folder), in any place | Viewer | Editor | Manager, owner |
|---|---|---|---|
| Open a folder, download a file (or a folder as a zip), see who can see it | yes | yes | yes |
| Upload into a folder (and replace, or keep both), make folders in it | | yes | yes |
| Share it: give access to others, change a role, take access away | | yes | yes |
| Make a [public link](#public-links) to it (not where [sharing outside the project](#sharing-outside-the-project-switched-off) is off), see it (who made it, how often it was used), remove it | | yes | yes |
| Invite a [guest](#guests) to it, change their role or end date (neither where sharing outside the project is off), take it away | | yes | yes |
| Rename, move or delete it: also needs editor of the folder it is in | | yes | yes |
| Restore it from the trash: also needs editor of the folder it goes back to, and either the place's whole trash or having put it there | | yes | yes |

Moving something into another place is only for the owner of a "My files": out of it, into a folder of a shared folder
where they may upload (see [Moving into a shared folder](#moving-into-a-shared-folder)).

| In a shared folder as a whole (its top folder) | Viewer | Editor | Manager |
|---|---|---|---|
| List it, download it as a zip, see who has access to it | yes | yes | yes |
| Upload, make folders, see its trash | | yes | yes |
| Decide who has access to it as a whole, switch sharing outside the project off for it (or back on), delete it | | | yes |

In their own "My files" the owner may do everything, including sharing any item in it, and downloading it whole as a
zip; nobody else may do anything there, project admins included, except with what was shared with them. Every request
of a resumable upload (starting it, each part, finishing and cancelling it) is decided as an upload into its folder,
and an upload in progress is its uploader's alone: for anyone else, project admins and the folder's owner included, it
does not exist. Someone whose right to upload there is taken away mid-upload can do nothing more with it, not even
cancel it: its file stays, unseen and unused, until it is removed a day later. A "My files" whose owner has left the member
list is held: nobody may do anything in it, and nothing shared from it opens, until they are back, or a project admin
hands it to another member or deletes it (see [When a member is removed](#when-a-member-is-removed)).

Renaming, moving, deleting and restoring change the folder an item is in (or goes back to), so they are decided there
too, as an upload into it is: whoever may put things into a folder may rename, take out and delete what is in it.
Whoever may delete something may restore it ("restore anything you could have deleted"), back into a folder they may
add to; those who may see the place's whole trash restore anything in it, and others only what they put there
themselves (by deleting it, or replacing it with an upload). Whoever may upload may delete (an upload can replace,
which puts a file in the trash). A test checks these rules for every person and place. An editor shares further, but
never with themselves; a viewer cannot share. Every list says, for each item, who can see it, and the details panel
lists each grant and where it comes from.

**Someone given part of a place.** A member given a folder deep in someone's "My files" (or in a shared folder they
have no role on as a whole) works inside it as their role allows, and nothing about the rest reaches them:

- the place is not in their places; they find what they were given in "Shared with me" (below), the folder's address
  (`/?place=…&folder=…` in the interface, its id in the API) opens it, and its files download at their own address;
- a listing's path starts at the highest folder they can open (`fromTop` is false): the names of the folders above it
  are never sent, nor in "who can see this", which says "from a folder above" for access given there, nor in the
  trash entry a delete answers with;
- they work inside the folder they were given, and share it, but cannot rename, move or delete that folder itself:
  that changes the folder it is in, which they cannot open (a clashing name would tell them what is there). They move
  things only into folders where they may upload, so nothing leaves what they were given;
- a place's trash is shown only to those who may upload into its top folder. Someone given part of it restores what
  they deleted themselves, by its address (the interface's "Undo"), and only back into a folder they may add to: not
  what the owner deleted or replaced before or after sharing it, and not into the top folder when the folder it came
  from is gone. The folder it goes into is decided on again just before it moves;
- every route answers an item they cannot reach exactly as one that does not exist: one message, "This is not in the
  Drive, or you have no access to it.", for every place, item or item in the trash that is not there or not theirs to
  act on, and for every refused action.

**Who has access, over HTTP.** `{member}` is a member's user id; "everyone" is everyone in the project.

```
GET    /api/places/{place}/access                                who has access to a shared folder as a whole
PUT    /api/places/{place}/access/members/{member}               {"role": "viewer" | "editor" | "manager"}  managers only
PUT    /api/places/{place}/access/everyone                       {"role": "viewer" | "editor"}
DELETE /api/places/{place}/access/members/{member}               (and …/everyone) take it away
GET    /api/places/{place}/items/{item}/access                   who can see an item, and where it comes from
PUT    /api/places/{place}/items/{item}/access/members/{member}  {"role": "viewer" | "editor"}  whoever may share it
PUT    /api/places/{place}/items/{item}/access/everyone          {"role": "viewer" | "editor"}
DELETE /api/places/{place}/items/{item}/access/members/{member}  (and …/everyone) take it away
POST   /api/places/{place}/items/{item}/access/guests            {"address", "role", "ends"}  a member's address shares
                                                                  with them; any other invites a guest (see Guests)
PUT    /api/places/{place}/items/{item}/access/guests/{guest}    {"role", "ends"}  an invitation's role and end date
DELETE /api/places/{place}/items/{item}/access/guests/{guest}    take it away
```

Each answers with who has access now: one line per grant (and the owner, or the project admins, first), each with
whether it was given right there or comes from a folder above (named when the person can open that folder), and, for
access with an end date, `until`. A guest's line is `{"guest": {"id", "address"}, "role", …}`, and, to whoever may share
the item, also `works` (whether it lets them in now), `uses` and `used`. A listing gives each item what the person may
do with it (`can`), who can see it (`seenBy`: everyone, or the members by user id; and `guests`, how many guests), which
guest added it (`addedBy`), and the folder's own `role` and `can`. Grants live in the index as one row per grantee per item (or per
place as a whole), keyed on the item's id, so they follow it through rename, move, replace and the trash, and go
with it when it is removed for good or dropped because the storage no longer has it.

**Shared with me.** It lists what others shared with the person, by name or with everyone in the project, that they
reach only through that share: an item in someone else's "My files", or in a shared folder they have no role on as a
whole. Each is the highest item they can open on its way down (`access.SharedWithMe`): a folder shared with them is
listed, and what is inside it is not listed again, whatever was given on it, because they reach it by opening the
folder; taking the folder away brings what was shared inside it on its own into the list. Nothing in their own My files,
or in a shared folder in their places, is listed. Every item something was given on to them or to everyone is a
candidate, and each is decided on the whole way down to it, every time, like any other request: whatever no longer counts
is left out (a held My files, an item or a shared folder in the trash, a share taken away, an item gone from the
storage). The most recently shared first.

```
GET    /api/shared-with-me
       200 {"items": [{"id", "name", "kind", "size", "changed", "can", "seenBy",   the item, as a listing gives it
                       "place", "placeKind",                   its place by id and kind, never a shared folder's name
                       "owner": {"userId", "email", "displayName"},    whose My files it is in (none in a shared folder)
                       "role", "sharedBy": {…}, "toEveryone", "shared"}]}    who shared it (while a member), and when
```

In the interface, "Shared with me" sits between the shared folders and the Trash. A folder from it opens at its own
address, with the breadcrumb starting at "Shared with me", then the folder; a file downloads at its own address, and its
details (who can see it, and sharing it further, for an editor) open beside the list.

People see a shared folder's name. Its top folder on the storage has that name, with ` (2)` and so on added when
another shared folder, one in the trash included, holds that folder or something is already there: a new shared folder
never takes over files nobody gave it. So two shared folders can share a name. Making one is refused only when the
person can already see one of that name; refusing a name they cannot see would tell them it exists.

**A shared folder in the trash.** Deleting a shared folder moves its whole top folder into an entry of its own trash
folder, beside what was deleted from it, and hides it from everyone. Its items, its trash and its roles stay in the
index, and its folder stays held, so restoring always has its own folder to go back to (something put there on the
storage directly is never replaced, unless its entry no longer holds its top folder: then that is its top folder, put
back by a rollback, and it is restored as it is). Its managers and the project
admins find it in the Trash view and can restore it; 30 days after it was deleted the Drive removes its trash folder,
and everything in it, for good. A shared folder always keeps someone in charge: taking a manager away, or giving them
less, is refused when nobody in the member list would be left managing it (there is always someone while the project
has an admin). Access shared on items in it stays with them while it is in the trash, and comes back with them.

Each place has a lock in memory: every change inside it (giving or taking access included, and a look at the storage
as it writes what it found into the index) holds it shared, and moving the whole shared folder into the trash or out
of it holds it alone, waiting up to ten seconds for the changes under way. A change that starts after the shared folder
went to the trash finds it gone.

### Moving into a shared folder

A move stays inside its place, except one: the owner of a "My files" may move a file or folder from it into a folder of
a shared folder where they may upload (`access.MoveOut` at the item, `access.MoveIn` where it goes). It then belongs to
the project. On the storage it moves into the shared folder's folder (one `MOVE`); in the index it keeps its id, and so
does everything in it, with what was given on each of them (the grants' foreign key on the item and its place cascades);
what was given on the folders above it in the My files stays there and no longer reaches it. What was deleted out of it
earlier stays in the My files' trash and goes back to its top folder.

Every other move between places is refused with 400 and says which there are: out of a shared folder (into another, or
into a My files), and out of someone else's My files (what was shared with someone stays where its owner keeps it). A
place or a folder the person cannot add to answers as one that does not exist (404). The move holds both places' locks,
always the one with the lower id first, so that two moves never each hold one and wait for the other; it waits (409)
while an upload in progress goes into what is moved, as that upload belongs to the old place.

Before such a move the interface warns that it becomes the project's, and says who will see it there. The Drive says
who before anything moves, as "who can see this" would show it there: the project admins, the shared folder's managers,
what was given on the folders on the way down, and what was given on the item itself, which it keeps. It is decided as
the move itself would be, and names a folder above the destination only when the mover can open it.

```
GET    /api/places/{place}/items/{item}/move?to=42[&place=7]    who would see it there (no "to": the top folder)
       200 {"becomesProjects", "access": [...as GET …/access answers...], "seenBy": {"everyone", "members"}}
POST   /api/places/{place}/items/{item}/move    {"to": 42 | null, "place": 7}    "place" only for another place
       200 the item, with "place"    400 a move between places that is not allowed    404 a place or folder that is not
       there or that the person cannot add to    409 a name taken there, or something in it being uploaded
```

### Public links

A member who may share a file or folder (an editor, the owner of a My files, a manager of a shared folder, a project
admin in a shared folder: `access.MakeLink`) makes a public link to it in its details. Anyone with the link's address
opens it without signing in, on a plain page under `/s/`, and downloads one file, or everything as a zip. A link has no
password and no end date: it works until it is removed. A viewer cannot make one, see one's details or remove one; they,
and everyone who can see the item, see in "who can see this" (and in the list's sentence) that anyone with the link can.

- **One link per item**, on a file or a folder, never on a place's top folder (a My files or a shared folder as a
  whole). A link on a folder covers everything in it, at any depth, and nothing above it or beside it. It lets visitors
  see and download, nothing else.
- **The address** is `DRIVE_ADDRESS` + `/s/` + a token of 192 random bits from `crypto/rand`, in base64url (32
  characters): it says nothing about the item. The index keeps only the token's SHA-256, so a copy or a backup of the
  index opens no link. The address is therefore given once, in the answer that makes the link: the details panel shows
  it, with Copy, until the page is reloaded, and says so; later it shows that the link exists, who made it and when, and
  how often it was used, and offers "Make a new link" (in its place: the old address stops working) and "Remove link".
- **Removed is dead for good**: its address answers exactly as one that never existed, or a malformed one (the same
  status, page and headers, after the same work: the token is hashed and looked up either way). A new link gets a new
  address. Neither the address nor the token is ever written to the log.
- **It follows its item**, by the item's id: through rename, move (out of a My files into a shared folder too), and
  replacing a file with an upload. It covers what is in the item where it is now. While the item is in the trash, or in a
  shared folder in the trash, the link opens nothing; restored, it works again at the same address. While the item is
  gone from the storage it opens nothing either; dropped from the index (see [Keeping in step with the
  storage](#keeping-in-step-with-the-storage)), or removed for good, the item takes its link with it, and no file put
  under its name later is ever reached through it.
- **Whose it is.** A shared folder belongs to the project, and so do the links made in it: they work whoever made them,
  also after their maker left the project. In a My files a link is its maker's share: it works only while they may still
  share the item there. It stops when that is taken away from them (or they leave the project), and works again if it is
  given back; a held My files (its owner left) opens nothing. Whoever may share the item sees in its details whether the
  link works now.
- **Sharing outside the project switched off** (for the whole project, or for the link's shared folder: see [Sharing
  outside the project switched off](#sharing-outside-the-project-switched-off)): no link opens there and none can be
  made, but links can still be seen and removed, and work again, at the same address, when it is switched back on. A
  visitor whose link would open is told so, on a page of its own ("This link is not available", 403); anything the link
  does not cover still answers as a link that does not exist.
- **Uses.** For each link the Drive keeps how often it was used and when last, and nothing else about visitors. One use
  is a page opened through it (its own, or a folder's inside it), or a download, a file opened or a zip from its start;
  not a `HEAD`, not a part of a file after its start (a video seeking, a download resuming), not a refused request. Link
  previews (chat programs that fetch a page to show it) and search engines' robots count as uses: nothing tells them
  apart. Uses are counted in memory, so a visitor's request never waits for the index or a lock for it; they are written
  every 30 seconds and when the Drive stops (a Drive that is killed loses the last half minute's).

```
GET    /api/places/{place}/items/{item}/link     200 {"link": {"kind": "view", "made", "madeBy", "former", "uses", "used", "works"} | null}
POST   /api/places/{place}/items/{item}/link     {"replace": true} (or no body)
       201 {"link": {…, "address": "https://files.example.com/s/<token>"}}    the only answer with the address
       409 {"error", "exists": true}             it has a link, and replacing it was not asked for
DELETE /api/places/{place}/items/{item}/link     204 (also when it has none)
```

Listings, search and "Shared with me" say `"seenBy": {…, "link": true}` for an item anyone with a working link can see,
and "who can see this" has a line `{"link": true, "role": "viewer", "given" | "inherited", "from"}`.

**What a visitor reaches**, all under `/s/`, and only with `GET` (or `HEAD`): any other method there is answered 405 with
`Allow: GET, HEAD` before anything else is looked at.

```
GET    /s/{token}                          the page of what the link is on
GET    /s/{token}/items/{item}             the page of a folder (or a file) inside it
GET    /s/{token}/items/{item}/download    a file, to be saved (as a member's download)
GET    /s/{token}/items/{item}/open        a file shown in the browser, by the safe list (as a member's …/open)
GET    /s/{token}/items/{item}/zip         a folder as one zip (as a member's …/zip, ?item=… too)
```

`{item}` must be the link's item or something inside it. A link that does not work, and anything it does not cover (a
folder above it, a file beside it, an item of another place or in the trash), gets the one answer: 404 with the page
"This link does not work". The one exception is a link that would open what is asked for but for sharing outside the
project being switched off where it is: 403 with the page "This link is not available", which says it is switched off
for now (only after the token matched, and nothing about what it is on).

**The page** is plain HTML made by the Drive (`internal/server/visitors.go`), in one column on every screen, with no
script and nothing of the members' interface: it works with JavaScript off. It says who shared it (the maker's display
name, never an email address or user id; the project's name when the maker has none or has left), the item's name, the
folders from the link's item down to the one shown (never anything above), "Download all as zip" for a folder, and what
is in it: folders open their own page, files have Download, and Open when their kind opens in the browser. A folder's
page lists at most 1,000 items (the zip holds them all). It lists what the index knows: what was put on the storage
directly shows once a member has opened its folder, or the walk has passed it; the zip looks at the storage. English
for now.

Every answer under `/s/` carries `X-Robots-Tag: noindex, nofollow` (and the page `<meta name="robots">`),
`Referrer-Policy: no-referrer` (the address is the secret), `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`,
`Cross-Origin-Resource-Policy: same-origin` and, on pages, `Content-Security-Policy: default-src 'none'; style-src
'sha256-…'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`: nothing loads or runs but the page's own style.
A file's bytes go out with the same content type and policy as to members (below). No cookie is set or read on a link's
addresses.

**What visitors can use up, and what bounds it.** Their transfers take at most 8 of the 32 slots, in a line of their
own (shared with guests), and at most 64 visitors wait in it at once (more are told 503 at once); a transfer that moves
nothing for two minutes is ended. Besides transfers, at most 16 visitors' requests are worked on at once, and 64 more wait up to 5 seconds; more
are answered 503 at once. Trying tokens costs a hash and one look in the index, with no lock and no log line. A zip is
bounded as for members (10,000 entries).

### Guests

A member who may share a file or folder (as for a [public link](#public-links): `access.InviteGuest`) types an email
address in its "Give access". An address in the member list (compared as below) is that member's: it is shared with
them as with any member, and the answer says so (`"member"`). Any other address invites a **guest**, as viewer or editor,
with an end date if the member gives one (none by default), and the answer says so (`"guest"`, and `"invited"` for a
new invitation). The guest is mailed an invitation: who invited them, to what (never a folder above it), what they may
do, until when, and a link to it on the guests' page, `DRIVE_ADDRESS/s/guest/?place=…&item=…#as=<their address>` (the
address after `#` is never sent to a server; the page offers it to confirm). A guest never gets an account and never
passes the reverse proxy: everything of theirs is under `/s/guest`.

- **Who is a guest.** Addresses are compared as `members.Address` writes them: without spaces at either end and in lower
  case, and nothing else, so dots and `+` tags make other addresses and are never merged. A guest's address is one plain
  address in ASCII that mail can be sent to (`mail.Recipient`), at most 254 characters; anything else is refused (400),
  so no address can add a header or a recipient. The member list may not hold one address twice (it is refused whole).
- **What a guest may do** is decided in `internal/access`, with actions only guests have (`access.AsAGuest`), by the
  role their invitations give them: a viewer opens and downloads (a file, or a folder as a zip) what was given to them
  and everything in it; an editor also uploads (in one request, or resumable), makes folders, and renames, moves and
  deletes what is inside it, as a member editor does, but never the item given to them itself (it is in a folder they
  cannot open), never out of it, never into another place. A guest never shares (no route, and no action), never makes
  or sees a link, never sees who has access, any trash, a place's top folder, the member list, a search, or anyone's
  address but their own; their "Shared with me" names who shared each item by display name (or the project's), as a
  link's page does. What they delete goes to the place's trash, for its owner or managers to restore; they restore
  nothing. They are never "everyone in the project". A guest is given a file or a folder, never a shared folder as a
  whole.
- **When an invitation lets them in**: while its item is live (not in the trash, not gone from the storage) and its place
  open (not a held My files, not a shared folder in the trash), before its end date, and where sharing outside the
  project is on. In a My files an invitation is its giver's share, as a link is: it counts only while they may still
  share its item there, and stops when they leave the project. In a shared folder it is the project's. Where sharing
  outside the project is switched off, what was given to them answers 403 with `"unavailable": true` and says so, their
  "Shared with me" leaves it out (not even its name) and says `"unavailable": true`, and they stay signed in: it works
  again, in the same session, when it is switched back on.
- **Signing in.** The guests' page asks for their address and mails a code: six digits from `crypto/rand`, only the
  newest of their codes works, once, for 10 minutes, and not after five wrong tries; at most five codes are sent to an
  address an hour, and at most 100 to all addresses together. A code is sent only to a guest's address with something
  shared with it that lets them in now (or will once sharing outside the project is switched back on where it is), and
  never to a member's; the page says the same, at once, for every address
  (the work is done afterwards, one address at a time, and nothing is logged about an address that is no guest's). Every
  try of a code does the same work whatever the address; past 100 wrong tries of live codes in an hour, from everyone
  together, no try is taken (429) until the hour has passed. The index keeps only each code's HMAC-SHA-256, keyed with a
  key made from `DRIVE_HEADER_SECRET`, which the index never holds.
- **Staying signed in.** The right code starts a session of 30 days on that browser, kept by the Drive (the index keeps
  its token's SHA-256; the browser the token, in a cookie): `__Secure-drive-guest` (`drive-guest` when `DRIVE_ADDRESS`
  is plain HTTP), `Path=/s/guest`, `HttpOnly`, `Secure` with HTTPS, `SameSite=Lax` (so it comes along when a guest opens
  the Drive from their mail, and never with another site's change). A guest stays signed in on at most ten browsers;
  signing out ends that one. A session ends, for good, when it ends; when the last of their invitations is taken away
  or ends (the guest is removed with it, and their codes, sessions and uploads in progress); when nothing given to them
  lets them in any more (in the trash, gone from the storage, in the My files of someone who left, its giver may no
  longer share it), sharing outside the project switched off aside; and when their address appears in the member list.
  Signed out, they sign in again with a new code.
- **A guest who joins the project** (their address appears in the member list) keeps their access as a member: within a
  second of the list changing (and when the Drive starts), every invitation to them becomes a grant to that member by
  user id, with its end date (the higher role, and the later end or none, where they had one already), and the guest
  is removed. From then on it follows their user id, also when their address changes. A member who leaves never
  becomes a guest; their address invited again is a new guest.
- **Labels and uses.** A file or folder a guest added (uploaded or made) is labelled with their address, for members
  (`"addedBy": {"guest": …}` in listings, "Added by" in the details): an upload over it by a member takes the label off.
  For each invitation the Drive keeps how often it was used and when last, and nothing else: a folder or file opened, a
  download, a file opened or a zip from its start, an upload, a new folder, a rename, a move or a delete through it.
  Counted in memory and written every 30 seconds, as links' uses are.
- **Mail that works.** Inviting a guest needs mail that works: without mail settings, or with a mail server the Drive
  cannot reach, cannot reach as `DRIVE_SMTP_TLS` says, or that refuses its login, inviting is refused (409, `"mail":
  false`) and says why, and `GET /api/me` says so before anyone types (`"guests": {"invite", "why"}`). Sharing with a
  member's address, and public links, still work.
- **Their transfers** take turns with visitors': outsiders hold at most 8 of the 32 slots, in a line of their own.

```
GET    /s/guest/                                      the guests' page (and /s/guest/assets/…, /s/guest/favicon.svg)
POST   /s/guest/api/code                {"address"}   202, the same for every address
POST   /s/guest/api/sign-in             {"address", "code"}   200 and the cookie; 400 not right; 429 too many tries
POST   /s/guest/api/sign-out                          204
GET    /s/guest/api/me                                {"address", "project"}; 401 {"signIn": true} when not signed in
GET    /s/guest/api/shared-with-me                    {"items": [{…, "can", "place", "role", "sharedBy", "until"}]}
GET    /s/guest/api/places/{place}/items/{item}       {"item", "path", "role"}: a file or folder given to them, or inside
GET    /s/guest/api/places/{place}/items/{item}/items[?wait]     a folder's list, as a member's, without seenBy or addedBy
GET    /s/guest/api/places/{place}/items/{item}/download | /open | /zip
POST   /s/guest/api/places/{place}/items/{item}/files | /uploads | /folders | /rename | /move {"to": <folder>}
GET | PUT | DELETE /s/guest/api/places/{place}/uploads/{upload}, POST …/finish     their own uploads in progress
DELETE /s/guest/api/places/{place}/items/{item}
```

Every answer under `/s/guest` carries `X-Robots-Tag: noindex, nofollow`, the members' `Content-Security-Policy` (the page
runs the interface's own script) and `Referrer-Policy: same-origin` (the addresses there hold no secret, and
`no-referrer` would make a change's `Origin` null); the API's answers `Cache-Control: no-store`, the page `no-cache`, and
the interface's files are kept (their names hold their content's hash). An item they cannot reach answers exactly
as one that does not exist (404, `notHere`); without a session every route but signing in answers 401.

### Sharing outside the project switched off

A cautious organisation can keep everything inside: a project admin switches sharing outside the project (public links
and guests) off for the whole project, and a shared folder's managers (the project admins among them) for that shared
folder, such as Payroll. A My files has no switch of its own. The switches live in the Drive's index, not in its
settings: the platform passes nothing, and they go wherever the index goes (its copies, and a Drive started from one).

Where it is off (the project's switch, or the shared folder's; `access.Place.NoSharingOutside`, set in
`server.accessPlace` from the place as the index reads it):

- **No public link opens**: a visitor whose link would open what they ask for gets the page "This link is not
  available" (403), anything else the one answer for a link that does not exist. No link is made.
- **No guest gets in**: what was given to a guest answers 403, `{"unavailable": true, "error"}`; their "Shared with me"
  leaves it out and says `"unavailable": true`. They stay signed in (and can sign in, and be sent a code), to be told
  so. No guest is invited, and no invitation's role or end date changed.
- **What was made stays as it is**: whoever may share an item still sees its link and its guests, each saying it does
  not work now (`"works": false`), and removes them; "who can see this" and the list's sentence no longer count them.
  Sharing with members, by name, by address or with everyone, goes on as before.
- **Switched back on**, every link (at its own address) and every invitation (in the guest's same session) works again
  as it did, except what was removed meanwhile, which stays removed.
- A member who may share the item and asks to make a link, invite a guest or change an invitation is answered 409,
  `{"outside": "project" | "folder", "error"}`, saying where it was switched off. Anyone else gets what they would get
  with it on: `access.SwitchedOff` says it only to whoever it is the only thing in the way of.
- Moving something from a My files into a shared folder where it is off is allowed: its link and invitations go with it
  and stop there; the move's warning says it is off there (`"outside"`).
- The shared folder's own switch stays with it in the trash, and goes when it is removed for good. Switched on for a
  shared folder, it is still off there while it is off for the whole project.

Every member sees whether it is on for the whole project, and who switched it off when (`GET /api/me`:
`"sharingOutside": {"on", "by", "former", "at"}`, and `"can"`, what they may do that concerns no place: `oversee` and
`switch-sharing-outside` for a project admin); whoever has a role on a shared folder sees its own switch the same
way in their places (`"sharingOutside"`). Every answer about a place or an item in it (its listing, who has access, its
link, a move's warning, the places list) says whether it is on there now: `"outside": "on" | "project" | "folder"`.

```
PUT    /api/sharing-outside                    {"on": false | true}   a project admin, for the whole project
       200 {"sharingOutside": {"on", "by", "former", "at"}}
PUT    /api/places/{place}/sharing-outside     {"on": false | true}   a shared folder's managers, for it
       200 the shared folder, as the places list gives it
```

Switching it is logged (`sharing outside the project was switched off for the whole project`, with who).

### What a project admin oversees

A project admin cannot open anyone's My files (`access`'s table test tries every action, with sharing outside on and
off), but sees what leaves the project, and the space each person and each shared folder takes:

```
GET    /api/oversight/shared-outside
       200 {"sharingOutside": {…}, "items": [{"kind": "link" | "guest", "item": {"id", "name", "kind"},
            "place": {"id", "kind", "name" (a shared folder's), "owner" | "ownerLeft" (a My files'), "outside"},
            "sharedBy" | "former", "guest": {"id", "address"}, "role", "since", "until", "uses", "used", "works"}]}
GET    /api/oversight/space
       200 {"used", "capacity", "people": [{"person": {…} | "former" and "folder", "used"}],
            "sharedFolders": [{"id", "name", "deleted", "used"}]}
```

- **Shared outside**: every public link, then every guest's invitation, on an item that is in the Drive now (not in the
  trash, not in a shared folder in the trash, not gone from the storage): the item by name only (never the folders above
  it, nor what is in it), whose My files or which shared folder it is in, who made or gave it, the guest's address or
  anyone with the link, the role, since when and until when, how often it was used and when last (as its maker sees
  it), and whether it lets anyone in now. Read from the index a page at a time and sent as it is made, however many
  there are. It opens nothing: the ids in it are decided on like any other request.
- **Space**: the sum of the sizes of the files in each person's My files and each shared folder, its trash included, as
  the sidebar's meter counts it: every member (0 for one who has never opened the Drive), everyone who has left with
  files held (by the name of their top folder on the storage, their address when they first opened the Drive), every
  shared folder (one in the trash too), the largest first. Sums only.
- Only project admins (`access.Oversee`); anyone else gets the one answer for what is not there.
- The held My files of people who left the member list, to hand over or delete: see [When a member is
  removed](#when-a-member-is-removed).

### When a member is removed

A person missing from a new version of the member list has been removed (the Drive reads the list within a second of
it changing). Nothing is deleted for that, and nothing changes on the storage or in the index: the Drive decides on the
list it has now, at every request, so someone added back with the same user id finds everything as it was.

- **They are refused**: every request of theirs, from a browser left open too, answers 403 ("You are not a member of this
  project, so its Drive is closed to you."), and the interface shows a page that says so in place of the files. An
  upload of theirs in progress can go no further.
- **Their My files is held**: kept as it is, its trash included, and closed to everyone, project admins included
  (`access.Place.OwnerLeft`). Nothing shared from it opens: its public links answer as links that do not exist,
  invitations let no guest in (a guest with nothing else that lets them in is signed out at their next request, and
  is sent no code), what was shared with members or with everyone opens nothing and leaves their "Shared with me", and a
  search finds nothing in it. The public links they made and the guests they invited in someone else's My files stop too
  (they were their shares; what they gave members there stays, for that My files' owner to take away); what they did in
  a shared folder is the project's and goes on: its files, its links, its guests and what they gave on it.
- **Back with the same user id** (a list briefly wrong, a mistake undone, a new address), they have it back as it was,
  with their trash, and everything shared from it works again; a guest who was signed out meanwhile signs in again with
  a new code. People are told apart by user id, never by address: two members swapping addresses keep their own files.
  (Its top folder on the storage keeps the name of their address when they first opened the Drive.)
- **Until a project admin decides.** Held lasts as long as it takes: nothing ends it but a project admin's act, a
  hand-over or a deletion, each of the held My files as a whole, at its top folder (`access.HandOverHeld`,
  `access.DeleteHeld`): it is never opened, listed or named to them. Only a project admin may, only for a My files whose
  owner is not in the member list, and only one act on it at a time.

```
GET    /api/held                                   a project admin: every held My files, and the record
       200 {"held": [{"place", "userId", "folder", "files", "size", "used", "act", "back"}],
            "acts": [{"id", "kind": "hand-over" | "delete", "userId", "folder", "files", "size",
                      "by", "byFormer", "at", "to", "toFormer", "into", "done"}]}
POST   /api/held/{place}/hand-over   {"to": "<user id of a member>"}
DELETE /api/held/{place}
       200 {"act"} done; 202 {"act"} under way    400 not a member    409 another act under way on it, its owner back
       in the member list meanwhile, or a change inside it still under way
```

`folder` is the name of their My files' top folder on the storage, their address when they first opened the Drive (the
Drive no longer knows their name); `files` and `size` are what it holds outside its trash, `used` all it takes.

**A hand-over** moves the whole held My files, in one `MOVE` on the storage, into the top folder of the receiver's My
files (made for them if they have never opened the Drive), as a folder named after it: `bence@example.com`, or
`bence@example.com (2)` and so on when the receiver has something of that name (on the storage, or in the index, even an
item missing from the storage): it is never merged into a folder that is there. The items keep their ids. What comes
along is the files, and nothing given on them: every share (to members, to everyone, to guests: a guest whose last
invitation it was is removed, and signed out), every public link (removed for good) and every upload in progress into it
are removed, so the folder arrives as private as anything the receiver made, and nothing the removed person decided
works again, whoever is let in later. What was in the held My files' trash is not handed over: it is removed. The
receiver is not mailed: the project admin tells them.

**A deletion** removes the held My files from the storage for good, its trash included, and from the index with
everything in it. It does not go to a trash: the hold before it is the time to change one's mind, and the confirmation
says it cannot be undone. On Superit's storage, the daily snapshots are the way back (a rollback, for the whole storage).

**The record.** Every act is kept in the index for good, read by project admins (`GET /api/held`, and the interface's
"People who left", in "Sharing and space"): whose files (user id, folder), how many files and bytes, who did it, when,
and, for a hand-over, to whom and the folder they became. It is logged too (`the files of a member who left were handed
over`, or `deleted`, with the ids). A top folder named in the record is never given to a new My files: someone with
that address (the person back, or another) gets `bence@example.com (2)`, so nothing left of the old one on the storage is
ever taken over.

**Steps, and a stop midway.** An act is recorded first (with what it covers and, for a hand-over, the folder's name), then
done on the storage (the move, then removing the trash; or removing the trash and the top folder), then in the index, by
SQL statements that move or remove 20,000 items per transaction (nothing of the tree is loaded into memory, and no
transaction holds the index for more than a moment: 100,000 items take about a second in all); what is moved below the
top folder stays out of reach in the receiver's My files until the last transaction moves the top folder's items, makes
their folder, and records the act done. Each step can be done again, and the Drive tells which are done by what the
storage and the index hold. While an act is under way:

- its held My files stays held, whatever the member list says (someone back meanwhile does not get it: the act was
  decided, and is finished; they get a new My files once it is);
- the storage paths it changes are claimed, so no look at a folder and no walk takes the moved folder for one put there
  directly, or the held one for one emptied; when the Drive starts, it claims them again from the record before anything
  looks at the storage;
- an act whose storage step fails (the storage not answering, a removal taking longer than ten minutes) stays under way:
  it is answered 202, shown as under way, and finished by the Drive by itself when it starts and every hour after (or
  when the project admin asks for the same act again);
- a Drive that stops midway (killed, restarted) finishes it when it starts again; a name taken on the storage meanwhile
  gets the next free one; a held top folder gone from the storage before the move becomes an empty folder, and the looks
  find its files gone.

Nothing is changed by an act unless the storage holds the Drive's mark (a storage that is empty or mounted wrongly would
show the held files gone). After a rollback to a snapshot taken before an act, the removed person's folder is back on the
storage and belongs to no place: nobody sees it, the walk logs it, and only someone with the storage login can hand it
over.

### Opening files in the browser

A file a member uploaded is served from the Drive's own address, where the sign-in cookie applies: a web page, an SVG
image or a script shown there would run as the Drive, with the access of whoever opened it. So only a fixed list opens
in the browser, decided by the extension of the file's name alone (the part after its last dot, in any case, in plain
ASCII letters and digits; a name starting with its only dot has none), each sent with the one content type the list
gives it, never a type from the upload, the storage or the bytes (`internal/server/opening.go`):

| Kind | Extensions | Content type sent |
|---|---|---|
| Images | `.jpg` `.jpeg`, `.png`, `.gif`, `.webp`, `.avif`, `.bmp` | `image/jpeg`, `image/png`, `image/gif`, `image/webp`, `image/avif`, `image/bmp` |
| PDF | `.pdf` | `application/pdf` (the browser's own viewer) |
| Plain text | `.txt`, `.log`, `.md` | `text/plain; charset=utf-8` (a text in another encoding shows its accented letters wrong: download it) |
| Video | `.mp4` `.m4v`, `.webm` | `video/mp4`, `video/webm` |
| Sound | `.mp3`, `.m4a`, `.aac`, `.wav`, `.flac`, `.ogg` `.oga` `.opus` | `audio/mpeg`, `audio/mp4`, `audio/aac`, `audio/wav`, `audio/flac`, `audio/ogg` |

Everything else downloads: web pages (`.html`, `.htm`, `.xhtml`), SVG, XML, scripts, and kinds Chrome does not show
(TIFF, QuickTime `.mov`, Ogg Theora `.ogv`). Opening and downloading are separate addresses:

```
GET    /api/places/{place}/items/{item}/open        shown in the browser when its kind is on the list; otherwise as …/download
GET    /api/places/{place}/items/{item}/download    always application/octet-stream, as an attachment, whatever is asked
```

Both are decided as a download (`access.Download`), honour `Range` (so a video or a sound seeks) and take a transfer
slot. Every answer with a file's bytes carries `X-Content-Type-Options: nosniff`, `Cross-Origin-Resource-Policy:
same-origin` (no other site can load the bytes into its page) and a `Content-Security-Policy` under which nothing runs:

- images, PDFs, text, downloads and zips: `default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors
  'none'; sandbox`: nothing loads or runs, and the page has an opaque origin;
- video and sound: `default-src 'none'; media-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';
  sandbox allow-same-origin`: the browser's player fetches the file again as media (with `Range`, to seek), which
  Chrome does not do from an opaque origin (and the Drive's resource policy would refuse); still no scripts.

Checked in Chrome 157: every kind on the list shows or plays (video and sound seek), with the policy above; a web page
named `.png` shows as a broken image, one named `.txt` as its source text, and nothing in them runs; `.html`, `.svg`,
`.xhtml` download. Chrome's PDF viewer still runs a PDF's own JavaScript (such as an alert) in its own sandbox, not as
the Drive. Firefox shows PDFs and images and plays sound and video under the same policies. Safari was not checked.

Listings say which files open (`"opens": true`); the interface opens those in a new tab from a click on the name, and
downloads the rest.

### Search

```
GET    /api/search?q=<part of a name>
       200 {"items": [{"id", "name", "kind", "size", "changed", "opens", "can", "seenBy",   the item, as a listing gives it
                       "place", "placeKind", "placeName", "owner", "role",
                       "path", "fromTop"}],                   where it is, as far as the person can open the folders above it
            "more": false}
       400 nothing to search for, or more than 255 bytes
```

It finds files and folders by name (never by what is in them) across everything the person can see, and nothing else:

- **Where it looks**, each part decided through `access` before the index is asked: their own My files (a search
  never makes one), the shared folders they have a role on, and each item shared with them in a place they cannot open
  (what "Shared with me" lists), with everything in it. The index walks down from there, past anything in the trash,
  and reads nothing else, so a name they cannot see is never read, and the time a search takes does not depend on it
  (a test reads the database's plan for the query; measured: 9,000 files the person cannot see, all matching, change
  nothing).
- **Every result** is decided again through `access.Finds` on the whole way down to it. Its `path` names only the folders
  above it that the person can open (from "Shared with me" otherwise), and `placeName` a shared folder's name only when
  they can open it. Not a result: anything in a held My files, in the trash, in a shared folder in the trash, or beside
  or above what was shared; shared folders themselves (they are in the sidebar).
- **How names match:** in any case and without accents, both ways (`index.SearchKey`: Unicode case folding, marks taken
  off, compatibility forms made plain), so `szamla`, `SZÁMLA` and `számla` all find "Számla őszi.pdf", also when a
  Mac program wrote its name decomposed; each word searched for must be somewhere in the name. The best first: the
  whole name (or without its extension), its start, the start of a word, anywhere, each word somewhere; then by name.
- **At most 50 results**, and `more` when there are others; no pages. Count, order and `more` come only from what the
  person can see. The search reads the index, not the storage, and takes no place's lock: what was put on the storage
  directly is found once its folder was opened or the walk passed it (within a walk's interval and length, see
  [Keeping in step with the storage](#keeping-in-step-with-the-storage)); what went from it is not found from the first
  look that missed it.

### Selecting several

In a folder's list each row has a tick box and the header a "select all"; a click elsewhere on a row selects it alone.
With two or more selected, the header becomes a bar with Move, Download as zip (`…/zip?item=…`), Delete and Clear.
There is no bulk share, and no new route: Move and Delete send one request per item, one after another, each decided
as it would be alone. Deleting several offers Undo for all of them; a move into a shared folder shows the warning, with
what each item keeps; and when some items are not moved, deleted or restored, a dialog names each with the Drive's
reason, and says what happened to the rest.

### Mail

The Drive sends three kinds of mail: "shared with you", to a member something was just shared with by name; an
invitation, to a guest invited to something; and the codes guests sign in with (see [Guests](#guests)). With
`DRIVE_SMTP_HOST` empty it sends none and attempts none: sharing with members and public links work the same, and
inviting guests is unavailable. The line the Drive logs at start says where mail goes
(`mail="smtp.example.com:587 (starttls)"`) or `mail=off`. Whether mail works is what the last mail sent, or the last
check (connect, encrypt, sign in, and nothing sent), came to: trusted for ten minutes when it worked, one when it did
not; inviting a guest checks again when that has run out.

- **When:** a new share to a named member, on a file, a folder or a shared folder as a whole. Not for everyone in the
  project, not for a role changed or given again, not for access taken away, and never to the person sharing (a
  project admin taking a role on a shared folder).
- **What it says,** in short plain text in English: who shared what, in which project, the role, and a link at
  `DRIVE_ADDRESS`: a folder at `/?place=…&folder=…`, a shared folder at `/?place=…`, and a file at the folder it is in
  when the member can open it, otherwise at "Shared with me" (`/?shared`). It never names a folder above what was
  shared, nor the shared folder an item is in.

  ```
  Subject: Bence Tóth shared “Clients” with you

  Bence Tóth shared the folder “Clients” with you in the Drive of Kovács Accounting.
  You can open and download it.

  Open it: https://files.example.com/?folder=12&place=3
  ```
- **How:** over SMTP from `DRIVE_MAIL_FROM`, with STARTTLS (or TLS from the first byte on port 465, or as
  `DRIVE_SMTP_TLS` says), signing in with `AUTH PLAIN` when there is a login; the server's certificate is checked
  against the system's authorities (the image has them). It uses Go's standard library only.
- **Never in the way:** a mail is queued (up to 64) and sent in the background, one at a time, within 30 seconds each;
  the share is answered at once whatever happens to it. (A guest's code is sent by the guests' own sender in the
  background, also never in the way of the page that asked.) A mail that fails is logged with what it was about (the
  recipient's user id, the place and item ids) and why, never with its text or the recipient's address, and is not
  tried again; so is one dropped because the queue is full. Mail still queued when the Drive stops is not sent.
- **Names from people** (display names, file names) have line breaks and other control characters taken out and are
  encoded (RFC 2047) in the subject, the recipient must be one plain address, and the headers are only those the Drive
  writes: no name can add a header or a recipient.

### Large transfers and the memory figure

The Drive is counted at 64 MiB of memory (a hard 96 MiB) on a customer's server, and the kernel's network buffers
for the transfers in flight are charged to its container too. A transfer is a request that moves a file's bytes: a
download (of a file or a zip, or a file opened in the browser), an upload in one request, or one part of a resumable
upload. Three rules hold the
figure (`internal/transfers`):

- **At most 32 transfers are in flight, of which outsiders (visitors with a public link, and guests) hold at most 8.**
  Members can always use the other 24, and all 32 while no outsider needs one; when a slot comes back, a member waiting
  gets it before an outsider. Outsiders wait in a line of their own, at most 64 of them; more are told at once that the
  Drive is busy. A
  download waits its turn in line (holding only its idle connection),
  for up to ten minutes, then gets 503. An upload is answered at once with 503, `{"busy": true}` and `Retry-After`,
  before any of its bytes are read, and keeps its place in line: a resumable upload by its id, an upload in one request
  by the `turn` it names (any word the interface makes up for it, counted only for that person). When its turn comes, a
  slot is kept for it for 30 seconds; a place not asked about again for a minute is lost. The interface shows such an
  upload as "Waiting for its turn". A transfer that moves no bytes for two minutes is ended, so a paused download does
  not keep a slot (the browser resumes it with `Range`).
- **Each connection's buffers are capped at 256 KiB**, send and receive, on the browser side (the listener) and on the
  storage side (the storage client's dialer). Linux counts twice what it is given, so a transfer holds at most about
  1 MiB of them.
- **Go's memory limit is 24 MiB** (`debug.SetMemoryLimit`), unless the standard `GOMEMLIMIT` says otherwise; the
  start-up line in the log says which.

Measured with `make test-memory` (below): the image in a container with `--memory=64m --memory-swap=64m`, against the
stand-in storage, idles at about 4.5 MiB; with 40 downloads (half of them read at 8 MiB/s) and 10 resumable uploads
of 256 MiB each at once, it peaked at 46 to 49.6 MiB over three runs, of which about 13 to 16 MiB was the Drive's own
memory and 29 to 31 MiB network buffers, and never reached the limit (Docker Desktop on an Apple Silicon Mac, arm64).
Walking a storage of 50,000 files in 402 folders (and one folder of 20,000, more than a look reads) took 3½ minutes and
its highest was 38 MiB, of which 17.5 MiB the Drive's own memory and 16 MiB page cache (the index's file, which the
kernel takes back first); eight people then opening the folder of 10,000 files at once reached 59 MiB, of which 35 MiB
its own (Go's heap at its limit, and SQLite's caches) and 16 MiB page cache. A copy of the index of 110,000 items (33.6
MB), made by a share while 16 downloads of 64 MiB (half of them read at 8 MiB/s) and four people opening a folder of
10,000 files went on over and over, took 1.7 seconds, into the data folder and onto the storage, and the downloads and
lists went on meanwhile. People's use alone held the container at its limit (64 MiB, mostly page cache the kernel
takes back), with 33.9 MiB of the Drive's own memory and 12.9 MiB network buffers; while it copied, its own memory was
34.1 MiB: the copy streams from disk to disk with small caches of its own (1 MiB for SQLite, 128 KiB to copy a file),
and writes in parts of 4 MiB, each synced and let go of. Written in one go, a copy that size stopped the whole Drive
for minutes there, the page cache full of what it had not yet written.

**Downloads** honour `Range` for one part of a file (`bytes=a-b`, `bytes=a-`, `bytes=-n`): 206 with `Content-Range`,
or 416 with `Content-Range: bytes */<size>` for a part after the end. They carry the storage's `ETag` and
`Last-Modified` and pass `If-Range` on to the storage, so a browser resumes a download, and a video seeks; several
parts, or anything else in `Range`, get the whole file, as HTTP allows.

**Zips.** A folder, a place's top folder, or some items of a folder download as one zip, made as it is sent: each
folder is looked at on the storage on the way, and each file streamed from the storage, stored as it is (no
compression). Folders are in it too, empty ones included. A backslash in a name becomes `_` in the zip. A zip holds
at most 10,000 files and folders (each costs a little memory until the zip's end); a larger folder is refused with
400, and is downloaded a folder inside it at a time. It has no length, and a zip cut short ends the connection, so the
browser shows the download as failed.

```
GET    /api/places/{place}/zip[?item=12&item=15]                  the top folder, or those items of it, as one zip
GET    /api/places/{place}/items/{folder}/zip[?item=…]            a folder, or those items of it
```

**Resumable uploads.** The interface uses them for files over 32 MiB.

```
POST   /api/places/{place}/uploads?name=…&size=…[&existing=replace|keep-both]          start (into the top folder)
POST   /api/places/{place}/items/{folder}/uploads?name=…&size=…[&existing=…]           start (into a folder)
       201 {"id", "name", "size", "offset": 0, "resumable", "partSize", "waiting"}
       409 {"error", "exists": "file"|"folder", "name"}  the name is taken: ask, then start again with existing
GET    /api/places/{place}/uploads/{upload}                       where it stands: the same as the start's answer
PUT    /api/places/{place}/uploads/{upload}?offset=<n>            one part, from n, with its Content-Length
       200 {"offset", "size"}                                     where what has arrived ends now
       409 {"error", "offset"}                                    it must start at offset: send from there
       409 {"error", "offset": 0, "resumable": false}             the storage cannot resume: send it all again
       503 {"error", "busy": true}, Retry-After                   not its turn: send it again then
POST   /api/places/{place}/uploads/{upload}/finish[?existing=…]   move it into its folder
       201 the file, as an upload in one request answers          409 with "exists", or with "offset" when incomplete
DELETE /api/places/{place}/uploads/{upload}                       cancel: 204, and what arrived is removed
```

A part holds at most `partSize` (32 MiB) and must start where what has arrived ends; one cut off keeps what arrived
(Apache keeps what a `PUT` with `Content-Range` wrote into a file that is there), so after a broken connection the
interface asks where the upload stands and goes on from there. Whether the storage can write into part of a file the
Drive finds out once, the first time an upload starts, the way the storage check does; and it checks the file's length
after every part. Where it cannot, `resumable` is false, the one part is the whole file, and an upload cut off starts
again from the beginning: the interface tells the person so.

A folder is uploaded (with "Upload folder", where the browser offers it, or by dropping it) by making it and the
folders in it (`POST …/folders`) and uploading each file into its own; when the folder's name is taken, it is made
under a name of its own, so nothing is mixed into a folder that is there.

## Running it yourself

The Drive runs without Superit, as one container against any WebDAV server that passes the storage check. What it
does today: a member signs in through the reverse proxy and works in their "My files": uploads and downloads files,
makes folders, renames and moves things, deletes them into a 30-day trash and restores them, and sees about how much
space is used. Members make shared folders, owned by the project, and work in them the same way; managers make other
members managers and delete shared folders into the trash, and project admins manage every one. Members share files
and folders with other members, or with everyone in the project, as viewers or editors, find what was shared with them
in "Shared with me", get a mail when something is shared with them by name (with the mail settings), and move things
from their own My files into a shared folder after a warning that says who will see them there. Images, PDFs, text,
video and sound open in the browser (anything else downloads), a search finds files and folders by name across
everything a member can see, and several items can be selected to move, download as a zip or delete together. Members
who may share a file or folder make a public link to it, which anyone opens without signing in, or invite someone
outside the project by email address as a guest (with the mail settings), who signs in with a mailed code; project
admins see everything shared outside the project and the space each person and shared folder uses, and switch sharing
outside the project off for the whole project (managers for one shared folder); when someone leaves the member list,
their My files is held, unopened, until a project admin hands it to another member or deletes it. Large files
upload in parts that resume after a broken connection, downloads resume and seek, and folders upload
whole and download as zips. Files changed on the WebDAV server by other programs show in the Drive as folders are
opened, and through a slow walk now and then (see [Keeping in step with the storage](#keeping-in-step-with-the-storage)).

It needs:

- **The image**, `ghcr.io/neonjelly-co/superit-drive:<version>`, for x86 (`linux/amd64`) and arm64 (`linux/arm64`).
  Each version has its own tag, and there is no `latest`: you choose the version, and change it to upgrade. No version
  has been published yet (see [Releases](#releases)); until one is, build the image from this repository with
  `docker build`.
- **A WebDAV server** for the files. Check it first (below).
- **A reverse proxy that signs people in** and, on every request it passes on, adds `Drive-Secret` and
  `Remote-User-Id` in place of any the browser sent (see [Signing in](#signing-in-headers-and-the-member-list)); except
  under the public prefix `/s/`, which it opens without sign-in and passes on without those headers or its sign-in
  cookie. The Drive listens on plain HTTP on port 8080: only the reverse proxy should reach it. For large files, the proxy should
  stream request and response bodies rather than hold them, pass `Range` and `If-Range` through, and let a download
  wait up to ten minutes for its answer when the Drive is busy.
- **The member list file**, in a folder of its own mounted into the container.
- **The data folder**, `/data` in the container, kept between restarts. It holds the index, which alone knows who
  has access to what, and a copy of it, `backup/index.sqlite`, which is what to back up (see [Backing up the
  Drive](#backing-up-the-drive)). The Drive also keeps a copy on the WebDAV server, and starts from it with an empty data
  folder (see [The copy of the index](#the-copy-of-the-index)).

The settings are in the [table above](#settings).

### Checking a WebDAV server

`superit-drive check-storage` finds out whether a WebDAV server can hold the Drive's files, before a Drive is set up
on it. It needs only the four storage settings, and it is in the image:

```sh
docker run --rm -e DRIVE_STORAGE_URL=https://dav.example.com -e DRIVE_STORAGE_USER=drive \
  -e DRIVE_STORAGE_PASSWORD=example-password -e DRIVE_STORAGE_FOLDER=/drive \
  ghcr.io/neonjelly-co/superit-drive:<version> check-storage
```

It takes these steps with the Drive's own WebDAV client and stops at the first that fails:

| Step | What it does |
|---|---|
| `sign in` | Lists `DRIVE_STORAGE_FOLDER` with the login. A folder that is not there yet is fine: the Drive makes it. |
| `make a folder` | Makes a folder of its own, `.superit-drive-check-<random>`, inside it (and `DRIVE_STORAGE_FOLDER` if missing). |
| `write a file` | Writes a file of 256 KiB there, and finds it listed with its size. |
| `read part of it` | Reads 1000 bytes from the middle of the file (`GET` with `Range`), and gets only those. |
| `move it` | Moves the file to another name on the server (`MOVE`), and finds it there and not at its old name. |
| `delete it` | Deletes the file, and finds it gone. |

Then it finds out whether resuming uploads is supported: it writes half a file, then the other half into the same
file from its middle (`PUT` with `Content-Range`), and reads the file back. A server that refuses that, or ignores
where the half goes and replaces the file with it, does not support it; this never fails the check. Last, it removes
its own folder. If it made `DRIVE_STORAGE_FOLDER`, that stays.

It prints one line per step, then the resume line, then the result, on standard output:

```
Checking the WebDAV server at https://dav.example.com/drive/ for Superit Drive <version>.
sign in: ok
make a folder: ok
write a file: ok
read part of it: ok
move it: ok
delete it: ok
resume an upload: not supported: the server ignored where the second half goes (Content-Range) and replaced the file with it
This server can hold the Drive's files. Resuming uploads: not supported.
```

A step that fails prints `<step>: failed: <reason>`, then `This server cannot hold the Drive's files: "<step>" failed.`
and nothing after it but, when its folder could not be removed, a `clean up:` line saying where it is. A step that
passes prints `<step>: ok`, sometimes with a note in brackets. The exit code is 0 when every step passed (whether or
not resuming is supported), 1 when a step failed, and 2 when a storage setting is missing or wrong (the reason goes to
standard error, and nothing was asked of the server). The check takes at most about two and a half minutes.

It does not find out how large a file the server takes, whether an upload cut off midway keeps what arrived, how many
connections the server allows at once, how fast it is, or how much room it has.

### A worked example

[`examples/compose/`](examples/compose/) runs the Drive with Docker Compose behind [Caddy](https://caddyserver.com)
(the reverse proxy, which also gets HTTPS certificates) and [Authelia](https://www.authelia.com) (the sign-in: a
sign-in page and a list of people with passwords). It runs as it is on your own computer, with a stand-in WebDAV
server and example people:

```sh
cd examples/compose
cp example.env .env
# Set DRIVE_VERSION in .env. Until a version is published, build one first, from the repository's top folder:
#   docker build -t ghcr.io/neonjelly-co/superit-drive:dev .    and set DRIVE_VERSION=dev
docker compose up -d
```

Open <https://drive.example.localhost> and sign in as `reka` with the password `example-password-reka`, or as `bence`
with `example-password-bence`. Most browsers send names under `.localhost` to your own computer; if yours does not,
add `drive.example.localhost` and `auth.example.localhost` to your hosts file. The browser warns about the
certificate: Caddy made it with its own authority, which the browser does not know. Ports 80 and 443 must be free.

What happens on each request:

0. A request under `/s/` (a public link, or a guest) goes straight to the Drive, without sign-in: Caddy removes
   `Drive-Secret`, the `Remote-*` headers and Authelia's session cookie, and passes the rest, the guests' cookie
   included (`handle /s/*` in the `Caddyfile`). Everything else goes on as follows.
1. Caddy asks Authelia whether the person is signed in (`forward_auth`). If not, Authelia's answer sends the browser
   to the sign-in page at `auth.example.localhost`, which sets a session cookie for `example.localhost` and sends the
   browser back.
2. If so, Authelia names the person by their username, and Caddy passes the request to the Drive with that username
   as `Remote-User-Id` and the secret from `.env` as `Drive-Secret`, replacing any the browser sent.
3. The Drive finds the user id in `members/members.json`, so each Authelia username is a member's `userId` there.
   The list gives their email address, name and role.

The Drive's port is not published: from the host, and from any container without the secret, it cannot be used. A
request that reaches it without `Drive-Secret` is refused with 403, except under `/s/`, where only a public link's
address, or a guest's session, opens anything. To try a link, sign in as `reka`, select a file or folder, make a public link in its details, and
open the address in a private window.

For real use on a server:

- Replace every secret in `.env` with a random one (`openssl rand -hex 32`), and the example people with real ones:
  in `authelia/users.yml` (each password as a hash, made with the command in that file's comment) and in
  `members/members.json`. Replace the member list by writing a new file and renaming it over the old one.
- Replace `drive.example.localhost`, `auth.example.localhost` and `example.localhost` with your own names, in the
  `Caddyfile`, in `authelia/configuration.yml` and as `DRIVE_ADDRESS` in `compose.yaml`. Point both names at the
  server; Caddy then gets certificates from Let's Encrypt by itself.
- Delete the `storage` service from `compose.yaml` and set `DRIVE_STORAGE_*` in `.env` to your WebDAV server. Check
  it with `docker compose run --rm drive check-storage`, which runs the storage check with those settings.
- Authelia can do much more, such as a second factor (`policy: two_factor`) and real mail for password resets; see
  its documentation.

## Releases

Pushing a tag `v<major>.<minor>.<patch>`, such as `v1.2.0`, runs `.github/workflows/release.yml`: it runs every test,
builds the image for x86 and arm64 (the Dockerfile cross-compiles, so neither needs emulation) and publishes it as
`ghcr.io/neonjelly-co/superit-drive:1.2.0`, with the version built into the binary. Nothing else is published, never
a moving tag such as `latest`. A tag that is not such a version publishes nothing, and a version the registry already
has is refused, not replaced. A published image does not reach Superit's customers by itself: that happens only when
the daemon's version catalogue lists the version with a tested upgrade path.

Before the first tag, on GitHub:

- If the organisation limits which actions may run, allow `actions/checkout`, `actions/setup-go`,
  `oven-sh/setup-bun`, `docker/setup-buildx-action`, `docker/login-action` and `docker/build-push-action` (each is
  pinned to a commit in the workflow). No secrets are needed: the workflow signs in to the registry with its own
  `GITHUB_TOKEN` and asks for `packages: write` itself.
- If the organisation limits who may create packages (organisation settings, Packages), allow container packages.
  The first run creates the package `superit-drive`, linked to this repository by the image's
  `org.opencontainers.image.source` label. A new package is private. If the package was ever made another way first,
  give this repository write access to it (package settings, Manage Actions access).
- When the repository goes public, make the package public too (package settings, Change visibility), so that
  anyone, and Superit's servers, can pull it without signing in.
- Protect version tags: a tag ruleset for `v*` that lets only maintainers create them and blocks moving or deleting
  them.

## Develop

You need Go 1.27, Bun 1.3 and Docker. `make help` lists the targets.

```sh
make build        # interface (web/dist) and binary (bin/superit-drive)
make test         # every test; needs Docker running
make test-short   # only the tests that need no Docker
make test-memory  # the image in a 64 MiB container under 40 downloads and 10 uploads at once (slow)
make image        # the container image superit-drive:dev
```

**Tests.** `test/` holds the tests through the Drive's HTTP interface. Each one starts the real binary against a
throwaway Apache `mod_dav` container (the stand-in storage, `test/webdav/`) with a temporary data folder, and acts
only as a browser or an outsider would: requests with the proxy's headers, and WebDAV with the storage login to see
what is on the storage. Start from `test/harness` and an existing test. Mail goes to a throwaway SMTP server in the
test itself (`internal/mail/mailtest`). The stand-in storage also has folders that
refuse or ignore one thing each, such as `MOVE` or `Range`, for the storage check's tests (`test/webdav/storage.conf`).
Next to the code are unit tests where a rule needs a table: the access rules in `internal/access`, settings, the
member list, the names that may become paths, the index's ids, and a check that every route goes through the
authorisation function. The interface has no tests of its own; check it in a browser.

**The memory test** (`test/memory`, build tag `memory`, `make test-memory`) builds the image from the Dockerfile and
runs it with `--memory=64m --memory-swap=64m` against the stand-in storage. It uploads a 256 MiB file with a part cut
off and resumed, then sends 40 downloads (half of them read slowly) and 10 resumable uploads of 256 MiB each through
it at once, then two zips of a folder holding 256 MiB, checks every byte and that the container was not killed, and
reports the container's memory from its cgroup (the peak, and the highest during each part, split into the Drive's
own memory, network buffers, page cache and kernel). A second test there puts 70,000 files on the stand-in storage (one
folder of 10,000, 200 folders of 200, and one folder of 20,000, more than a look reads), lets the released Drive walk
them all, then has four people open the folder of 10,000 at once, and reports the same. Both take about eight minutes
and leave only the images `superit-drive:memory-test` and `superit-drive-test-webdav`. A third puts 110,000 files on
the storage (11 folders of 10,000), lets the Drive walk them all, then, while 16 downloads (half of them read slowly) and
four people opening a folder of 10,000 go on over and over, has a share make the Drive copy its index into its data
folder and onto the storage, and reports the same; it takes about three minutes.

The harness builds the Drive with the build tag `testclock`, which is the only difference from a released Drive: in
it, `SUPERIT_DRIVE_TEST_CLOCK_SHIFT` (a Go duration such as `744h`) moves the Drive's clock forward, so a test can
restart a Drive (`Drive.Restart`) 31 days later and see the trash emptied, or ten minutes later and see an item gone
from the storage dropped; `SUPERIT_DRIVE_TEST_STOP_AT` stops the Drive at once, as if it were killed, at a named point of
its work (`hand-over moved`, `hand-over trash removed`, `delete removed`), so a test sees it finish what it left when it
starts again; and `SUPERIT_DRIVE_TEST_WALK_EVERY` and `SUPERIT_DRIVE_TEST_WALK_PAUSE` (Go durations) make the
walk through the storage start that soon, run that often and rest that long after each folder; and
`SUPERIT_DRIVE_TEST_COPY_QUIET`, `SUPERIT_DRIVE_TEST_COPY_LATEST` and `SUPERIT_DRIVE_TEST_COPY_RETRY` (Go durations) make
the copy of the index follow access changes that soon (see [The copy of the index](#the-copy-of-the-index)). A Drive
built without the tag reads no such variable. Tests change the storage directly with the harness's own WebDAV client
(`Drive.Storage`), including a `MOVE` that keeps a file's time (a storage that answered wrong for a while), `Fill`,
which puts thousands of files straight onto the stand-in storage's disk, and `Snapshot` and `RollBack`, which copy the
Drive's folder on that disk with every time kept and put it back, as a Storage Box's snapshots do (stop the Drive first,
`Drive.Stop`, then `Restart` it with the rollback settings). `Drive.OnANewServer` starts the Drive again as a new server
would, after the old one was lost: the same storage and settings, an empty data folder and a new header secret;
`Drive.Kill` stops it as a server that dies does, writing nothing more.

**Running it locally.**

```sh
make dev-storage  # stand-in storage on 127.0.0.1:8081
make run          # the Drive on 127.0.0.1:8080, with dev/drive.env and dev/members.json
make dev-ui       # in another terminal: the interface on http://localhost:5173, with hot reload
make check-storage  # the storage check against the stand-in storage
```

A browser cannot reach the Drive on :8080 directly, because nothing adds the sign-in headers. `make dev-ui` serves the
interface and passes `/api` to the Drive with the headers for `DEV_USER_ID` (in `dev/drive.env`). That is why
`DRIVE_ADDRESS` there is `http://localhost:5173`: it is the page the browser is on, so uploads from it are accepted.

**The image.** `docker build` makes one image holding one static binary, CA certificates, and empty `/data` and
`/tmp`, both owned by user 65532, which it runs as (SQLite writes a temporary file in `/tmp` when one change or one sort
is too large to keep in memory). `make image` tags it `superit-drive:dev`; the [worked example](#a-worked-example)
expects the name `ghcr.io/neonjelly-co/superit-drive:<version>`. It starts with the environment variables above and a
member list file:

```sh
docker run -p 8080:8080 -v /path/to/members:/members:ro \
  -e DRIVE_MEMBERS_FILE=/members/members.json -e DRIVE_STORAGE_URL=… (and the other settings) superit-drive:dev
```

Mount the folder that holds the member list, not the file itself: a file mount does not follow a replacement by
rename.

## Repository layout

```
cmd/superit-drive/     main: runs the Drive, or the storage check (`check-storage`)
internal/app/          wires everything together and serves until stopped
internal/settings/     every environment variable
internal/members/      the member list file, re-read when it changes
internal/mail/         the Drive's own mail over SMTP, sent in the background; mailtest/ is a throwaway SMTP server
                       for tests
internal/access/       the one authorisation function (Decide), the role at an item (RoleAt), what Shared with me lists
                       and what a search finds (SharedWithMe, Finds), whether sharing outside the project switched
                       off is all that is in the way (SwitchedOff), and their table tests
internal/server/       HTTP: routes.go lists every route; server.go's guard runs identify (under /s/, the visitor's
                       link; under /s/guest, the guest's session) → own page (for changes; through a link, only
                       reading) → place → item in it, or in its trash, or the person's upload in progress into it →
                       the access given there → access.Decide; transfers.go has downloads, zips and uploads,
                       opening.go what opens in the browser and how, search.go the search by name, links.go making
                       and removing public links, visitors.go the public prefix and the visitor's pages,
                       invitations.go inviting guests, guests.go the guests' sign-in, pages and codes,
                       oversight.go sharing outside the project switched off and what a project admin oversees,
                       held.go the held My files of people who left, handed over or deleted
internal/drive/        the Drive's operations, joining the index and the storage: files, folders, the trash,
                       shared folders, the access given on them; reread.go and walk.go keep the index in step with
                       the storage, rollback.go takes in the storage put back to a snapshot, held.go hands
                       over or deletes the held My files of people who left, copies.go writes the copies of the
                       index, and adopt.go makes the index from one when the data folder holds none
internal/index/        the SQLite index (search.go: names as a search compares them, and the walk down a scope;
                       copies.go: copies of it, made, cleared and checked);
                       schema changes are numbered files in migrations/, run with foreign keys
                       off (SQLite's recipe for rebuilding a table) and checked with foreign_key_check before each
                       commits
internal/clock/        the Drive's clock, which test builds can move forward
internal/transfers/    what holds the memory figure: capped network buffers, and the 32 transfer slots
internal/storage/      the WebDAV client
internal/storagecheck/ `superit-drive check-storage`: can a WebDAV server hold the Drive's files?
web/                   the interface (Vite, React, Tailwind with the Pilot light tokens): the members' page
                       (index.html) and the guests' (guest.html); built into web/dist
test/                  tests through HTTP; harness/ runs the Drive; webdav/ is the stand-in storage; memory/ is the
                       64 MiB test
dev/                   settings and a member list for running locally
examples/compose/      the worked example: the Drive behind Caddy and Authelia
.github/workflows/     release.yml publishes a version's image when it is tagged
```

## Licence

Superit Drive is published by neonjelly studio under the GNU Affero General Public License, version 3 (AGPL-3.0).
The full text is in [LICENSE](LICENSE).

## Contributing

Code from outside neonjelly studio is not merged until contributor terms exist.
