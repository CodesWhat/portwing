# Lookout

Lightweight remote Docker agent for the [Drydock](https://github.com/codeswhat/drydock) container monitoring platform.

```
Remote Host A        Remote Host B           Your Server
+----------+        +----------+        +------------------+
| Lookout  |--WSS-->|          |        |    DockPilot     |
| (agent)  |        | Lookout  |--WSS-->|    (gateway)     |
|          |        | (agent)  |        |        |         |
| Docker   |        |          |        |    Drydock       |
| Engine   |        | Docker   |        |    (platform)    |
+----------+        | Engine   |        +------------------+
                    +----------+
```

## Features

- **Dual connection modes** -- Standard (inbound HTTP) and Edge (outbound WebSocket)
- **Transparent Docker API proxy** -- All Docker Engine API paths forwarded
- **Container inventory** -- Full container metadata with `dd.*` label parsing
- **Host metrics** -- CPU, memory, disk, network, uptime collection
- **Interactive exec** -- Terminal sessions via WebSocket or HTTP hijack
- **Docker Compose** -- Full lifecycle management with security hardening
- **SSE compatibility** -- Drop-in replacement for existing Drydock agents
- **Minimal footprint** -- Static Go binary, ~10 MB container image

## Quick Start

### Docker

```bash
docker run -d \
  --name lookout \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 3000:3000 \
  ghcr.io/codeswhat/lookout:latest
```

### Edge Mode

```bash
docker run -d \
  --name lookout \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e DRYDOCK_URL=wss://your-server:3001 \
  -e TOKEN=your-secret-token \
  ghcr.io/codeswhat/lookout:latest
```

### Binary Install

```bash
curl -fsSL https://raw.githubusercontent.com/codeswhat/lookout/main/scripts/install.sh | bash
```

## Connection Modes

### Standard Mode

Lookout runs an HTTP(S) server. Drydock/DockPilot connects inbound.

- Set when `DRYDOCK_URL` is not configured
- Transparent Docker API proxy on all paths
- Agent endpoints under `/_lookout/*`
- Drydock-compatible REST + SSE under `/api/*`
- Optional TLS with modern cipher suites

### Edge Mode

Lookout initiates an outbound WebSocket connection to DockPilot.

- Set when both `DRYDOCK_URL` and `TOKEN` are configured
- Works behind NAT, firewalls, and dynamic IPs
- Auto-reconnect with exponential backoff + jitter
- All communication multiplexed over a single WebSocket

## Configuration

### Connection

| Variable | Default | Description |
|----------|---------|-------------|
| `DRYDOCK_URL` | -- | WebSocket URL for Edge mode (`wss://...`) |
| `TOKEN` | -- | Authentication token (plaintext) |
| `TOKEN_FILE` | -- | Path to file containing token |
| `TOKEN_HASH` | -- | Argon2id hash of token (generate with `lookout hash-token`) |
| `TOKEN_HASH_FILE` | -- | Path to file containing Argon2id hash |
| `CA_CERT` | -- | Custom CA certificate for Edge mode |
| `TLS_SKIP_VERIFY` | `false` | Skip TLS verification (testing only) |
| `PORT` | `3000` | HTTP server port |
| `BIND_ADDRESS` | `0.0.0.0` | Bind address |
| `TLS_CERT` | -- | Server TLS certificate (Standard mode) |
| `TLS_KEY` | -- | Server TLS key (Standard mode) |
| `TRUSTED_PROXIES` | -- | Comma-separated CIDRs of reverse proxies whose `X-Forwarded-For` is trusted; unset means forwarding headers are ignored |

### Docker

| Variable | Default | Description |
|----------|---------|-------------|
| `DOCKER_SOCKET` | Auto-detect | Docker socket path |
| `DOCKER_HOST` | -- | Docker TCP host (alternative) |
| `STACKS_DIR` | `/data/stacks` | Compose stack file directory |

### Agent Identity

| Variable | Default | Description |
|----------|---------|-------------|
| `AGENT_ID` | UUID v4 | Unique agent identifier |
| `AGENT_NAME` | Hostname | Human-readable name |

### Operational

| Variable | Default | Description |
|----------|---------|-------------|
| `HEARTBEAT_INTERVAL` | `30` | Ping interval (seconds) |
| `REQUEST_TIMEOUT` | `30` | Docker API request timeout (seconds) |
| `RECONNECT_DELAY` | `1` | Initial reconnect delay (seconds) |
| `MAX_RECONNECT_DELAY` | `60` | Max reconnect delay (seconds) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `SKIP_DF_COLLECTION` | -- | Disable disk metrics |

### Drydock Compatibility

| Variable | Default | Description |
|----------|---------|-------------|
| `DD_AGENT_SECRET` | -- | Backward-compatible auth token |
| `DD_AGENT_SECRET_FILE` | -- | Backward-compatible token file |
| `DD_POLL_INTERVAL` | `300` | Container inventory refresh (seconds) |

## API Reference

### Health Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/health` | GET | No | Simple health check — `{"status":"ok"}` |
| `/_lookout/health` | GET | No | Health check + Docker connectivity |

`/_lookout/health` returns HTTP 503 when the Docker daemon is unreachable.
Both endpoints are unauthenticated and safe to use for load-balancer probes
and Docker HEALTHCHECK instructions.

### Agent Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/_lookout/info` | GET | Yes | Agent version, mode, capabilities |
| `/_lookout/compose` | POST | Yes | Docker Compose operations |
| `/_lookout/metrics` | GET | Yes | Prometheus metrics (agent-scoped) |
| `/metrics` | GET | Yes | Prometheus metrics (compat alias) |

### Drydock-Compatible Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/events` | GET | SSE event stream (`dd:ack`, container events) |
| `/api/containers` | GET | Container inventory |
| `/api/containers/:id/logs` | GET | Container logs |
| `/api/containers/:id` | DELETE | Remove container |
| `/api/watchers` | GET | Watcher components |
| `/api/triggers` | GET | Trigger components |

### Docker API Proxy

All other paths (`/*`) are transparently proxied to the Docker Engine API, including streaming endpoints and exec session hijacking.

### Metrics

Lookout exposes Prometheus metrics at `/_lookout/metrics` (and the alias
`/metrics`). Both require bearer auth.

Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: lookout
    scheme: https          # or http if TLS not configured
    static_configs:
      - targets: ["your-host:3000"]
    authorization:
      type: Bearer
      credentials: YOUR_LOOKOUT_TOKEN
    tls_config:
      # ca_file: /etc/prometheus/lookout-ca.crt  # if using custom CA
      insecure_skip_verify: false
```

## Token Security

### Plaintext token (quickstart)

```bash
# Generate a strong token
TOKEN=$(openssl rand -hex 32)
docker run -e TOKEN="$TOKEN" ... ghcr.io/codeswhat/lookout:latest
```

### Hash-at-rest with TOKEN_HASH

Store only an Argon2id hash so the plaintext token never appears in env dumps
or config files:

```bash
# Generate the hash
lookout hash-token --token "$TOKEN"
# $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>

# Use the hash instead of the plaintext
docker run -e TOKEN_HASH="$HASH" ... ghcr.io/codeswhat/lookout:latest
```

Or write the hash to a file and use `TOKEN_HASH_FILE`:

```bash
lookout hash-token --token "$TOKEN" > /run/secrets/lookout-token-hash
docker run -e TOKEN_HASH_FILE=/run/secrets/lookout-token-hash ...
```

## Verify a Release

Lookout releases are signed with [Sigstore cosign](https://github.com/sigstore/cosign)
via GitHub Actions keyless signing. Checksums and container images can be
verified without managing signing keys.

### Verify the checksums file

```bash
TAG=v0.1.0

cosign verify-blob \
  --certificate-identity-regexp "https://github.com/CodesWhat/lookout/.github/workflows/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle "checksums.txt.bundle" \
  "checksums.txt"
```

### Verify the container image

```bash
TAG=v0.1.0

cosign verify \
  --certificate-identity-regexp "https://github.com/CodesWhat/lookout/.github/workflows/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "ghcr.io/codeswhat/lookout:${TAG}"
```

### SBOM

Each release includes a CycloneDX SBOM attached as a release asset
(`lookout-${TAG}-sbom.cdx.json`). Download and inspect it with any
CycloneDX-compatible tool, or verify it with cosign the same way as the
checksums file.

## Security

- **Authentication**: Token-based with timing-safe comparison (`crypto/subtle`); hash-at-rest via `TOKEN_HASH` (Argon2id)
- **Rate Limiting**: 10 failed auth attempts per IP per minute
- **TLS**: TLS 1.2+ with modern AEAD cipher suites
- **Compose Security**: Path traversal protection, env var denylist, service name injection prevention
- **Resource Limits**: WebSocket (16 MB), response body (100 MB), exec sessions (100 concurrent)

See [docs/security-model.md](docs/security-model.md) for the full citable spec and CVE mapping.

## Building from Source

```bash
go build -trimpath -ldflags="-s -w" -o lookout ./cmd/lookout
```

## License

[AGPL-3.0](LICENSE)
