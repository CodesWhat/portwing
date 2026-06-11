# Deployment examples

Ready-to-run Docker Compose files, hardened by default (`read_only`, `cap_drop: [ALL]`, `no-new-privileges`, secrets instead of inline tokens).

| File | Mode | When to use |
| ---- | ---- | ----------- |
| [`docker-compose.standard.yml`](docker-compose.standard.yml) | Standard (inbound HTTP on :3000) | Drydock or any client can reach the host directly |
| [`docker-compose.edge.yml`](docker-compose.edge.yml) | Edge (outbound WebSocket, no inbound ports) | Agent is behind NAT/firewall; it dials out to your Drydock instance |
| [`docker-compose.with-sockguard.yml`](docker-compose.with-sockguard.yml) | Standard + [sockguard](https://github.com/CodesWhat/sockguard) socket filter | Two-layer defense: even a compromised agent is constrained to an explicit Docker API allowlist |

Before starting any of them, generate a token:

```bash
openssl rand -hex 32 > lookout_token.txt
```

Validate a file without starting anything:

```bash
docker compose -f docker-compose.standard.yml config -q
```

For Ed25519 key-based auth instead of a shared token, see the Authentication section of the main [README](../README.md) and [`docs/security-model.md`](../docs/security-model.md).
