# The web app is built with Node.js here, in its own stage; the image that
# ships has only the Go binary, with the app embedded (build tag "ui").
FROM --platform=$BUILDPLATFORM node:26.12.1-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --ignore-scripts --no-audit
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG SOURCE_URL=""
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/ui/dist ./internal/ui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags ui -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.sourceURL=${SOURCE_URL}" -o /out/eyrie ./cmd/eyrie && \
    mkdir /out/data

# Alpine, for the C library (musl) the Claude Code binary that
# EYRIE_CLAUDE_CODE=true downloads at start needs; the Go binary is static.
FROM alpine:3
ARG VERSION=dev
# Where the source is, for published images (github-image sets it).
ARG SOURCE_URL=""
LABEL org.opencontainers.image.title="Eyrie" \
      org.opencontainers.image.description="Self-hosted uptime and infrastructure monitor" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="${SOURCE_URL}" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later"
# alpine:3 is the newest Alpine release, and apk upgrade adds every fix
# published since the image was built. The base already has the CA
# certificates HTTPS checks use.
# Runs as root, as WAN Drawbridge does: the mounted Docker socket is only
# writable by root or the host's docker group, whose ID differs between
# hosts.
RUN apk upgrade --no-cache
COPY --from=build /out/eyrie /eyrie
COPY --from=build /out/data /data
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/eyrie", "healthcheck"]
ENTRYPOINT ["/eyrie"]
