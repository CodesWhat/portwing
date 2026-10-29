# Compatibility Matrix

This is the canonical, cross-repo compatibility matrix for Portwing, Drydock,
and sockguard. It supersedes any duplicate matrix in per-repo docs — those
should link here rather than maintain a second copy.

The versioning and deprecation rules applied to these pairings are defined in
[STABILITY.md](STABILITY.md).

**DrydockCompat only bumps on a wire-protocol-breaking change, independent of
product version numbers — check this file, not the product version, before
assuming compatibility.** Product releases (portwing `vX.Y.Z`, Drydock
`vX.Y.Z`, sockguard preset revisions) can ship any number of times between
DrydockCompat bumps.

## Version matrix

| Portwing version | Drydock wire connection | Watcher/update feature compatibility | sockguard preset | Wire compat (`DrydockCompat` / `serverCompatLevel`) |
|---|---|---|---|---|
| v1.0.1 (latest release) / `main` | 1.7.x, 1.6.x; 1.5.x with `DD_EXPERIMENTAL_PORTWING=true` | `v1.6.0-rc.11+` | `portwing.yaml`, `portwing-with-exec.yaml`, `portwing-with-compose.yaml` | `1.4.0` |
| v0.9.x (unsupported) | 1.6.x; 1.5.x with `DD_EXPERIMENTAL_PORTWING=true` | `v1.6.0-rc.11+` | `portwing.yaml`, `portwing-with-exec.yaml`, `portwing-with-compose.yaml` | `1.4.0` |

v1.0.1 keeps the existing wire and watcher/update contract. The 0.9 line is
no longer supported; see [SECURITY.md](SECURITY.md) for support boundaries.

### Published candidate evidence

The exact v1.0.0-rc.1 artifacts passed the hosted matrix on 2026-10-16.
The exact v1.0.0-rc.2 artifacts passed the hosted matrix on 2026-10-26:

