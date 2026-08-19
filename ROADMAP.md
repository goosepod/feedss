# feedss Roadmap

This document tracks the planned work for feedss in priority order. Keep each
item's status and implementation notes current as work lands.

Status values: `planned`, `in progress`, `complete`.

## Architecture principle: SQLite first

Use SQLite's built-in capabilities as the default persistence and coordination
layer. Durable sessions and refresh state belong in tables; future search should
use FTS5; future queues should be database-backed; and backup work should use
SQLite's backup facilities. Avoid adding a separate cache, queue, search service,
or other infrastructure when SQLite can provide the required behavior cleanly.

## 1. Secure sessions

**Status:** complete

- Replace client-controlled identity cookies with opaque, random session tokens.
- Store only token hashes and expirations in SQLite.
- Revoke sessions on logout and rotate them after account or password changes.
- Cover session creation, expiry, revocation, and forged-cookie rejection with tests.

Implemented with the `sessions` table, 256-bit random browser tokens, SHA-256
token hashes, seven-day expirations, and session rotation.

## 2. Smarter feed fetching

**Status:** complete

- Persist HTTP `ETag` and `Last-Modified` validators per feed.
- Use conditional requests and handle `304 Not Modified` without reparsing content.
- Refresh feeds concurrently with a small, bounded worker pool.
- Preserve feed-health reporting and test both changed and unchanged responses.

Implemented with validator columns on `feeds`, a 16 MiB response limit, and a
four-worker bounded dispatcher shared by automatic and manual refreshes.

## 3. Progressive web app

**Status:** complete

- Add an application manifest and installable icons.
- Register a service worker that caches the application shell.
- Provide a useful offline shell without caching private API responses or articles.
- Verify manifest, registration, and offline behavior in UI tests.

Implemented with an embedded manifest, generated 192 px and 512 px icons, and a
service worker that caches static shell assets and a connection-status page. It
does not cache navigations, API responses, account data, or articles.

## 4. Saved articles

**Status:** planned

- Let readers save and unsave articles.
- Add a Saved view and keyboard action.
- Exempt saved articles from age- and count-based cleanup.

## 5. Search

**Status:** planned

- Add SQLite full-text search across article titles, summaries, and content.
- Support global search and optional feed/group scope.

## 6. Backups

**Status:** planned

- Create consistent SQLite backups from the admin UI or CLI.
- Document and test the restore workflow.

## 7. Feed discovery

**Status:** planned

- Accept ordinary website URLs and discover linked RSS/Atom feeds.
- Offer a choice when a page advertises multiple feeds.

## 8. Global reader views

**Status:** planned

- Add top-level All unread, Saved, and Recently read views.
- Keep pagination and unread-count behavior consistent with feed and group views.
