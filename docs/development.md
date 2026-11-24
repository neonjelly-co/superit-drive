# Develop

You need Go 1.27, Bun 1.3 and Docker. `make help` lists the targets.

```sh
make build        # interface (web/dist) and binary (bin/superit-drive)
make test         # every test (the interface's catalogue test with Bun, then Go's); needs Docker running
make test-short   # only the tests that need no Docker
make test-memory  # the image in a 64 MiB container under 40 downloads and 10 uploads at once (slow)
make image        # the container image superit-drive:dev
make notices      # THIRD-PARTY-NOTICES.txt again, after a dependency changed
```

**Tests.** `test/` holds the tests through the Drive's HTTP interface. Each one starts the real binary against a
throwaway Apache `mod_dav` container (the stand-in storage, `test/webdav/`) with a temporary data folder, and acts
only as a browser or an outsider would: requests with the proxy's headers, and WebDAV with the storage login to see
what is on the storage. Start from `test/harness` and an existing test. Mail goes to a throwaway SMTP server in the
test itself (`internal/mail/mailtest`). The stand-in storage also has folders that
refuse or ignore one thing each, such as `MOVE` or `Range`, for the storage check's tests (`test/webdav/storage.conf`).
Next to the code are unit tests where a rule needs a table: the access rules in `internal/access`, settings, the
member list, the names that may become paths, the index's ids, and a check that every route goes through the
authorisation function. The interface's one test (`web/src/i18n/catalogue.test.ts`, `bun test` in `web/`) checks its
words in both languages (see [Languages](using-the-drive.md#languages)); otherwise check the interface in a browser, at phone width too.

**The memory test** (`test/memory`, build tag `memory`, `make test-memory`) builds the image from the Dockerfile and
runs it with `--memory=64m --memory-swap=64m` against the stand-in storage. It uploads a 256 MiB file with a part cut
off and resumed, then sends 40 downloads (half of them read slowly) and 10 resumable uploads of 256 MiB each through it
at once, then two zips of a folder holding 256 MiB, then 12 strangers sending files of 256 MiB through an upload-only
link (each in one request, at most 8 in flight) while members download 10 (half of them slowly), checks every byte and
that the container was not killed, and reports the container's memory from its cgroup (the peak, and the highest during
each part, split into the Drive's own memory, network buffers, page cache and kernel). A second test there puts 70,000
files on the stand-in storage (one folder of 10,000, 200 folders of 200, and one folder of 20,000, more than a look
reads), lets the released Drive walk them all, then has four people open the folder of 10,000 at once, and reports the
same. Both take about eight minutes and leave only the images `superit-drive:memory-test`
and `superit-drive-test-webdav`. A third puts 110,000 files on the storage (11 folders of 10,000), lets the Drive walk
them all, then, while 16 downloads (half of them read slowly) and four people opening a folder of 10,000 go on over and
over, has a share make the Drive copy its index into its data folder and onto the storage, and reports the same; it
takes about three minutes.

The harness builds the Drive with the build tag `testclock`, which is the only difference from a released Drive: in
it, `SUPERIT_DRIVE_TEST_CLOCK_SHIFT` (a Go duration such as `744h`) moves the Drive's clock forward, so a test can
restart a Drive (`Drive.Restart`) 31 days later and see the trash emptied, or ten minutes later and see an item gone
from the storage dropped; `SUPERIT_DRIVE_TEST_STOP_AT` stops the Drive at once, as if it were killed, at a named point of
its work (`hand-over moved`, `hand-over trash removed`, `delete removed`), so a test sees it finish what it left when it
starts again; and `SUPERIT_DRIVE_TEST_WALK_EVERY` and `SUPERIT_DRIVE_TEST_WALK_PAUSE` (Go durations) make the
walk through the storage start that soon, run that often and rest that long after each folder; and
`SUPERIT_DRIVE_TEST_COPY_QUIET`, `SUPERIT_DRIVE_TEST_COPY_LATEST` and `SUPERIT_DRIVE_TEST_COPY_RETRY` (Go durations) make
the copy of the index follow access changes that soon (see [The copy of the index](storage.md#the-copy-of-the-index)); and
`SUPERIT_DRIVE_TEST_LINK_LIMIT` (bytes) and `SUPERIT_DRIVE_TEST_LINK_FILES` (files) are how much an upload-only link
takes in before it is full, so a test need not send 10 GB or a thousand files (see [Upload-only links](public-links-and-guests.md#upload-only-links)); and
`SUPERIT_DRIVE_TEST_SEARCH_MOST` (a Go duration) is how long a search may take before it is ended, so a test need not
fill the index (see [Search](using-the-drive.md#search)). A Drive
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

**The image.** `docker build` makes one image holding one static binary, CA certificates, the licences
(`/licenses`), and empty `/data` and
`/tmp`, both owned by user 65532, which it runs as (SQLite writes a temporary file in `/tmp` when one change or one sort
is too large to keep in memory). `make image` tags it `superit-drive:dev`; the [worked example](operations.md#a-worked-example)
expects the name `ghcr.io/neonjelly-co/superit-drive:<version>`. It starts with the [settings](settings.md) and a
member list file:

```sh
docker run -p 127.0.0.1:8080:8080 -v /path/to/members:/members:ro \
  -e DRIVE_MEMBERS_FILE=/members/members.json -e DRIVE_STORAGE_URL=… (and the other settings) superit-drive:dev
```

Mount the folder that holds the member list, not the file itself: a file mount does not follow a replacement by
rename.

## Repository layout

```
cmd/superit-drive/     main: runs the Drive, the storage check (`check-storage`), or prints the licences (`licenses`)
licenses.go            the Drive's licence (LICENSE) and THIRD-PARTY-NOTICES.txt, built into the binary
internal/app/          wires everything together and serves until stopped
internal/settings/     every environment variable
internal/members/      the member list file, re-read when it changes
internal/text/         every text the server writes for a person, in English and Hungarian, and a request's language
internal/mail/         the Drive's own mail over SMTP, sent in the background; mailtest/ is a throwaway SMTP server
                       for tests
internal/access/       the one authorisation function (Decide), the role at an item (RoleAt), what Shared with me lists
                       and what a search finds (SharedWithMe, Finds), whether sharing outside the project switched
                       off is all that is in the way (SwitchedOff), and their table tests
internal/server/       HTTP: routes.go lists every route; server.go's guard runs identify (under /s/, the visitor's
                       link; under /s/guest, the guest's session) → own page (for changes; through a link, only
                       reading) → place → item in it, or in its trash, or the person's upload in progress into it →
                       the access given there → access.Decide; transfers.go has downloads, zips and uploads,
                       again.go a transfer decided again (when its turn comes, as a zip goes on, once an upload is whole),
                       opening.go what opens in the browser and how, search.go the search by name, links.go making,
                       changing and removing public links, visitors.go the public prefix and the visitor's pages,
                       sendin.go an upload-only link's page and the files sent through it,
                       invitations.go inviting guests, guests.go the guests' sign-in, pages and codes,
                       oversight.go sharing outside the project switched off and what a project admin oversees,
                       held.go the held My files of people who left, handed over or deleted
internal/drive/        the Drive's operations, joining the index and the storage: files, folders, the trash,
                       shared folders, the access given on them, sendin.go files sent through an upload-only link
                       within its limit; reread.go and walk.go keep the index in step with
                       the storage, rollback.go takes in the storage put back to a snapshot, held.go hands
                       over or deletes the held My files of people who left, copies.go writes the copies of the
                       index, and adopt.go makes the index from one when the data folder holds none
internal/index/        the SQLite index (search.go: names as a search compares them, and the walk down a scope;
                       copies.go: copies of it, made, cleared and checked);
                       schema changes are numbered files in migrations/, run with foreign keys
                       off (SQLite's recipe for rebuilding a table) and checked with foreign_key_check before each
                       commits
internal/clock/        the Drive's clock, which test builds can move forward
internal/seldom/       the log's lines that anyone can cause: written at most once in a while, and cut short
internal/transfers/    what holds the memory figure: capped network buffers, and the 32 transfer slots
internal/storage/      the WebDAV client
internal/storagecheck/ `superit-drive check-storage`: can a WebDAV server hold the Drive's files?
web/                   the interface (Vite, React, Tailwind with the Pilot light tokens): the members' page
                       (index.html) and the guests' (guest.html); built into web/dist; src/i18n holds its words in
                       each language
test/                  tests through HTTP; harness/ runs the Drive; webdav/ is the stand-in storage; memory/ is the
                       64 MiB test
dev/                   settings and a member list for running locally; notices/ writes THIRD-PARTY-NOTICES.txt
examples/compose/      the worked example: the Drive behind Caddy and Authelia
.github/workflows/     release.yml publishes a version's image when it is tagged
```

## Dependencies and their licences

The Drive is AGPL-3.0, and what is built into it must have a licence that allows that: MIT, BSD, ISC, Apache-2.0,
MPL-2.0 and the like for code, the SIL Open Font License for fonts. Nothing under a licence that forbids it (one that
is not free, or that bars commercial use) may be added. What only builds or tests the Drive and never goes out with
it (the interface's `devDependencies`, the images in the Dockerfile's first stages) does not count.

Those licences ask that their notices go with every copy, so `THIRD-PARTY-NOTICES.txt` holds them in full. `make
notices` writes it (`dev/notices`): it asks Go which modules are compiled into the binary for the published platforms,
follows the interface's `dependencies` through `web/node_modules`, and copies every licence, notice and patent file at
the top of each; a package with none stops it, to be read by a person. A test there fails while the file is not what
the dependencies give, so after a change to `go.mod` or `web/package.json`, run `make notices` and read the difference:
a new licence shows up in it. The file goes out three ways: built into the binary (`superit-drive licenses` prints it
after the Drive's own licence), in the image under `/licenses`, and next to the interface's files
(`assets/THIRD-PARTY-NOTICES.txt`, which every script names in its first line).

## Releases

Pushing a tag `v<major>.<minor>.<patch>`, such as `v1.2.0`, runs `.github/workflows/release.yml`: it runs every test,
builds the image for x86 and arm64 (the Dockerfile cross-compiles, so neither needs emulation) and publishes it as
`ghcr.io/neonjelly-co/superit-drive:1.2.0`, with the version built into the binary. Nothing else is published, never
a moving tag such as `latest`. A tag that is not such a version publishes nothing, and a version the registry already
has is refused, not replaced: the workflow publishes only when the registry itself answers that the version is not
there, and fails on any other answer (the registry down, a timeout, a login refused), to be run again. A published image does not reach Superit's customers by itself: that happens only when
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
