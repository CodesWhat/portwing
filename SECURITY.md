# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Lookout, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please email security@codeswhat.com with:

1. A description of the vulnerability
2. Steps to reproduce
3. Potential impact
4. Suggested fix (if any)

We will acknowledge your report within 48 hours and aim to release a fix within 7 days for critical vulnerabilities.

## Supported Versions

| Version | Supported |
|---------|-----------|
| 1.x.x   | Yes       |

## Security Measures

Lookout implements the following security measures:

- **Authentication**: Token-based auth with timing-safe comparison (`crypto/subtle`). Supports raw token (`TOKEN`) or Argon2id PHC hash (`TOKEN_HASH`) — the two are mutually exclusive.
- **Token at Rest**: Set `TOKEN_HASH` to an Argon2id PHC string (produced by `lookout hash-token`) so a compromised config or environment variable does not expose the credential. A SHA-256 success cache keeps per-request cost flat after the first verified request; failed attempts always traverse the full Argon2id derivation and are subject to rate limiting.
- **Rate Limiting**: 10 failed auth attempts per IP per minute, checked before token verification.
- **TLS**: TLS 1.2+ with modern AEAD cipher suites only
- **Docker Compose Security**: Path traversal protection, env var validation and denylist, service name injection prevention
- **Resource Limits**: WebSocket read (16 MB), response body (100 MB), exec body (10 MB), concurrent sessions (100)
- **Minimal Attack Surface**: Static binary, Wolfi OS base image with no package manager
