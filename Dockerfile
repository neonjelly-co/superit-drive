# One image holding one static binary, the interface compiled into it. It starts with only environment variables and
# a member list file (see the README for both).
#
#   docker build -t superit-drive:dev .
#
# The first two stages run on the machine that builds (BUILDPLATFORM) and cross-compile, so building for both x86 and
# arm64 needs no emulation.

FROM --platform=$BUILDPLATFORM oven/bun:1.3.6-alpine AS interface
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=interface /src/web/dist ./web/dist
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/superit-drive ./cmd/superit-drive \
 && mkdir -p /out/data /out/tmp && chmod 1777 /out/tmp

FROM scratch
# Certificates to check the storage's HTTPS; a /tmp the Drive's user can write, where SQLite puts a temporary file when
# one change or one sort is too large to keep in memory (COPY copies what is in a folder, not the folder itself, so the
# folder made here gets its owner from --chown); /data for the index.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/tmp /tmp
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/superit-drive /superit-drive
USER 65532:65532
ENV DRIVE_DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/superit-drive"]
