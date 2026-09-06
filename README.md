# RekordLink

RekordLink keeps two DJs' rekordbox libraries and authorized local audio synchronized in the background. Each source library remains untouched; both DJs receive a complete, requester-local merged XML library whose referenced synchronized audio is guaranteed to exist before the output becomes visible.

> RekordLink is independent software. It is not affiliated with, endorsed by, or supported by AlphaTheta Corporation or Pioneer DJ.

## What v0.4.5 does

- Watches rekordbox XML Auto Export output in the background.
- Merges both complete playlist trees under `RekordLink / <DJ name>`.
- Treats the generated top-level `RekordLink` folder as replace-only derived output, preventing an imported shared tree from being published back and nested again.
- Copies referenced local audio into a managed library using SHA-256 content identity.
- Uploads audio before publishing its track metadata.
- Verifies every downloaded byte and writes merged XML only after all synchronized audio exists locally.
- De-duplicates byte-identical audio regardless of filename, tags, or source path.
- Supports direct two-laptop rooms and symmetric, always-on relay rooms.
- Uses certificate-pinned TLS for direct rooms and system-verified HTTPS for public relays, plus twelve-digit invite pairing, rate limits, and 256-bit bearer credentials.
- Resumes interrupted XML and audio transfers in proxy-safe 4 MiB chunks.
- Installs as a macOS LaunchAgent, Linux systemd user service, or Windows login task.
- Includes a cross-platform local management dashboard and a native macOS menu-bar app.
- Ships a hardened, persistent Docker deployment for the always-on relay on `linux/amd64` and `linux/arm64`.
- Never reads or writes rekordbox's encrypted `master.db`.

## Open the management UI

On macOS, unzip the universal release and open `RekordLink.app`. It runs as a menu-bar app (without a Dock icon) and provides **Open Dashboard**, **Start**, **Stop**, and **Restart** commands. Quitting the menu-bar UI does not stop an installed background sync service.

On macOS, Windows, or Linux, the same dashboard is available from the CLI:

```bash
rekordlink ui
```

The dashboard provides the complete first-run setup, native file/folder selection where the operating system supports it, service controls, private invite copying for hosts/relays, recent logs, and the current `READY`/`INCOMPLETE` library receipt. It listens only on the local loopback interface; it is not a remote administration console.

## The seamlessness boundary

AlphaTheta provides two official mechanisms that make the safe workflow possible:

1. rekordbox can automatically export its collection XML when it closes. RekordLink detects that file change and synchronizes it without another export command.
2. rekordbox 7 Professional Collaborative Playlist can update shared playlist order, cues, grids, and track metadata while rekordbox is running.

RekordLink cannot force a running rekordbox process to refresh or commit an imported XML file because AlphaTheta publishes no transactional desktop-library API. The reliable workflow is therefore:

- **While rekordbox is open:** use its native Collaborative Playlist for live shared-set edits, if available.
- **When rekordbox closes:** XML Auto Export captures the complete library; RekordLink automatically converges both full snapshots and their local audio.
- **On the next rekordbox launch:** the merged Bridge XML and all audio are ready locally. Dragging a new RekordLink playlist into the collection is still the one action rekordbox itself controls.

This is automatic eventual synchronization, not unsupported live mutation of an open database.

## One-time rekordbox setup

On both laptops in rekordbox 7:

1. Open **Preferences → Advanced → Others**.
2. Under **Tribe XR**, enable **XML Auto Export**.
3. Choose a stable XML destination. Despite the older Dropbox-oriented label, this is the file RekordLink watches.
4. Open **Preferences → Bridge** and choose `rekordlink-shared.xml` as the imported library.
5. Back up the current rekordbox library before the first merge.

AlphaTheta documents that XML Auto Export runs when rekordbox closes. It does not publish each keystroke while the application is open.

### Import without creating duplicates

The top-level playlist folder named `RekordLink` is managed output. Keep each DJ's original editable playlists outside that folder and treat the imported folder as read-only.

For the first import, refresh the Bridge XML and drag the **top-level `RekordLink` folder once** into rekordbox's Playlists tree. For a later refresh, remove the previously imported `RekordLink` playlist folder first, then drag the refreshed top-level folder in once. Removing that playlist folder does not remove tracks from Collection or delete audio files, but local edits made only inside that generated folder will be lost; make shared edits in an original playlist instead.

