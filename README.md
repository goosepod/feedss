# feedss

feedss is a small, self-hosted RSS reader for people who want a fast, plain feed list without a lot of ceremony. It supports OPML import/export, nested feed groups, unread counts, per-feed display modes, keyboard navigation, manual refresh, release notifications, and a local SQLite database.

See the [changelog](CHANGELOG.md) for release history.

It is designed for a trusted home-lab, desktop, LAN, or Tailscale-style environment rather than public multi-tenant hosting.

## Quick Start

The first run creates a default admin account:

- Username: `admin`
- Password: `admin123`

Open http://localhost:4317 after starting the app.

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
    image: ghcr.io/goosepod/feedss:v0.5.0
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
  ghcr.io/goosepod/feedss:v0.5.0
```

Use `ghcr.io/goosepod/feedss:latest` for the newest tagged release.

## Keyboard Shortcuts

- `j` / `k`: next or previous article
- `v`: open the selected article
- `c`: open comments when available
- `r`: refresh the current feed or group from the top and hide read articles
- `Shift+A`: mark the current feed or group as read
- `?`: show shortcuts

## Releases

Pushing a tag like `v0.5.0` runs the release workflow. It builds Windows, Linux, and macOS binaries, creates or updates the GitHub release, and publishes Docker images to GHCR.
