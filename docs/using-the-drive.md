# Using the Drive

## Opening files in the browser

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

## Search

```
GET    /api/search?q=<part of a name>
       200 {"items": [{"id", "name", "kind", "size", "changed", "opens", "can", "seenBy",   the item, as a listing gives it
                       "place", "placeKind", "placeName", "owner", "role",
                       "path", "fromTop"}],                   where it is, as far as the person can open the folders above it
            "more": false}
       400 nothing to search for, or more than 255 bytes
       503 too many searches are under way (Retry-After), or this one took too long and was ended
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
  [Keeping in step with the storage](storage.md#keeping-in-step-with-the-storage)); what went from it is not found from the first
  look that missed it.
- **A few at once, each for a bounded time.** A search reads every name in its scope, on one of the index's four
  connections, which everything else the Drive does needs too (measured at the index before there was a bound: eight
  searches at once over 50,000 items made an unrelated lookup take up to 2.8 seconds, from 0.3 ms). So two searches
  run at once (`index.SearchesAtOnce`), whoever asked; the rest wait their turn, for at most 5 seconds, then are told
  the Drive is busy searching (503, `Retry-After`); and one that has run for 5 seconds is ended in the middle of its
  reading and says so (503), its connection free again. The bound is for everyone together: one member's many searches
  make another's wait, but never longer than that.

## Selecting several

In a folder's list each row has a tick box and the header a "select all"; a click elsewhere on a row selects it alone.
With two or more selected, the header becomes a bar with Move, Download as zip (`…/zip?item=…`), Delete and Clear.
There is no bulk share, and no new route: Move and Delete send one request per item, one after another, each decided
as it would be alone. Deleting several offers Undo for all of them; a move into a shared folder shows the warning, with
what each item keeps; and when some items are not moved, deleted or restored, a dialog names each with the Drive's
reason, and says what happened to the rest.

## On a phone

Below 48rem (768 px), at a phone's width, the same page folds (`useNarrow` in `web/src/narrow.ts`, and Tailwind's
`max-md:`):

- The sidebar is behind a menu button at the top: a drawer from the left, which closes when the person goes somewhere,
  chooses what to upload, taps outside it or presses Escape. The language switch is at its bottom.
- Each row is the name with one line under it: who can see it and the size ("From a guest" or "Through a link" first).
  Tapping the name opens it, as a click does on a wide screen; the ⋯ button shows its details, as a sheet that slides up
  from the bottom. The trash, search and "Shared with me" fold the same way.
- Tick boxes show after "Select", or a press and hold on a row; a tap then ticks a row, and Move, Download as zip,
  Delete and Done are in a bar at the bottom.
- Upload opens the phone's own picker, for files and photos. Only dragging files in is missing, and "Upload folder"
  where the browser cannot choose a folder (phones' and tablets' pickers choose files only, so it is not offered there).
- Every target a finger taps is at least 44 px. Dialogs sit at the top of the screen and scroll within it, so the
  on-screen keyboard does not cover their field or buttons (the pages also ask the browser to make room for it,
  `interactive-widget=resizes-content`). Nothing is reachable only by hover or by dragging: what a tooltip says on a wide
  screen is in the details, or shown.
- The outsiders' pages are one plain column on every screen: a public link's, an upload-only link's (which says "Choose
  files" on a touch screen rather than "Drop files here"), and the guests'.

## Languages

The Drive speaks English and Hungarian, to members, guests and visitors alike, and writes its mail in them. Nothing
about anyone's language is kept on the server: no setting, no cookie, nothing in the index.

- **Which language a page is in.** The one the person chose with the switch (members at the bottom of the sidebar,
  guests at the top of their page), kept in that browser (`localStorage`); otherwise the first of the browser's
  languages that the Drive speaks; English when it names neither. Switching loads the page again in the other language.
  A page is never in two: it loads its words before it shows anything.
- **The Drive's own answers** (the `"error"` of every refusal, `guests.why` in `GET /api/me`, the guests' "a sign-in link is on
  its way") are in the language of the request: its `lang` query parameter when that is `en` or `hu`, otherwise the one
  of the two its `Accept-Language` prefers (by weight, then order; English when it names neither), with
  `Content-Language` saying which. The interface sends `Accept-Language` with its page's language on every call, and adds
  `lang` to the addresses it opens in a tab or downloads, so an answer always matches the page.
- **Visitors**: a view link's pages run no script, so a choice travels in the address. The switch at the bottom of a
  page links to `?lang=hu` (or `en`), and every address on that page then carries it, so the folders inside, the files
  and the error pages stay in that language; without a choice a page follows the browser. No cookie is set or read under
  `/s/`. An upload-only link's page has its one script, which holds no words: what it says is in the page
  (`data-words`), so one script, and one hash in the policy, serves both languages. It sends each file with the page's
  `lang`, so a refusal is in the page's language too.
- **Mail** is in the language of the page whose act sends it: "shared with you" in the language of the member who
  shared, an invitation in the inviting member's, a guest's sign-in link in the language of the guests' page that asked for it.
  The Drive knows no other language of the person it mails.
- What only the people running the Drive read stays in English: the log, the settings' errors, and the storage check's
  output (which the daemon reads).

**Where the words are.** The server's are `text.Message`s (`internal/text`), each made once, when the program starts,
with its English and Hungarian side by side: `internal/server/words.go`, and the names' and mail's own in
`internal/drive/files.go` and `internal/mail/mail.go`. `refuse` takes only a `Message`, and `refuseWith` is the one
place that writes `"error"`. The interface's are in `web/src/i18n`: `en.ts` defines the keys, `hu.ts` is typed against
it (a key missing, or one too many, does not build), and each page downloads only its own language's catalogue. Tests
fail on a Hungarian text missing, left in English, or saying other values than the English
(`TestEveryTextAPersonReadsIsInEnglishAndHungarian`, `catalogue.test.ts`), on a sentence for a person written around the
languages in the server or the interface (`TestNoTextForAPersonIsWrittenAroundTheLanguages`, and the components' scan in
`catalogue.test.ts`), and on words in the visitors' page template itself; `test/language_test.go` checks the answers,
pages and mail in Hungarian through HTTP.

**How the Hungarian is written.** As a Hungarian writes, in the platform's plain "te" form, with its terms: Drive-tárhely,
pillanatkép, kuka, tag, vendég, projektadmin. A name, an address or a number put into a sentence never takes a suffix or
an article, which change with the value's sounds: it follows a colon, sits in quotes („…”) or parentheses, or is the
sentence's subject; and a noun after a number stays singular (3 elem). Dates and sizes follow the language (2026. nov.
20., 1,2 MB).

| English | Hungarian |
|---|---|
| My files | Saját fájlok |
| shared folder | közös mappa |
| Shared with me | Velem megosztott |
| trash | kuka |
| member, guest, project admin | tag, vendég, projektadmin |
| viewer, editor, manager, owner | megtekintő, szerkesztő, kezelő, tulajdonos |
| public link, upload-only link | nyilvános link, feltöltési link |
| Drive storage, snapshot | Drive-tárhely, pillanatkép |
| sharing outside the project | projekten kívüli megosztás |
| restore (from the trash) | visszaállítás |
