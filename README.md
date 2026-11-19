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
  folder, holds the places, the folder tree, the roles given on shared folders and (as later tickets add them) other
  access, links and guests. The storage wins for which files exist; the index alone holds access.
- Members do not sign in to the Drive. A reverse proxy in front of it signs them in and adds headers saying who they
  are (see below). Every route passes through one authorisation function, `internal/access`.

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
| `DRIVE_ADDRESS` | yes | The Drive's own address as people reach it, such as `https://files.example.com`. Changes are accepted only from pages at this address. |
| `DRIVE_PROJECT_NAME` | yes | Name of the project (the organisation) the Drive belongs to. |
| `DRIVE_HEADER_SECRET` | yes | Secret the reverse proxy adds as `Drive-Secret`. At least 32 characters. |
| `DRIVE_MEMBERS_FILE` | yes | Path of the member list file. |
| `DRIVE_DATA_DIR` | yes | Folder for the index. The container image sets it to `/data`. |
| `DRIVE_LISTEN` | no, `:8080` | Address the web server listens on. |

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
```

Files and folders keep their real names, up to 255 bytes, exactly as given; the interface sends names in Unicode's
composed form (NFC), so a name from Safari on macOS matches the same name from another browser. Making a folder,
renaming and moving are each one change on the storage (`MKCOL`, or `MOVE` with `Overwrite: F`), after a look that
the name is free, and nothing ever replaces what is there.

An upload is one `PUT` straight to the file's place, with its `Content-Length` and `If-None-Match: *`: the storage
writes the whole file or nothing (Apache's `mod_dav`, which a Storage Box runs, writes to a hidden temporary file and
renames it at the end). When the name is taken, the interface asks first: **replace** moves the file that is there
into the trash and then uploads (it is moved back if the upload fails), and the file keeps its place, its id and so,
later, its sharing; **keep both** stores the upload as `Name (2).ext`, `Name (3).ext` and so on. A file never replaces
a folder.

**The trash.** Deleting moves the item, with everything in it, into the place's trash on the storage; restoring
moves it back to the folder it came from (or the place's top folder when that folder is gone), adding ` (2)` to its
name if the name is taken there. Items keep their ids throughout. The Drive removes an item for good 30 days after it
was deleted: when it starts, every hour, and before showing the Trash view. Entries in a trash that the index does not
know (left by a change the Drive did not finish) are removed once the day in their names is 30 days past.

**Space used** is the Drive's own sum of the sizes of the files its index knows, in every place, the trash included,
shown in the sidebar as "about" against `DRIVE_CAPACITY` (or alone, without it). Files put on the storage directly
count once the Drive has listed their folder.

Uploads in progress and the index copy get hidden folders of their own beside `trash/`, with their tickets.

### Shared folders and who may do what

A shared folder is a top-level place owned by the project. Any member can make one; its maker becomes its first
manager, as a role given to them like any other, and nothing else about it depends on their account: it stays, with
its files, when they leave. Everyone holding the admin role on the project (in the member list) is a manager of every
shared folder, for as long as they hold it. A member with no role on a shared folder does not see it at all: it is
not in their places, and every address that names it answers exactly as for a place that does not exist.

`internal/access` decides everything, from the person, the action, the kind of place and the role the index says
they were given there. Roles go up a ladder, each doing what the one before it does:

| | Viewer | Editor | Manager |
|---|---|---|---|
| List, download, see who has access | yes | yes | yes |
| Upload (and replace, or keep both), make folders, rename, move, delete | | yes | yes |
| See the trash, restore from it | | yes | yes |
| Make others managers, take a role away, delete the shared folder | | | yes |

Viewer and editor are in the rules already; they are given by sharing, a later ticket. In "My files" its owner may
list, download, upload, make folders, rename, move, delete and restore, and nobody else anything, project admins
included. Two rules hold the table together, and a test
checks them for every person and place: whoever may upload may delete (an upload can replace, which puts a file in
the trash), and whoever may delete may see the trash and restore ("restore anything you could have deleted").

People see a shared folder's name. Its top folder on the storage has that name, with ` (2)` and so on added when
another shared folder, one in the trash included, holds that folder or something is already there: a new shared folder
never takes over files nobody gave it. So two shared folders can share a name. Making one is refused only when the
person can already see one of that name; refusing a name they cannot see would tell them it exists.

**A shared folder in the trash.** Deleting a shared folder moves its whole top folder into an entry of its own trash
folder, beside what was deleted from it, and hides it from everyone. Its items, its trash and its roles stay in the
index, and its folder stays held, so restoring always has its own folder to go back to. Its managers and the project
admins find it in the Trash view and can restore it; 30 days after it was deleted the Drive removes its trash folder,
and everything in it, for good. A shared folder always keeps someone in charge: taking a manager away is refused when
nobody in the member list would be left managing it (there is always someone while the project has an admin).

Each place has a lock in memory: every change inside it (and a listing, which makes a missing top folder again) holds
it shared, and moving the whole shared folder into the trash or out of it holds it alone, waiting up to ten seconds for
the changes under way. A change that starts after the shared folder went to the trash finds it gone.

## Running it yourself

The Drive runs without Superit, as one container against any WebDAV server that passes the storage check. What it
does today: a member signs in through the reverse proxy and works in their "My files": uploads and downloads files,
makes folders, renames and moves things, deletes them into a 30-day trash and restores them, and sees about how much
space is used. Members make shared folders, owned by the project, and work in them the same way; managers make other
members managers and delete shared folders into the trash, and project admins manage every one. Sharing items with
members, guests, public links and large or resumable transfers are not built yet.

It needs:

- **The image**, `ghcr.io/neonjelly-co/superit-drive:<version>`, for x86 (`linux/amd64`) and arm64 (`linux/arm64`).
  Each version has its own tag, and there is no `latest`: you choose the version, and change it to upgrade. No version
  has been published yet (see [Releases](#releases)); until one is, build the image from this repository with
  `docker build`.
- **A WebDAV server** for the files. Check it first (below).
- **A reverse proxy that signs people in** and, on every request it passes on, adds `Drive-Secret` and
  `Remote-User-Id` in place of any the browser sent (see [Signing in](#signing-in-headers-and-the-member-list)). The
  Drive listens on plain HTTP on port 8080: only the reverse proxy should reach it.
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

1. Caddy asks Authelia whether the person is signed in (`forward_auth`). If not, Authelia's answer sends the browser
   to the sign-in page at `auth.example.localhost`, which sets a session cookie for `example.localhost` and sends the
   browser back.
2. If so, Authelia names the person by their username, and Caddy passes the request to the Drive with that username
   as `Remote-User-Id` and the secret from `.env` as `Drive-Secret`, replacing any the browser sent.
3. The Drive finds the user id in `members/members.json`, so each Authelia username is a member's `userId` there.
   The list gives their email address, name and role.

The Drive's port is not published: from the host, and from any container without the secret, it cannot be used. A
request that reaches it without `Drive-Secret` is refused with 403.

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
make image        # the container image superit-drive:dev
```

