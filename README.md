# feedss

feedss is a small, self-hosted RSS reader for people who want a fast, plain feed list without a lot of ceremony. It supports OPML import/export, nested feed groups, unread counts, saved articles, SQLite full-text search, per-feed display modes, keyboard navigation, feed health reporting, multiple local users, manual refresh, release notifications, installable PWA support, and a local SQLite database.

See the [changelog](CHANGELOG.md) for release history. Contributions are covered by the [contributing guide](CONTRIBUTING.md), [Code of Conduct](CODE_OF_CONDUCT.md), and [security policy](SECURITY.md). feedss is licensed under the [GNU General Public License v3.0](LICENSE).

It is designed for a trusted home-lab, desktop, LAN, or Tailscale-style environment rather than public multi-tenant hosting.

## First Start

Start feedss and open http://localhost:4317. When the database has no users, the login page becomes a one-time setup screen. Enter the username and password you want to use; that first account becomes the administrator. feedss does not create or display default credentials.

The administrator can open **Settings → User accounts** to create additional users with temporary passwords. On their first login, those users must replace the temporary password before they can access the reader. New and changed passwords are stored as bcrypt hashes; passwords from older installations are upgraded to bcrypt after a successful login.

Any signed-in user can open **Account** to change their username or password. Account changes require the current password.

Each user has separate groups, feeds, articles, saved state, reading history, and unread state.

When the same account is open in multiple browsers or installed apps, read state,
saved stars, subscriptions, and unread counts synchronize automatically. Active
clients reconcile within a few seconds without replacing the article list being
read, and a backgrounded client reconciles as soon as it returns to the foreground.
SQLite maintains the per-user revision counters, so synchronization also works when
multiple feedss processes share the same database.

Sessions use opaque random browser tokens. Only token hashes and expirations are stored in SQLite, and logging out revokes the active session.

## Run From Source

```bash
go run .
```

By default, feedss listens on port `4317` and stores data at `data/feedss.db`.

## Run a Release Binary

Download the archive for your platform from the GitHub release, unpack it, and run the binary.

Windows PowerShell:

```powershell
.\feedss.exe
```

Linux/macOS:

```bash
chmod +x ./feedss
./feedss
```

Optional environment variables:

```bash
APP_PORT=4317
APP_DB_PATH=./data/feedss.db
APP_DISABLE_AUTO_REFRESH=false
```

## Run With Docker Compose

```bash
docker compose up --build
```

The included compose file stores the SQLite database in `./data/feedss.db`.

To run the published image from your own compose file:

```yaml
services:
  feedss:
    image: ghcr.io/goosepod/feedss:v1.0.0
    container_name: feedss
    ports:
      - "4317:4317"
    environment:
      APP_DB_PATH: /data/feedss.db
      APP_PORT: 4317
    volumes:
      - feedss-data:/data
    restart: unless-stopped

volumes:
  feedss-data:
```

## Run the Published Docker Image

Release images are published to GitHub Container Registry:

```bash
docker run --rm \
  -p 4317:4317 \
  -e APP_DB_PATH=/data/feedss.db \
  -v feedss-data:/data \
  ghcr.io/goosepod/feedss:v1.0.0
```

Use `ghcr.io/goosepod/feedss:latest` for the newest tagged release.

## Keyboard Shortcuts

- `j` / `k`: next or previous article
- `v`: open the selected article
- `c`: open comments when available
- `r`: refresh the current feed or group from the top and hide read articles
- `s`: save or unsave the selected article
- `/`: show search and select the current query
- `+`: add a feed
- `Shift+A`: mark the current feed or group as read
- `?`: show shortcuts

## Adding Feeds

The Add feed form accepts either a direct RSS/Atom URL or an ordinary website URL.
When a website advertises one feed, feedss uses it automatically. When it advertises
multiple feeds, choose the one you want before adding the subscription.

The Library section provides paginated views for all unread articles, saved articles,
and articles read since upgrading to the reading-history schema. Search remains an
on-demand Library action and can search globally or within the current feed or group.

## Backups and Restore

Administrators can open **Settings → Data → Download backup**. feedss asks SQLite to
create a transactionally consistent, compact database using `VACUUM INTO`; the
download is a standalone `.db` file and does not require separate `-wal` or `-shm`
files.

To restore a backup:

1. Stop feedss completely so no process has the database open.
2. Make a safety copy of the current database and its `-wal` and `-shm` files.
3. Replace the file configured by `APP_DB_PATH` with the downloaded backup.
4. Remove stale `-wal` and `-shm` files belonging to the replaced database.
5. Start feedss. Startup migrations will safely apply if the backup came from an
   older release.

Keep backups somewhere protected: they contain accounts, password hashes,
subscriptions, article content, sessions, and application settings.

## Problem Feeds

When a feed update fails, feedss stores the latest error and attempt time. A **Problem feeds** button appears only while one or more feeds are failing. From there, you can retry an update, edit the feed URL and display settings, or remove a feed that has gone away. A successful update clears the warning automatically.

Feed updates use HTTP `ETag` and `Last-Modified` validators when servers provide them, avoiding unnecessary downloads and parsing. Automatic and manual batch updates use a bounded worker pool.

## Install as an App

On a supporting browser, use its **Install app** or **Add to Home Screen** action. The installed PWA caches only static application files and a connection-status page; account data, API responses, and articles are not stored in the service-worker cache.

## Releases

Pushing a tag like `v1.0.0` runs the release workflow. It builds Windows, Linux, and macOS binaries, creates or updates the GitHub release, and publishes Docker images to GHCR.
