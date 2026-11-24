# Public links and guests

## Public links

A member who may share a file or folder (an editor, the owner of a My files, a manager of a shared folder, a project
admin in a shared folder: `access.MakeLink`) makes a public link to it in its details. Anyone with the link's address
opens it without signing in, on a plain page under `/s/`, and downloads one file, or everything as a zip; or, through an
upload-only link on a folder, sends files into that folder and sees nothing of what is in it (see [Upload-only
links](#upload-only-links)). A link has no password and no end date: it works until it is removed. A viewer cannot make
one, see one's details or remove one; they, and everyone who can see the item, see in "who can see this" (and, for a
view link, in the list's sentence) that anyone with the link can.

- **One link per item**, on a file or a folder, never on a place's top folder (a My files or a shared folder as a
  whole). A link is a **view link** or, on a folder only, an **upload-only link**; its kind is chosen when it is made,
  and changed (`PUT …/link`) at the same address. A view link on a folder covers everything in it, at any depth, and
  nothing above it or beside it, and lets visitors see and download, nothing else.
- **The address** is `DRIVE_ADDRESS` + `/s/` + a token of 192 random bits from `crypto/rand`, in base64url (32
  characters): it says nothing about the item. The index keeps only the token's SHA-256, so a copy or a backup of the
  index opens no link. The address is therefore given once, in the answer that makes the link: the details panel shows
  it, with Copy, until the page is reloaded, and says so; later it shows that the link exists, who made it and when, and
  how often it was used, and offers "Make a new link" (in its place: the old address stops working) and "Remove link".
- **Removed is dead for good**: its address answers exactly as one that never existed, or a malformed one (the same
  status, page and headers, after the same work: the token is hashed and looked up either way). A new link gets a new
  address. Neither the address nor the token is ever written to the log. A download or zip that was waiting for its
  turn when the link was removed sends nothing, and a zip on its way is cut short before the next thing it would add
  (see [Large transfers](operations.md#large-transfers-and-the-memory-figure)).
- **It follows its item**, by the item's id: through rename, move (out of a My files into a shared folder too), and
  replacing a file with an upload. It covers what is in the item where it is now. While the item is in the trash, or in
  a shared folder in the trash, the link opens nothing; restored, it works again at the same address. While the item is
  gone from the storage it opens nothing either; dropped from the index (see [Keeping in step with the
  storage](storage.md#keeping-in-step-with-the-storage)), or removed for good, the item takes its link with it, and no file put
  under its name later is ever reached through it.
- **Whose it is.** A shared folder belongs to the project, and so do the links made in it: they work whoever made them,
  also after their maker left the project. In a My files a link is its maker's share: it works only while they may still
  share the item there. It stops when that is taken away from them (or they leave the project), and works again if it is
  given back; a held My files (its owner left) opens nothing. Whoever may share the item sees in its details whether the
  link works now. Whoever changes a link's kind becomes its maker: an upload-only link that someone else turned into one
  that shows the folder is theirs, and stops with their access, not with the access of whoever made it first. (An
  invitation changed by someone else becomes theirs the same way.)
- **Sharing outside the project switched off** (for the whole project, or for the link's shared folder: see [Sharing
  outside the project switched off](sharing.md#sharing-outside-the-project-switched-off)): no link opens there and none can be
  made, but links can still be seen and removed, and work again, at the same address, when it is switched back on. A
  visitor whose link would open is told so, on a page of its own ("This link is not available", 403); anything the link
  does not cover still answers as a link that does not exist.
- **Uses.** For each link the Drive keeps how often it was used and when last, and nothing else about visitors. One use
  is a page opened through it (its own, or a folder's inside it), or a download, a file opened or a zip from its start,
  or a file sent in through an upload-only link; not a `HEAD`, not a part of a file after its start (a video seeking, a
  download resuming), not a refused request. Link previews (chat programs that fetch a page to show it) and search
  engines' robots count as uses: nothing tells them apart. Uses are counted in memory, so a visitor's request never
  waits for the index or a lock for it; they are written every 30 seconds and when the Drive stops (a Drive that is
  killed loses the last half minute's).

```
GET    /api/places/{place}/items/{item}/link     200 {"link": {"kind": "view" | "upload-only", "made", "madeBy",
                                                 "former", "uses", "used", "works", and for an upload-only link
                                                 "takenIn", "limit", "files", "filesLimit", "full"} | null}
POST   /api/places/{place}/items/{item}/link     {"replace": true, "kind": "view" | "upload-only"} (all may be left out:
                                                 a view link, not in place of one)
       201 {"link": {…, "address": "https://files.example.com/s/<token>"}}    the only answer with the address
       400 {"error"}                             an upload-only link on a file, or a kind there is not
       409 {"error", "exists": true}             it has a link, and replacing it was not asked for
PUT    /api/places/{place}/items/{item}/link     {"kind": "view" | "upload-only"}    the same link, at the same address
       200 {"link": {…}}    400 an upload-only link on a file, or a kind there is not    409 it has no link
POST   /api/places/{place}/items/{item}/link/reset    an upload-only link counts what it takes in from 0 again
       200 {"link": {…}}    409 it has no link
DELETE /api/places/{place}/items/{item}/link     204 (also when it has none)
```

Making, changing and resetting a link are `access.MakeLink` (not where sharing outside the project is off: 409 with
`"outside"`); seeing and removing it `access.ManageLink`. Listings, search and "Shared with me" say `"seenBy": {…,
"link": true}` for an item anyone with a working view link can see, and "who can see this" has a line `{"link": true,
"linkKind": "view", "role": "viewer", "given" | "inherited", "from"}`, or, for an upload-only link, on its own folder
only and without a role, `{"link": true, "linkKind": "upload-only", "given": true}`.

**What a visitor reaches**, all under `/s/`, and only with `GET` (or `HEAD`), but for sending a file through an
upload-only link: any other method there, and `POST` anywhere but that one route, is answered 405 with `Allow: GET,
HEAD` before anything else is looked at.

```
GET    /s/{token}                          the page of what the link is on (an upload-only link's: where to send files)
GET    /s/{token}/items/{item}             the page of a folder (or a file) inside it
GET    /s/{token}/items/{item}/download    a file, to be saved (as a member's download)
GET    /s/{token}/items/{item}/open        a file shown in the browser, by the safe list (as a member's …/open)
GET    /s/{token}/items/{item}/zip         a folder as one zip (as a member's …/zip, ?item=… too)
POST   /s/{token}/files?name=…[&turn=…]    a file sent through an upload-only link (below)
```

`{item}` must be the link's item or something inside it, through a view link; an upload-only link reaches no item at
all, its own folder included (only its page and sending). A link that does not work, and anything it does not cover (a
folder above it, a file beside it, an item of another place or in the trash), gets the one answer: 404 with the page
"This link does not work" (the same bytes for every one of them, in each language). The one exception is a link that
would open what is asked for but for sharing outside the project being switched off where it is: 403 with the page "This
link is not available", which says it is switched off for now (only after the token matched, and nothing about what it
is on).

**The page** is plain HTML made by the Drive (`internal/server/visitors.go`), in one column on every screen, with no
script and nothing of the members' interface (but an upload-only link's: below): it works with JavaScript off. It says
who shared it (the maker's display name, never an email address or user id; the project's name when the maker has none
or has left), the item's name, the folders from the link's item down to the one shown (never anything above), "Download
all as zip" for a folder, and what is in it: folders open their own page, files have Download, and Open when their kind
opens in the browser. A folder's page lists at most 1,000 items (the zip holds them all). It lists what the index knows:
what was put on the storage directly shows once a member has opened its folder, or the walk has passed it; the zip looks
at the storage. It is in the visitor's language (see [Languages](using-the-drive.md#languages)), with a switch to the other one at the
bottom.

Every answer under `/s/` carries `X-Robots-Tag: noindex, nofollow` (and the page `<meta name="robots">`),
`Referrer-Policy: no-referrer` (the address is the secret), `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`,
`Cross-Origin-Resource-Policy: same-origin` and, on pages, `Content-Security-Policy: default-src 'none'; style-src
'sha256-…'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`: nothing loads or runs but the page's own style.
A file's bytes go out with the same content type and policy as to members (see [Opening files in the browser](using-the-drive.md#opening-files-in-the-browser)). No cookie is set or read on a link's
addresses.

**What visitors can use up, and what bounds it.** Their transfers (files sent through an upload-only link included) take
at most 8 of the 32 slots, in a line of their own (shared with guests), and at most 64 visitors wait in it at once, and
at most 64 outsiders' turns keep a place in it (more are told 503 at once); a transfer that moves nothing for two
minutes is ended. Besides transfers, at most 16 visitors' requests are worked on at once, and 64 more wait up to 5
seconds; more are answered 503 at once. A turn is for the Drive's own work, never for a connection: a page is made
while its turn is held and sent after the turn is given back, to be taken within 30 seconds (long pages waiting to be
taken hold 8 MiB together at most; one more is told 503), and the small body of a change (a guest's, read before any
turn is taken; a member's too) must arrive within 15 seconds. Trying tokens costs a hash and one look in the index, with no lock and no log
line. A zip is bounded as for members (10,000 entries).

### What anyone can ask for is bounded

- **Headers.** A request's first line and headers may take 32 KiB together (`app.MaxHeaderBytes`; Go's own slack of
  4 KiB on top): a longer one is answered 431 before anything about it is looked at, a visitor's, a guest's or a
  member's. An address the Drive answers and what a browser and the reverse proxy send with it take a few kilobytes.
- **The log.** The lines someone outside the project can cause as often as they ask are written at most once a minute
  each, with what they quote of the request cut to 200 bytes, and the next one says how many were left out (`leftOut`;
  package `seldom`): `refused a change that did not come from the Drive's own pages` (anyone, under `/s/guest`; a
  member's browser, made to by another site's page), the three lines about asks for a sign-in link (some were
  dropped, one came too soon after a link was sent, too many links were sent; anyone, the last two with a guest's
  address; see [Guests](#guests)), and `the storage refused a name` (a visitor through an upload-only link, a
  guest, a member; see [Layout on the storage](storage.md#layout-on-the-storage)). The counts are in memory: a restart forgets them, and
  the next line of each kind is written at once.

- **A change, once begun, is finished.** From the moment the Drive asks the storage to move a file or folder (an upload
  into its folder, a rename, a move, a delete, a restore), it goes on without whoever asked: hanging up then would
  leave the storage changed and the index not. So a storage that does not answer keeps that request's hold on its
  place, on the name, and for an upload on its transfer slot, after the person has gone: for as long as the storage's
  own time limits, 30 seconds for most steps, ten minutes for moving a whole upload into its folder (a storage that
  copies where a Storage Box renames), some twenty minutes at the very worst. Nobody can make the storage slow; while
  it is, outsiders' uploads can keep their 8 slots that long, and members' 24 are their own.

### Upload-only links

A folder's link can be upload-only: anyone with its address sends files into that folder, and sees nothing of what is in
it, not even the names, nor what anyone else sent; nothing they send replaces anything. (One thing does show: how much
room the link has left, since a file larger than that is refused before it is read; and so, to someone who keeps
asking, how large the files that arrive through it are.) It is for collecting files from
people outside the project (a client's documents, a supplier's invoices), without a guest's sign-in. It counts where a
view link would (its folder live, its place open, sharing outside the project on there; in a My files while its maker
may still share the folder) and lets its visitors do two things only, at its own folder (`access.EnterThroughLink`,
`access.UploadThroughLink`, never the members' `Upload`, which can replace a file, and so delete one): open its page,
and send a file into that folder itself, never into a folder inside it. A file's link is a view link: an upload-only one
on a file is refused (400), and counts for nothing in `internal/access` if one were in the index.

**The page** (`GET /s/{token}`) says who shared the folder and its name, "Send files to this folder. What is in it stays
hidden: you cannot see its files, their names, or what anyone else sent. Nothing you send replaces anything in it.", and
offers a place to drop files or choose them, and Send. It runs one small script of its own, inline
(`internal/server/sendin.go`), which sends each file chosen or dropped (files only: a folder dropped is refused with a
line that says so) as one request, one after another, shows how far each has gone, waits its turn when the Drive is
busy, and says why a file was not taken. Its policy is the visitors' with that one script, by its hash, and somewhere to
send to: `… script-src 'sha256-…'; connect-src https://files.example.com/s/; … form-action 'none' …`. Nothing else runs
or loads, no form goes anywhere, and the script talks only to the public prefix of `DRIVE_ADDRESS`. Without JavaScript
the page says that sending needs it. A link that has taken in all it may says so instead ("This link has taken in all it
can for now. Ask whoever sent it to you to make room for more."), with nothing to send and no script.

Why a script, and not a plain form posting the files: a form sends every file chosen in one `multipart/form-data`
request, in which no file says how long it is until it has all arrived. The Drive could then neither count a file
against the link's limit before reading it, nor stream it to the storage with its length (the one `PUT` that writes the
whole file or nothing), and one large file cut off would lose all the others with it. One request per file, its length
in `Content-Length`, is the shape a member's upload in one request already has, bounded the same way.

**Sending a file.**

```
POST   /s/{token}/files?name=<its name>[&turn=<word>]    Content-Length: <its size>, and its bytes as the body
       201 {"size": n}                         taken: the same answer whatever was in the folder (a name taken
                                               up to " (999)" gets a few random letters in its place)
       400 {"error"}                           a name the Drive does not store, or the file stopped before its end
       411 {"error"}                           no Content-Length (sent in chunks)
       413 {"error", "full": true | false}     it would take the link past its limit (full: it has reached it)
       503 {"error", "busy": true}, Retry-After    not its turn: send it again then, with the same turn
       404, 403                                the pages of a link that does not work, or is not available now
```

- **It streams**, as a member's upload in one request does: straight to a file of its own in the hidden uploads folder,
  with one `PUT` of its length, then moved into the folder (`MOVE`, never replacing anything). Bytes past its
  `Content-Length` are not read as the file; fewer, and nothing is kept. A file cut off starts again from its start (the
  page sends it again, with the same turn, for a few minutes): there is no resuming through a link, so no upload in
  progress, and nothing to keep for a visitor between requests.
- **A taken name is neither revealed nor replaced.** When something in the folder has the name (a file, a folder, a file
  sent before, a change on its way), the file is kept beside it, as `Name (2).ext`, `Name (3).ext` and so on, labelled
  as come through a link. The answer is `201 {"size": n}` either way, to the byte. The free name is found in the index
  and the Drive's own claims, never by asking the storage, so that the time an answer takes does not say either: a look
  on the storage costs a round trip per name taken, which a visitor timing small files could count. (A file only the
  storage has, put there directly and not yet seen by a look, still makes the move fail, and the next name is tried:
  that much a visitor could time, and only about such a file.)
- **Names** are as for any file (`drive.CheckName`: one plain name, no `/`, no control character, valid text, at most
  255 bytes), and, from a stranger, without the characters that turn the direction of text (U+202A to U+202E, U+2066 to
  U+2069), with which `Invoice` + U+202E + `fdp.exe` would read as `Invoiceexe.pdf` in the list: 400, before anything is
  read. A name that only looks like a path (`..Notes.txt`, `..\Notes.txt`, `%2e%2e%2f`) is a name, in the link's folder.
- **Decided on twice.** The link is decided on when the request comes in, and again once the file has all arrived,
  holding the place, with the link, its place (with its switches, and whether it is held) and its folder read anew. A
  link removed, made a view link or made anew meanwhile, its folder deleted or gone, its maker no longer allowed to
  share it or gone from the project, and its My files held or being handed over: nothing is kept, and the visitor is
  answered as the link now answers (404); sharing outside the project switched off meanwhile: 403. Its folder moved into
  another place meanwhile: 409, nothing kept (sent again, it goes there).

**10 GB or a thousand files, then a reset.** An upload-only link takes in at most 10 GB (10^9 bytes each, as storage
is sold) and at most 1,000 files, an empty file counting as one (bytes alone would let empty files in without end),
until whoever may share its folder resets it (`POST …/link/reset`, "Make room for more" in its details); what it took stays in the
folder. Each file's `Content-Length` is held against the limit from before its first byte is read, under one lock for
all links, so files on their way at once never pass it together, whatever they say about themselves: one that would is
refused at once (413), one cut off gives its room back, and what is kept is counted only once it is in its folder. A
full link (in bytes or in files) refuses every file, an empty one too, and its page says so; its member sees both
counts in its details. Changing its kind keeps the counts; making a new link in its place, or removing it, ends them.
A folder that already holds 10,000 entries, as many as the Drive keeps in step with the storage and puts in a zip,
takes no file through a link, however often the link is reset (413, as a full link). The counts (`links.taken_in`,
`links.taken_files`) are not an access change (no copy of the index is made for them); copies carry them as they were
when they were made. A test build reads `SUPERIT_DRIVE_TEST_LINK_LIMIT` and `SUPERIT_DRIVE_TEST_LINK_FILES` for smaller
limits.

**Labelled.** A file sent through a link carries `"addedBy": {"link": true}` in members' listings, search and "Shared
with me" (as a guest's carries `{"guest": <their address>}`), and the interface shows "Through a link" under its name
and "Someone with an upload-only link: from outside the project" in its details. A member's upload over it (replace)
takes the label off. Guests never see labels.

**What strangers can use up.** A file sent is a transfer in the outsiders' line (8 slots at most, shared with view
links' downloads and guests): members' 24 are never theirs. A file not let through is answered 503 before any of it is
read, and its turn keeps its place; at most 64 outsiders' turns are kept at once, so new turns sent as fast as a
stranger likes cost a fixed amount of memory. Each link takes in at most 10 GB and 1,000 files until it is reset. A stranger who sends a
file slowly holds one of the 8 slots, and the room their file says it needs, for as long as bytes keep coming (a
transfer is ended only after two minutes without any); members are not slowed by it, and whoever may share the folder
can remove the link. Measured in the [memory test](operations.md#large-transfers-and-the-memory-figure).

## Guests

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
- **Signing in.** The guests' page asks for their address and mails it a sign-in link: the guests' page, with the
  address and a code after the `#` (which no browser sends by itself, so it reaches no log and no other site). Opened,
  the page takes the code out of the address bar and the history and offers "Sign in as (the address)"; on that click
  it sends the code with the address, as a change from the Drive's own page, and the guest is signed in in that
  browser. Nothing signs in by itself: a program that fetches the links in a mail, or opens them and runs the page (a
  mail scanner, a preview), signs nobody in and uses nothing up, and nobody is signed in as someone else by following a
  link. The code is 32 bytes
  from `crypto/rand` (43 characters), far too long to guess, and works once, for 30 minutes. So nothing a stranger does
  at the sign-in page takes a guest's sign-in away:
  - wrong codes are not counted, for one address or for all: no number of them stops a right link from working;
  - asking again leaves the links already mailed working; while one mailed in the last 5 minutes is not used yet, no
    other is sent to that address (the answer says so to everyone), so a stranger makes that address get one mail in
    5 minutes at most, and it always has a link that works; once a link is used, another can be asked for at once, up
    to 12 links to one address within an hour, used or not;
  - an address waits once to be sent a link, however often it is asked for; 64 addresses wait at most, and the rest are
    dropped, answered the same;
  - all addresses together are sent 1,000 links an hour at most, a bound on the mail the page can make the Drive send
    that takes more than eighty guests' addresses, asked for without pause, to reach (one address is sent 12 an hour
    at most).

  Reaching any of these is logged, once a minute at most, without any address. A link is sent only to a guest's address
  with something shared with it that lets them in now (or will once sharing outside the project is switched back on
  where it is), and never to a member's; the page says the same, at once, for every address (the work is done
  afterwards, one address at a time, and nothing is logged about an address that is no guest's). A try of a code is one
  look for it, the same whatever the address. The index keeps only each code's HMAC-SHA-256, keyed with a key made from
  `DRIVE_HEADER_SECRET`, which the index never holds. A guest who reads their mail on another device than the one they
  want to work on opens the link there: the link signs in the browser it is opened in.
- **Staying signed in.** The link starts a session of 30 days on that browser, kept by the Drive (the index keeps
  its token's SHA-256; the browser the token, in a cookie): `__Host-drive-guest`, `Path=/`, `Secure` with HTTPS
  (`drive-guest`, `Path=/s/guest` when `DRIVE_ADDRESS` is plain HTTP), `HttpOnly`, `SameSite=Lax` (so it comes along
  when a guest opens the Drive from their mail, and never with another site's change). Under the `__Host-` name a
  browser takes the cookie from the Drive's own name alone: a page on another name under the same domain cannot give a
  guest's browser a session of its own. The price is `Path=/`: the browser sends the cookie with members' requests too
  (and so to the sign-in in front of them), where the Drive never reads it. Guests signed in before a version with
  this name sign in once more. A guest stays signed in on at most ten browsers;
  signing out ends that one. A session ends, for good, when it ends; when the last of their invitations is taken away
  or ends (the guest is removed with it, and their codes, sessions and uploads in progress); when nothing given to them
  lets them in any more (in the trash, gone from the storage, in the My files of someone who left, its giver may no
  longer share it), sharing outside the project switched off aside; and when their address appears in the member list.
  Signed out, they sign in again with a new link.
- **A guest who joins the project** (their address appears in the member list) keeps their access as a member: within a
  second of the list changing (and when the Drive starts), every invitation to them that works at that moment becomes
  a grant to that member by user id, with its end date, and the guest is removed. An invitation that does not work then
  is dropped with the guest, so that nothing taken away comes back: one that has ended, one whose giver may no longer
  share its item or has left the project, one on something in the trash, and one the new member gave their own address.
  (One stopped only by sharing outside the project being switched off comes along.) Where the member was given
  something on the same item already, the two are never mixed: the invitation takes that grant's place only when it is
  no less on both counts (its role no lower, its end no earlier), and otherwise the member's grant stays as it is.
  From then on it follows their user id, also when their address changes. A member who leaves never
  becomes a guest; their address invited again is a new guest.
- **Labels and uses.** A file or folder a guest added (uploaded or made) is labelled with their address, for members
  (`"addedBy": {"guest": …}` in listings, "From a guest" under its name and "Added by" in the details), as a file sent
  through an upload-only link is labelled "Through a link" (see [Upload-only links](#upload-only-links)): an upload
  over it by a member takes the label off.
  For each invitation the Drive keeps how often it was used and when last, and nothing else: a folder or file opened, a
  download, a file opened or a zip from its start, an upload, a new folder, a rename, a move or a delete through it.
  Counted in memory and written every 30 seconds, as links' uses are.
- **Mail that works.** Inviting a guest needs mail that works: without mail settings, or with a mail server the Drive
  cannot reach, cannot reach as `DRIVE_SMTP_TLS` says, or that refuses its login, inviting is refused (409, `"mail":
  false`) and says why, and `GET /api/me` says so before anyone types (`"guests": {"invite", "why"}`). Sharing with a
  member's address, and public links, still work.
- **At most 30 invitations an hour from one member.** Every new invitation is a mail from the Drive to an address the
  member typed, with names they chose (their own, and the file's or folder's), and inviting an address, taking the
  invitation away and inviting it again mails it each time. So a member has 30 invitations mailed within any hour; the
  next is refused (429, with `Retry-After`), says so, and makes no invitation. Changing an invitation that is there,
  taking one away and sharing with a member's address are not counted. The count is kept in memory: a restart forgets
  it, and the hour starts again. ("Shared with you" mail to members has a count of its own: one member's shares mail
  at most 60 times within any hour; past that the share is made all the same, and no mail goes.) An address whose part
  after the `@` is a machine's number, such as `x@[10.0.0.5]`, is not an address a guest can have.
- **Their transfers** take turns with visitors': outsiders hold at most 8 of the 32 slots, in a line of their own. A
  download or zip of theirs that waited is decided again when its turn comes, a zip as it goes on, and an upload in one
  request once its file is whole (see [Large transfers](operations.md#large-transfers-and-the-memory-figure)): an invitation taken
  away or ended meanwhile sends and stores nothing more. A guest has at most 50 resumable uploads in progress.

```
GET    /s/guest/                                      the guests' page (and /s/guest/assets/…, /s/guest/favicon.svg)
POST   /s/guest/api/code                {"address"[, "place", "item"]}   202, the same for every address: a sign-in
                                        link is mailed (to where the guest was going, when the page says)
POST   /s/guest/api/sign-in             {"address", "code"}   the link's code: 200 and the cookie; 400 not right,
                                        used, or more than 30 minutes old
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

A name a guest gives (to a file they upload, a folder they make, something they rename) is refused with the characters
that turn the direction of text (U+202A to U+202E, U+2066 to U+2069), as a name sent through an upload-only link is: a
member reads those names in their own list.

Every answer under `/s/guest` carries `X-Robots-Tag: noindex, nofollow`, the members' `Content-Security-Policy` (the page
runs the interface's own script) and `Referrer-Policy: same-origin` (the addresses there hold no secret, and
`no-referrer` would make a change's `Origin` null); the API's answers `Cache-Control: no-store`, the page `no-cache`, and
the interface's files are kept (their names hold their content's hash). An item they cannot reach answers exactly
as one that does not exist (404, `notHere`); without a session every route but signing in answers 401.
