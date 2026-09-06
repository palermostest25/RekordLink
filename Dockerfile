# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/root/.cache/go-build \
    go test ./...
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" \
    -o /out/rekordlink ./cmd/rekordlink

FROM alpine:3.23 AS runtime

ARG VERSION=dev
LABEL org.opencontainers.image.title="RekordLink Relay" \
	  org.opencontainers.image.description="Two-DJ rekordbox library and authorized-audio relay" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.licenses="MIT"

RUN addgroup -S -g 10001 rekordlink \
    && adduser -S -D -H -u 10001 -G rekordlink rekordlink \
    && mkdir -p /data \
    && chown rekordlink:rekordlink /data

COPY --from=build --chmod=0555 /out/rekordlink /usr/local/bin/rekordlink
COPY --chmod=0555 docker/entrypoint.sh /usr/local/bin/rekordlink-entrypoint

USER 10001:10001
WORKDIR /data
VOLUME ["/data"]
EXPOSE 9777/tcp

ENV REKORDLINK_LISTEN=":9777" \
    REKORDLINK_STATE="/data" \
    REKORDLINK_METADATA_ONLY="false" \
    REKORDLINK_LOG_INVITE="false"

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD if [ -n "$REKORDLINK_PUBLIC_ENDPOINT" ]; then scheme=http; else scheme=https; fi; wget --no-check-certificate --quiet --output-document=- "$scheme://127.0.0.1:9777/healthz" | grep --quiet '"ok":true' || exit 1

STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/rekordlink-entrypoint"]
