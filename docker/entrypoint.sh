#!/bin/sh
set -eu

rekordlink_bin="${REKORDLINK_BIN:-/usr/local/bin/rekordlink}"

# Explicit arguments take precedence over environment configuration. Flags are
# treated as relay flags; a named RekordLink command is passed through as-is.
if [ "$#" -gt 0 ]; then
    case "$1" in
        host|join|relay|service|ui|inspect|status|merge|version|help|--version|-v|--help|-h)
            exec "$rekordlink_bin" "$@"
            ;;
        *)
            exec "$rekordlink_bin" relay "$@"
            ;;
    esac
fi

advertise="${REKORDLINK_ADVERTISE:-}"
public_endpoint="${REKORDLINK_PUBLIC_ENDPOINT:-}"
listen="${REKORDLINK_LISTEN:-:9777}"
state="${REKORDLINK_STATE:-/data}"
metadata_only="${REKORDLINK_METADATA_ONLY:-false}"
log_invite="${REKORDLINK_LOG_INVITE:-false}"

if [ -z "$advertise" ] && [ -z "$public_endpoint" ]; then
    echo "rekordlink container: set REKORDLINK_PUBLIC_ENDPOINT for an HTTPS tunnel or REKORDLINK_ADVERTISE for direct TLS" >&2
	exit 64
fi

if [ -n "$advertise" ] && [ -n "$public_endpoint" ]; then
    echo "rekordlink container: REKORDLINK_PUBLIC_ENDPOINT and REKORDLINK_ADVERTISE are mutually exclusive" >&2
    exit 64
fi

case "$metadata_only" in
    1|true|TRUE|yes|YES)
        metadata_only=true
        ;;
    0|false|FALSE|no|NO)
        metadata_only=false
        ;;
    *)
        echo "rekordlink container: REKORDLINK_METADATA_ONLY must be true or false" >&2
        exit 64
        ;;
esac

case "$log_invite" in
    1|true|TRUE|yes|YES)
        log_invite=true
        ;;
    0|false|FALSE|no|NO)
        log_invite=false
        ;;
    *)
        echo "rekordlink container: REKORDLINK_LOG_INVITE must be true or false" >&2
        exit 64
        ;;
esac

set -- relay \
	--listen "$listen" \
	--state "$state" \
	--show-invite="$log_invite"

if [ -n "$public_endpoint" ]; then
    set -- "$@" --public-endpoint "$public_endpoint"
else
    set -- "$@" --advertise "$advertise"
fi

if [ "$metadata_only" = true ]; then
    set -- "$@" --metadata-only
fi

exec "$rekordlink_bin" "$@"
