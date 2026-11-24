# Sharing

## Shared folders, sharing and who may do what

A shared folder is a top-level place owned by the project. Any member can make one; its maker becomes its first
manager, as a role given to them like any other, and nothing else about it depends on their account: it stays, with
its files, when they leave. Everyone holding the admin role on the project (in the member list) is a manager of every
shared folder, for as long as they hold it. A member with no role on a shared folder does not see it in their places.

**Sharing.** Access is given on a file or a folder, in "My files" or in a shared folder, to a named member (anyone in
the member list, including people who have never opened the Drive), to everyone in the project (every member, as
long as they are one, new members included), or to a guest by email address (see [Guests](public-links-and-guests.md#guests)), as viewer or
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
| Make a [public link](public-links-and-guests.md#public-links) to it, change what a folder's link lets visitors do, let an upload-only link take files again (none of these where [sharing outside the project](#sharing-outside-the-project-switched-off) is off); see it (who made it, how often it was used, what it took in), remove it | | yes | yes |
| Invite a [guest](public-links-and-guests.md#guests) to it, change their role or end date (neither where sharing outside the project is off), take it away | | yes | yes |
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
went to the trash finds it gone. An upload in one request holds the lock only while its whole file is put into its
folder, never while its bytes arrive: one that trickles keeps no shared folder from being deleted, and one whole after
its shared folder went to the trash is not stored.

## Moving into a shared folder

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

**Public links and invitations that had stopped working stay behind.** In a My files a link or an invitation works only
while whoever made it may still share its item; in a shared folder it does not depend on its maker, so one that had
stopped (its maker lost their access, or left the project) would start again there. The move judges every link and
invitation on the moved item and on everything inside it, at any depth, each where it is now: those that do not work
are removed as it moves, and logged as a count (an invitation that has ended among them; one on something in the trash
or missing from the storage unless the owner made it). Those stopped only by sharing outside the project being switched
off come along. The warning lists every one that works inside what is moved (`"inside"`), with the item it is on, so
the mover sees what stays reachable from outside the project.

```
GET    /api/places/{place}/items/{item}/move?to=42[&place=7]    who would see it there (no "to": the top folder)
       200 {"becomesProjects", "access": [...as GET …/access answers...], "seenBy": {"everyone", "members"},
            "inside": [{"item": {"id", "name"}, "path": [{"id", "name"}], "link": "view" | "upload-only"}
                     | {"item", "path", "guest": <address>, "role", "until"}]}
POST   /api/places/{place}/items/{item}/move    {"to": 42 | null, "place": 7}    "place" only for another place
       200 the item, with "place"    400 a move between places that is not allowed    404 a place or folder that is not
       there or that the person cannot add to    409 a name taken there, or something in it being uploaded
```

## Sharing outside the project switched off

A cautious organisation can keep everything inside: a project admin switches sharing outside the project (public links
and guests) off for the whole project, and a shared folder's managers (the project admins among them) for that shared
folder, such as Payroll. A My files has no switch of its own. The switches live in the Drive's index, not in its
settings: the platform passes nothing, and they go wherever the index goes (its copies, and a Drive started from one).

Where it is off (the project's switch, or the shared folder's; `access.Place.NoSharingOutside`, set in
`server.accessPlace` from the place as the index reads it):

- **No public link opens**: a visitor whose link would open what they ask for gets the page "This link is not
  available" (403), anything else the one answer for a link that does not exist. No link is made.
- **No guest gets in**: what was given to a guest answers 403, `{"unavailable": true, "error"}`; their "Shared with me"
  leaves it out and says `"unavailable": true`. They stay signed in (and can sign in, and be sent a link), to be told
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

## What a project admin oversees

A project admin cannot open anyone's My files (`access`'s table test tries every action, with sharing outside on and
off), but sees what leaves the project, and the space each person and each shared folder takes:

```
GET    /api/oversight/shared-outside
       200 {"sharingOutside": {…}, "items": [{"kind": "link" | "guest", "item": {"id", "name", "kind"},
            "place": {"id", "kind", "name" (a shared folder's), "owner" | "ownerLeft" (a My files'), "outside"},
            "sharedBy" | "former", "guest": {"id", "address"}, "role", "linkKind": "view" | "upload-only" (a link's; an
            upload-only link has no role), "since", "until", "uses", "used", "works"}]}
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

## When a member is removed

A person missing from a new version of the member list has been removed (the Drive reads the list within a second of
it changing). Nothing is deleted for that, and nothing changes on the storage or in the index: the Drive decides on the
list it has now, at every request, so someone added back with the same user id finds everything as it was.

- **They are refused**: every request of theirs, from a browser left open too, answers 403 ("You are not a member of this
  project, so its Drive is closed to you."), and the interface shows a page that says so in place of the files. An
  upload of theirs in progress can go no further.
- **Their My files is held**: kept as it is, its trash included, and closed to everyone, project admins included
  (`access.Place.OwnerLeft`). Nothing shared from it opens: its public links answer as links that do not exist,
  invitations let no guest in (a guest with nothing else that lets them in is signed out at their next request, and
  is sent no link), what was shared with members or with everyone opens nothing and leaves their "Shared with me", and a
  search finds nothing in it. The public links they made and the guests they invited in someone else's My files stop too
  (they were their shares; what they gave members there stays, for that My files' owner to take away); what they did in
  a shared folder is the project's and goes on: its files, its links, its guests and what they gave on it.
- **Back with the same user id** (a list briefly wrong, a mistake undone, a new address), they have it back as it was,
  with their trash, and everything shared from it works again; a guest who was signed out meanwhile signs in again with
  a new link. People are told apart by user id, never by address: two members swapping addresses keep their own files.
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
