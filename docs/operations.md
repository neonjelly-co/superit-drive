# Running the Drive

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
who may share a file or folder make a public link to it, which anyone opens without signing in (or, on a folder, an
upload-only link, through which anyone sends files in and sees nothing of what is there, up to 10 GB until it is
reset), or invite someone
outside the project by email address as a guest (with the mail settings), who signs in with a mailed link; project
admins see everything shared outside the project and the space each person and shared folder uses, and switch sharing
outside the project off for the whole project (managers for one shared folder); when someone leaves the member list,
their My files is held, unopened, until a project admin hands it to another member or deletes it. Large files
upload in parts that resume after a broken connection, downloads resume and seek, and folders upload
whole and download as zips. Files changed on the WebDAV server by other programs show in the Drive as folders are
opened, and through a slow walk now and then (see [Keeping in step with the storage](storage.md#keeping-in-step-with-the-storage)).
It works in a phone's browser too, and speaks English and Hungarian (see [On a phone](using-the-drive.md#on-a-phone) and
[Languages](using-the-drive.md#languages)).

It needs:

- **The image**, `ghcr.io/neonjelly-co/superit-drive:<version>`, for x86 (`linux/amd64`) and arm64 (`linux/arm64`).
  Each version has its own tag, and there is no `latest`: you choose the version, and change it to upgrade. No version
  has been published yet (see [Releases](development.md#releases)); until one is, build the image from this repository with
  `docker build`.
- **A WebDAV server** for the files. Check it first (below).
- **A reverse proxy that signs people in** and, on every request it passes on, adds `Drive-Secret` and
  `Remote-User-Id` in place of any the browser sent (see [Signing in](signing-in.md#signing-in-headers-and-the-member-list)); except
  under the public prefix `/s/`, which it opens without sign-in and passes on without those headers or its sign-in
  cookie. The Drive listens on plain HTTP on port 8080: only the reverse proxy should reach it. For large files, the proxy should
  stream request and response bodies rather than hold them, pass `Range` and `If-Range` through, and let a download
  wait up to ten minutes for its answer when the Drive is busy.
- **The member list file**, in a folder of its own mounted into the container.
- **The data folder**, `/data` in the container, kept between restarts. It holds the index, which alone knows who
  has access to what, and a copy of it, `backup/index.sqlite`, which is what to back up (see [Backing up the
  Drive](storage.md#backing-up-the-drive)). The Drive also keeps a copy on the WebDAV server, and starts from it with an empty data
  folder (see [The copy of the index](storage.md#the-copy-of-the-index)).

The settings are in the [settings table](settings.md).

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

[`examples/compose/`](../examples/compose/) runs the Drive with Docker Compose behind [Caddy](https://caddyserver.com)
(the reverse proxy, which also gets HTTPS certificates) and [Authelia](https://www.authelia.com) (the sign-in: a
sign-in page and a list of people with passwords). It runs as it is on your own computer, with a stand-in WebDAV
server and example people:

```sh
cd examples/compose
cp example.env .env
# Set DRIVE_HEADER_SECRET in .env to a secret of your own (openssl rand -hex 32): the Drive does not start without one.
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
   as `Remote-User-Id` and the secret from `.env` as `Drive-Secret`. What the browser itself sent under those names is
   removed first (`request_header -Remote-User-Id`, inside a `route` block, which keeps the order written), before
   Authelia is asked: `copy_headers` alone replaces the user
   id only when Authelia names someone, so a rule that lets a request through without sign-in (`policy: bypass`) would
   pass a browser's own `Remote-User-Id` on, with the secret added. Keep those lines in a proxy set-up of your own.
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

## Mail

The Drive sends three kinds of mail: "shared with you", to a member something was just shared with by name; an
invitation, to a guest invited to something; and the links guests sign in with (see [Guests](public-links-and-guests.md#guests)). With
`DRIVE_SMTP_HOST` empty it sends none and attempts none: sharing with members and public links work the same, and
inviting guests is unavailable. The line the Drive logs at start says where mail goes
(`mail="smtp.example.com:587 (starttls)"`) or `mail=off`. Whether mail works is what the last mail sent, or the last
check (connect, encrypt, sign in, and nothing sent), came to: trusted for ten minutes when it worked, one when it did
not; inviting a guest checks again when that has run out.

- **When:** a new share to a named member, on a file, a folder or a shared folder as a whole. Not for everyone in the
  project, not for a role changed or given again, not for access taken away, and never to the person sharing (a
  project admin taking a role on a shared folder).
- **What it says,** in short plain text, in the language of the page it was shared from (see [Languages](using-the-drive.md#languages)): who
  shared what, in which project, the role, and a link at
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
  the share is answered at once whatever happens to it. (A guest's sign-in link is sent by the guests' own sender in the
  background, also never in the way of the page that asked.) A mail that fails is logged with what it was about (the
  recipient's user id, the place and item ids) and why, never with its text or the recipient's address, and is not
  tried again; so is one dropped because the queue is full. Mail still queued when the Drive stops is not sent.
- **Names from people** (display names, file names) have line breaks and other control characters taken out and are
  encoded (RFC 2047) in the subject, the recipient must be one plain address, and the headers are only those the Drive
  writes: no name can add a header or a recipient.

## Large transfers and the memory figure

The Drive is counted at 64 MiB of memory (a hard 96 MiB) on a customer's server, and the kernel's network buffers
for the transfers in flight are charged to its container too. A transfer is a request that moves a file's bytes: a
download (of a file or a zip, or a file opened in the browser), an upload in one request (a file sent through an
upload-only link too), or one part of a resumable upload. Three rules hold the figure (`internal/transfers`):

- **At most 32 transfers are in flight, of which outsiders (visitors with a public link, and guests) hold at most 8.**
  Members can always use the other 24, and all 32 while no outsider needs one; when a slot comes back, a member waiting
  gets it before an outsider. Outsiders wait in a line of their own, at most 64 of them, and at most 64 outsiders' turns
  keep a place in it (an upload's, below); more are told at once that the Drive is busy. A
  download waits its turn in line (holding only its idle connection),
  for up to ten minutes, then gets 503; when its turn comes, it is decided again (below). An upload is answered at once with 503, `{"busy": true}` and `Retry-After`,
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

Measured with `make test-memory` (see [Develop](development.md)): the image in a container with `--memory=64m --memory-swap=64m`, against the
stand-in storage, idles at about 4.5 MiB; with 40 downloads (half of them read at 8 MiB/s) and 10 resumable uploads
of 256 MiB each at once, it peaked at 46 to 49.6 MiB over three runs, of which about 13 to 16 MiB was the Drive's own
memory and 29 to 31 MiB network buffers, and never reached the limit (Docker Desktop on an Apple Silicon Mac, arm64).
Then 12 strangers sending files of 256 MiB at once through an [upload-only link](public-links-and-guests.md#upload-only-links), each in one
request (8 in flight, the rest waiting their turn), while members downloaded 10 (half of them slowly), reached 29.5 MiB,
of which 13.7 MiB the Drive's own memory and 9.9 MiB network buffers.
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
compression). Folders are in it too, empty ones included. A backslash in a name becomes `_` in the zip, and a number is added when that is another entry's name in the same folder (`a\b.txt` beside `a_b.txt` is `a_b (2).txt`): no two entries have the same name. A zip holds
at most 10,000 files and folders (each costs a little memory until the zip's end); a larger folder is refused with
400, and is downloaded a folder inside it at a time. It has no length, and a zip cut short ends the connection, so the
browser shows the download as failed.

**Decided again.** A download, a file opened or a zip that waited for its turn is decided again when its turn comes,
on everything read again: through a link removed meanwhile, by a guest whose invitation was taken away or ended, where
sharing outside the project was switched off, or by a member whose access was taken away, it is refused as it would
be if asked for then, and sends nothing. A zip is decided again as it goes on too: before each file and folder it
adds, and every 15 seconds while it sends one large file. Once the person may no longer download the folder, the zip
is cut short there, and nothing put into the folder after that is ever in it; a reader who takes a zip slowly keeps
it no longer than that. (A file's own download, once its bytes are on their way, goes on to its end.)

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
       429 {"error"}                                     the person has 50 uploads in progress: no more until one ends
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
