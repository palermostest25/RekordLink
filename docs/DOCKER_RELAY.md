# Running the RekordLink relay with Docker

This guide covers direct/private networking. For a public hostname that requires no VPN or client-side `cloudflared`, use the [Cloudflare public relay guide](CLOUDFLARE_RELAY.md).

This deployment runs only the always-on relay in a container. rekordbox and each DJ's RekordLink join agent continue to run directly on their laptops.

```text
DJ A join agent ──TLS──► Docker relay ◄──TLS── DJ B join agent
                            │
                     persistent /data
```

The relay accepts exactly two clients. It persists its room identity, pinned TLS certificate, latest XML snapshots, resumable transfer state, and content-addressed audio in `/data`. Both laptops can therefore synchronize at different times.

## Security boundary

The client-to-relay connection is TLS 1.3 with certificate pinning, but v0.4.5 does not end-to-end encrypt objects stored by the relay. XML metadata and authorized audio are plaintext inside the Docker volume. Do not deploy this as a public multi-tenant service.

The supplied Compose configuration:

- runs as UID/GID `10001`, not root;
- drops every Linux capability;
- enables `no-new-privileges`;
- makes the container root filesystem read-only;
- keeps all durable writes in `/data`;
- includes an HTTPS health check;
- restarts unless explicitly stopped;
- keeps the pairing invite out of normal container logs.

Anyone who can administer the Docker host or volume can read the library and audio. Protect the host, Docker socket, backups, and volume accordingly.

## 1. Choose the reachable address

The two important addresses are:

- `REKORDLINK_ADVERTISE`: the stable IP or DNS name embedded in the private invite. Both laptops must be able to reach it.
- `REKORDLINK_BIND_IP`: the relay server's host interface on which Docker publishes TCP 9777.

For Tailscale, use the server's Tailscale IPv4 address for both:

```dotenv
REKORDLINK_ADVERTISE=100.100.20.30
REKORDLINK_BIND_IP=100.100.20.30
```

For public Internet access, do not bind the private listener to `0.0.0.0`. Use the separate Cloudflare deployment, which publishes no origin port and advertises a standard HTTPS URL.

Keep the advertised address stable. Changing it later requires distributing an updated invite and updating both join-service configurations.

## 2. Create the environment file

From the repository root:

```bash
cp .env.example .env
```

Edit `.env` and set the two addresses. The default configuration enables audio relay and suppresses the private invite in Docker logs:

```dotenv
REKORDLINK_ADVERTISE=100.100.20.30
REKORDLINK_BIND_IP=100.100.20.30
REKORDLINK_VERSION=0.4.5
REKORDLINK_METADATA_ONLY=false
REKORDLINK_LOG_INVITE=false
```

`.env` is excluded from the Docker build context and should not be committed.

## 3. Build and start

```bash
docker compose build --pull
docker compose up -d
docker compose ps
```

Wait until `docker compose ps` reports the relay as `healthy`. The first launch generates the room, pinned certificate, and invite in the persistent volume.

Follow operational logs with:

```bash
docker compose logs --follow relay
```

With the supplied defaults, the full invite is not written to those logs.

## 4. Retrieve the private invite

After the container is healthy:

```bash
docker compose exec relay cat /data/invite.txt
```

The result begins with `rekordlink://join/`. Send the complete value privately to both DJs. It contains the room pairing secret and, in this direct mode, the TLS certificate pin.

Do not post the invite in a ticket, public chat, Compose file, or container environment variable. The invite file is mode `0600` inside the volume.

## 5. Connect both DJ laptops

On each laptop, open the RekordLink dashboard and choose **join host or relay**. Configure that laptop's own:

- DJ name;
- rekordbox XML Auto Export input;
- `rekordlink-shared.xml` output;
- managed audio directory;
- copied relay invite.

Audio must be enabled on both laptop clients because the default Docker relay has audio enabled. The equivalent command on each laptop is:

```bash
rekordlink service install join \
  --name "DJ A" \
  --library "/absolute/path/rekordbox-auto-export.xml" \
  --output "/absolute/path/rekordlink-shared.xml" \
  --audio-root "/absolute/path/RekordLink Audio" \
  --allow-audio-copy \
  --invite 'rekordlink://join/PASTE_PRIVATE_INVITE'
```

Change the name and local paths for DJ B, but use the same invite. The first client can publish immediately; complete merged output becomes available after both client slots have registered and published.

## Persistent data

The Compose file uses the named volume `rekordlink_relay-data`. Normal recreation and this command preserve it:

```bash
docker compose down
```

Do not add `--volumes` unless you intentionally want to destroy the room identity, client registration, snapshots, and relayed audio. Losing the volume means creating a new room and pairing both laptops again.

