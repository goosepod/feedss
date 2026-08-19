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

**Status:** complete

- Let readers save and unsave articles.
- Add a Saved view and keyboard action.
- Exempt saved articles from age- and count-based cleanup.

Implemented with an indexed `is_saved` article flag, a top-level Saved view,
per-article star controls, and the `S` keyboard shortcut. Cleanup and per-feed
retention queries exclude saved rows.

## 5. Search

**Status:** complete

- Add SQLite full-text search across article titles, summaries, and content.
- Support global search and optional feed/group scope.

Implemented with an external-content SQLite FTS5 table, weighted BM25 ranking,
prefix matching, and insert/update/delete triggers that keep the index synchronized.
The reader can search globally or scope a query to the currently selected feed or
group.

## 6. Backups

**Status:** complete

- Create consistent SQLite backups from the admin UI or CLI.
- Document and test the restore workflow.

Implemented with SQLite's `VACUUM INTO` command, exposed as an administrator-only
download. The resulting compact database is standalone, integrity-tested, and does
not depend on the live database's WAL files. Restore steps are documented in the
README.

## 7. Feed discovery

**Status:** complete

- Accept ordinary website URLs and discover linked RSS/Atom feeds.
- Offer a choice when a page advertises multiple feeds.

Implemented by recognizing direct RSS/Atom responses and parsing HTML alternate-feed
links with relative URL resolution, deduplication, response limits, and an in-dialog
candidate picker.

## 8. Global reader views

**Status:** complete

- Add top-level All unread, Saved, and Recently read views.
- Keep pagination and unread-count behavior consistent with feed and group views.

Implemented as paginated, user-scoped SQLite queries for All unread, Saved articles,
and Recently read. A persisted `read_at` timestamp records reads after migration so
the recent view reflects actual reading order without fabricating legacy history.

## 9. Cross-client synchronization

**Status:** planned

- Reflect read state, saved articles, subscription changes, and unread counts across
  every open browser and installed app for the same account.
- Store a monotonic change revision or change log in SQLite so synchronization
  remains durable and works across multiple feedss processes.
- Let foreground clients apply changes promptly without replacing or repositioning
  the article list currently being read.
- Reconcile immediately when a backgrounded phone or PWA returns to the foreground.
