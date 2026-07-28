# Compatibility and Stability Policy

This document defines the compatibility promises Portwing intends to make at
`v1.0.0`. Portwing `v0.8.x` publishes this policy as a release candidate so
integrators can review it before the guarantees become binding.

The version matrix in [COMPATIBILITY.md](COMPATIBILITY.md) remains the
authoritative record of which Portwing, Drydock, and sockguard releases are
tested together.

## Versioning

Portwing follows Semantic Versioning:

- **Patch releases** fix defects or security issues without intentionally
  changing documented behavior.
- **Minor releases** add backward-compatible functionality and may deprecate
  behavior.
- **Major releases** may remove deprecated behavior or otherwise make breaking
  changes.

An urgent security fix may narrow or disable unsafe behavior in a patch or
minor release. The release notes must identify the exception, its impact, and
the migration path.

## HTTP and OpenAPI surface

Beginning with `v1.0.0`, the following documented surfaces are stable:

- the generic REST and SSE API under `/api/v1`;
- the documented Drydock-compatible HTTP and SSE routes;
- `/_portwing/health`, `/_portwing/info`, `/_portwing/metrics`,
  `/_portwing/audit`, and `/_portwing/mcp`;
- `/health` as a minimal unauthenticated liveness response; and
- Ed25519 HTTP signature version 2, including its exact escaped-path and raw
  query request target.

The following are backward-compatible:

- a new endpoint or event type;
- a new optional request field, query parameter, or response field;
- a new enum value where consumers are documented to tolerate unknown values;
- additional response headers; and
- a new optional authentication mechanism that does not weaken an existing
  one.

The following require a major release once `v1.0.0` ships:

- removing or renaming an endpoint, event, field, or required header;
- making an optional input required;
- changing an existing field's type or documented meaning;
- narrowing accepted input or changing success into failure for a previously
  valid request, except for a documented security fix; or
- weakening authentication, authorization, replay protection, or rate limits.

The raw Docker API proxy is intentionally outside this guarantee. Its paths,
payloads, and behavior follow the connected Docker daemon's negotiated API
version. Experimental endpoints are also outside the guarantee until their
documentation explicitly marks them stable.

## Environment variables

At `v1.0.0`, every variable in the configuration reference is stable in name,
value grammar, default, precedence, and security semantics.

Adding an optional variable is backward-compatible. Renaming or removing a
variable, changing its default, rejecting a previously valid value, or changing
precedence is breaking unless the old behavior is retained through a
deprecation alias. Secret-file variants remain subject to the same guarantee as
their inline counterparts.

Variables are deprecated through the process below. Undocumented internal test
variables are not public API.

## MCP surface

The MCP endpoint remains experimental through `v0.x`. At `v1.0.0`, the
documented JSON-RPC transport, tool names, input schemas, output schemas, and
read-only security model become stable.

Adding a tool, an optional tool argument, or an optional result field is
backward-compatible. Removing or renaming a tool, making an argument required,
changing a field type, or granting an existing tool new mutation authority is
breaking. A new mutating MCP surface, if ever introduced, must use a separately
documented opt-in and must not silently change the existing read-only endpoint.

## Portwing/Drydock wire contract

The `portwing/1.0` WebSocket subprotocol consists of the envelope and message
shapes documented in `internal/protocol/messages.go` and
`docs/drydock-integration.md`.

Both peers must:

- ignore unknown JSON object fields;
- ignore unknown message types after logging them;
- correlate concurrent operations with `requestId` where the message defines
  one;
- preserve the ordering guarantees documented for exec input/output; and
- bound frames, queues, concurrent work, and stream lifetimes.

Adding an optional field or a new message type is backward-compatible. Removing
or renaming a message or field, changing its type or meaning, adding a required
field, or changing ordering/correlation semantics is breaking.

`drydockCompat` in the agent hello and `serverCompatLevel` in the controller
welcome use `MAJOR.MINOR.PATCH` syntax but the major component is the wire
compatibility epoch. A major mismatch is operator-visible and is not considered
a supported pairing. Minor and patch components may document additive
capabilities without rejecting the connection. Product versions and wire
compatibility versions are independent.

The exact supported product pairings and capability notes live in
[COMPATIBILITY.md](COMPATIBILITY.md). Every release must run contract tests
against the oldest and newest Drydock versions listed for that Portwing release
before the matrix is updated.

## Deprecation process

A stable feature can be deprecated only when all of the following are true:

1. The changelog and relevant reference page name the deprecated behavior.
2. Runtime use produces a bounded, actionable warning when practical.
3. A replacement or migration is documented.
4. The deprecated behavior remains functional for at least one minor release
   and 90 days.
5. Removal occurs only in the next major release.

Security fixes may shorten this window when continuing the behavior would leave
operators exposed. The advisory and release notes must explain the exception.

## Change-control gates

A release that changes a stable surface must update the corresponding artifact:

- `api/openapi.yaml` and API contract tests for HTTP changes;
- the configuration reference and parser tests for environment changes;
- MCP protocol/tool tests for MCP changes;
- Portwing and Drydock wire-contract tests for edge changes; and
- `COMPATIBILITY.md`, the changelog, and operator documentation for any support
  or deprecation change.

Protected-branch checks and required reviews remain mandatory for these
changes. Compatibility is not established by bypassing either repository's
ruleset.
