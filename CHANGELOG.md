# Changelog

All notable changes to feedss are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.8.0] - 2026-08-19

### Added

- Saved articles with per-article star controls, a dedicated Saved view, the `S` shortcut, and cleanup protection for saved rows.
- SQLite FTS5 search across article titles, summaries, and content, with relevance ranking and optional current-feed or current-group scope.
- A roadmap item for keeping read and saved state synchronized across open clients.

### Changed

- Reworked mobile navigation into an off-canvas subscription drawer and compact action menu so article content remains the focus.
- Moved search into an on-demand Library overlay on desktop and mobile instead of permanently occupying reader space.

### Fixed

- Keep failed sign-ins on the login form with a clear inline error while preserving the entered username.

## [0.7.0] - 2026-08-19

### Added

- SQLite-backed, opaque sessions with token hashing, expiry, logout revocation, and rotation after account or password changes.
- Conditional feed requests using persisted `ETag` and `Last-Modified` validators, bounded response sizes, and concurrent batch refreshes.
- Installable PWA metadata, icons, and a service worker with a static offline shell that does not cache private API responses or articles.
- A living project roadmap with a SQLite-first architecture principle.

## [0.6.1] - 2026-08-19

### Fixed

- Keep the startup banner and subsequent log messages on one output stream so Docker preserves their order.
- Prevent browsers from reusing an outdated application shell after a container upgrade.

## [0.6.0] - 2026-08-19

### Added

- First-run administrator creation with no default credentials, plus administrator-managed users with temporary passwords and mandatory password replacement.
- Self-service username and password changes protected by current-password verification.
- Persistent problem-feed reporting with retry, URL editing, and feed removal actions.
- A startup banner with version, runtime configuration, project, license, and readiness information.

### Changed

- Store new passwords with bcrypt and transparently upgrade legacy plaintext passwords after a successful login.
- Use compact decimal favicon counts such as `2.3k` for large unread totals.

### Fixed

- Visually center the white RSS artwork within the application icon.

## [0.5.1] - 2026-08-19

### Fixed

- Limit Mark all read to the current article-list snapshot so articles arriving in the background remain unread and appear at the top after the list reloads.

## [0.5.0] - 2026-08-18

### Added

- Automatic loading of older articles as the reader approaches the bottom of the page.
- An `R` shortcut that returns to the top, reloads the current source, and hides read articles.
- Live unread-count polling every 30 seconds while focused and every 15 minutes in the background, with an immediate check when focus returns.
- A confirmation dialog for the Mark all read button and `Shift+A` shortcut.

### Changed

- Article pagination now uses a stable chronological cursor so newly arriving articles cannot disrupt an in-progress reading session.
- Selecting a feed or group with unread articles now shows unread articles only; sources with no unread articles continue to show their history.
- Reselecting the current feed or group reloads it from the newest available article.

### Fixed

- Decode nested HTML entities and remove inline formatting tags from article titles.
- Keep read-state changes from shifting or skipping articles during pagination.
- Refresh sidebar and favicon unread counts without replacing the article list currently being read.

## [0.4.0] - 2026-08-17

### Added

- An unread-count favicon badge styled with the feedss icon color.
- The feedss icon in the application header.
- Cached source favicons beside article source names.
- Directly clickable article headlines.

### Changed

- Moved article source and publication time below the headline.
- Improved article spacing and the selected-article top inset.
- Removed the unread count from the browser title when it is already shown in the favicon.

### Fixed

- Made settings notifications dismissible.
- Use a feed's own favicon instead of the linked story's favicon for aggregator feeds.

## [0.3.0] - 2026-08-16

### Added

- Optional in-app notifications when a newer feedss release is available.
- Stable and prerelease notification channels.
- Documented versioned and `latest` GHCR image usage.

### Changed

- Updated the release workflow and Docker Compose documentation.

## [0.2.0] - 2026-08-16

### Added

- Cross-platform release archives for Windows, Linux, and macOS.
- Multi-architecture container images published to GitHub Container Registry.

### Changed

- Expanded release and self-hosting documentation.

## [0.1.0] - 2026-08-16

### Added

- Initial self-hosted RSS reader release.
- Feed groups, unread counts, OPML import and export, display modes, keyboard navigation, automatic refresh, and SQLite storage.

[Unreleased]: https://github.com/goosepod/feedss/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/goosepod/feedss/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/goosepod/feedss/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/goosepod/feedss/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/goosepod/feedss/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/goosepod/feedss/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/goosepod/feedss/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/goosepod/feedss/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/goosepod/feedss/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/goosepod/feedss/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/goosepod/feedss/releases/tag/v0.1.0
