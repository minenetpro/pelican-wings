# Changelog

## v1.2.0 - 2026-03-07
### Changed
* Restic backup and snapshot endpoints now accept request-scoped repository credentials instead of relying on static configuration.
* Restic repository initialization now uses per-repository locking to avoid concurrent setup races.
* Restic backup documentation now reflects the updated request payloads and credential flow.

### Fixed
* `wings update` now defaults to the `minenetpro/pelican-wings` GitHub repository so self-updates pull assets from this fork's releases.
