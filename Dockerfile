# The web app is built with Node.js here, in its own stage; the image that
# ships has only the Go binary, with the app embedded (build tag "ui").
FROM --platform=$BUILDPLATFORM node:24.22.1-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --ignore-scripts --no-audit
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/ui/dist ./internal/ui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags ui -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/eyrie ./cmd/eyrie && \
    mkdir /out/data

FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev
# Where the source is, for published images (github-image sets it).
ARG SOURCE_URL=""
LABEL org.opencontainers.image.title="Eyrie" \
      org.opencontainers.image.description="Self-hosted uptime and infrastructure monitor" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="${SOURCE_URL}"
COPY --from=build /out/eyrie /eyrie
# /data belongs to the image's non-root user, so a new volume does too.
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/eyrie", "healthcheck"]
ENTRYPOINT ["/eyrie"]
