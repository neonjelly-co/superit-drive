# Settings

Every setting is an environment variable; there is no settings page. This table is the full list. The Drive refuses
to start without the required ones and names every one that is missing. (`internal/settings` holds the same list, and
a test keeps the two equal.)

| Variable | Required | Meaning |
|---|---|---|
| `DRIVE_STORAGE_URL` | yes | Address of the WebDAV server that holds the files (http or https). Over http the login and every file travel unencrypted: the Drive warns as it starts, unless the host is this computer or a name without a dot (another container beside it). |
| `DRIVE_STORAGE_USER` | yes | Login name for the WebDAV server. |
| `DRIVE_STORAGE_PASSWORD` | yes | Password for the WebDAV server. |
| `DRIVE_STORAGE_FOLDER` | no, `/` | Folder on the WebDAV server that the Drive keeps everything in. Made if missing. |
| `DRIVE_CAPACITY` | no | Size of the storage, in bytes or with a unit (`500GB`, `1TB`, `1TiB`). Empty when not known. |
| `DRIVE_ADDRESS` | yes | The Drive's own address as people reach it, such as `https://files.example.com`. Changes are accepted only from pages at this address, and a public link's address starts with it. |
| `DRIVE_PROJECT_NAME` | yes | Name of the project (the organisation) the Drive belongs to. |
| `DRIVE_HEADER_SECRET` | yes | Secret the reverse proxy adds as `Drive-Secret`. At least 32 characters, and your own (`openssl rand -hex 32`): whoever knows it and reaches the Drive's port is whoever they say they are. The Drive does not start with the secret the worked example once came with. It takes the repository's development secret only when `DRIVE_LISTEN` is on this computer alone (such as `127.0.0.1:8080`), and warns loudly when it runs with it. The key that keeps guests' codes (see [Guests](public-links-and-guests.md#guests)) is made from it, so changing it ends the codes on their way (not the sessions). |
| `DRIVE_MEMBERS_FILE` | yes | Path of the member list file. |
| `DRIVE_DATA_DIR` | yes | Folder for the index, and its copy in `backup/` (see [Backing up the Drive](storage.md#backing-up-the-drive)). The container image sets it to `/data`. |
| `DRIVE_LISTEN` | no, `:8080` | Address the web server listens on. |
| `DRIVE_SMTP_HOST` | no | Mail server the Drive sends its own mail through (see [Mail](operations.md#mail)), such as `smtp.example.com`. Empty: the Drive sends no mail; sharing with members and public links work without it, and inviting guests is unavailable. |
| `DRIVE_SMTP_PORT` | no, `587` | Port of the mail server. |
| `DRIVE_SMTP_TLS` | no | How the connection to the mail server is encrypted: `starttls`, `implicit` (TLS from the first byte) or `none`. Empty: `implicit` on port 465, `starttls` on any other. `none` only without a login. |
| `DRIVE_SMTP_USER` | no | Login name for the mail server. Empty when it needs none. |
| `DRIVE_SMTP_PASSWORD` | no | Password for the mail server, with `DRIVE_SMTP_USER`. |
| `DRIVE_MAIL_FROM` | no | Address the Drive's mail comes from, such as `drive@files.example.com` or `Drive <drive@files.example.com>`. Needed with `DRIVE_SMTP_HOST`. |
| `DRIVE_ROLLBACK_AT` | no | When the storage was last put back to a snapshot, as a time with its offset (RFC 3339), such as `2026-11-20T09:41:00Z`. It tells one rollback from another. Set with the next two, or none of the three; see [After a rollback](storage.md#after-a-rollback). |
| `DRIVE_ROLLBACK_TO` | no | When the snapshot it was put back to was taken (RFC 3339), before `DRIVE_ROLLBACK_AT`. |
| `DRIVE_ROLLBACK_BY` | no | Who put it back, as members should read it: their name. Shown as plain text; control characters are taken out, and it is cut to 100 characters. |