RekordLink excludes `RekordLink`, `RekordLink (1)`, `RekordLink (2)`, and equivalent numeric copies from every outgoing snapshot. This stops cross-computer feedback even if an old imported copy is still present when rekordbox Auto Export runs. Do not rename the generated folder or drag only an inner DJ folder into the top level: the documented XML format provides no hidden provenance field with which RekordLink could safely distinguish that renamed copy from a genuine user playlist.

When upgrading from v0.4.4 or earlier, install v0.4.5 on both laptops, restart both services, close rekordbox once on both computers, and wait for a new `READY` receipt. Then replace any accumulated imported `RekordLink` folders with one fresh copy from Bridge. An upgraded v0.4.5 relay adds defense in depth, although the protocol remains compatible with older relays when both clients are upgraded.

## Direct room: one laptop hosts

Both DJs must have the legal right to make the copies. Audio transfer is impossible to enable accidentally: both `--audio-root` and `--allow-audio-copy` are required.

On DJ A's laptop:

```bash
rekordlink host \
  --name "DJ A" \
  --library "/absolute/path/rekordbox-auto-export.xml" \
  --output "/absolute/path/rekordlink-shared.xml" \
  --audio-root "/absolute/path/RekordLink Audio" \
  --allow-audio-copy
```

Send the printed `rekordlink://join/...` invite privately to DJ B. If the wrong network interface is chosen, add `--advertise 192.168.1.20` or a private VPN address.

On DJ B's laptop:

```bash
rekordlink join \
  --name "DJ B" \
  --library "/absolute/path/rekordbox-auto-export.xml" \
  --output "/absolute/path/rekordlink-shared.xml" \
  --audio-root "/absolute/path/RekordLink Audio" \
  --allow-audio-copy \
  --invite 'rekordlink://join/PASTE_INVITE_HERE'
```

The direct host must be online and reachable while changes synchronize. A private VPN is preferable to router port forwarding.

## Always-on relay: different locations

### Docker deployment

Docker is the recommended way to operate the relay on an always-on Linux server.

For the simplest Internet setup, publish `rekordlink.zeusyboy.com` through the supplied Cloudflare Tunnel stack. Only the server runs `cloudflared`; the two laptops need only RekordLink and make normal outbound HTTPS connections:

```bash
cp .env.cloudflare.example .env.cloudflare
# Put the Cloudflare Tunnel token in .cloudflare-tunnel-token.
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml up -d --build
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml exec relay cat /data/invite.txt
```

Follow the complete [Cloudflare public relay guide](docs/CLOUDFLARE_RELAY.md). It covers the exact dashboard route, private token file, verification, laptop pairing, upgrades, and security boundary.

For a private VPN-bound relay instead, use the original Compose file:

```bash
cp .env.example .env
# Edit REKORDLINK_ADVERTISE and REKORDLINK_BIND_IP in .env.
docker compose build --pull
docker compose up -d
docker compose ps
```

When the container is healthy, retrieve the private two-client invite:

```bash
docker compose exec relay cat /data/invite.txt
```

The container runs non-root with no Linux capabilities, a read-only root filesystem, a health check, and a persistent Docker volume for the room identity, snapshots, and audio. The invite is not printed in normal container logs. Read the [Docker relay deployment guide](docs/DOCKER_RELAY.md) for private/direct deployment details.

### Native relay process

Run a relay on an always-on private server or VPN node:

```bash
rekordlink relay --listen :9777 --advertise 100.64.0.10
```

Send the printed invite to both DJs. Both use the `join` command above with separate state and audio-root directories. The relay accepts exactly two DJs and stores content-addressed audio plus library snapshots on its private disk.

The relay currently provides pinned TLS transport but not end-to-end encryption from one DJ to the other. Operate it only on infrastructure controlled by the duo, preferably behind WireGuard/Tailscale. Do not expose it as an unreviewed public multi-tenant service.

## Keep RekordLink running after login

First run the normal `host`, `join`, or `relay` command interactively and verify one successful merge. Stop it with Ctrl-C, then install the same command as a per-user background service:

```bash
rekordlink service install host \
  --name "DJ A" \
  --library "/absolute/path/rekordbox-auto-export.xml" \
  --output "/absolute/path/rekordlink-shared.xml" \
  --audio-root "/absolute/path/RekordLink Audio" \
  --allow-audio-copy
```

For the second DJ, replace `host` and its options with the complete `join` command, including the invite. RekordLink saves service configuration with owner-only permissions.

