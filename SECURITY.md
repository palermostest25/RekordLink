# Security policy

## Supported release

Security fixes currently target the latest v0.x release only.

## Report a vulnerability

Do not open a public issue containing a pairing invite, token, filesystem path, library XML, or exploit details. Send a private report to the release maintainer with:

- affected version and operating system;
- reproduction steps using synthetic library data;
- security impact;
- suggested mitigation, if known.

Replace this section with a monitored security email before publishing the repository.

## Deployment guidance

- Use direct mode only on a trusted private network. Do not expose its certificate-pinned TCP 9777 listener directly to the public Internet.
- For an Internet relay, use public HTTPS mode behind the supplied Cloudflare Tunnel deployment. It publishes no origin port and accepts application data only after room authentication.
- Treat the complete `rekordlink://` invite as a secret.
- Delete the host state directory to create a new room and certificate if an invite or token leaks. Back it up first if the existing room must be recoverable.
- Protect the operating-system account and disk; RekordLink stores host, client, and background-service secrets in mode-`0600` files, not in the OS keychain.
- Audio synchronization is opt-in and content is plaintext on each laptop and relay disk. Use full-disk encryption and infrastructure controlled by the duo.
- Enable audio copying only when both DJs have the legal right to make every copy. RekordLink does not transfer recognized streaming caches or bypass DRM.
- Do not operate the two-person relay as a generic or multi-tenant public file service.

## Threat-model boundary

RekordLink protects against passive network observation, host impersonation after the invite was shared, path traversal through blob identifiers, incomplete output publication, and accidental/corrupt transfer through authenticated TLS, validated SHA-256 paths, bounded requests, and verified atomic files. Direct mode pins its private certificate. Public relay mode requires a publicly trusted HTTPS hostname and deliberately refuses redirects. It does not protect a compromised laptop or relay, a person who obtained the complete invite before pairing, malware running as the same OS user, traffic analysis, or a malicious authorized peer. The self-hosted relay is not designed as a public multi-tenant service and does not provide end-to-end encryption at rest.

## Management UI

The management dashboard binds only to an IPv4 or IPv6 loopback address. Non-loopback listen addresses are rejected. It also rejects non-loopback HTTP `Host` values, requires an in-memory 256-bit request token on every mutating request, caps request bodies, disables caching, and sends a restrictive Content Security Policy. The page has no CDN, telemetry, analytics, or external assets.

Any process running as the same operating-system user can already read that user's RekordLink service configuration and files. The loopback UI is designed for that same-user trust boundary; it is not intended to be exposed through a proxy or port forward.

The join invite remains redacted when configuration is read into the UI. Leaving the invite field blank retains the existing private value. Host and relay invites are stored as mode-`0600` files and are returned only by the token-protected local “Copy invite” action. Background operation logs the invite's private path, not the invite itself.

## Docker relay

The supplied relay container runs as UID/GID `10001`, drops all Linux capabilities, enables `no-new-privileges`, uses a read-only root filesystem, and writes durable data only to `/data`. Its health check accesses only the local container endpoint. The pairing invite is not emitted to logs unless `REKORDLINK_LOG_INVITE=true` is deliberately configured.

The Docker volume contains the room certificate and identity, client registrations, XML snapshots, and synchronized audio. It is sensitive, is not encrypted by RekordLink, and must be protected and backed up accordingly. Access to the Docker daemon is effectively administrative access to that data.

The private Compose stack publishes TCP 9777 only on the explicitly configured interface. The Cloudflare Compose stack publishes no host port: only its `cloudflared` container can reach the relay origin. Never publish the loopback management UI through Docker or a reverse proxy.
