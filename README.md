# Superit Drive

Superit Drive ("the Drive") is where a project's people keep, open and share their files, in the browser. Each member
has "My files", private to them; files live on WebDAV storage (a Hetzner Storage Box bought from Superit, or the
customer's own WebDAV server). It runs as one small container on the customer's own server.

Version 1 is being built. The spec is [neonjelly-co/superit#38](https://github.com/neonjelly-co/superit/issues/38);
the build tickets are this repository's issues. Words follow the `GLOSSARY.md` of neonjelly-co/superit (member,
guest, My files, shared folder, Drive storage, trash).

## How it works

- One Go binary. The interface (TypeScript and React, in `web/`) is compiled into it as static files.
- Files live on the storage as a readable folder tree with real names. The index, one SQLite file in the data
  folder, holds the folder tree and (as later tickets add them) access, links and guests. The storage wins for which
  files exist; the index alone holds access.
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

Every setting is an environment variable; there is no settings page. The Drive refuses to start without the
required ones and names every one that is missing. (`internal/settings` holds the same list.)

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
    reka@example.com/      one top folder per member's "My files", named after their email address
    bence@example.com/     when they first open the Drive; the index ties it to their user id
        Tax notes.pdf      their files, with their real names
```

Files keep their real names, up to 255 bytes. An upload is one `PUT` straight to the file's place, with its
`Content-Length` and `If-None-Match: *`: the storage writes the whole file or nothing (Apache's `mod_dav`, which a
Storage Box runs, writes to a hidden temporary file and renames it at the end), and an upload never replaces anything
that is there. Shared folders, and hidden folders for the trash, uploads in progress and the index copy, come with
their tickets.

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
what is on the storage. Start from `test/harness` and an existing test. Next to the code are unit tests where a rule
needs a table: the access rules in `internal/access`, settings, the member list, and a check that every route goes
through the authorisation function. The interface has no tests of its own; check it in a browser.

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
`/tmp`, running as user 65532. It starts with the environment variables above and a member list file:

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
                       place → access.Decide → item in that place
internal/drive/        the Drive's operations, joining the index and the storage
internal/index/        the SQLite index; schema changes are numbered files in migrations/
internal/storage/      the WebDAV client
internal/storagecheck/ `superit-drive check-storage`: can a WebDAV server hold the Drive's files?
web/                   the interface (Vite, React, Tailwind with the Pilot light tokens); built into web/dist
test/                  tests through HTTP; harness/ runs the Drive; webdav/ is the stand-in storage
dev/                   settings and a member list for running locally
```

## Licence

Superit Drive is published by neonjelly studio under the GNU Affero General Public License, version 3 (AGPL-3.0).
The full text is in [LICENSE](LICENSE).
