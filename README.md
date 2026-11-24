# Superit Drive

Superit Drive ("the Drive") is where a project's people keep, open and share their files, in the browser. Each member
has "My files", private to them, and the project has shared folders, owned by the project and not by a person; files
live on WebDAV storage (a Hetzner Storage Box bought from Superit, or the customer's own WebDAV server). It runs as one
small container on the customer's own server.

Version 1 is being built. The spec is [neonjelly-co/superit#38](https://github.com/neonjelly-co/superit/issues/38);
the build tickets are this repository's issues. Words follow the `GLOSSARY.md` of neonjelly-co/superit (member,
guest, My files, shared folder, Drive storage, trash). The Drive also runs without Superit: see
[Running it yourself](docs/operations.md#running-it-yourself).

## How it works

- One Go binary. The interface (TypeScript and React, in `web/`) is compiled into it as static files.
- Files live on the storage as a readable folder tree with real names. The index, one SQLite file in the data
  folder, holds the places, the folder tree, the access given on places and items and (as later tickets add them)
  links and guests. The storage wins for which files exist; the index alone holds access. Lists come from the index,
  and the Drive looks at the storage again when a folder is opened and in a slow walk now and then, so changes made on
  the storage directly reach the index (see
  [Keeping in step with the storage](docs/storage.md#keeping-in-step-with-the-storage)).
- The index is copied into the data folder, for backups, and onto the storage, so that a Drive on a new server starts
  with the sharing the lost one had (see [The copy of the index](docs/storage.md#the-copy-of-the-index)).
- Members do not sign in to the Drive. A reverse proxy in front of it signs them in and adds headers saying who they
  are (see [Signing in](docs/signing-in.md)). Everyone else comes in under one public prefix, `/s/`, which the reverse
  proxy opens without sign-in: visitors with a public link, who open and download what it is on or, through an
  upload-only link, send files into a folder and see nothing of it (see
  [Public links](docs/public-links-and-guests.md#public-links)), and guests, who sign in there with a link mailed to
  them (see [Guests](docs/public-links-and-guests.md#guests)). Every route passes through one authorisation function,
  `internal/access`.

## Documentation

The rest is in [`docs/`](docs/):

| Page | What is in it |
|---|---|
| [Signing in](docs/signing-in.md) | The headers the reverse proxy adds, the member list file, and the public prefix `/s/`. |
| [Settings](docs/settings.md) | Every environment variable the Drive reads. |
| [Storage and the index](docs/storage.md) | The layout on the storage, keeping in step with it, rollbacks, the copy of the index, starting from a copy, backups, and an upgrade that failed. |
| [Sharing](docs/sharing.md) | Shared folders and who may do what, moving into a shared folder, sharing outside the project switched off, what a project admin oversees, and what happens when a member is removed. |
| [Public links and guests](docs/public-links-and-guests.md) | Public links, upload-only links, and guests invited by email address. |
| [Using the Drive](docs/using-the-drive.md) | Opening files in the browser, search, selecting several, the phone layout, and languages. |
| [Running the Drive](docs/operations.md) | Running it yourself, checking a WebDAV server, a worked example with Caddy and Authelia, mail, and large transfers and the memory figure. |
| [Develop](docs/development.md) | Building, the tests, running it locally, the image, the repository layout, and releases. |

## Running it yourself

The Drive runs without Superit, as one container against any WebDAV server that passes the storage check. It needs the
image, a WebDAV server, a reverse proxy that signs people in, a member list file and a data folder:
[Running it yourself](docs/operations.md#running-it-yourself) says what each must do, and
[`examples/compose/`](examples/compose/) is a set-up that runs as it is on your own computer (see
[A worked example](docs/operations.md#a-worked-example)).

## Develop

You need Go 1.27, Bun 1.3 and Docker. `make help` lists the targets, and [Develop](docs/development.md) has the rest.

```sh
make build        # interface (web/dist) and binary (bin/superit-drive)
make test         # every test (the interface's catalogue test with Bun, then Go's); needs Docker running
make test-short   # only the tests that need no Docker
```

## Licence

Superit Drive is published by neonjelly studio under the GNU Affero General Public License, version 3 (AGPL-3.0).
The full text is in [LICENSE](LICENSE).

## Contributing

Code from outside neonjelly studio is not merged until contributor terms exist.
