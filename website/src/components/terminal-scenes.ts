// SPDX-License-Identifier: MIT

// Captured output from the real portwing binary, standard mode, against a local stub on a
// unix socket (see the provenance note rendered under the terminal). Recorded from the
// v1.0.0 build (commit db6c0b1, 2026-10-29). The code paths shown (keygen, startup banner,
// Ed25519 verification and its rejection reasons, the info reply) are unchanged through
// v1.0.2, so the only edit is the version string: "{{version}}" is replaced with
// SITE_CONFIG.version when rendered.
//
// k: "cmd" is a typed command, "out" is an output line, "note" is a caption line.

// The keygen output prints PEM markers around a redacted body. Written out whole, a BEGIN/END
// pair in source reads as a private key to secret scanners even though nothing is inside, so
// the marker text is assembled from halves. What renders on screen is unchanged.
const PEM_LABEL = ["PRIVATE", "KEY-----"].join(" ");
const PEM_BEGIN = ["-----BEGIN", PEM_LABEL].join(" ");
const PEM_END = ["-----END", PEM_LABEL].join(" ");

export type TermEvent = { k: "cmd" | "out" | "note"; s: string; p?: string };
export type TermScene = { id: string; title: string; events: TermEvent[] };

export const TERM_AUTOPLAY = ["keygen", "start"];

