# Compatibility Matrix

This is the canonical, cross-repo compatibility matrix for Portwing, Drydock,
and sockguard. It supersedes any duplicate matrix in per-repo docs — those
should link here rather than maintain a second copy.

**DrydockCompat only bumps on a wire-protocol-breaking change, independent of
product version numbers — check this file, not the product version, before
assuming compatibility.** Product releases (portwing `vX.Y.Z`, Drydock
`vX.Y.Z`, sockguard preset revisions) can ship any number of times between
DrydockCompat bumps.

## Version matrix

| Portwing version | Drydock version | sockguard preset | Wire compat (`DrydockCompat` / `serverCompatLevel`) |
|---|---|---|---|
| v0.6.0 (latest release) / `main` (unreleased) | 1.5.x / `dev/v1.6` | `portwing.yaml`, `portwing-with-exec.yaml`, `portwing-with-compose.yaml` | `1.4.0` |

## What "wire compat" means

- Portwing sends `drydockCompat` in its `hello` message (Standard Mode's
  `dd:ack` doesn't carry it; Edge Mode's `hello` does). The constant lives at
  `internal/protocol/version.go` (`DrydockCompat = "1.4.0"`).
- Drydock's controller sends `serverCompatLevel` inside the `welcome` frame's
  `config` map (Edge Mode only).
- Both sides compare **major version only** (`1.x.x` vs `1.y.y` — any `x`/`y`
  mismatch is a warning, not a rejection): the wire connection is accepted
  either way; the comparison exists purely to flag operators who should check
  this file before assuming full feature compatibility. See
  `internal/edge/client.go` (portwing) and `app/api/portwing-ws.ts` (drydock).
- Increment the major component only when introducing a breaking wire-protocol
  change (a field whose absence or renamed shape would break the other side's
  parsing) — not for every new optional field or message type.
- **Hello-rejection reconnect behavior**: the agent treats a subset of the
  controller's hello-rejection `code` values as terminal (it exits instead of
  reconnecting): `ed25519-required`, `unknown-key`, `bad-signature`,
  `protocol-mismatch`, `no-auth`, `invalid-agent-name`, `parse-error`,
  `expected-hello`, `agent-name-claimed`. All other codes — including any the
  agent doesn't recognize — are retried with backoff. This list mirrors the
  drydock controller's `app/api/portwing-ws.ts` and is **not** a versioned wire
  contract; if drydock renames or repurposes a rejection code, update
  `internal/edge/hello_reject.go` in lockstep.

## Sockguard preset compatibility

All three presets below are validated against the Portwing agent's Docker API
usage as of the Portwing version in the row above:

| Preset | Purpose |
|---|---|
| `app/configs/portwing.yaml` | Base preset: container lifecycle, image pull/inspect/remove, `/events`, narrow network/volume/distribution/service reads. No exec, no compose-stack network/volume creation, no build. |
| `app/configs/portwing-with-exec.yaml` | `portwing.yaml` plus the exec/attach paths Portwing's interactive terminal feature needs. |
| `app/configs/portwing-with-compose.yaml` | `portwing.yaml` plus `POST /networks/create`, `POST /networks/*/connect`, `DELETE /networks/*`, `POST /networks/*/disconnect`, `POST /volumes/create`, `DELETE /volumes/*` — what compose-stack deploys through Portwing need. Still denies `/build`; BuildKit fallback needs `/session` + `/grpc`, which no preset here models yet. |

Portwing's `examples/sockguard.yaml` is a manually-synced copy of sockguard's
`app/configs/portwing.yaml` (the no-exec, no-compose base preset) — update
both together when either changes.

## Related pages

- Portwing: [`docs/drydock-integration.md`](docs/drydock-integration.md) / [`docs/content/docs/drydock-integration.mdx`](docs/content/docs/drydock-integration.mdx) — full wire-protocol and REST/SSE contract detail.
- Drydock: see its README's ecosystem section.
- sockguard: see its README's ecosystem section.
