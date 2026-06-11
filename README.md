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
| `AUDIT_LOG` | -- | Audit log sink: `stdout`, `stderr`, or a file path; unset disables auditing |

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
| `/_lookout/mcp` | POST | Yes | MCP server (JSON-RPC 2.0, protocol 2025-11-25) |

### MCP — AI Assistant Integration

Lookout exposes a read-only [Model Context Protocol](https://modelcontextprotocol.io/) endpoint
at `POST /_lookout/mcp`. AI assistants (Claude, Cursor, Windsurf, or any MCP client) can query
live container state through this endpoint using their standard tool-call flow.

**Protocol:** MCP 2025-11-25 — Streamable HTTP, stateless single-request mode, `Content-Type: application/json`.

**Available tools:**

| Tool | Description |
|------|-------------|
| `list_containers` | All containers — id, names, image, state, status, labels |
| `inspect_container(id)` | State, image, env-var count (values never exposed), mounts, networks, restart policy |
| `container_logs(id, tail)` | Last N lines of stdout/stderr (max 500) |
| `host_metrics` | CPU, memory, disk, network, uptime snapshot |
| `container_stats(id)` | One-shot CPU/memory/network stats for a container |

**Credential hygiene:** `inspect_container` returns only the *count* of environment variables —
values are never transmitted, preventing accidental secret leakage.

#### Add to Claude Desktop (claude_desktop_config.json)

```json
{
  "mcpServers": {
    "lookout": {
      "command": "curl",
      "args": ["-s", "-X", "POST",
               "-H", "Content-Type: application/json",
               "-H", "Authorization: Bearer YOUR_LOOKOUT_TOKEN",
               "http://your-host:3000/_lookout/mcp"],
      "type": "http",
      "url": "http://your-host:3000/_lookout/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_LOOKOUT_TOKEN"
      }
    }
  }
}
```

#### Add via claude mcp add (CLI)

```bash
claude mcp add --transport http \
  --header "Authorization: Bearer YOUR_LOOKOUT_TOKEN" \
  lookout http://your-host:3000/_lookout/mcp
```

#### .mcp.json (project-level, Cursor / Windsurf / any client)

```json
{
  "mcpServers": {
    "lookout": {
      "type": "http",
      "url": "http://your-host:3000/_lookout/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_LOOKOUT_TOKEN"
      }
    }
  }
}
```

Replace `YOUR_LOOKOUT_TOKEN` with the value you set in `TOKEN` / `TOKEN_FILE` / `TOKEN_HASH`.

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

### Plaintext token (testing only)

> **Warning:** Environment variables are visible in `docker inspect` and
> process listings. For production, use `TOKEN_FILE` or `TOKEN_HASH_FILE`
> with a mounted secret.

```bash
# Generate a strong token
TOKEN=$(openssl rand -hex 32)
docker run -e TOKEN="$TOKEN" ... ghcr.io/codeswhat/lookout:latest
```

### File-based token (production)

```bash
TOKEN=$(openssl rand -hex 32)
printf '%s' "$TOKEN" > /run/secrets/lookout-token
chmod 600 /run/secrets/lookout-token
docker run -e TOKEN_FILE=/run/secrets/lookout-token \
  -v /run/secrets/lookout-token:/run/secrets/lookout-token:ro \
  ... ghcr.io/codeswhat/lookout:latest
```

### Hash-at-rest with TOKEN_HASH

Store only an Argon2id hash so the plaintext token never appears in env dumps
or config files:

```bash
# Generate the hash (token is read from stdin, never argv)
HASH=$(printf '%s' "$TOKEN" | lookout hash-token)
# $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>

# Use the hash instead of the plaintext
docker run -e TOKEN_HASH="$HASH" ... ghcr.io/codeswhat/lookout:latest
```

Or write the hash to a file and use `TOKEN_HASH_FILE`:

```bash
printf '%s' "$TOKEN" | lookout hash-token > /run/secrets/lookout-token-hash
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

## Audit Logging

Lookout ships structured JSON audit logging for every security-relevant action — a feature that commercial container management platforms lock behind paid tiers.

### Enable

```bash
# Write to a file (opened append-only, mode 0600)
docker run -e AUDIT_LOG=/var/log/lookout-audit.log ...

# Or to stdout/stderr (useful with log aggregators)
docker run -e AUDIT_LOG=stdout ...
```

Auditing is disabled by default (`AUDIT_LOG` unset). When disabled the overhead is a single nil pointer check per request.

### Events

| `event` | Triggered when |
|---------|---------------|
| `api_request` | Any authenticated API call completes |
| `auth_failure` | An invalid token is presented |
| `rate_limited` | An IP is blocked by the rate limiter |
| `compose_op` | A Docker Compose operation runs |
| `exec_start` | An interactive exec tunnel opens |

### Sample JSON line

```json
{"time":"2026-01-15T10:23:45.123456789Z","level":"INFO","msg":"","event":"api_request","actor":"203.0.113.42","method":"POST","path":"/_lookout/compose","outcome":"allowed","status":200,"duration_ms":3.14}
```

Compose operations include additional fields:

```json
{"time":"2026-01-15T10:23:45.200Z","level":"INFO","msg":"","event":"compose_op","actor":"203.0.113.42","operation":"up","stack":"nginx-stack","outcome":"allowed"}
```

Exec tunnel events:

```json
{"time":"2026-01-15T10:24:01.500Z","level":"INFO","msg":"","event":"exec_start","actor":"203.0.113.42","container":"abc123def456","exec_id":"e7f8a9b1","outcome":"allowed"}
```

## Building from Source

```bash
go build -trimpath -ldflags="-s -w" -o lookout ./cmd/lookout
```

## License

[AGPL-3.0](LICENSE)