For a bind-mounted data directory instead of a named volume, replace the volume mapping in `compose.yaml` with:

```yaml
volumes:
  - /srv/rekordlink-relay:/data
```

The host directory must be writable only by the intended administrator and by container UID/GID `10001`. It may need to be prepared before startup:

```bash
sudo install -d -m 0700 -o 10001 -g 10001 /srv/rekordlink-relay
```

Use encrypted, access-controlled backups. The data includes complete audio files and private room material.

## Updates

When building locally from a newer source release:

```bash
docker compose build --pull
docker compose up -d
```

The container is replaced, but the `/data` volume and paired room survive. Confirm `healthy` after every upgrade.

Tagged releases can also publish multi-architecture `linux/amd64` and `linux/arm64` images through the included GitHub Container Registry workflow. Set the `image:` entry in `compose.yaml` to the repository's published GHCR name if using that path instead of a local build.

## Metadata-only relay

Set this in `.env`:

```dotenv
REKORDLINK_METADATA_ONLY=true
```

Then recreate the container. Both laptop join configurations must omit `--audio-root` and `--allow-audio-copy`. Audio mode is part of the invite and must agree on every participant.

## Direct `docker run` alternative

Build the image:

```bash
docker build --build-arg VERSION=0.4.5 -t rekordlink-relay:0.4.5 .
```

Start it on a VPN interface:

```bash
docker run -d \
  --name rekordlink-relay \
  --restart unless-stopped \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /tmp:size=16m,mode=1777 \
  -p 100.100.20.30:9777:9777/tcp \
  -e REKORDLINK_ADVERTISE=100.100.20.30 \
  -v rekordlink-relay-data:/data \
  rekordlink-relay:0.4.5
```

Retrieve its invite with:

```bash
docker exec rekordlink-relay cat /data/invite.txt
```

## Environment reference

| Variable | Default | Meaning |
|---|---|---|
| `REKORDLINK_ADVERTISE` | required | Stable VPN IP, public IP, or DNS name placed in the invite |
| `REKORDLINK_LISTEN` | `:9777` | Address inside the container; normally leave unchanged |
| `REKORDLINK_STATE` | `/data` | Persistent state path inside the container |
| `REKORDLINK_METADATA_ONLY` | `false` | Disable audio storage and transfer when `true` |
| `REKORDLINK_LOG_INVITE` | `false` | Print the secret invite in logs when deliberately set to `true` |
| `REKORDLINK_VERSION` | `0.4.5` | Local image build/tag version used by Compose |

Explicit Docker command arguments override the environment-driven launch. For example:

```bash
docker run --rm rekordlink-relay:0.4.5 version
```

## Troubleshooting

### The container exits immediately

`REKORDLINK_ADVERTISE` is required. Check `.env` and then run:

```bash
docker compose logs relay
```

### The volume reports permission denied

Named volumes should inherit the `/data` ownership from the image. For bind mounts, make the host directory writable by UID/GID `10001` as shown above.

### The container is healthy but laptops cannot connect

Confirm that:

- `REKORDLINK_ADVERTISE` is reachable from both laptops;
- `REKORDLINK_BIND_IP` exists on the Docker host;
- TCP 9777 is allowed by the host firewall or VPN policy;
- the Docker port mapping is present;
- both laptops use the latest invite generated for that address.

### A third or reinstalled client cannot join

The room deliberately accepts exactly two client registrations. Preserve each laptop's RekordLink join-state directory when reinstalling. Replacing a registered laptop currently requires creating a fresh relay room and pairing both DJs again.

### Imported playlists keep nesting or multiplying

Upgrade both laptop clients to v0.4.5 and preferably rebuild the relay at v0.4.5. Restart the clients, close rekordbox once on both laptops, and wait for fresh `READY` receipts. Then remove the old imported top-level `RekordLink` playlist folders from rekordbox, refresh Bridge, and import the new top-level folder once. Current clients exclude that managed folder from outgoing snapshots, while a current relay also filters retained older snapshots.

### The join client reports an audio-mode mismatch

The relay and both clients must agree. With `REKORDLINK_METADATA_ONLY=false`, both clients require an audio root and audio-copy confirmation. With it set to `true`, both clients must omit those options.

## Implementation references

The image follows Docker's recommended multi-stage separation between the Go build environment and the smaller runtime image. Sources: [Docker multi-stage builds](https://docs.docker.com/build/building/multi-stage/), [Docker build best practices](https://docs.docker.com/build/building-best-practices/). The release workflow follows GitHub's documented GHCR publishing pattern: [Publishing Docker images](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images).