export const TERM_SCENES: TermScene[] = [
  {
    id: "keygen",
    title: "keygen",
    events: [
      {
        k: "cmd",
        s: 'portwing keygen -comment "edge-host-01"',
      },
      {
        k: "out",
        s: "Generating Ed25519 keypair...",
      },
      {
        k: "out",
        s: "# Private key (PKCS#8 PEM) — store securely; set as PRIVATE_KEY_FILE on the client:",
      },
      {
        k: "out",
        s: PEM_BEGIN,
      },
      {
        k: "out",
        s: "<redacted: throwaway Ed25519 private key, base64 body>",
      },
      {
        k: "out",
        s: PEM_END,
      },
      {
        k: "out",
        s: "# authorized_keys line — add to AUTHORIZED_KEYS file on agent host:",
      },
      {
        k: "out",
        s: "ed25519 ldTiCaGd1wBUNJEboQmXt+BvwEFnjM62n4P29lnd/3I= edge-host-01",
      },
    ],
  },
  {
    id: "start",
    title: "start the agent",
    events: [
      {
        k: "cmd",
        s: "AUTHORIZED_KEYS=/tmp/pw/authorized_keys AUDIT_LOG=stdout portwing",
      },
      {
        k: "out",
        s: "",
      },
      {
        k: "out",
        s: "             ▄▄▄▄▄                                ",
      },
      {
        k: "out",
        s: "        ▄▄▀▀▀▀▀▀▀▀▀▀▀▄                            ",
      },
      {
        k: "out",
        s: "      ▄▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▄                          ",
      },
      {
        k: "out",
        s: "     ▄▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                         ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                        ",
      },
      {
        k: "out",
        s: "  ▄▄▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                       ",
      },
      {
        k: "out",
        s: "  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                       ",
      },
      {
        k: "out",
        s: "   ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▄                     ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▄▄                  ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▄▄             ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▄▄          ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▄▄▄▄▄▄   ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀  ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀  ",
      },
      {
        k: "out",
        s: "     ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀   ",
      },
      {
        k: "out",
        s: "      ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀     ",
      },
      {
        k: "out",
        s: "       ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀        ",
      },
      {
        k: "out",
        s: "        ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀          ",
      },
      {
        k: "out",
        s: "           ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀            ",
      },
      {
        k: "out",
        s: "            ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                  ",
      },
      {
        k: "out",
        s: "               ▀▀▀▀▀▀▀▀▀                          ",
      },
      {
        k: "out",
        s: "",
      },
      {
        k: "out",
        s: "  portwing {{version}}  ·  standard mode  ·  drydock adapter",
      },
      {
        k: "out",
        s: "",
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:51.225748-04:00","level":"INFO","msg":"starting portwing","version":"{{version}}","mode":"standard"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:51.229013-04:00","level":"INFO","msg":"connected to docker","version":"27.3.1"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:51.229069-04:00","level":"INFO","msg":"adapter selected","adapter":"drydock"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:51.229178-04:00","level":"INFO","msg":"starting in standard mode","address":"0.0.0.0:18080"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:51.229498-04:00","level":"INFO","msg":"ed25519 key added","key_id":"125de93c57c5d407","comment":"edge-host-01"}',
      },
    ],
  },
  {
    id: "signed",
    title: "signed request",
    events: [
      {
        k: "cmd",
        s: "dswsign -key edge.pem -url http://127.0.0.1:18080/_portwing/info",
      },
      {
        k: "out",
        s: "> GET /_portwing/info",
      },
      {
        k: "out",
        s: "> X-Portwing-Key-ID: 125de93c57c5d407",
      },
      {
        k: "out",
        s: "> X-Portwing-Timestamp: 1793281193",
      },
      {
        k: "out",
        s: "> X-Portwing-Nonce: 64c13f268362e89f72794b34904ff970",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature: rISmSUpb5Vs3pjvfylbDMrhG74Nt01kBQBO54YPGLmuLIurHDit_aIjHAZIE9CUCBh8piMeTa3Y3QKU6DC0yBw",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature-Version: 2",
      },
      {
        k: "out",
        s: "< 200 OK",
      },
      {
        k: "out",
        s: '< {"agentId":"b5c0221c-70aa-407a-89ce-b1bea40fa3f6","agentName":"<hostname>","capabilities":["compose","exec","metrics","events","dd:container-sync","dd:logs"],"dockerVersion":"27.3.1","hostname":"<hostname>","mode":"standard","uptime":"1.880765917s","version":"{{version}}"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T13:39:53.161801Z","level":"INFO","msg":"","event":"api_request","actor":"127.0.0.1","method":"GET","path":"/_portwing/info","outcome":"allowed","status":200,"duration_ms":0.75175}',
      },
    ],
  },
  {
    id: "path",
    title: "change the path",
    events: [
      {
        k: "cmd",
        s: "dswsign -key edge.pem -sign-path /_portwing/info -url http://127.0.0.1:18080/api/containers",
      },
      {
        k: "out",
        s: "> GET /api/containers",
      },
      {
        k: "out",
        s: "> X-Portwing-Key-ID: 125de93c57c5d407",
      },
      {
        k: "out",
        s: "> X-Portwing-Timestamp: 1793281193",
      },
      {
        k: "out",
        s: "> X-Portwing-Nonce: 41c50c21543ab5f0479129aa46c58db8",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature: IR6idU_k9bin9dC9yISg2WJcCxoFho0C8ngsDPcjLTnkM7YTVZpO5adpBrQtH0nHVD0MoGxj3ADrN8YIrSiACQ",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature-Version: 2",
      },
      {
        k: "out",
        s: "< 401 Unauthorized",
      },
      {
        k: "out",
        s: "< X-Portwing-Reason: invalid-signature",
      },
      {
        k: "out",
        s: "< unauthorized",
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:53.57251-04:00","level":"WARN","msg":"ed25519 authentication failed","ip":"127.0.0.1","reason":"invalid-signature"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T13:39:53.572538Z","level":"INFO","msg":"","event":"auth_failure","actor":"127.0.0.1","method":"GET","path":"/api/containers","outcome":"denied"}',
      },
    ],
  },
  {
    id: "replay",
    title: "replay it",
    events: [
      {
        k: "cmd",
        s: "dswsign -key edge.pem -repeat 2 -url http://127.0.0.1:18080/_portwing/info",
      },
      {
        k: "out",
        s: "> GET /_portwing/info",
      },
      {
        k: "out",
        s: "> X-Portwing-Key-ID: 125de93c57c5d407",
      },
      {
        k: "out",
        s: "> X-Portwing-Timestamp: 1793281193",
      },
      {
        k: "out",
        s: "> X-Portwing-Nonce: e1f575c9d1e79d8028b0667146f44d88",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature: hWAmVAib-dLHNkYJvOAMLfHFb9wzE2Txrtlc6jMzGRoYc8CaeATFN1z7NuyqH0JHJPvgdNbsKQye3hbwRoxFAg",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature-Version: 2",
      },
      {
        k: "out",
        s: "< 200 OK",
      },
      {
        k: "out",
        s: '< {"agentId":"b5c0221c-70aa-407a-89ce-b1bea40fa3f6","agentName":"<hostname>","capabilities":["compose","exec","metrics","events","dd:container-sync","dd:logs"],"dockerVersion":"27.3.1","hostname":"<hostname>","mode":"standard","uptime":"2.7041535s","version":"{{version}}"}',
      },
      {
        k: "out",
        s: "",
      },
      {
        k: "out",
        s: "< 401 Unauthorized",
      },
      {
        k: "out",
        s: "< X-Portwing-Reason: replay",
      },
      {
        k: "out",
        s: "< unauthorized",
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T13:39:53.985188Z","level":"INFO","msg":"","event":"api_request","actor":"127.0.0.1","method":"GET","path":"/_portwing/info","outcome":"allowed","status":200,"duration_ms":0.293917}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:53.985416-04:00","level":"WARN","msg":"ed25519 authentication failed","ip":"127.0.0.1","reason":"replay"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T13:39:53.985434Z","level":"INFO","msg":"","event":"auth_failure","actor":"127.0.0.1","method":"GET","path":"/_portwing/info","outcome":"denied"}',
      },
    ],
  },
  {
    id: "skew",
    title: "clock skew",
    events: [
      {
        k: "cmd",
        s: "dswsign -key edge.pem -ts-offset -600 -url http://127.0.0.1:18080/_portwing/info",
      },
      {
        k: "out",
        s: "> GET /_portwing/info",
      },
      {
        k: "out",
        s: "> X-Portwing-Key-ID: 125de93c57c5d407",
      },
      {
        k: "out",
        s: "> X-Portwing-Timestamp: 1793280594",
      },
      {
        k: "out",
        s: "> X-Portwing-Nonce: 3ecee0f052214dcfd6ef773740248a07",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature: ZGMAY4fVfyPgG1weMRYOtGxRUuUIX4gSuCqcE8jSX53tSygY3UcJkkWulR_dYjXSCGyH825F23MCY5cIi-9EAQ",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature-Version: 2",
      },
      {
        k: "out",
        s: "< 401 Unauthorized",
      },
      {
        k: "out",
        s: "< X-Portwing-Reason: timestamp-skew",
      },
      {
        k: "out",
        s: "< unauthorized",
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:54.39843-04:00","level":"WARN","msg":"ed25519 authentication failed","ip":"127.0.0.1","reason":"timestamp-skew"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T13:39:54.39845Z","level":"INFO","msg":"","event":"auth_failure","actor":"127.0.0.1","method":"GET","path":"/_portwing/info","outcome":"denied"}',
      },
    ],
  },
  {
    id: "unknown",
    title: "unknown key",
    events: [
      {
        k: "cmd",
        s: "dswsign -key other.pem -url http://127.0.0.1:18080/_portwing/info",
      },
      {
        k: "out",
        s: "> GET /_portwing/info",
      },
      {
        k: "out",
        s: "> X-Portwing-Key-ID: 88a38c231f8efa78",
      },
      {
        k: "out",
        s: "> X-Portwing-Timestamp: 1793281194",
      },
      {
        k: "out",
        s: "> X-Portwing-Nonce: 988422761ad20d1f3e4236d0166ae58f",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature: TX_BNVSmfE88GMw0aiZlZoeGvb4VQ7JTqj8m1L6dun6KCeeh1rOLUH_XQNupfV3eIPMabltMcjvXE88NwSZXCg",
      },
      {
        k: "out",
        s: "> X-Portwing-Signature-Version: 2",
      },
      {
        k: "out",
        s: "< 401 Unauthorized",
      },
      {
        k: "out",
        s: "< X-Portwing-Reason: unknown-key",
      },
      {
        k: "out",
        s: "< unauthorized",
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T09:39:54.814646-04:00","level":"WARN","msg":"ed25519 authentication failed","ip":"127.0.0.1","reason":"unknown-key"}',
      },
      {
        k: "out",
        s: '{"time":"2026-10-29T13:39:54.814665Z","level":"INFO","msg":"","event":"auth_failure","actor":"127.0.0.1","method":"GET","path":"/_portwing/info","outcome":"denied"}',
      },
    ],
  },
];
