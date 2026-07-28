# Deployment examples

Ready-to-run Docker Compose and Kubernetes examples, hardened by default (`read_only`/`readOnlyRootFilesystem`, `cap_drop: [ALL]`, `no-new-privileges`/`allowPrivilegeEscalation: false`, secrets instead of inline tokens).

| File | Mode | When to use |
| ---- | ---- | ----------- |
| [`docker-compose.standard.yml`](docker-compose.standard.yml) | Standard (inbound HTTP on :3000) | Drydock or any client can reach the host directly |
| [`docker-compose.edge.yml`](docker-compose.edge.yml) | Edge (outbound WebSocket, no inbound ports) | Agent is behind NAT/firewall; it dials out to your Drydock instance |
| [`docker-compose.with-sockguard.yml`](docker-compose.with-sockguard.yml) | Standard + [sockguard](https://github.com/CodesWhat/sockguard) socket filter | Two-layer defense: even a compromised agent is constrained to an explicit Docker API allowlist |
| [`observability/docker-compose.yml`](observability/docker-compose.yml) | Standard or edge + Prometheus + Fluent Bit | Complete TLS/auth, metrics, readiness, and audit-shipping topology selected with a Compose profile |

Before starting any of them, generate a token and export the Docker socket's group ID (the images run as the non-root `portwing` user, UID 65532, and need `group_add` to reach the socket):

```bash
kubectl create namespace portwing --dry-run=client -o yaml | kubectl apply -f -
openssl rand -hex 32 > portwing_token.txt
sudo chown 65532:65532 portwing_token.txt && sudo chmod 0400 portwing_token.txt
export DOCKER_SOCK_GID=$(stat -c '%g' /var/run/docker.sock)
```

Validate a file without starting anything:

```bash
docker compose -f docker-compose.standard.yml config -q
```

## Full observability profile

The observability example pins Prometheus and Fluent Bit by multi-architecture
digest. It uses TLS and bearer auth for standard-mode metrics, keeps the
edge-mode operations listener on the private Compose network, and forwards the
audit file through Fluent Bit. The example's stdout output is immediately
collectable by Docker; replace its output block with the destination used by
your SIEM or log platform.

```bash
mkdir -p runtime/standard-audit runtime/edge-audit tls
sudo chown -R 65532:65532 runtime

openssl rand -hex 32 > portwing_token.txt
portwing keygen -comment "edge-host-01" > portwing_ed25519.pem

openssl req -x509 -newkey ed25519 -nodes -days 30 \
  -subj /CN=portwing-standard \
  -addext subjectAltName=DNS:portwing-standard \
  -keyout tls/portwing.key -out tls/portwing.crt
chmod 0400 portwing_token.txt portwing_ed25519.pem tls/portwing.key
chmod 0444 tls/portwing.crt
sudo chown 65532:65532 portwing_token.txt portwing_ed25519.pem tls/portwing.*

export DOCKER_SOCK_GID=$(stat -c '%g' /var/run/docker.sock)
docker compose -f observability/docker-compose.yml --profile standard up -d
# Or:
DRYDOCK_URL=https://drydock.example.com \
  docker compose -f observability/docker-compose.yml --profile edge up -d
```

Prometheus is available on `http://localhost:9090`. Portwing standard mode is
available on `https://localhost:3000`; edge mode publishes no host port.

For Ed25519 key-based auth instead of a shared token, see the Authentication section of the main [README](../README.md) and [`docs/security-model.md`](../docs/security-model.md).

## Kubernetes

| File | Mode | When to use |
| ---- | ---- | ----------- |
| [`kubernetes/standard.yaml`](kubernetes/standard.yaml) | Standard (inbound HTTP on :3000) | Cluster nodes can reach Portwing directly; Drydock or another in-cluster client connects on port 3000 |
| [`kubernetes/edge.yaml`](kubernetes/edge.yaml) | Edge (outbound WebSocket, no inbound ports) | Nodes are behind NAT/firewall; the agent dials out to your Drydock instance |
| [`kubernetes/observability-standard.yaml`](kubernetes/observability-standard.yaml) | Prometheus for standard mode | Authenticated HTTPS scrape using the Portwing token and TLS secrets |
| [`kubernetes/observability-edge.yaml`](kubernetes/observability-edge.yaml) | Prometheus for edge mode | Private ClusterIP scrape of edge connection/backpressure metrics |

Both agent manifests deploy a `DaemonSet` so one agent runs on each
Docker-capable node. They use `/health` for liveness, `/ready` for readiness,
and a pinned Fluent Bit sidecar to forward the audit file into the cluster log
collector. Secrets are deliberately created out of band so applying a manifest
can never replace a real credential with a checked-in placeholder.

Before applying, uncomment `supplementalGroups` in the manifest's pod `securityContext` and set it to the Docker socket's group ID on the node (the images run as the non-root `portwing` user, UID 65532, and need supplemental group access to reach the socket; `fsGroup` does not affect a `hostPath`-mounted socket):

```bash
stat -c '%g' /var/run/docker.sock
```

**Create the secret before applying:**

Standard mode:

```bash
openssl rand -hex 32 > portwing_token.txt
openssl req -x509 -newkey ed25519 -nodes -days 30 \
  -subj /CN=portwing.portwing.svc \
  -addext subjectAltName=DNS:portwing.portwing.svc,DNS:portwing \
  -keyout portwing.key -out portwing.crt
kubectl -n portwing create secret generic portwing-token --from-file=token=portwing_token.txt
kubectl -n portwing create secret tls portwing-tls --cert=portwing.crt --key=portwing.key
kubectl apply -f kubernetes/standard.yaml
kubectl apply -f kubernetes/observability-standard.yaml
```

Edge mode:

```bash
kubectl create namespace portwing --dry-run=client -o yaml | kubectl apply -f -
portwing keygen -comment "edge-host-01" > portwing_ed25519.pem   # PKCS#8 key; also prints an authorized_keys line
chmod 0600 portwing_ed25519.pem   # kubectl reads this file, so keep it owned by you (no chown needed here)
# Register the authorized_keys line with Drydock (POST /api/v1/portwing/keys)
kubectl -n portwing create secret generic portwing-key --from-file=portwing_ed25519.pem=portwing_ed25519.pem
kubectl apply -f kubernetes/edge.yaml
kubectl apply -f kubernetes/observability-edge.yaml
```

**Caveat — Docker socket requirement:** Portwing proxies a single host's Docker daemon via `/var/run/docker.sock`. It is not a Kubernetes controller and needs no cluster RBAC permissions. These manifests only work on nodes that actually run Docker and expose that socket. Most modern clusters use containerd or CRI-O, which don't expose it — use the `nodeSelector` (`portwing.dev/docker: "true"`) to target only the nodes where the socket exists. If you only have one Docker node, convert the DaemonSet to a `Deployment` with `replicas: 1` as noted in the file header.
