# Signing in: headers and the member list

A signed-in request must carry two headers, added by the reverse proxy (which removes any copies a browser sent):

| Header | Value |
|---|---|
| `Drive-Secret` | The value of `DRIVE_HEADER_SECRET`. Without it, or with any other value, the request is refused, whatever else it carries. |
| `Remote-User-Id` | The member's stable user id. |

Each must come once: a request with two `Drive-Secret` headers, or two `Remote-User-Id` headers, is refused (403). A
proxy set up to add the user id, instead of replacing what the browser sent, would put the browser's own value first.

The person must also be in the member list file (`DRIVE_MEMBERS_FILE`), which the Drive reads and never writes. It
re-reads the file within a second of it changing; replace it by writing a new file and renaming it over the old one.
A version it cannot read (not JSON, a role it does not know, one address twice, the file gone) is refused whole, and
the list before it stays: until a version reads again, nobody removed since loses their access. The log says so
(`level=ERROR`, `the member list could not be re-read`) when it starts, and again every ten minutes while it lasts.
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

- send `/s/*` to the Drive without sign-in, removing `Drive-Secret`, `Remote-User-Id` (and the other `Remote-*` headers)
  and its own sign-in cookie, and nothing else: every method (guests upload, rename, move and delete there with `POST`,
  `PUT` and `DELETE`; visitors send files through an upload-only link with `POST`), the request body streamed as it
  arrives (a file sent through a link is one request, up to 10 GB), `Origin`, `Range`, `If-Range`, and the guests'
  cookie (`__Host-drive-guest`, or `drive-guest` when `DRIVE_ADDRESS` is plain HTTP) both ways, as for any other
  request;
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
logs both values. Anyone can send such a request under `/s/guest`, so the line is written at most once a minute, with
the address and the `Origin` cut to 200 bytes and a count of the refusals it left out (`leftOut`; see [What anyone can
ask for is bounded](public-links-and-guests.md#what-anyone-can-ask-for-is-bounded)). The reverse proxy must pass `Origin` through unchanged, and
`DRIVE_ADDRESS` must be the address in the browser's address bar. The one change that needs no `Origin` is a file sent through an upload-only link: it carries no
cookie and says nothing about who sends it, so another site's page can do through it only what anyone with the
address can (and the link's own page sends `Origin: null`, under its `no-referrer` policy).
