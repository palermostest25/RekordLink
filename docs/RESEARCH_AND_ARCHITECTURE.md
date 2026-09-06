# Linking two rekordbox libraries: feasibility, market review, and architecture

**Status:** implementation report and release architecture  
**Research date:** 5 September 2026  
**Project:** RekordLink v0.4.5

## Executive summary

The requested product is feasible, but “instant library linking” has two materially different meanings:

1. **Native, truly live collaboration inside rekordbox:** already available through rekordbox 7 Collaborative Playlist for Professional-plan users. It can share playlist order and metadata such as BPM, beatgrid, and cue points between distinct AlphaTheta accounts. It does not transfer locally owned audio files.
2. **An independent, supported integration:** feasible through AlphaTheta's documented rekordbox XML bridge. It can merge tracks, playlists, cues, loops, and beatgrid markers without touching the encrypted live database. Changes become available shortly after each DJ exports XML, but rekordbox retains control of refreshing/importing them.

There is no documented public API for a third party to subscribe to every live desktop-library change or safely transact against rekordbox's current `master.db`. Direct database modification would depend on reverse-engineered encryption and an unstable private schema. It would also create unacceptable corruption, compatibility, support, and licence risk. The architecture therefore deliberately excludes it.

RekordLink v0.4.5 implements the safe second path as a dependency-free background sidecar. It watches rekordbox's supported XML Auto Export output, synchronizes authorized local audio by its SHA-256 content identity, and emits a requester-specific merged XML library only after its audio is present and verified locally. Direct rooms use certificate-pinned HTTPS; public relays use system-verified HTTPS through a standard reverse proxy and restartable 4 MiB transfers. A cross-platform, loopback-only dashboard manages the existing service, while a small AppKit wrapper exposes the controls as a native macOS menu-bar application. The relay ships as hardened Docker deployments for private networks and Cloudflare Tunnel. Unreadable source audio is isolated as a per-track warning so one macOS privacy denial cannot stop the background synchronizer. Bulk client cache data follows the chosen managed audio folder and same-filesystem materialization avoids duplicate audio bytes. Weak or removed reverse-proxy ETags fall back to stable room revisions without repeatedly rewriting the merged library. Generated `RekordLink` playlist roots are removed at the publication boundary so importing derived XML cannot recursively feed the previous merge into the next one.

The result is seamless **eventual** convergence: after rekordbox closes and performs its automatic XML export, no further export, copy, path-edit, or merge command is required. It is not live mutation of an open rekordbox database. AlphaTheta's Collaborative Playlist remains the supported live channel while both DJs are editing inside rekordbox.

The strongest practical recommendation for a duo is:

- Use rekordbox Collaborative Playlist when both DJs accept the Professional subscription and need live cue/grid collaboration.
- Use RekordLink for subscription-independent library comparison, namespaced playlist union, local-path preservation, backups, and deterministic exchange through the official XML bridge.
- Keep audio copying explicit. RekordLink requires both a managed audio destination and `--allow-audio-copy`; both DJs remain responsible for having the legal right to make each copy.

## 1. User problem and success criteria

Two DJs performing as a duo typically have:

- separate AlphaTheta accounts and laptops;
- overlapping but non-identical audio collections;
- different filesystem paths for the same recording;
- independently edited playlists, memory cues, hot cues, loops, beatgrids, comments, ratings, and keys;
- a need to prepare remotely, then arrive at a performance with a predictable common view;
- personal playlists that should remain private or at least clearly separated;
- a low tolerance for duplicates, missing-file surprises, lost cues, database corruption, or a sync process that changes the live collection immediately before a set.

The ideal experience is “pair once, see a shared workspace, know what changed, and import only what is intended.” A production-quality system needs:

- deterministic track identity across different paths and machines;
- explicit ownership and conflict policy;
- ordered-playlist convergence;
- cue/grid precision;
- offline operation and later reconciliation;
- encryption in transit and at rest;
- backups, audit history, rollback, and dry-run previews;
- no dependency on modifying undocumented rekordbox internals;
- a clear audio-rights boundary;
- reliable performance with 100,000+ tracks and multi-terabyte collections.

## 2. What rekordbox officially provides

### 2.1 Collaborative Playlist

rekordbox 7's Collaborative Playlist is the closest direct solution. AlphaTheta describes it as a way to share playlists and metadata such as track order, BPM, GRID, and CUE information for uses including B2B preparation. The current plan page places the feature in the Professional tier. The current manual describes an owner inviting a member by AlphaTheta-account email, followed by acceptance through the notification or email flow.

