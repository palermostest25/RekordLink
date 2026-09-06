# Public RekordLink relay with Cloudflare Tunnel

This deployment gives both DJs the intended client experience:

```text
RekordLink on DJ A ─┐
                    ├─ normal HTTPS ─ rekordlink.zeusyboy.com ─ Cloudflare Tunnel ─ relay
RekordLink on DJ B ─┘
```

Neither DJ installs a VPN, WARP, or `cloudflared`. They install RekordLink once, paste the private room invite, and its background service reconnects automatically. Only the always-on Docker server runs `cloudflared`.

Public relay mode is different from RekordLink's direct mode. The client validates the public hostname using the operating system CA store instead of pinning the relay's private certificate. The relay still requires the one-time pairing code, admits exactly two clients, and then authenticates every library and audio request with that client's 256-bit credential.

Audio and XML uploads use independent 4 MiB requests with durable server offsets. Downloads use 4 MiB HTTP byte ranges. An interrupted transfer resumes from its last stored byte; the merged XML remains unchanged until all referenced audio has passed SHA-256 verification locally.

## Requirements

- A domain using Cloudflare DNS. The examples use `rekordlink.zeusyboy.com`.
- An always-on Linux machine with Docker Compose and outbound Internet access.
- A remotely managed Cloudflare Tunnel.
- Enough persistent disk for both libraries and their authorized audio.

Cloudflare documents public application routes as hostname-to-local-service mappings and provides a Docker command/token for a remotely managed tunnel. Its Free and Pro request-body limit is 100 MB, which is why RekordLink never sends a full audio file as one proxied request. Sources: [Cloudflare Tunnel setup](https://developers.cloudflare.com/tunnel/setup/), [Cloudflare upload limits](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/4xx-client-error/error-413/).

## 1. Create the Tunnel and hostname

In the Cloudflare dashboard:

1. Open **Networking → Tunnels**.
2. Create a remotely managed tunnel named `rekordlink`.
3. Choose the Docker environment and copy the tunnel token from the generated command. The token is the long value following `--token`.
4. Add a **Published application** route.
5. Set the hostname to `rekordlink.zeusyboy.com`.
6. Set the service type to **HTTP** and the service URL to `http://relay:9777`.
7. Save the route.

`relay` is the Docker Compose service name. The origin is deliberately not published on the host. The local Docker hop is HTTP, while the laptop-to-Cloudflare connection and Cloudflare Tunnel connection are encrypted.

Do not put a browser-based Cloudflare Access policy in front of this hostname: a background RekordLink agent cannot complete an interactive browser challenge. Do not configure a Cache Everything rule for it. RekordLink marks every authenticated response `private, no-store`.

## 2. Create the private server files

From the RekordLink source/release directory:

```bash
cp .env.cloudflare.example .env.cloudflare
```

Edit `.env.cloudflare` if the hostname is different. Create `.cloudflare-tunnel-token` beside it and paste only the tunnel token into that file. Protect both files:

```bash
chmod 600 .env.cloudflare .cloudflare-tunnel-token
```

Both names are ignored by Git. The token is mounted into the `cloudflared` container as a read-only Docker Compose secret rather than appearing in the Compose command. Anyone with this token can run a connector for the Tunnel, so rotate it in Cloudflare if it is disclosed. [Cloudflare tunnel tokens](https://developers.cloudflare.com/tunnel/advanced/tunnel-tokens/)

## 3. Start the relay

```bash
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml build --pull
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml up -d
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml ps
```

The `relay` service should become healthy and the `tunnel` service should remain running. Inspect startup problems with:

```bash
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml logs relay tunnel
```

Verify the public path:

```bash
curl https://rekordlink.zeusyboy.com/healthz
```

It should return JSON containing `"ok":true`. If Cloudflare returns `502`, confirm that the published application's service URL is exactly `http://relay:9777` and both containers were started by the same Compose project.

No inbound router or firewall rule is needed. Cloudflare Tunnel makes outbound connections from the server; Cloudflare documents port `7844` as the required outbound tunnel port. [Tunnel firewall requirements](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/)

## 4. Retrieve and distribute the invite

```bash
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml exec relay cat /data/invite.txt 
```

Send the complete `rekordlink://join/...` value privately to both DJs. The invitation embeds `https://rekordlink.zeusyboy.com`, the room identity, its pairing secret, audio mode, and the public-TLS transport mode. It does not contain the Cloudflare Tunnel token.

## 5. Connect each laptop

On each laptop:

1. Install and open RekordLink.
2. Select **DJ B — join host or relay**. Both relay participants use this option.
3. Choose that DJ's rekordbox XML Auto Export file.
4. Choose the merged `rekordlink-shared.xml` output and managed audio folder.
5. Paste the same private invitation.
6. Select **Save and run in background**.

That is the entire client-side network setup. RekordLink uses normal outbound HTTPS on port 443 and reconnects automatically after login, network changes, sleep, Tunnel restarts, and transient transfer failures.

## Updating an existing private relay

The same persistent relay volume can be reused. Start it with `compose.cloudflare.yaml`, retrieve the newly generated public invitation, and paste that invitation into both existing join configurations. If their join-state directories were preserved, their existing client credentials remain valid because the room ID is unchanged.

## Operations and limits

- Back up the `rekordlink-cloudflare_relay-data` Docker volume. It contains the room identity, client credentials, snapshots, resumable transfer state, and audio.
- XML and audio are plaintext on the relay disk. Use full-disk encryption and restrict Docker administration.
- The room intentionally accepts exactly two registered clients. Preserve each laptop's RekordLink state when reinstalling.
- Do not use the hostname as a general file download service. It is an authenticated, private two-person synchronization relay.
- Cloudflare documents fixed proxy timeouts. RekordLink's short resumable requests avoid depending on a multi-hour proxy connection. [Cloudflare connection limits](https://developers.cloudflare.com/fundamentals/reference/connection-limits/)
- Cloudflare notes plan/service considerations for serving large files over public hostname routes. Review the current terms for the expected size and frequency of your duo's private audio transfers. [Cloudflare Tunnel routing](https://developers.cloudflare.com/tunnel/routing/)

To stop the stack without deleting its room:

```bash
docker compose --env-file .env.cloudflare -f compose.cloudflare.yaml down
```

Do not add `--volumes` unless you intentionally want to destroy the room and pair both laptops again.

### The client logs `updated merged library` every scan interval

Upgrade that laptop to RekordLink v0.4.4 or newer. Some reverse-proxy paths can weaken, remove, or fail to forward an HTTP ETag validator. Current clients normalize weak validators, fall back to the authenticated room revision when conditional requests are ineffective, and do not rewrite byte-identical merged XML. This client fix remains compatible with v0.4.1 relay servers.

### Imported playlists keep nesting or multiplying

Upgrade both laptop clients to v0.4.5 and restart their background services. Upgrade the relay too for defense in depth; an older relay remains protocol-compatible if both clients are current. Close rekordbox on both laptops once so XML Auto Export creates fresh inputs, then wait for both dashboards to report `READY`.

In rekordbox, remove the old imported top-level `RekordLink` playlist folder (and any numbered copies), refresh the Bridge XML, and drag the refreshed top-level `RekordLink` folder in once. Keep that wrapper name and treat it as replace-only managed output. v0.4.5 excludes managed roots from publication, so a shared tree can no longer feed back into the next shared tree. Collection tracks and audio files are not removed by this filter; byte-identical audio remains stored once per content hash.
