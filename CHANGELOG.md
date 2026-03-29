# Changelog

## v3.0.0 - 2026-03-29
### Breaking
* Secure shared-node deployment is now the primary operating model for this fork: Wings runs directly on the host with a gVisor-backed `runsc` runtime, and the repo no longer ships Docker packaging for running Wings itself in a container.
* Secure mode rejects tenant-controlled host mounts and `force_outgoing_ip`, and isolates workloads onto per-server bridge networks.

### Changed
* Documentation has been refactored around end-to-end secure host deployment, including gVisor installation, Docker runtime registration, and host-side Wings configuration.
* Tenant images may now use either tags or digests; digest pinning is recommended for provenance, but not required for sandboxing.
* Cached local images are now reused correctly during pull failures for both tag references and digest references.

## v2.1.1 - 2026-03-07
### Fixed
* Local control-plane persistence now preserves configuration `replace_with` values when process configurations are written to and reloaded from `servers.json`.

## v2.1.0 - 2026-03-07
### Fixed
* Local control-plane persistence now stores `process_configuration.startup.done` matchers as strings so `servers.json` can be reloaded cleanly after restart.

## v2.0.1 - 2026-03-07
### Fixed
* Imported servers created with `skip_install` now restore their actual runtime state by checking whether the environment is already running and reattaching when possible.

## v2.0.0 - 2026-03-07
### Breaking
* Wings now initializes a local `servers.json` control-plane store under the system root directory instead of creating the Panel remote API client at startup.
* `POST /api/servers` now expects local-control payloads that include `settings` and `process_configuration`, with optional `installation_script` and `skip_install` fields.

### Added
* Added a local control-plane client that persists server definitions on disk and serves configuration, installation script, state-change, and SFTP validation requests without a Panel round-trip.
* Added `PUT /api/servers/:server` to update a locally managed server definition and resync the running server in place.
* Server API responses now include `process_configuration` to support local-control consumers.

### Changed
* Deleting a locally managed server now also removes its persisted server definition.
* Server sync now reapplies the filesystem denylist when configuration changes are loaded.
* Startup and state-change logging now refer to the configured control plane instead of the Panel.

## v1.2.0 - 2026-03-07
### Changed
* Restic backup and snapshot endpoints now accept request-scoped repository credentials instead of relying on static configuration.
* Restic repository initialization now uses per-repository locking to avoid concurrent setup races.
* Restic backup documentation now reflects the updated request payloads and credential flow.

### Fixed
* `wings update` now defaults to the `minenetpro/pelican-wings` GitHub repository so self-updates pull assets from this fork's releases.
