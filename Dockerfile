# From-source build: `docker build -t portwing .`
# (Release images are built by GoReleaser from prebuilt binaries via
# Dockerfile.release; this file is the standalone equivalent.)

# Stage 1: Build the binary from source.
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /portwing ./cmd/portwing

# Stage 2: Assemble the Wolfi runtime rootfs (CVE-minimal, no package manager
# in the final image; the apk database is retained for scanners/SBOM).
FROM cgr.dev/chainguard/wolfi-base:latest@sha256:e161445c05b19e668cb5cc44df2f0403329fd4f0ac892794255e328e760612a1 AS rootfs
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
    CMD ["wget", "-q", "--spider", "http://localhost:3000/_portwing/health"]

ENTRYPOINT ["/usr/bin/portwing"]