# Stage 1: Build
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lookout ./cmd/lookout

# Stage 2: Build custom Wolfi rootfs with apko
FROM cgr.dev/chainguard/apko:latest AS rootfs
COPY apko.yaml /work/apko.yaml
RUN apko build /work/apko.yaml lookout:latest /work/output.tar

# Stage 3: Final image
FROM scratch
COPY --from=rootfs /work/output.tar.d/* /
COPY --from=builder /lookout /usr/bin/lookout

VOLUME /data/stacks
EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["wget", "-q", "--spider", "http://localhost:3000/_lookout/health"]

ENTRYPOINT ["/usr/bin/lookout"]