```bash
rekordlink service status
rekordlink service uninstall
```

The installer copies the engine into a stable, private per-user application-data location, then uses LaunchAgents on macOS, user services on Linux, or Task Scheduler on Windows. This prevents moving or deleting an extracted release archive from breaking the service. Use absolute library/audio paths because login services do not inherit an interactive shell's working directory.

All bulk client audio storage follows the **Managed audio folder** selected in the dashboard. Content-addressed upload objects live below `.rekordlink/blobs`, and rekordbox-facing synchronized files live below `.rekordlink/library`. When the filesystem supports hard links, those two paths refer to the same immutable bytes and do not consume duplicate file data. Application Support contains only the executable, configuration, room credentials, receipts, and logs.

On the first v0.4.3-or-newer start, RekordLink verifies every completed object in the legacy Application Support blob cache, moves it to the managed audio folder, and removes the legacy cache only after migration succeeds. Partial transfers may restart from the authoritative peer when the folders are on different disks.

### macOS reports `operation not permitted` for audio

macOS privacy controls can deny a background LaunchAgent access to protected locations such as `~/Library/Messages/Attachments`, even when the signed-in user owns the file. Prefer moving the audio into a normal managed music folder and using rekordbox's **Relocate** command so its XML points to the durable copy. If the protected location must remain, grant **Full Disk Access** to the installed engine at `~/Library/Application Support/RekordLink/bin/rekordlink`, then restart RekordLink from the dashboard.

RekordLink treats an unreadable source as a per-track warning: the track's metadata continues to synchronize and the rest of the library is not interrupted, but that track cannot be copied or reported audio-ready until the source becomes readable.

## Data guarantees

RekordLink follows a fail-closed publication order:

```text
read valid XML
→ hash and durably cache referenced audio
→ upload missing audio
→ server validates each SHA-256
→ publish metadata snapshot
→ peer downloads and validates missing audio
→ materialize requester-local paths
→ atomically replace merged XML
```

If power, disk, or network failure interrupts any stage, the previous known-good merged XML remains in place. Source XML and original music files are never modified or deleted. Missing source files are reported and remain metadata-only instead of blocking unrelated tracks.

Every successful output has a mode-`0600` receipt at `rekordlink-shared.xml.status.json`. Verify the XML hash and re-check all referenced local audio at any time:

```bash
rekordlink status --output "/absolute/path/rekordlink-shared.xml"
```

It reports `READY` only when the receipt matches the current XML and every local audio reference exists. A missing file returns `INCOMPLETE` and a non-zero exit code.

## Commands

```text
rekordlink host       Direct room owner and local participant
rekordlink join       Join a direct or relay room
rekordlink relay      Always-on symmetric two-DJ relay
rekordlink ui         Cross-platform local management dashboard
rekordlink inspect    Validate and summarize rekordbox XML
rekordlink merge      Offline, metadata-only library merge
rekordlink service    Install, start, stop, restart, inspect, or remove background operation
rekordlink version    Print release version
```

## Build and verify

Go 1.24 or newer is required.

```bash
make test
make vet
make package VERSION=0.4.5
make docker-build VERSION=0.4.5
```

Release archives and checksums are written to `dist/` for macOS Apple Silicon and Intel, Linux x86-64, Windows x86-64, and a self-contained Docker build-context archive. When packaging on macOS, the build also produces a universal `RekordLink.app` containing both Apple Silicon and Intel executables.

## Important limits

- rekordbox XML does not contain every private database field or analysis artifact.
- Imported XML still requires rekordbox's Bridge workflow; RekordLink does not simulate clicks.
- Cue/grid conflicts for the same recording default to the requesting DJ's version. Use native Collaborative Playlist for live co-editing.
- Streaming-service cache files and DRM-protected downloads are never made portable.
- Audio is immutable inside the managed `.rekordlink/library` tree; edit tags on the original, then let the next export create a new content version.
- Public relay transfers use resumable 4 MiB chunks; direct mode retains legacy whole-object compatibility.
- The first full-library synchronization can require substantial time, bandwidth, and additional local/relay storage.

Read the [research and architecture report](docs/RESEARCH_AND_ARCHITECTURE.md) and [security guidance](SECURITY.md) before using a working performance library.

## License

MIT. Audio and metadata remain subject to their own licences and applicable law. `--allow-audio-copy` is an operator confirmation, not a grant of rights.