The useful properties are:

- distinct accounts can collaborate;
- playlist additions, removals, and ordering synchronize;
- title/artist and cue/grid edits can be updated between members;
- streaming entries work when each member has and signs into the same streaming service;
- it operates within rekordbox, so it has the only supported route to genuinely live metadata changes.

The important limits are:

- local music files are not transferred;
- the other member must already have a matching file, then use Auto Relocate if its path differs;
- both inviter and invitee need the required plan;
- AlphaTheta's FAQ says inactive collaborative playlists can revert after 30 days without changes, or when the Professional plan is cancelled, deactivated, or expires;
- it is playlist-scoped rather than an independently controlled, versioned union of two complete libraries;
- it depends on AlphaTheta's cloud and account service.

Sources: [rekordbox 7 manual, pp. 45–47](https://cdn.rekordbox.com/files/20260409151936/rekordbox7.214_manual_EN.pdf), [rekordbox feature overview](https://rekordbox.com/en/feature/overview/), [rekordbox 7 Collaborative Playlist FAQ](https://rekordbox.com/en/support/faq/rekordbox7/#faq-71519), [current plan comparison](https://rekordbox.com/en/plan/).

### 2.2 Cloud Library Sync

Cloud Library Sync synchronizes one user's library between computers and mobile devices logged into the same AlphaTheta account. Depending on the plan, it can synchronize selected tracks or an entire library and use Dropbox or Google Drive for media. It is excellent for one DJ with multiple devices, but sharing account credentials is not an appropriate two-person collaboration architecture.

Sources: [rekordbox Library Sync FAQ](https://rekordbox.com/en/support/faq/library-sync-mobile/), [cloud feature overview](https://rekordbox.com/en/feature/cloud/), [Cloud Library Sync operation guide](https://cdn.rekordbox.com/files/20251202174725/rekordbox7.2.8_cloud_library_sync_operation_guide_EN.pdf).

### 2.3 LINK EXPORT and PRO DJ LINK

LINK EXPORT exposes tracks from one rekordbox computer or mobile device to compatible players on a local network. PRO DJ LINK lets compatible performance hardware share media and performance information. These solve playback topology, not durable two-computer library collaboration: they do not merge two desktop collections, establish shared ownership, or resolve editing conflicts.

Source: [rekordbox manual](https://cdn.rekordbox.com/files/20260409151936/rekordbox7.214_manual_EN.pdf).

### 2.4 rekordbox XML bridge

AlphaTheta publishes a developer page and a formal one-page XML field list. The format includes collection tracks, metadata, playlists and folders, beatgrid `TEMPO` elements, and cue/loop `POSITION_MARK` elements. `Location` is required and uses a file URI. rekordbox exposes an imported XML library in its Bridge pane; a user can drag an XML playlist into the local Playlists area.

This is the correct compatibility boundary for an independent product because it is documented, inspectable, backup-friendly, and does not modify the running database. Its limits are equally important:

- it is snapshot interchange, not an event stream;
- rekordbox does not continuously export the current collection to XML on every edit;
- XML cannot represent every private database feature (for example, third-party reports and tools note gaps around My Tags and some analysis assets);
- importing remains an explicit rekordbox/user action;
- the XML collection model has one cue/grid version per track, so conflicting versions require a policy.

rekordbox 7 also exposes **XML Auto Export** under Preferences → Advanced → Others → Tribe XR. AlphaTheta states that it automatically exports the collection when rekordbox closes. This removes the repeated manual export step and provides the safe background change detector used by RekordLink. It is a close-time checkpoint, not a live event stream.

Sources: [rekordbox for Developers](https://rekordbox.com/en/support/developer/), [official XML format list](https://cdn.rekordbox.com/files/20200410160904/xml_format_list.pdf), [rekordbox manual XML import flow, p. 44](https://cdn.rekordbox.com/files/20260409151936/rekordbox7.214_manual_EN.pdf), [rekordbox XML Auto Export FAQ](https://rekordbox.com/en/support/faq/rekordbox7/#faq-84681).

### 2.5 OneLibrary

OneLibrary standardizes essential performance information such as playlists, cue points, and beatgrids across participating DJ software and hardware. As currently documented, rekordbox creates it on USB/SD media. It is strategically promising for future adapters, but the public support material does not provide a desktop collaboration API or an open, stable writer contract for arbitrary third-party services.

Sources: [AlphaTheta: What is OneLibrary?](https://support.alphatheta.com/en-US/articles/51298636949017), [rekordbox OneLibrary FAQ](https://rekordbox.com/en/support/faq/rekordbox7/#faq-q700014).

### 2.6 Private desktop database route evaluated and rejected

The desktop `master.db` is a SQLCipher-encrypted SQLite database. The independent Pyrekordbox project demonstrates that it can be queried and that selected tables can be modified, including tracks and playlists. Its own changelog notes two decisive limits: commits are prevented while rekordbox is running, and newly inserted tracks still require tag reload/analysis in rekordbox. Lexicon's commercial direct adapter likewise instructs users to close rekordbox completely before synchronization. Reports of analysis loss reinforce that private-schema writes are not a trustworthy always-on boundary.

AlphaTheta's EULA restricts modifying, reverse engineering, disassembling, or decompiling the program except where applicable law permits it under stated conditions. Even apart from licence analysis, an external writer racing an open DJ database is the opposite of the requested trust guarantee. RekordLink therefore does not obtain the database key, copy the WAL, load AlphaTheta binaries, or write `master.db`.

Sources: [Pyrekordbox project and supported operations](https://github.com/dylanljones/pyrekordbox), [Pyrekordbox changelog](https://github.com/dylanljones/pyrekordbox/blob/master/CHANGELOG.md), [Lexicon rekordbox direct-sync instructions](https://www.lexicondj.com/manual/all), [rekordbox EULA](https://rekordbox.com/en/license-agreement/).

## 3. Existing third-party solutions

| Product/approach | Strength | Gap for this use case |
|---|---|---|
| rekordbox Collaborative Playlist | Native live playlist/cue/grid workflow between accounts | Professional-plan dependency; no local audio transfer; playlist-scoped |
| rekordbox Cloud Library Sync | Deep first-party sync, including media options | Designed for the same AlphaTheta account across devices, not two independent owners |
| Lexicon | Mature library manager and converter; full, playlist, and modified sync; broad DJ-app support | Its documented official rekordbox route still uses XML and an extra import step; its faster direct route is explicitly unofficial database writing |
| MIXO | Cross-platform library conversion, backup, and cloud storage integration | Primarily a personal master library/cloud workflow; not a two-owner, field-level collaborative workspace inside rekordbox |
| Shared Dropbox/Drive folder | Easy audio availability and basic file history | Does not merge rekordbox metadata; syncing a live database file risks concurrent-write corruption and path problems |
| Library backup/restore | Faithful point-in-time migration | Whole-library replacement, not continuous collaboration; poor conflict behavior |
| USB export/import | Reliable performance handoff and familiar club workflow | Manual, device-scoped, easy to become stale, and not suited to remote simultaneous preparation |
| M3U/streaming playlist sharing | Widely compatible track lists | Normally loses rekordbox-specific cues, beatgrids, loops, analysis, and local-file identity |

Lexicon's own documentation distinguishes its official XML route from an unofficial direct-database route and notes that XML needs an additional import step. MIXO describes cloud backup and cross-application imports/exports through formats such as DJXML and M3U. These products validate demand, but neither changes the lack of a documented live rekordbox desktop write API.

Sources: [Lexicon sync documentation](https://www.lexicondj.com/manual/sync), [Lexicon rekordbox documentation](https://www.lexicondj.com/manual/all), [MIXO sync overview](https://www.mixo.dj/guides/mixo-sync-overview).

## 4. Feasibility verdict

| Capability | Feasible through a supported interface? | v0.3 status |
|---|---:|---:|
| Parse tracks/playlists/cues/loops/beatgrids | Yes, XML | Implemented |
| Merge two playlist trees without overwriting sources | Yes | Implemented |
| Resolve different local file paths | Partly; metadata match plus rekordbox Auto Relocate | Implemented with conservative heuristic |
| Secure same-LAN exchange | Yes | Implemented |
| Update shared output shortly after XML changes | Yes | Implemented; ~2-second default scan |
| Detect every edit the moment it happens inside rekordbox | No documented third-party event/API path | Not implemented |
| Automatically commit changes into the live collection | No documented transactional write API | Intentionally not implemented |
| Preserve every rekordbox-private field and analysis asset | No, XML is a subset | Not complete by design |
| Copy authorized locally owned audio | Yes, outside rekordbox | Implemented with explicit opt-in and SHA-256 verification |
| Sync streaming-service audio offline | No; service/DRM restrictions apply | Prohibited/not implemented |
| Remote sync without either laptop accepting inbound connections | Yes with an always-on relay | Implemented as a self-hosted relay |
| Run continuously after login | Yes through OS user services | Implemented for launchd, systemd, and Windows Task Scheduler |

**Conclusion:** release RekordLink as a secure, background XML-and-authorized-audio synchronization sidecar. Its convergence contract begins at rekordbox's automatic close-time XML checkpoint. Do not claim it replaces native Collaborative Playlist for edits made while rekordbox remains open. Pursue an AlphaTheta partnership/API if live transactional in-app sync is a hard requirement.

## 5. Product behavior and conflict policy

### 5.1 Non-destructive namespacing

The merged root is:

```text
ROOT
└── RekordLink
    ├── DJ A
    │   └── <DJ A's original folders/playlists>
    └── DJ B
        └── <DJ B's original folders/playlists>
```

Source XML files are read-only. RekordLink never removes or rewrites their tracks or playlists. Namespace separation avoids same-level duplicate playlist names, which AlphaTheta's developer documentation says are not permitted.

`RekordLink` is a reserved top-level playlist namespace. Before publication, the client parses a copy of the Auto Export XML and excludes folders named `RekordLink` or a rekordbox-style numeric copy such as `RekordLink (2)`. The source file itself is never changed. The merge engine repeats this exclusion for defense in depth and for snapshots retained from earlier clients. Collection tracks are not excluded: their audio content identity and requester-local record precedence already provide the correct de-duplication behavior.

The imported managed folder is replace-only. A user refreshes the Bridge view, removes the preceding managed playlist folder from rekordbox, and imports the new top-level folder once. Preserving that wrapper is part of the provenance contract because the documented XML format has no hidden playlist-origin identifier. Renamed or individually extracted child playlists cannot be identified reliably without either modifying visible names or writing rekordbox's private database, so those workflows are intentionally unsupported.

### 5.2 Track identity

v0.3 hashes every available referenced local audio file and replaces its transport location with `rekordlink://sha256/<content-hash>`. The exact content hash is the primary track identity, so byte-identical files match despite filenames, tags, or original paths. Missing or unsupported local audio falls back to a SHA-256 identifier derived from normalized title, artist, duration, and size; the normalized filename is included if tags are absent.

The content path is exact for byte-identical assets. A transcoded AIFF and MP3 of the same recording intentionally remain different assets; a later acoustic fingerprint layer can associate them without pretending they are interchangeable. Metadata-only fallbacks can still create false positives or negatives. Production should extend the matcher with:

1. user-confirmed persistent RekordLink ID;
2. acoustic/content fingerprint for files the user authorizes the app to read;
3. recording identifiers from tags (ISRC, MusicBrainz, store ID) where available;
4. duration/size/tag heuristic with a confidence score;
5. explicit conflict requiring human review.

Never match on path alone.

### 5.3 Cue/grid conflicts

XML provides one track record, hence one cue/grid set, even if both DJs reference the track in different playlists. v0.3 uses **requester wins** for the complete track record when that requester owns a matched copy. This preserves local intent and prevents a background merge from silently replacing local cues or beatgrids. If only the other DJ has a track, that remote record is included and its content address is materialized to the requester's managed audio root.

A production UI should show alternate versions and let the user choose:

- keep mine;
- take collaborator's;
- copy selected cues only;
- store both as named cue sets in RekordLink, then materialize one during export.

Beatgrids should be treated as atomic versioned sequences, not merged marker-by-marker. Cue slots require stable identities and explicit collision handling.

### 5.4 Ordered playlists

v0.3 does not co-edit one logical ordered playlist; it preserves both source lists. rekordbox Collaborative Playlist should be used for live co-editing. A future independent editor should use an ordered sequence CRDT such as LSEQ/RGA or a server-serialized operation log with stable entry IDs. Plain last-writer-wins on the whole ordered list is unacceptable because simultaneous insertions cause lost work.

## 6. Shipped v0.4.5 architecture

```text
DJ A laptop (host)                              DJ B laptop (guest)
┌──────────────────────┐                       ┌──────────────────────┐
│ rekordbox            │                       │ rekordbox            │
│ export: a.xml        │                       │ export: b.xml        │
└──────────┬───────────┘                       └──────────┬───────────┘
           │ file watch                                   │ file watch
┌──────────▼───────────┐   TLS 1.3 + cert pin + token    ┌▼─────────────────────┐
│ RekordLink host      │◄───────────────────────────────►│ RekordLink guest     │
│ room + audio blobs   │ snapshot + verified blob I/O    │ private blob cache   │
└──────────┬───────────┘                                 └──────────┬───────────┘
           │ atomic write                                           │ atomic write
┌──────────▼───────────┐                                 ┌──────────▼───────────┐
│ shared XML + audio A │                                 │ shared XML + audio B │
│ requester-local URIs │                                 │ requester-local URIs │
└──────────────────────┘                                 └──────────────────────┘
```

### 6.1 Components

- **XML adapter:** strict Go `encoding/xml` parser and serializer for documented track attributes, `TEMPO`, `POSITION_MARK`, and playlist `NODE` trees.
- **Merge engine:** deterministic identity map, sequential output TrackIDs, requester-local record preference, and recursive playlist-key remapping.
- **Audio manager:** SHA-256 hashing, content-addressed immutable blob cache, supported-audio filtering, verified atomic writes, and requester-local file URI materialization.
- **Room state:** host-owned JSON snapshot store with atomic persistence and mode `0600`.
- **Pairing:** one room owner plus one guest, twelve-digit pairing code, rate-limited registration, 256-bit random guest token, and a room-bound invite.
- **Transport:** direct rooms use a self-signed ECDSA P-256 certificate, TLS 1.3, and an invitation-bound SHA-256 certificate pin. Public relays use a normal HTTPS hostname validated through the operating-system trust store. Both use bounded bodies, redirect refusal, and bearer authentication.
- **Convergence:** guest publishes only when its XML SHA-256 changes. It polls merged output with an ETag. Host and guest atomically replace output when content changes.
- **Publication barrier:** every new local audio blob is durably hashed and uploaded before its snapshot is accepted; every remote blob is downloaded and verified before merged output replacement. XML and audio uploads resume at durable 4 MiB boundaries, while downloads resume through HTTP byte ranges.
- **Verifiable receipt:** each output records its room revision, XML SHA-256, counts, generation time, and audio availability. `rekordlink status` recomputes the XML hash and current file presence before reporting `READY`.
- **Relay:** optional symmetric room accepts exactly two authenticated clients, allowing both laptops to make outbound connections to an always-on private or public HTTPS node.
- **Background runtime:** installer generates a per-user launchd agent, systemd service, or Windows login task with restart-on-failure behavior.
- **Local management UI:** embedded responsive dashboard on loopback HTTP with configuration validation, native path pickers, service controls, private invite copying, bounded logs, and receipt status. It uses no external assets or web services.
- **macOS shell:** a separate AppKit `NSStatusItem` executable starts the embedded dashboard and invokes the existing service-control commands. `LSUIElement` keeps it out of the Dock. This wrapper contains no sync or merge logic.
- **Container relay:** multi-stage Docker build produces a small non-root Linux runtime for amd64 and arm64. Private Compose explicitly publishes one selected interface; Cloudflare Compose publishes no host port and gives origin access only to its Tunnel sidecar. Both supply persistent `/data`, health checks, restart behavior, a read-only root filesystem, and capability-free processes.
- **CLI:** `host`, `join`, `relay`, `ui`, `service`, `status`, `inspect`, `merge`, and `version` commands.
- **Release:** pure Go binary with no runtime package dependencies.

### 6.2 API

| Method/path | Authentication | Purpose |
|---|---|---|
| `GET /healthz` | None | Version/readiness without library disclosure |
| `POST /api/v1/register` | Pairing code | Register the one allowed guest and issue a bearer token |
| `POST /api/v1/snapshot` | Peer ID + bearer token | Validate and publish one complete XML snapshot |
| `HEAD/PUT /api/v1/snapshot-uploads/{sha256}` | Peer ID + bearer token | Discover and append a resumable XML snapshot upload |
| `GET /api/v1/combined.xml` | Peer ID + bearer token | Generate requester-specific merged XML; supports ETag |
| `GET /api/v1/status` | Peer ID + bearer token | Room revision and peer track/playlist counts |
| `HEAD /api/v1/blobs/{sha256}` | Peer ID + bearer token | Check whether verified audio is already durable |
| `PUT /api/v1/blobs/{sha256}` | Peer ID + bearer token | Upload up to 8 GiB; server verifies content hash before commit |
| `GET /api/v1/blobs/{sha256}` | Peer ID + bearer token | Download immutable content; HTTP range requests supported by the server |
| `HEAD/PUT /api/v1/uploads/{sha256}` | Peer ID + bearer token | Discover and append a resumable audio upload |

The public dashboard exposes setup guidance but no peer or library metadata. Blob paths are validated hashes and never filesystem paths supplied by a client.

The separate management dashboard is never bound to the room's network listener. It accepts requests only from loopback, validates the HTTP host against loopback, and requires an in-memory random request token for mutations and private-invite reads. The join invite is never returned by its configuration endpoint. Host/relay invites are saved mode `0600`; background logs expose only their private path. Apple documents `NSStatusItem` as the menu-bar item API and `LSUIElement` as the flag for a background agent app without a Dock presence; the macOS wrapper follows those public AppKit boundaries. Sources: [Apple `NSStatusItem`](https://developer.apple.com/documentation/appkit/nsstatusitem), [Apple `LSUIElement`](https://developer.apple.com/documentation/bundleresources/information-property-list/lsuielement).

### 6.3 Docker relay boundary

The container runs the same `relay` command and protocol as the native executable; no second server implementation exists. `/data` is the only durable path and contains the room identity, any private-mode TLS certificate, client registrations, snapshots, partial uploads, and audio blobs. The entrypoint requires either an explicit direct address or public HTTPS endpoint, supports metadata-only operation, and suppresses the full invite in logs by default. The invite remains retrievable from `/data/invite.txt` by a Docker administrator.

The private Compose service publishes TCP 9777 only on an explicitly selected host address. The Cloudflare Compose service publishes no origin port and reaches the Internet only through an outbound authenticated Tunnel container. Both run without root capabilities, enable `no-new-privileges`, use read-only image filesystems, and keep durable data in the relay volume. These controls reduce container attack surface but do not add end-to-end content encryption or protect against a compromised Docker host.

The Dockerfile uses a native-build test step followed by a target-architecture static Go build, then copies only the executable and minimal entrypoint into the runtime stage. Tagged releases can publish amd64/arm64 manifests with provenance and an SBOM through GHCR. Sources: [Docker multi-stage builds](https://docs.docker.com/build/building/multi-stage/), [Docker build best practices](https://docs.docker.com/build/building-best-practices/), [GitHub container publishing](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images).

### 6.4 Performance

The current implementation parses complete XML snapshots and keeps them in memory. Complexity is approximately O(T log T + P), where T is the total track count and P is the total playlist-entry count; sorting deterministic track identities contributes the log factor. Initial audio ingestion is O(total audio bytes) because every file is hashed. A persistent index keyed by absolute path, size, and nanosecond modification time avoids rehashing unchanged audio on later XML checkpoints. This is reasonable for an initial desktop sidecar but not the final design for very large libraries.

Production should use SQLite with WAL locally, immutable operations, streaming XML parsing, incremental indexes, and generated-output caching by `(room revision, requester)`. XML bodies are capped at 128 MiB and audio objects at 8 GiB in v0.3 to prevent accidental memory exhaustion.

## 7. Production architecture

### 7.1 Local-first agent

Keep a native agent on each laptop. It should own adapters, filesystem permissions, path maps, content fingerprints, local encryption keys, an operation journal, and the import/export queue. The agent should continue to work without internet and reconcile later.

Use SQLite locally with:

- WAL mode;
- foreign keys enabled;
- periodic `PRAGMA integrity_check`;
- encrypted sensitive columns or an encrypted volume;
- transactionally written checkpoints;
- rotating, recoverable backups.

### 7.2 Canonical domain model

Avoid mirroring rekordbox tables. Define a vendor-neutral model:

- `Recording`: stable content/acoustic identities, duration, technical format;
- `Asset`: one authorized local or cloud file, path, checksum, owner, availability;
- `TrackMetadataVersion`: title, artist, key, BPM, tags, provenance, timestamp;
- `CueSetVersion`: memory/hot cues and loops as an immutable set;
- `BeatgridVersion`: ordered tempo/beat markers and analysis provenance;
- `Playlist`: stable owner and policy;
- `PlaylistEntry`: stable entry ID, recording ID, ordering token;
- `Operation`: actor, device, Lamport/HLC timestamp, base revision, signature;
- `Conflict`: competing versions, classification, resolution, audit record;
- `ExportProfile`: adapter/version, field mapping, path map, selected versions.

Track and asset must be separate. One recording may have AIFF and MP3 assets, and each computer may have a different location. A cue set belongs to a recording/version context, not to a filesystem path.

### 7.3 Sync control plane

For remote collaboration, deploy a stateless HTTPS/WebSocket API behind a load balancer plus:

- PostgreSQL for rooms, membership, device keys, and compacted operations;
- an append-only operation stream (PostgreSQL first; NATS/Kafka only when justified by scale);
- Redis for short-lived presence, rate limits, and WebSocket fan-out;
- object storage for encrypted snapshots and optional authorized assets;
- a background compactor that creates signed room checkpoints;
- region-aware backups and tested point-in-time restore.

Clients should send operations, not entire databases. Server acknowledgement gives a durable revision. Reconnection asks for operations after the last durable revision, then falls back to a checkpoint if the gap was compacted.

### 7.4 End-to-end security

Recommended production security:

- device-generated Ed25519 signing keys and X25519 key agreement;
- room key distributed through an out-of-band QR/invite and rotated when membership changes;
- XChaCha20-Poly1305 or AES-256-GCM per payload with unique nonces;
- TLS 1.3 in addition to end-to-end encryption;
- OS Keychain/Credential Manager/Secret Service for device secrets;
- Argon2id recovery-key derivation;
- signed operations to prevent server-side forgery;
- least-privilege filesystem access selected by the user;
- short-lived access tokens, refresh rotation, device revocation, and audit log;
- redacted structured logs with no track titles, paths, tokens, or invite data;
- dependency scanning, SBOM, signed builds, reproducible release pipeline, and auto-update signature verification.

The v0.3 certificate-pinned TLS model is suitable for a trusted LAN or duo-controlled private relay/VPN, not for a multi-tenant internet service. Relay metadata and audio are plaintext on the relay's private disk; end-to-end encrypted objects remain a production requirement for hosted service.

### 7.5 Implemented authorized-audio flow—not unrestricted sharing

The shipped audio flow:

1. Requires the operator to pass `--audio-root` and `--allow-audio-copy` together.
2. Reads only supported local audio paths referenced by the selected exported library.
3. Computes SHA-256 and caches the immutable content locally.
4. Checks the room for the hash, uploads missing content, and requires server-side verification before rename.
5. Publishes the XML snapshot only after those uploads succeed.
6. Downloads missing remote hashes, verifies them, materializes them below the local managed root, rewrites XML locations, and only then atomically publishes the merged output.
7. Leaves unavailable files metadata-only and reports them without blocking unrelated synchronization.
8. Never ingests streaming cache files or attempts to make DRM content portable/offline.

Future multi-tenant hosted operation should add client-side room encryption, quotas, proof/attestation where appropriate, and legitimate-store resolution for assets neither DJ owns. Resumable transport is complete in v0.4.5.

AlphaTheta's EULA restricts reverse engineering and unauthorized copying/transfer of music data. A commercial launch requires jurisdiction-specific legal review, terms, takedown handling, and a stronger rights-confirmation UX. RekordLink's flag is a technical interlock, not proof of a licence.

Source: [rekordbox EULA, updated 4 March 2025](https://rekordbox.com/en/license-agreement/).

### 7.6 Supported-adapter strategy

Adapters should be capability-described and version-gated:

```text
adapter: rekordbox-xml
read: tracks, metadata, playlists, cues, loops, beatgrid
write: same documented XML subset
live_events: false
commit_to_library: user_import
private_db_access: false
```

Add OneLibrary only when AlphaTheta publishes or licenses a stable writer specification. Keep any future official API adapter in a separate package with contract tests. Never silently fall back from a supported adapter to private database modification.

## 8. Failure modes and controls

| Failure | Control |
|---|---|
| Same recording at different paths | Requester-local path wins; production path mapping and content identity |
| Different recordings falsely matched | Conservative confidence, review queue, user pin/unlink |
| Stale XML export | Show export age and source hash; block “ready for gig” if stale |
| Corrupt or partial XML | Strict parse before acceptance; body cap; atomic output |
| Simultaneous playlist edits | v0.3 namespaces sources; use native collaboration or future operation log/ordered CRDT |
| Conflicting cues or grids | Requester-wins now; explicit version resolution in production |
| Host disappears | Local last-good output remains; production relay/checkpoints |
| Invite intercepted | Direct certificate pin or public CA validation plus pairing secret; rotate room state after leak |
| Guest token stolen | File mode `0600`; production Keychain and device revocation |
| Firewall/NAT blocks host | Public HTTPS relay over an outbound-only Cloudflare Tunnel |
| Disk full during write | Temp-file write/sync before rename; report error and retain old output |
| rekordbox schema changes | XML version contract tests and fixture matrix; refuse unknown breaking versions |
| Metadata appears before its audio | Upload-before-publish and download-before-output barriers |
| Interrupted or corrupt audio | Durable offsets, byte ranges, SHA-256 verification, automatic retry, old output retained |
| Unauthorized audio copy | Explicit dual-flag opt-in; no DRM cache support; operator/legal responsibility |

## 9. Validation and release gates

### v0.4.5 gates completed in this repository

- parse/marshal round-trip test;
- official-field cue and beatgrid fixture;
- cross-path track de-duplication test;
- requester metadata/cue/path precedence test;
- recursive playlist remap test;
- invite encode/decode test;
- registration, authentication, persistence, and state-permission test;
- verified audio cache, corrupt-download rejection, and content-address materialization tests;
- real TLS end-to-end audio upload, server verification, download, and merged-output test;
- exactly-two-client relay test;
- stale-receipt and post-sync missing-audio detection tests;
- background service render tests and Windows cross-compilation;
- loopback binding, CSRF/host rejection, invite redaction, and UI configuration tests;
- visual dashboard render and browser-console verification;
- native AppKit compilation for Apple Silicon and Intel plus universal bundle assembly;
- Docker entrypoint validation, Compose configuration review, and cross-architecture container release workflow;
- public/private invitation compatibility and public endpoint validation;
- interrupted resumable-upload, chunked transfer, range-download, and final-hash validation;
- verified migration of legacy client caches into the chosen managed audio root and same-filesystem hard-link materialization;
- weak, ignored, and removed reverse-proxy ETag regression tests plus deterministic same-revision merge output;
- two-sided imported-playlist feedback filtering, including rekordbox-style numeric root copies;
- cross-platform pure-Go compilation and archive checksums;
- `go vet` and `go test` release commands.

### Gates required before broad public promotion

- round-trip fixtures exported by supported rekordbox 6 and 7 point releases on macOS and Windows;
- 1k, 10k, 100k, and 250k track performance tests;
- Unicode, emoji, malformed URI, long path, duplicate name, and removable-volume tests;
- real rekordbox import tests for cues, loops, variable beatgrids, and playlist hierarchy;
- firewall and multi-interface pairing UX tests;
- abrupt power loss and disk-full fault injection;
- fuzzing for XML, invite, and HTTP input;
- independent security review;
- code signing/notarization on macOS and Authenticode on Windows;
- legal review of the name, notices, EULA interaction, privacy policy, and authorized-audio workflow.

## 10. Roadmap

### Phase 0 — shipped v0.4.5 source release

- safe XML parser/serializer;
- offline merge;
- secure two-peer LAN host/join;
- verified authorized-audio synchronization with publish barriers;
- self-hosted two-client relay;
- background login-service installation;
- cross-platform management dashboard and native macOS menu-bar wrapper;
- hardened Docker relay deployment and multi-architecture image publishing;
- no-client-VPN public HTTPS relay mode and Cloudflare Tunnel Compose deployment;
- resumable chunked XML/audio transfer;
- namespaced shared output;
- deterministic packaging and checksums.

### Phase 1 — usability release

- Developer ID signing/notarization and Windows Authenticode;
- optional native Windows/Linux tray wrappers around the same dashboard;
- QR invite and network diagnostics;
- source freshness, diff preview, and conflict report;
- persistent track-match approvals;
- backup/restore UI and export presets;
- explicit “refresh/import in rekordbox” guidance.

### Phase 2 — robust collaboration

- SQLite operation journal;
- versioned cue/grid sets and manual resolution;
- ordered collaborative playlists;
- privacy scopes for personal versus duo playlists;
- offline edits and deterministic reconciliation;
- relay storage quotas and cleanup policy.

### Phase 3 — remote service

- outbound-only encrypted relay;
- end-to-end encrypted operation log and snapshots;
- device management, revocation, observability, quotas, and billing;
- optional rights-aware asset workflow after legal review.

### Phase 4 — first-party depth

- pursue AlphaTheta developer partnership;
- use an official desktop/cloud event and transaction API if one becomes available;
- evaluate formally documented OneLibrary read/write support;
- retain XML as the portable fallback and recovery format.

## 11. Go/no-go decision

**Go** for RekordLink as a local-first, authorized-audio and metadata collaboration, comparison, and preflight product built on official XML Auto Export.

**No-go** for marketing an independent tool as seamless live rekordbox database sync today. Without a first-party API, that promise can only be met through UI automation or private database access; both are too fragile for a release that DJs must trust before performances.

**Do not operate** it as a generic public audio-sharing service without a separate rights and compliance program. The shipped private-room capability assumes both DJs are authorized to copy each selected file; capability is not permission.

The product's defensible value is not merely copying a library. It is making two independently owned libraries understandable, comparable, versioned, and safe to combine—while leaving rekordbox and each DJ's source collection intact.
