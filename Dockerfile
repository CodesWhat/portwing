# From-source build: `docker build -t portwing .`
# (Release images are built by GoReleaser from prebuilt binaries via
# Dockerfile.release; this file is the standalone equivalent.)

# Stage 1: Build the binary from source.
FROM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /portwing ./cmd/portwing

# Stage 2: Assemble the Wolfi runtime rootfs (CVE-minimal, no package manager
# in the final image; the apk database is retained for scanners/SBOM).
FROM cgr.dev/chainguard/wolfi-base:latest@sha256:003627df3c1e1bba0c4116afcddb314aca9594ee2328c7e876a8081a6c988b2e AS rootfs
RUN apk add --no-cache --initdb --root /out \
    --repository https://packages.wolfi.dev/os \
    --keys-dir /etc/apk/keys \
    ca-certificates-bundle busybox docker-cli docker-compose wget \
    && echo 'portwing:x:65532:65532:portwing:/home/portwing:/sbin/nologin' >>/out/etc/passwd \
    && echo 'portwing:x:65532:' >>/out/etc/group \
    && install -d -o 65532 -g 65532 /out/home/portwing /out/data/stacks \
    && rm -rf /out/var/cache/apk/*

# Stage 3: Final image — Wolfi rootfs plus the binary. Runs as the dedicated
# `portwing` user (UID 65532); reaching the host Docker socket requires adding
# the socket's group at deploy time via group_add / --group-add (see examples/
# and SECURITY.md).
FROM scratch
COPY --from=rootfs /out /
COPY --from=builder /portwing /usr/bin/portwing

# DOCKER_CONFIG points at the /tmp tmpfs so `docker login` during compose
# deploys can write config.json under a read-only root filesystem.
ENV HOME=/home/portwing \
    DOCKER_CONFIG=/tmp/.docker

USER 65532:65532

# /data/stacks is pre-created owned by 65532 in the rootfs stage, so volumes
# initialized from it are writable by the non-root user.
VOLUME /data/stacks
EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD if [ -n "$TLS_CERT" ]; then wget -q --no-check-certificate --spider "https://localhost:${PORT:-3000}/health"; else wget -q --spider "http://localhost:${PORT:-3000}/health"; fi

ENTRYPOINT ["/usr/bin/portwing"]