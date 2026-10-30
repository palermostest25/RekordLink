# Product

<!-- impeccable:product-schema 1 -->

## Platform

adaptive

## Users

Two DJs who each maintain a rekordbox library and want one dependable combined library without manually reconciling copies.

## Product Purpose

RekordLink continuously combines two rekordbox XML libraries and, when both people opt in, their referenced audio. A successful setup produces one verified shared XML that either DJ can import and export to USB.

## Positioning

Each DJ keeps ownership of their local rekordbox library while selected contribution playlists become one deduplicated Duo Library. RekordLink does not require a paid cloud-library subscription.

## Operating Context

The macOS app normally stays in the menu bar. DJs choose a rekordbox XML export, an output XML, and optionally a managed audio folder. These locations may be on removable volumes. Sync continues in the background after the configuration window closes.

## Capabilities and Constraints

- Host, join, and relay modes use the existing Go synchronization engine.
- Playlist exclusions are outbound only.
- Selected shared playlists feed one combined Duo Library.
- The macOS interface must use native controls and native file panels.
- Background access must be tied to the installed app and must not repeatedly request removable-volume permission.
- Updates come from GitHub Releases and must be integrity checked before installation.

## Brand Commitments

The product name is RekordLink. Language should be direct, calm, and understandable without networking or command-line knowledge.

## Evidence on Hand

The repository contains the synchronization engine, service management, tests, release packaging, and the previous local management dashboard. There are no approved commercial claims or third-party endorsements.

## Product Principles

- Make the shared library state obvious.
- Keep setup understandable for working DJs.
- Preserve local libraries and user control.
- Treat file permission failures as actionable states, never prompt loops.
- Keep background operation quiet and recoverable.

