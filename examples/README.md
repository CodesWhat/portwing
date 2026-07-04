# Deployment examples

Ready-to-run Docker Compose and Kubernetes examples, hardened by default (`read_only`/`readOnlyRootFilesystem`, `cap_drop: [ALL]`, `no-new-privileges`/`allowPrivilegeEscalation: false`, secrets instead of inline tokens).

| File | Mode | When to use |
| ---- | ---- | ----------- |
| [`docker-compose.standard.yml`](docker-compose.standard.yml) | Standard (inbound HTTP on :3000) | Drydock or any client can reach the host directly |
| [`docker-compose.edge.yml`](docker-compose.edge.yml) | Edge (outbound WebSocket, no inbound ports) | Agent is behind NAT/firewall; it dials out to your Drydock instance |
| [`docker-compose.with-sockguard.yml`](docker-compose.with-sockguard.yml) | Standard + [sockguard](https://github.com/CodesWhat/sockguard) socket filter | Two-layer defense: even a compromised agent is constrained to an explicit Docker API allowlist |

Before starting any of them, generate a token and export the Docker socket's group ID (the images run as the non-root `portwing` user, UID 65532, and need `group_add` to reach the socket):

```bash
openssl rand -hex 32 > portwing_token.txt
sudo chown 65532:65532 portwing_token.txt && sudo chmod 0400 portwing_token.txt
export DOCKER_SOCK_GID=$(stat -c '%g' /var/run/docker.sock)
```

Validate a file without starting anything:

```bash
docker compose -f docker-compose.standard.yml config -q
```

For Ed25519 key-based auth instead of a shared token, see the Authentication section of the main [README](../README.md) and [`docs/security-model.md`](../docs/security-model.md).

## Kubernetes

| File | Mode | When to use |
| ---- | ---- | ----------- |
| [`kubernetes/standard.yaml`](kubernetes/standard.yaml) | Standard (inbound HTTP on :3000) | Cluster nodes can reach Portwing directly; Drydock or another in-cluster client connects on port 3000 |
| [`kubernetes/edge.yaml`](kubernetes/edge.yaml) | Edge (outbound WebSocket, no inbound ports) | Nodes are behind NAT/firewall; the agent dials out to your Drydock instance |

Both manifests deploy a `DaemonSet` so one agent runs on each Docker-capable node. The Namespace and Secret are included in each file for a self-contained apply.

**Create the secret before applying:**

Standard mode:

```bash
openssl rand -hex 32 > portwing_token.txt
kubectl -n portwing create secret generic portwing-token --from-file=token=portwing_token.txt
kubectl apply -f kubernetes/standard.yaml
```

Edge mode:

```bash
portwing keygen -comment "edge-host-01" > portwing_ed25519.pem   # PKCS#8 key; also prints an authorized_keys line
chmod 0600 portwing_ed25519.pem   # kubectl reads this file, so keep it owned by you (no chown needed here)
# Register the authorized_keys line with Drydock (POST /api/v1/portwing/keys)
kubectl -n portwing create secret generic portwing-key --from-file=portwing_ed25519.pem=portwing_ed25519.pem
kubectl apply -f kubernetes/edge.yaml
```

**Caveat — Docker socket requirement:** Portwing proxies a single host's Docker daemon via `/var/run/docker.sock`. It is not a Kubernetes controller and needs no cluster RBAC permissions. These manifests only work on nodes that actually run Docker and expose that socket. Most modern clusters use containerd or CRI-O, which don't expose it — use the `nodeSelector` (`portwing.dev/docker: "true"`) to target only the nodes where the socket exists. If you only have one Docker node, convert the DaemonSet to a `Deployment` with `replicas: 1` as noted in the file header.