**Tests.** `test/` holds the tests through the Drive's HTTP interface. Each one starts the real binary against a
throwaway Apache `mod_dav` container (the stand-in storage, `test/webdav/`) with a temporary data folder, and acts
only as a browser or an outsider would: requests with the proxy's headers, and WebDAV with the storage login to see
what is on the storage. Start from `test/harness` and an existing test. The stand-in storage also has folders that
refuse or ignore one thing each, such as `MOVE` or `Range`, for the storage check's tests (`test/webdav/storage.conf`).
Next to the code are unit tests where a rule needs a table: the access rules in `internal/access`, settings, the
member list, the names that may become paths, the index's ids, and a check that every route goes through the
authorisation function. The interface has no tests of its own; check it in a browser.

The harness builds the Drive with the build tag `testclock`, which is the only difference from a released Drive: in
it, `SUPERIT_DRIVE_TEST_CLOCK_SHIFT` (a Go duration such as `744h`) moves the Drive's clock forward, so a test can
restart a Drive (`Drive.Restart`) 31 days later and see the trash emptied. A Drive built without the tag reads no such
variable.

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
`/tmp`, running as user 65532. `make image` tags it `superit-drive:dev`; the [worked example](#a-worked-example)
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
internal/access/       the one authorisation function (Decide) and its table test
internal/server/       HTTP: routes.go lists every route; server.go's guard runs identify → own page (for changes) →
                       place and the role given there → access.Decide → item in that place, or in its trash
internal/drive/        the Drive's operations, joining the index and the storage: files, folders, the trash,
                       shared folders
internal/index/        the SQLite index; schema changes are numbered files in migrations/, run with foreign keys
                       off (SQLite's recipe for rebuilding a table) and checked with foreign_key_check before each
                       commits
internal/clock/        the Drive's clock, which test builds can move forward
internal/storage/      the WebDAV client
internal/storagecheck/ `superit-drive check-storage`: can a WebDAV server hold the Drive's files?
web/                   the interface (Vite, React, Tailwind with the Pilot light tokens); built into web/dist
test/                  tests through HTTP; harness/ runs the Drive; webdav/ is the stand-in storage
dev/                   settings and a member list for running locally
examples/compose/      the worked example: the Drive behind Caddy and Authelia
.github/workflows/     release.yml publishes a version's image when it is tagged
```

## Licence

Superit Drive is published by neonjelly studio under the GNU Affero General Public License, version 3 (AGPL-3.0).
The full text is in [LICENSE](LICENSE).

## Contributing

Code from outside neonjelly studio is not merged until contributor terms exist.