| Portwing | Drydock | Sockguard | Standard / Edge / legacy floor | Evidence |
|---|---|---|---|---|
| v1.0.0-rc.1 | v1.6.0 | v2.2.10 | PASS / PASS / PASS | [Matrix run](https://github.com/CodesWhat/sockguard/actions/runs/38836428734) |
| v1.0.0-rc.1 | v1.6.1 | v2.2.10 | PASS / PASS / PASS | [Matrix run](https://github.com/CodesWhat/sockguard/actions/runs/38837070094) |
| v1.0.0-rc.2 | v1.7.0 | v2.2.11 | PASS / PASS / PASS | [Matrix run](https://github.com/CodesWhat/sockguard/actions/runs/40266007939) |

The exact v1.0.0 artifacts passed the hosted matrix on 2026-10-29 for Edge and the legacy floor; Standard failed enrollment, fixed in v1.0.1:

| Portwing | Drydock | Sockguard | Standard / Edge / legacy floor | Evidence |
|---|---|---|---|---|
| v1.0.0 | v1.7.0 | v2.2.11 | FAIL / PASS / PASS | [Matrix run](https://github.com/CodesWhat/sockguard/actions/runs/40870930418) |

The rc.2 run used candidate manifest
`sha256:4ef115970be2ceacfb58cdc603745da509b7637a62501507a40f300633048c3e`. The
same image also passed controller-assisted Standard and Edge rotation, including
a restart, against Drydock v1.8.0-rc.1 on a dedicated test host.

Both rc.1 runs used candidate manifest
`sha256:e92d810c0281551efeeb5adb2b71c6bfe345d42760ebd67134a02d2b5834a611`.
The Standard identity checks exercised enrollment, manual overlapping-key
rotation, revocation, SIGHUP reloads and clock-skew recovery. They did not test
the later controller-assisted implementation. Rerun the matrix against the
exact next release; these results do not verify newer code. The v1.0.1
artifacts are verified after publication, and that evidence is recorded here
once it exists.

### Controller-assisted rotation

Support first shipped in the Portwing v1.0 line (its rc.2 candidate) and in
Drydock
[`v1.8.0-rc.1`](https://github.com/CodesWhat/drydock/releases/tag/v1.8.0-rc.1).
Use Portwing v1.0 or later with Drydock v1.8.0-rc.1 or later on the
v1.8 line for opt-in managed rotation. The first v1.0 release candidate and Drydock's
v1.6/v1.7 lines do not include this pairing.

Standard mode requires `KEY_ROTATION_DIR` on Portwing and `rotationdir` on its
Drydock agent configuration. Edge mode requires Portwing's managed storage and
the negotiated `edge-key-rotation` capability. The wire remains `portwing/1.0`
and `DrydockCompat` remains `1.4.0`; wire compatibility alone does not establish
rotation support. See the [operator guide](docs/content/docs/current/authentication.mdx#controller-assisted-rotation).

## Wire compatibility versus feature compatibility

`DrydockCompat` answers whether the two sides can parse and maintain the stable
`portwing/1.0` connection. It does **not** promise that an older controller
implements every additive Portwing feature.

Portwing v0.9.0 keeps `DrydockCompat` at `1.4.0`: the watcher configuration
keys (`transport=docker-api`, `execution=controller`, `events=portwing`), empty
remote-trigger advertisement, and component-before-inventory ordering are
additive. A wire-compatible older Drydock can still connect, but it does not
provide the complete controller-owned watcher/update path. Use Drydock
`v1.6.0-rc.11` or later for full v0.9.0 behavior in both Standard Mode (Docker
calls through authenticated HTTP) and Edge Mode (correlated WebSocket
`request`/`response`).

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

## HTTP signature compatibility

Portwing v0.7.0 introduces Ed25519 HTTP signature version 2. Clients set
`X-Portwing-Signature-Version: 2` and sign the escaped path plus the exact raw
query string as the request target. Legacy unversioned signatures remain valid
only for query-free requests. This does not change `DrydockCompat`: it affects
the Standard Mode HTTP authentication contract, not the edge WebSocket wire
protocol.

## Sockguard preset compatibility

The presets below are sockguard's Portwing-targeted presets as of sockguard
v2.0.0. The first three are validated against the Portwing agent's Docker API
usage as of the Portwing version in the row above; the two build presets extend
`portwing-with-compose.yaml` and are not part of that validated row:

| Preset | Purpose |
|---|---|
| `app/configs/portwing.yaml` | Base preset: container lifecycle, image pull/inspect/remove, `/events`, narrow network/volume/distribution/service reads. No exec, no compose-stack network/volume creation, no build. |
| `app/configs/portwing-with-exec.yaml` | `portwing.yaml` plus the exec/attach paths Portwing's interactive terminal feature needs. |
| `app/configs/portwing-with-compose.yaml` | `portwing.yaml` plus `POST /networks/create`, `POST /networks/*/connect`, `DELETE /networks/*`, `POST /networks/*/disconnect`, `POST /volumes/create`, `DELETE /volumes/*` — what compose-stack deploys through Portwing need. Still denies `/build`, `/session` and `/grpc` — use one of the two build presets below for either builder transport. |
| `app/configs/portwing-with-build.yaml` | `portwing-with-compose.yaml` plus classic `POST /build` only. `/session` and `/grpc` stay denied, so a default (BuildKit) `docker compose build` fails by design; set `DOCKER_BUILDKIT=0` in the client environment to use this transport. |
| `app/configs/portwing-with-mediated-build.yaml` | `portwing-with-compose.yaml` plus mediated BuildKit: sockguard terminates `POST /session` and `POST /grpc` as h2c and inspects every gRPC message crossing them (sockguard issue #185) rather than hijacking the streams. Classic `POST /build` stays denied. This is the preset a stock `docker compose build` / `up --build` needs with no client-side env var. |

Portwing's `examples/sockguard.yaml` is a manually-synced copy of sockguard's
`app/configs/portwing.yaml` (the no-exec, no-compose base preset) — update
both together when either changes.

## Related pages

- Portwing: [`STABILITY.md`](STABILITY.md) — semantic-versioning, deprecation,
  and change-control guarantees for the HTTP, environment, MCP, and wire
  surfaces.
- Portwing: [`docs/drydock-integration.md`](docs/drydock-integration.md) / [`docs/content/docs/current/drydock-integration.mdx`](docs/content/docs/current/drydock-integration.mdx) — full wire-protocol and REST/SSE contract detail.
- Drydock: see its README's ecosystem section.
- sockguard: see its README's ecosystem section.
