# Changelog

All notable changes to feedss are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/goosepod/feedss/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/goosepod/feedss/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/goosepod/feedss/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/goosepod/feedss/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/goosepod/feedss/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/goosepod/feedss/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/goosepod/feedss/releases/tag/v0.1.0
