# Changelog

## Unreleased

- Added a free shared-playlist workflow: each DJ selects their own editable contribution playlist or folder, and RekordLink publishes one de-duplicated `Duo Library` containing both contributions.
- Kept contribution sources out of the generated per-DJ trees so the imported XML contains one combined playlist rather than duplicate contribution copies.
- Made shared contributions fail closed when a configured source path disappears or points inside generated RekordLink output.
- Persisted repeatable `--shared-playlist` selections in the dashboard and background service configuration. Updated merge hosts/relays consume the internal contribution marker without changing the HTTP protocol.
- Added a dashboard folder/playlist picker for sharing everything except selected paths, plus repeatable `--exclude-playlist` flags for hosts and joining clients.
- Applied exclusions before audio preparation and publication, including metadata-only mode. Descendant playlists, their collection tracks, same-location duplicate records, and references from other playlists are withheld; unfiled tracks remain shared.
- Persisted exclusions in background-service settings and blocked publication when a selected path disappears, preventing renamed folders from silently becoming shared.
- Kept exclusions outbound-only: local Collection entries, analysis, playback, USB export, and incoming partner tracks remain available.
- Added tests for nested paths, escaped names, overlapping playlists, service settings, and real relay uploads that verify excluded audio is never cached or uploaded.

## 0.4.5 - 2026-09-06

- Added a managed-playlist publication boundary: an imported top-level `RekordLink` tree is derived output and is no longer sent back as original source input.
- Recognized rekordbox-style numeric duplicates such as `RekordLink (2)` at the same boundary.
- Added the same filter inside the merge engine so upgraded relays also clean snapshots retained from older clients.
- Kept imported collection tracks available for local cue/metadata precedence while continuing to de-duplicate their audio by SHA-256 content identity.
- Added a two-sided import-feedback regression test and documented the replace-only managed-folder workflow.

## 0.4.4 - 2026-09-06

- Normalized proxy-generated weak ETags so clients can use them with older relays that compare validators strictly.
- Added an authenticated room-revision fallback when a reverse proxy strips ETags or ignores conditional requests.
- Suppressed output and receipt rewrites when the materialized merged XML is byte-identical.
- Made relay snapshot and status ordering deterministic and allowed weak `If-None-Match` comparison before performing an unchanged merge.
- Added regression coverage for weak, ignored, and removed ETags through reverse-proxy-compatible HTTP behavior.

## 0.4.3 - 2026-09-06

- Moved the client blob cache out of the operating-system configuration directory and into the operator-selected managed audio folder.
- Added automatic, SHA-256-verified migration that removes the legacy Application Support cache after the managed copy is durable.
- Materialized library tracks now use same-filesystem hard links to cached blobs when supported, avoiding a second full copy of identical RekordLink-managed audio.
- Added cache-location, migration, corruption, and hard-link regression tests.

## 0.4.2 - 2026-09-06

- Made unreadable source audio non-fatal: protected or permission-denied tracks remain metadata-only while the rest of the library continues synchronizing.
- Added a Unix permission-denial regression test.

## 0.4.1 - 2026-09-06

- Corrected the Linux-executed multi-chunk snapshot integration fixture.
- Added release-package checks that refuse to archive empty required Go/XML source files.

## 0.4.0 - 2026-09-06

- Added public HTTPS relay invitations that use normal operating-system certificate validation and work through an HTTP Cloudflare Tunnel without client-side VPN or `cloudflared` software.
- Added durable, resumable 4 MiB audio and XML uploads plus byte-range audio downloads to tolerate proxy timeouts, sleep, and network interruption.
- Preserved backward compatibility with certificate-pinned direct and private-relay invitations and legacy whole-object endpoints.
- Added a hardened two-container Cloudflare Compose deployment with no published origin port and a file-mounted Tunnel token.
- Added public-relay setup to the CLI and cross-platform management UI.
- Added a complete `rekordlink.zeusyboy.com` Cloudflare deployment and migration guide.

## 0.3.1 - 2026-09-06

- Added a multi-stage Docker image dedicated to the always-on relay.
- Added a hardened Compose deployment with non-root execution, dropped capabilities, read-only root filesystem, health checks, explicit interface binding, restart policy, and persistent relay state.
- Added environment-driven relay configuration and safe explicit-argument overrides.
- Kept the private pairing invite out of container logs by default and documented retrieval from the mode-`0600` persistent file.
- Added multi-architecture GHCR publishing for `linux/amd64` and `linux/arm64`, including provenance and SBOM generation.
- Added a complete Docker deployment, VPN/public networking, persistence, update, and troubleshooting guide.
- Added a self-contained Docker build-context archive to packaged releases.

## 0.3.0 - 2026-09-06

- Added a polished, responsive management dashboard built into the existing binary.
- Added setup forms for direct host, guest, and relay modes, including audio-copy consent and native path pickers.
- Added live background-service state, start/stop/restart/removal controls, receipt verification, and bounded recent logs.
- Restricted management HTTP to loopback addresses with host validation, CSRF protection, strict response headers, body limits, and no external web assets.
- Added a lightweight native AppKit `NSStatusItem` application for the macOS menu bar; the application is an `LSUIElement` and does not appear in the Dock.
- Added Apple Silicon, Intel, and universal macOS app packaging without changing the synchronization engine.
- Added cross-platform service runtime and control APIs plus CLI commands.
- Staged the service engine in a stable per-user application-data directory and kept background host/relay invites out of logs.

## 0.2.0 - 2026-09-05

- Added opt-in, content-addressed audio synchronization for local rekordbox tracks.
- Added upload-before-metadata and download-before-output publication barriers.
- Added SHA-256 verification, atomic audio writes, requester-local path materialization, and byte-identical de-duplication.
- Added a symmetric, exactly-two-client relay for remote/private-VPN operation.
- Added background login-service installation for macOS, Linux, and Windows.
- Added XML Auto Export workflow documentation, missing-audio warnings, integration tests, race tests, and cross-platform build checks.

## 0.1.0 - 2026-09-05

- Added strict rekordbox XML parsing and serialization for documented collection, playlist, cue/loop, and beatgrid fields.
- Added deterministic, requester-safe two-library merging and an offline merge command.
- Added certificate-pinned HTTPS pairing for one host and one guest.
- Added file watching, snapshot publication, ETag polling, and atomic merged XML output.
- Added validation, security documentation, architecture report, tests, and cross-platform release packaging.
