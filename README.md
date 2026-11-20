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
- Members do not sign in to the Drive. A reverse proxy in front of it signs them in and adds headers saying who they
  are (see below). Everyone else comes in under one public prefix, `/s/`, which the reverse proxy opens without sign-in:
  so far, visitors with a public link (see [Public links](#public-links)). Every route passes through one authorisation
  function, `internal/access`.

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

**The public prefix.** Everything someone outside the project reaches lives under `/s/`: the pages of public links, the
files and zips they download, and (later) guests' pages. The reverse proxy opens it without sign-in, and passes nothing
about who anyone is there: it removes `Drive-Secret`, the identity headers and its own sign-in cookie, as it removes them
everywhere else from what a browser sent. The Drive needs no secret there, and reads none of them: a request under
`/s/` is a visitor's, always, whatever it carries, so a signed-in member who opens a link is a visitor like anyone, and a
forged `Remote-User-Id` there does nothing. No request outside `/s/` is ever a visitor's: without the secret it is
refused with 403 as before. In short, the reverse proxy must:

- send `/s/*` to the Drive without sign-in, removing `Drive-Secret`, `Remote-User-Id` (and the other `Remote-*`
  headers) and its sign-in cookie; and pass `Range`, `If-Range` and the method through as for any other request;
- sign in everything else and add the two headers above, as before, and never add `Drive-Secret` to a request under
  `/s/` (the Drive would ignore it there, but the secret would travel where it is not needed);
- not cache answers under `/s/` (they say `Cache-Control: no-store`) nor log their addresses: an address there is the
  secret that opens a link.

Paths are matched as the Drive's router cleans them: `/s/x/../../api/places` is redirected to `/api/places`, which
needs sign-in. Caddy's path matcher ignores case, so it also opens `/S/…` without sign-in; the Drive refuses that as
any request outside `/s/` without the secret.

**Changes come only from the Drive's own pages.** The reverse proxy signs members in with a cookie, which a browser
also sends when a page on another site makes it call the Drive. So every request other than `GET` and `HEAD` must
carry an `Origin` header equal to `DRIVE_ADDRESS` (browsers add it themselves); any other origin, `null`, or none is
refused with 403, and the Drive logs both values. The reverse proxy must pass `Origin` through unchanged, and
`DRIVE_ADDRESS` must be the address in the browser's address bar.

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
| `DRIVE_HEADER_SECRET` | yes | Secret the reverse proxy adds as `Drive-Secret`. At least 32 characters. |
| `DRIVE_MEMBERS_FILE` | yes | Path of the member list file. |
| `DRIVE_DATA_DIR` | yes | Folder for the index. The container image sets it to `/data`. |
| `DRIVE_LISTEN` | no, `:8080` | Address the web server listens on. |
| `DRIVE_SMTP_HOST` | no | Mail server the Drive sends its own mail through (see [Mail](#mail)), such as `smtp.example.com`. Empty: the Drive sends no mail, and sharing works without it. |
| `DRIVE_SMTP_PORT` | no, `587` | Port of the mail server. |
| `DRIVE_SMTP_TLS` | no | How the connection to the mail server is encrypted: `starttls`, `implicit` (TLS from the first byte) or `none`. Empty: `implicit` on port 465, `starttls` on any other. `none` only without a login. |
| `DRIVE_SMTP_USER` | no | Login name for the mail server. Empty when it needs none. |
| `DRIVE_SMTP_PASSWORD` | no | Password for the mail server, with `DRIVE_SMTP_USER`. |
| `DRIVE_MAIL_FROM` | no | Address the Drive's mail comes from, such as `drive@files.example.com` or `Drive <drive@files.example.com>`. Needed with `DRIVE_SMTP_HOST`. |

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
knows, going back to the place's top folder; any other is removed once the day in its name is 30 days past.

**Space used** is the Drive's own sum of the sizes of the files its index knows, in every place, the trash included,
shown in the sidebar as "about" against `DRIVE_CAPACITY` (or alone, without it). Files put on the storage directly
count once the Drive has looked at their folder (opened, or walked through); files gone from it count until they are
dropped from the index; uploads in progress count once they are in their folder.

An upload nothing has arrived for in a day is removed, with its file, when the Drive starts and every hour; so is a
file in the uploads folder the index does not know (left by an upload the Drive did not finish) once the storage says
it has not changed for a day. The index copy gets a hidden folder of its own beside `trash/` with its ticket.

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
  a minute after it starts, then six hours after each walk ends. After each folder it rests half a second, or three
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
  written back, looks go on. Most looks change nothing, and then ask and write nothing more.

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

**After the storage is put back to an earlier state** (a snapshot rolled back; the Drive is stopped meanwhile, and the
walk goes on a minute after it starts): files that were added or changed since are gone or as they were; the looks
hide what the index knows that is gone, and drop it with its sharing ten minutes later; files that are back (deleted or
moved since) are new items with their folder's access only; files changed since keep their ids with their older
content; trash entries made since are gone (their items hidden, then dropped), and entries that are back (restored or
removed for good since) show in the Trash as entries put there directly; an upload in progress whose file went starts
again from its beginning. Sharing itself is never put back. The mark is in every snapshot taken after the Drive's first
place, so looks go on after a rollback; one to before that changes nothing until the mark is written back.

**Connections to the storage.** A server closes a connection kept open between requests after a while, and can do so
just as the Drive sends on it; the Drive then cannot tell whether the server acted. So every request that changes the
storage (`PUT`, `MKCOL`, `MOVE`, `DELETE`) goes on a new connection of its own, which a server never closes for being
idle, and is never sent twice. Reads (`GET`, `PROPFIND`) use kept connections, closed by the Drive after four seconds
idle, and are sent once more on a new connection when the server closed the kept one first.

### Shared folders, sharing and who may do what

A shared folder is a top-level place owned by the project. Any member can make one; its maker becomes its first
manager, as a role given to them like any other, and nothing else about it depends on their account: it stays, with
its files, when they leave. Everyone holding the admin role on the project (in the member list) is a manager of every
shared folder, for as long as they hold it. A member with no role on a shared folder does not see it in their places.

**Sharing.** Access is given on a file or a folder, in "My files" or in a shared folder, to a named member (anyone in
the member list, including people who have never opened the Drive) or to everyone in the project (every member, as
long as they are one, new members included), as viewer or editor. A shared folder as a whole can also be given, by
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
| Make a [public link](#public-links) to it, see it (who made it, how often it was used), remove it | | yes | yes |
| Rename, move or delete it: also needs editor of the folder it is in | | yes | yes |
| Restore it from the trash: also needs editor of the folder it goes back to, and either the place's whole trash or having put it there | | yes | yes |

Moving something into another place is only for the owner of a "My files": out of it, into a folder of a shared folder
where they may upload (see [Moving into a shared folder](#moving-into-a-shared-folder)).

| In a shared folder as a whole (its top folder) | Viewer | Editor | Manager |
|---|---|---|---|
| List it, download it as a zip, see who has access to it | yes | yes | yes |
| Upload, make folders, see its trash | | yes | yes |
| Decide who has access to it as a whole, delete it | | | yes |

In their own "My files" the owner may do everything, including sharing any item in it, and downloading it whole as a
zip; nobody else may do anything there, project admins included, except with what was shared with them. Every request
of a resumable upload (starting it, each part, finishing and cancelling it) is decided as an upload into its folder,
and an upload in progress is its uploader's alone: for anyone else, project admins and the folder's owner included, it
does not exist. Someone whose right to upload there is taken away mid-upload can do nothing more with it, not even
cancel it: its file stays, unseen and unused, until it is removed a day later. A "My files" whose owner has left the member
list is held: nothing shared from it opens, until they are back (what happens to it otherwise is a later ticket).

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
```

Each answers with who has access now: one line per grant (and the owner, or the project admins, first), each with
whether it was given right there or comes from a folder above (named when the person can open that folder). A
listing gives each item what the person may do with it (`can`) and who can see it (`seenBy`: everyone, or the members
by user id), and the folder's own `role` and `can`. Grants live in the index as one row per grantee per item (or per
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
index, and its folder stays held, so restoring always has its own folder to go back to. Its managers and the project
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
- **Sharing outside the project switched off** (a later ticket adds the switches, for the project and for one shared
  folder): no link opens there and none can be made, but links can still be seen and removed. The decision is already
  in `internal/access` (`Place.NoSharingOutside`).
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
"This link does not work".

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
A file's bytes go out with the same content type and policy as to members (below). No cookie is set.

**What visitors can use up, and what bounds it.** Their transfers take at most 8 of the 32 slots, in a line of their
own, and at most 64 visitors wait in it at once (more are told 503 at once); a transfer that moves nothing for two minutes
is ended. Besides transfers, at most 16 visitors' requests are worked on at once, and 64 more wait up to 5 seconds; more
are answered 503 at once. Trying tokens costs a hash and one look in the index, with no lock and no log line. A zip is
bounded as for members (10,000 entries).

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

The Drive sends one kind of mail so far: "shared with you", to a member something was just shared with by name. With
`DRIVE_SMTP_HOST` empty it sends none and attempts none, and nothing else changes: sharing works the same. The line the
Drive logs at start says where mail goes (`mail="smtp.example.com:587 (starttls)"`) or `mail=off`.

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
  the share is answered at once whatever happens to it. A mail that fails is logged with what it was about (the
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

- **At most 32 transfers are in flight, of which visitors with a public link hold at most 8.** Members can always use
  the other 24, and all 32 while no visitor needs one; when a slot comes back, a member waiting gets it before a
  visitor. Visitors wait in a line of their own, at most 64 of them; more are told at once that the Drive is busy. A
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
its own (Go's heap at its limit, and SQLite's caches) and 16 MiB page cache.

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
who may share a file or folder make a public link to it, which anyone opens without signing in. Guests are not built
yet. Large files upload in parts that resume after a broken connection, downloads resume and seek, and folders upload
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
  has access to what; keep a backup of it.

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

0. A request under `/s/` (a public link) goes straight to the Drive, without sign-in: Caddy removes `Drive-Secret`, the
   `Remote-*` headers and the cookie (`handle /s/*` in the `Caddyfile`). Everything else goes on as follows.
1. Caddy asks Authelia whether the person is signed in (`forward_auth`). If not, Authelia's answer sends the browser
   to the sign-in page at `auth.example.localhost`, which sets a session cookie for `example.localhost` and sends the
   browser back.
2. If so, Authelia names the person by their username, and Caddy passes the request to the Drive with that username
   as `Remote-User-Id` and the secret from `.env` as `Drive-Secret`, replacing any the browser sent.
3. The Drive finds the user id in `members/members.json`, so each Authelia username is a member's `userId` there.
   The list gives their email address, name and role.

The Drive's port is not published: from the host, and from any container without the secret, it cannot be used. A
request that reaches it without `Drive-Secret` is refused with 403, except under `/s/`, where only a public link's
address opens anything. To try a link, sign in as `reka`, select a file or folder, make a public link in its details, and
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
and leave only the images `superit-drive:memory-test` and `superit-drive-test-webdav`.

The harness builds the Drive with the build tag `testclock`, which is the only difference from a released Drive: in
it, `SUPERIT_DRIVE_TEST_CLOCK_SHIFT` (a Go duration such as `744h`) moves the Drive's clock forward, so a test can
restart a Drive (`Drive.Restart`) 31 days later and see the trash emptied, or ten minutes later and see an item gone
from the storage dropped; and `SUPERIT_DRIVE_TEST_WALK_EVERY` and `SUPERIT_DRIVE_TEST_WALK_PAUSE` (Go durations) make the
walk through the storage start that soon, run that often and rest that long after each folder. A Drive built without
the tag reads no such variable. Tests change the storage directly with the harness's own WebDAV client
(`Drive.Storage`), including a `MOVE` that keeps a file's time (a storage that answered wrong for a while) and `Fill`,
which puts thousands of files straight onto the stand-in storage's disk.

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
                       and what a search finds (SharedWithMe, Finds), and their table tests
internal/server/       HTTP: routes.go lists every route; server.go's guard runs identify (under /s/, the visitor's
                       link) → own page (for changes; under /s/, only reading) → place → item in it, or in its trash,
                       or the person's upload in progress into it → the access given there → access.Decide;
                       transfers.go has downloads, zips and uploads, opening.go what opens in the browser and how,
                       search.go the search by name, links.go making and removing public links, visitors.go the
                       public prefix and the visitor's pages
internal/drive/        the Drive's operations, joining the index and the storage: files, folders, the trash,
                       shared folders, the access given on them; reread.go and walk.go keep the index in step with
                       the storage
internal/index/        the SQLite index (search.go: names as a search compares them, and the walk down a scope);
                       schema changes are numbered files in migrations/, run with foreign keys
                       off (SQLite's recipe for rebuilding a table) and checked with foreign_key_check before each
                       commits
internal/clock/        the Drive's clock, which test builds can move forward
internal/transfers/    what holds the memory figure: capped network buffers, and the 32 transfer slots
internal/storage/      the WebDAV client
internal/storagecheck/ `superit-drive check-storage`: can a WebDAV server hold the Drive's files?
web/                   the interface (Vite, React, Tailwind with the Pilot light tokens); built into web/dist
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
