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

- **Authentication**: Token-based auth with timing-safe comparison (`crypto/subtle`)
- **Rate Limiting**: 10 failed auth attempts per IP per minute
- **TLS**: TLS 1.2+ with modern AEAD cipher suites only
- **Docker Compose Security**: Path traversal protection, env var validation and denylist, service name injection prevention
- **Resource Limits**: WebSocket read (16 MB), response body (100 MB), exec body (10 MB), concurrent sessions (100)
- **Minimal Attack Surface**: Static binary, Wolfi OS base image with no package manager
