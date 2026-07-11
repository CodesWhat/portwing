export const faqItems: Array<{ question: string; answer: string }> = [
  {
    question: "What is Portwing and how does it relate to Drydock?",
    answer:
      "Portwing is a lightweight Go agent that gives Drydock a secure foothold on a Docker host. Drydock is the orchestrator — it manages container update policies, schedules checks, and sends update actions. Portwing is the agent it talks to on each host: it sits next to the Docker socket, proxies the Docker Engine API to Drydock, and handles container lifecycle actions on request. Without an agent on each host, Drydock can only reach hosts it has a direct network path to. Portwing adds two connection options: standard mode (Drydock connects inbound to the agent's HTTP port) and edge mode (the agent dials out to Drydock over a WebSocket, so NAT and firewalled hosts work too).",
  },
  {
    question: "What are standard mode and edge mode, and which should I use?",
    answer:
      "Standard mode runs an HTTP/SSE server on port 3000. The Drydock controller connects inbound, calls the agent's inventory endpoints, and holds a long-lived SSE stream for real-time events. This is the production-ready path — use it when the host is reachable from your Drydock instance. Edge mode flips the direction: the agent dials out to Drydock over a WebSocket tunnel, so no inbound port is required. That makes it the right choice for hosts behind NAT, dynamic IPs, or restrictive firewalls. Edge mode requires Drydock 1.5+ and Portwing 0.3.0+; it works end-to-end but is still early access — prefer standard mode for production until you have validated edge mode for your specific workload.",
  },
  {
    question: "How does Ed25519 authentication work and why use it over a shared token?",
    answer:
      "Ed25519 gives each client a distinct cryptographic identity. Every request carries a detached signature over the HTTP method, path, a SHA-256 body hash, a Unix timestamp, and a 128-bit random nonce. The agent verifies the signature, rejects requests with a timestamp skew greater than 60 seconds, and tracks nonces in an in-memory LRU to block replays within the window. No shared secret is transmitted. Compromising one client's private key leaves all other enrolled clients unaffected. By contrast, a shared token (TOKEN or TOKEN_HASH) is a single credential — rotate or revoke it and every caller is disrupted at once. Ed25519 is the recommended production option; token auth is still supported for evaluation and staged migration. Portwing ships a keygen subcommand: run portwing keygen to generate a keypair and get the authorized_keys line to drop on the agent host.",
  },
  {
    question: "Is it safe to expose Docker control through Portwing? What's the security model?",
    answer:
      "Portwing is not a firewall — it is an authenticated transport. Out of the box it authenticates callers and proxies Docker API requests to the daemon. If you want a socket-level allowlist that blocks individual Docker API paths and request body properties (privileged mode, host mounts, capability additions), pair Portwing with sockguard: sockguard writes a filtered unix socket into a shared volume, and Portwing talks to that filtered socket instead of the raw /var/run/docker.sock. The recommended compose file ships with this two-layer configuration pre-wired. All official images run with read_only: true, cap_drop: ALL, no-new-privileges, and Docker secrets instead of inline tokens. The agent has no bundled UI and no inbound ports in edge mode.",
  },
  {
    question: "What does the audit log record and how do I enable it?",
    answer:
      "Set AUDIT_LOG to a file path, stdout, or stderr. The agent writes one JSON record per line for every security-relevant event: authenticated API calls (api_request), failed auth attempts (auth_failure), rate-limit blocks (rate_limited), Compose lifecycle operations (compose_op), exec tunnel opens (exec_start), and Ed25519 key enrollments (enrollment). Every record includes a UTC nanosecond timestamp, the client IP, HTTP method and path, and an outcome (allowed / denied / error). auth_failure records fire before any business logic runs, so you get complete visibility into credential probing even when no request succeeds. Portwing also keeps an in-memory ring buffer of recent records (default 256, controlled by AUDIT_BUFFER_SIZE) accessible via GET /_portwing/audit for pull-based inspection. Audit logging is off by default — no overhead when unset.",
  },
  {
    question: "What platforms does Portwing run on, and how do I install it?",
    answer:
      "Portwing ships as a multi-arch container image from ghcr.io/codeswhat/portwing — linux/amd64 and linux/arm64. The recommended way to run it is via Docker Compose; the examples directory in the repo has ready-to-use compose files for standard mode, standard-with-sockguard, and edge mode. For a local evaluation you can use docker run: generate a token (openssl rand -hex 32), pass it as TOKEN_FILE, and mount the Docker socket. For edge mode, generate an Ed25519 keypair with the portwing keygen subcommand, register the public key with Drydock, and point DRYDOCK_URL at your controller. See https://github.com/CodesWhat/portwing for full getting-started instructions.",
  },
  {
    question: "Is Portwing production-ready?",
    answer:
      "Honestly: it's alpha (v0.6.x). Standard mode is functionally complete and has been running in test environments. The v0.5.x hardening pass tightened pre-auth limits, fixed key-permission enforcement, and added a three-tier fuzz harness plus weekly soak tests; v0.6.0 followed up with the container image running as non-root (UID 65532) by default, a CI egress lockdown across every workflow job, and a Go 1.26.5 bump that clears reachable crypto/tls and os.Root CVEs. Edge mode works end-to-end on Drydock 1.5 but exec-session robustness under sustained load is not yet battle-tested. The public roadmap targets v1.0 for a stable, semantically-versioned API. If you want to run Portwing today, standard mode with sockguard is the strongest posture and the one we test against most heavily. Report issues or security concerns at https://github.com/CodesWhat/portwing/issues or security@codeswhat.com.",
  },
  {
    question: "Do I need sockguard? What does adding it actually change?",
    answer:
      "Sockguard is optional but recommended for production. Without it, Portwing proxies any Docker API request that passes authentication — which includes container creates with privileged mode, arbitrary bind mounts, or capability additions if a caller requests them. Sockguard sits between Portwing and the Docker socket and applies a default-deny allowlist: every Docker API call is blocked unless an explicit rule in sockguard.yaml permits it (method, path, and request body). The bundled sockguard.yaml mirrors the Drydock preset, which covers container lifecycle, image operations, events, and narrow network and volume reads. Even if Portwing were fully compromised, sockguard constrains it to that explicit allowlist. The two-layer docker-compose example in the repo pre-wires both containers with a shared filtered socket.",
  },
];
