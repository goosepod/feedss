# feedss

A home-lab RSS reader focused on fast keyboard navigation and simple local use over Tailscale or LAN.

## Stack

- Go + standard HTTP server
- SQLite database
- Docker deployment
- Keyboard-first UI

## Features planned

- Feed groups and simple feed management
- Keyboard navigation with `j` / `k` and `Shift+j` / `Shift+k`
- `v` to open the article in a new tab
- `c` to open comments when available
- Per-feed display modes: headline, headline + blurb, full content
- Sort order: ascending or descending
- OPML import/export
- Single-user and admin-lite flow for local use

## Local development

```bash
go run .
```

Then open http://localhost:4317.

The default admin account is created automatically on first boot:

- Username: `admin`
- Password: `admin123`

## Docker

```bash
docker compose up --build

Then open http://localhost:4317.
```

## Notes

This is intentionally a lean starter project designed for a local, trusted environment rather than a public SaaS deployment.
