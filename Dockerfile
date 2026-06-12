# From-source build: `docker build -t lookout .`
# (Release images are built by GoReleaser from prebuilt binaries via
# Dockerfile.release; this file is the standalone equivalent.)

# Stage 1: Build the binary from source.
FROM golang:1.26.4-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lookout ./cmd/lookout

# Stage 2: Assemble the Wolfi runtime rootfs (CVE-minimal, no package manager
# in the final image; the apk database is retained for scanners/SBOM).
FROM cgr.dev/chainguard/wolfi-base:latest AS rootfs
RUN apk add --no-cache --initdb --root /out \
      --repository https://packages.wolfi.dev/os \
      --keys-dir /etc/apk/keys \
      ca-certificates-bundle busybox docker-cli docker-compose wget \
 && echo 'lookout:x:65532:65532:lookout:/home/lookout:/sbin/nologin' >> /out/etc/passwd \
 && echo 'lookout:x:65532:' >> /out/etc/group \
 && rm -rf /out/var/cache/apk/*

# Stage 3: Final image — Wolfi rootfs plus the binary. No USER directive — the
# agent runs as root by default so it can reach the host Docker socket; drop to
# UID 65532 at deploy time (see examples/ and SECURITY.md).
FROM scratch
COPY --from=rootfs /out /
COPY --from=builder /lookout /usr/bin/lookout

VOLUME /data/stacks
EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["wget", "-q", "--spider", "http://localhost:3000/_lookout/health"]

ENTRYPOINT ["/usr/bin/lookout"]
