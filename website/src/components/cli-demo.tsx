"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { SITE_CONFIG } from "@/lib/site-config";

// ───────────────────────────────────────────────────────────────
// Terminal color tokens. Tailwind-free JSX so the component is
// self-contained and easy to drop anywhere. The container sets
// base fg/bg; tokens below are evaluated inside that context.
// ───────────────────────────────────────────────────────────────
const tok = {
  dim: "opacity-60",
  bold: "font-semibold text-neutral-900 dark:text-neutral-50",
  green: "text-emerald-600 dark:text-emerald-400",
  red: "text-rose-600 dark:text-rose-400",
  yellow: "text-amber-600 dark:text-amber-400",
  cyan: "text-cyan-600 dark:text-cyan-400",
  violet: "text-violet-600 dark:text-violet-400",
} as const;

type RawFrameLine =
  | { kind: "text"; content: React.ReactNode }
  | { kind: "colorart" }
  | { kind: "blank" };

type FrameLine = RawFrameLine & { id: string };

type RawFrame = {
  comment?: string;
  command: string;
  output: RawFrameLine[];
  pauseAfterMs?: number;
};

type Frame = Omit<RawFrame, "output"> & { output: FrameLine[] };

// The startup bird is the real 24-bit truecolor banner art from
// internal/banner/portwing.ans, rendered to /portwing-bird-banner.png by
// scripts/gen-bird-png.mjs (the same pixels `portwing` prints) and shown
// as a crisp pixelated <img>. A browser <pre> of half-blocks adds sub-pixel
// scanlines and warps the aspect; an image reproduces the terminal exactly.

// ───────────────────────────────────────────────────────────────
// Output helpers — build lines that mirror what the real CLI
// prints, using the Tailwind color tokens above.
// ───────────────────────────────────────────────────────────────
const dim = (s: string) => <span className={tok.dim}>{s}</span>;
const bold = (s: string) => <span className={tok.bold}>{s}</span>;
const cyan = (s: string) => <span className={tok.cyan}>{s}</span>;
const violet = (s: string) => <span className={tok.violet}>{s}</span>;
const green = (s: string) => <span className={tok.green}>{s}</span>;

// ───────────────────────────────────────────────────────────────
// Audit log helpers — build JSON audit entries in Portwing's real
// schema (from internal/audit/audit.go). Each line is one JSON
// record on a single line, matching what AUDIT_LOG=stdout emits.
// ───────────────────────────────────────────────────────────────
type AuditEntry = {
  event:
    | "api_request"
    | "auth_failure"
    | "rate_limited"
    | "compose_op"
    | "exec_start"
    | "enrollment";
  actor: string;
  method: string;
  path: string;
  outcome: "allowed" | "denied" | "error";
  status?: number;
  duration_ms?: number;
  // compose_op extras
  operation?: string;
  stack?: string;
  // exec_start extras
  container?: string;
  exec_id?: string;
};

function buildAuditLine(entry: AuditEntry, iso: string): RawFrameLine {
  const outcomeCls = entry.outcome === "allowed" ? tok.green : tok.red;
  return {
    kind: "text",
    content: (
      <span className="whitespace-nowrap text-[0.72rem]">
        <span className={tok.dim}>time={iso} level=INFO msg=</span>
        <span className={tok.dim}>&quot;&quot; </span>
        <span className={tok.dim}>event=</span>
        <span className={tok.violet}>{entry.event}</span> <span className={tok.dim}>actor=</span>
        {entry.actor}{" "}
        {entry.method && (
          <>
            <span className={tok.dim}>method=</span>
            {entry.method}{" "}
          </>
        )}
        {entry.path && (
          <>
            <span className={tok.dim}>path=</span>
            <span className={tok.cyan}>{entry.path}</span>{" "}
          </>
        )}
        <span className={tok.dim}>outcome=</span>
        <span className={outcomeCls}>{entry.outcome}</span>
        {entry.status !== undefined && (
          <>
            {" "}
            <span className={tok.dim}>status=</span>
            {entry.status}
          </>
        )}
        {entry.duration_ms !== undefined && (
          <>
            {" "}
            <span className={tok.dim}>duration_ms=</span>
            {entry.duration_ms.toFixed(2)}
          </>
        )}
        {entry.operation && (
          <>
            {" "}
            <span className={tok.dim}>operation=</span>
            {entry.operation}
          </>
        )}
        {entry.stack && (
          <>
            {" "}
            <span className={tok.dim}>stack=</span>
            {entry.stack}
          </>
        )}
        {entry.container && (
          <>
            {" "}
            <span className={tok.dim}>container=</span>
            {entry.container}
          </>
        )}
        {entry.exec_id && (
          <>
            {" "}
            <span className={tok.dim}>exec_id=</span>
            {entry.exec_id}
          </>
        )}
      </span>
    ),
  };
}

// buildAuditStream builds the fake audit log stream that plays under the
// standard-mode boot banner. Entry fields mirror Portwing's real audit schema
// from internal/audit/audit.go — event/actor/method/path/outcome/status/
// duration_ms, with compose_op and exec_start extras as documented.
function buildAuditStream(): RawFrameLine[] {
  const entries: Array<AuditEntry> = [
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/_portwing/info",
      outcome: "allowed",
      status: 200,
      duration_ms: 1.23,
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/api/containers",
      outcome: "allowed",
      status: 200,
      duration_ms: 6.04,
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/api/watchers",
      outcome: "allowed",
      status: 200,
      duration_ms: 2.18,
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/api/triggers",
      outcome: "allowed",
      status: 200,
      duration_ms: 1.89,
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/api/events",
      outcome: "allowed",
      status: 200,
      duration_ms: 0.41,
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/containers/a3f1b9c/json",
      outcome: "allowed",
      status: 200,
      duration_ms: 3.17,
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/containers/json",
      outcome: "allowed",
      status: 200,
      duration_ms: 4.73,
    },
    {
      event: "auth_failure",
      actor: "198.51.100.9",
      method: "GET",
      path: "/_portwing/info",
      outcome: "denied",
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "POST",
      path: "/containers/a3f1b9c/restart",
      outcome: "allowed",
      status: 204,
      duration_ms: 812.4,
    },
    {
      event: "compose_op",
      actor: "10.0.0.42",
      method: "POST",
      path: "/_portwing/compose",
      outcome: "allowed",
      operation: "up",
      stack: "nginx-stack",
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/api/containers",
      outcome: "allowed",
      status: 200,
      duration_ms: 5.91,
    },
    {
      event: "exec_start",
      actor: "10.0.0.42",
      method: "POST",
      path: "/exec/abc123def456",
      outcome: "allowed",
      container: "abc123def456",
      exec_id: "e7f8a9b1",
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/_portwing/health",
      outcome: "allowed",
      status: 200,
      duration_ms: 0.19,
    },
    {
      event: "api_request",
      actor: "10.0.0.42",
      method: "GET",
      path: "/api/events",
      outcome: "allowed",
      status: 200,
      duration_ms: 0.38,
    },
  ];

  // Timestamps cascade a few ms apart so the eye registers them as a
  // live stream rather than a bulk dump.
  let t = new Date("2026-06-30T00:00:00.000Z").getTime();
  return entries.map((e) => {
    t += 140 + Math.floor(Math.random() * 200);
    const iso = new Date(t).toISOString();
    return buildAuditLine(e, iso);
  });
}

// buildSlogLine builds a single logfmt-style operational log line matching
// Go's slog text handler output: time=<iso> level=INFO msg="<msg>" k=v ...
function slogLine(msg: string, fields: Array<[string, string]>): RawFrameLine {
  const ts = "2026-06-30T00:00:00.001Z";
  return {
    kind: "text",
    content: (
      <span className="whitespace-nowrap text-[0.72rem]">
        <span className={tok.dim}>time={ts} level=</span>
        <span className={tok.cyan}>INFO</span>
        <span className={tok.dim}> msg=&quot;{msg}&quot;</span>
        {fields.map(([k, v]) => (
          <span key={k}>
            {" "}
            <span className={tok.dim}>{k}=</span>
            {v}
          </span>
        ))}
      </span>
    ),
  };
}

// ───────────────────────────────────────────────────────────────
// The script. Each frame is one beat of the CLI tour. Raw frames
// are defined without the `id` field on output lines; the block at
// the bottom of this section stamps stable sequential IDs onto
// every line so React keys never fall back to array indexes.
// ───────────────────────────────────────────────────────────────
const RAW_FRAMES: RawFrame[] = [
  {
    comment:
      "Standard mode: controller connects inbound. Boot the agent and watch the audit stream.",
    command: "portwing",
    output: [
      { kind: "colorart" },
      { kind: "blank" },
      {
        kind: "text",
        content: (
          <>
            {"  "}
            {bold("portwing")} v{SITE_CONFIG.version}
            {"  "}
            {dim("·")}
            {"  "}
            {violet("standard")} {dim("mode")}
            {"  "}
            {dim("·")}
            {"  "}
            {dim("drydock adapter")}
          </>
        ),
      },
      { kind: "blank" },
      slogLine("starting portwing", [
        ["version", `v${SITE_CONFIG.version}`],
        ["mode", "standard"],
      ]),
      slogLine("connected to docker", [["version", "27.3.1"]]),
      slogLine("adapter selected", [["adapter", "drydock"]]),
      slogLine("starting in standard mode", [["address", "0.0.0.0:3000"]]),
      { kind: "blank" },
      ...buildAuditStream(),
    ],
    pauseAfterMs: 5000,
  },
  {
    comment:
      "Edge mode: DRYDOCK_URL + PRIVATE_KEY_FILE set — agent dials out through NAT. No inbound port needed.",
    command: "DRYDOCK_URL=https://drydock.example.com portwing",
    output: [
      { kind: "colorart" },
      { kind: "blank" },
      {
        kind: "text",
        content: (
          <>
            {"  "}
            {bold("portwing")} v{SITE_CONFIG.version}
            {"  "}
            {dim("·")}
            {"  "}
            {violet("edge")} {dim("mode")}
            {"  "}
            {dim("·")}
            {"  "}
            {dim("drydock adapter")}
          </>
        ),
      },
      { kind: "blank" },
      slogLine("starting portwing", [
        ["version", `v${SITE_CONFIG.version}`],
        ["mode", "edge"],
      ]),
      slogLine("connected to docker", [["version", "27.3.1"]]),
      slogLine("adapter selected", [["adapter", "drydock"]]),
      slogLine("starting in edge mode", [["url", "https://drydock.example.com"]]),
      { kind: "blank" },
      {
        kind: "text",
        content: (
          <span className="whitespace-nowrap text-[0.72rem]">
            <span className={tok.dim}>time=2026-06-30T00:00:00.120Z level=</span>
            <span className={tok.cyan}>INFO</span>
            <span className={tok.dim}> msg=&quot;websocket connected&quot;</span>{" "}
            <span className={tok.dim}>controller=</span>
            https://drydock.example.com
          </span>
        ),
      },
      {
        kind: "text",
        content: (
          <span className="whitespace-nowrap text-[0.72rem]">
            <span className={tok.dim}>time=2026-06-30T00:00:00.140Z level=</span>
            <span className={tok.cyan}>INFO</span>
            <span className={tok.dim}> msg=&quot;hello sent&quot;</span>{" "}
            <span className={tok.dim}>agent=</span>
            edge-host-01 <span className={tok.dim}>id=</span>
            f4e3d2c1-b0a9-4e7f-8c6d-5a4b3c2d1e0f
          </span>
        ),
      },
      {
        kind: "text",
        content: (
          <span className="whitespace-nowrap text-[0.72rem]">
            <span className={tok.dim}>time=2026-06-30T00:00:00.280Z level=</span>
            <span className={tok.cyan}>INFO</span>
            <span className={tok.dim}> msg=&quot;welcome received&quot;</span>{" "}
            <span className={tok.dim}>agent_id=</span>
            f4e3d2c1-b0a9-4e7f-8c6d-5a4b3c2d1e0f
          </span>
        ),
      },
      {
        kind: "text",
        content: (
          <span className="whitespace-nowrap text-[0.72rem]">
            <span className={tok.dim}>time=2026-06-30T00:00:00.310Z level=</span>
            <span className={tok.cyan}>INFO</span>
            <span className={tok.dim}> msg=&quot;container sync sent&quot;</span>{" "}
            <span className={tok.dim}>count=</span>
            {green("14")}
          </span>
        ),
      },
      { kind: "blank" },
      {
        kind: "text",
        content: (
          <span className="whitespace-nowrap text-[0.72rem]">
            <span className={tok.dim}>
              time=2026-06-30T00:00:30.001Z level=INFO msg=&quot;&quot;{" "}
            </span>
            <span className={tok.dim}>event=</span>
            <span className={tok.violet}>api_request</span> <span className={tok.dim}>actor=</span>
            edge-tunnel <span className={tok.dim}>method=</span>
            GET <span className={tok.dim}>path=</span>
            <span className={tok.cyan}>/api/containers</span>{" "}
            <span className={tok.dim}>outcome=</span>
            <span className={tok.green}>allowed</span> <span className={tok.dim}>status=</span>
            200 <span className={tok.dim}>duration_ms=</span>
            5.91
          </span>
        ),
      },
      {
        kind: "text",
        content: (
          <span className="whitespace-nowrap text-[0.72rem]">
            <span className={tok.dim}>
              time=2026-06-30T00:00:30.200Z level=INFO msg=&quot;&quot;{" "}
            </span>
            <span className={tok.dim}>event=</span>
            <span className={tok.violet}>api_request</span> <span className={tok.dim}>actor=</span>
            edge-tunnel <span className={tok.dim}>method=</span>
            GET <span className={tok.dim}>path=</span>
            <span className={tok.cyan}>/api/events</span> <span className={tok.dim}>outcome=</span>
            <span className={tok.green}>allowed</span> <span className={tok.dim}>status=</span>
            200 <span className={tok.dim}>duration_ms=</span>
            0.44
          </span>
        ),
      },
    ],
    pauseAfterMs: 4000,
  },
  {
    comment: "Generate an Ed25519 keypair — no shared secret on the wire.",
    command: 'portwing keygen -comment "edge-host-01"',
    output: [
      {
        kind: "text",
        content: <>{dim("Generating Ed25519 keypair...")}</>,
      },
      {
        kind: "text",
        content: (
          <>
            {dim(
              "# Private key (PKCS#8 PEM) — store securely; set as PRIVATE_KEY_FILE on the client:",
            )}
          </>
        ),
      },
      {
        kind: "text",
        content: <>{cyan("-----BEGIN PRIVATE KEY-----")}</>,
      },
      {
        kind: "text",
        content: (
          <span className="text-[0.72rem]">
            MC4CAQAwBQYDK2VwBCIEIJ8vQm3NdXqKpLhY2eRfWs6cTnAzBxOuDiGvE1Fk7mPs
          </span>
        ),
      },
      {
        kind: "text",
        content: <>{cyan("-----END PRIVATE KEY-----")}</>,
      },
      {
        kind: "text",
        content: <>{dim("# authorized_keys line — add to AUTHORIZED_KEYS file on agent host:")}</>,
      },
      {
        kind: "text",
        content: (
          <span className="whitespace-nowrap text-[0.72rem]">
            <span className={tok.violet}>ssh-ed25519</span>{" "}
            AAAAC3NzaC1lZDI1NTE5AAAAIGZr8vQm3NdXqKpLhY2eRfWs6cTnAzBxOuDiGvE1Fk {dim("edge-host-01")}
          </span>
        ),
      },
    ],
    pauseAfterMs: 2800,
  },
];

// Stamp stable sequential IDs onto every output line so React keys
// don't fall back to the array index (which biome's noArrayIndexKey
// rule rightly flags). Generated once at module load — the frames
// array never mutates at runtime, so the IDs stay stable across
// re-renders for the full lifetime of the component.
const frames: Frame[] = RAW_FRAMES.map((f, fi) => ({
  ...f,
  output: f.output.map((line, li) => ({
    ...line,
    id: `f${fi}-l${li}`,
  })),
}));

// ───────────────────────────────────────────────────────────────
// Component
// ───────────────────────────────────────────────────────────────
type Props = {
  className?: string;
  typeMs?: number; // ms per character while typing
  lineMs?: number; // ms between each output line reveal
  autoStart?: boolean;
};

type RunState = {
  frameIdx: number;
  typedChars: number; // how far into frames[frameIdx].command
  revealedLines: number; // how many output lines are visible
  playing: boolean;
  speed: number; // 1, 1.5, 2
};

export function CliDemo({ className, typeMs = 35, lineMs = 55, autoStart = true }: Props) {
  const [state, setState] = useState<RunState>({
    frameIdx: 0,
    typedChars: 0,
    revealedLines: 0,
    playing: autoStart,
    speed: 1,
  });

  const reset = useCallback(() => {
    setState({
      frameIdx: 0,
      typedChars: 0,
      revealedLines: 0,
      playing: autoStart,
      speed: 1,
    });
  }, [autoStart]);

  // Driver effect — walks the state machine one step per tick. The
  // four phases per frame are: typing the command → holding the fully
  // typed prompt → cascading output lines one at a time → pausing on
  // the finished frame → advancing.
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (!state.playing) {
      return;
    }
    const frame = frames[state.frameIdx];
    if (!frame) {
      // End of tour — wait a beat, then loop back to the top.
      timerRef.current = setTimeout(() => {
        setState((s) => ({
          ...s,
          frameIdx: 0,
          typedChars: 0,
          revealedLines: 0,
        }));
      }, 1500 / state.speed);
      return () => {
        if (timerRef.current) clearTimeout(timerRef.current);
      };
    }
    // Phase 1: typing the command character-by-character.
    if (state.typedChars < frame.command.length) {
      timerRef.current = setTimeout(() => {
        setState((s) => ({ ...s, typedChars: s.typedChars + 1 }));
      }, typeMs / state.speed);
      return () => {
        if (timerRef.current) clearTimeout(timerRef.current);
      };
    }
    // Phase 2 + 3: hold for a beat after typing, then cascade lines.
    if (state.revealedLines < frame.output.length) {
      const isFirstLine = state.revealedLines === 0;
      const delay = isFirstLine ? 280 / state.speed : lineMs / state.speed;
      timerRef.current = setTimeout(() => {
        setState((s) => ({ ...s, revealedLines: s.revealedLines + 1 }));
      }, delay);
      return () => {
        if (timerRef.current) clearTimeout(timerRef.current);
      };
    }
    // Phase 4: all output shown, hold for pauseAfterMs, then advance.
    timerRef.current = setTimeout(
      () => {
        setState((s) => ({
          ...s,
          frameIdx: s.frameIdx + 1,
          typedChars: 0,
          revealedLines: 0,
        }));
      },
      (frame.pauseAfterMs ?? 1500) / state.speed,
    );
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [state, typeMs, lineMs]);

  const currentFrame = frames[state.frameIdx];
  const typedCommand = currentFrame ? currentFrame.command.slice(0, state.typedChars) : "";
  const showCursor = state.playing;
  const visibleLines = currentFrame ? currentFrame.output.slice(0, state.revealedLines) : [];

  // Auto-scroll the terminal body to the bottom whenever a new line
  // is revealed. Content flows top-down; when it exceeds the body
  // height, the oldest content scrolls off the top and the newest
  // line stays pinned at the bottom edge — the tail -f feel. At the
  // start of a new frame (revealedLines === 0) we reset to the top
  // so the beat opens with the prompt in view instead of mid-scroll.
  const bodyRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = bodyRef.current;
    if (!el) return;
    if (state.revealedLines === 0) {
      el.scrollTop = 0;
      return;
    }
    // Referencing frameIdx here keeps biome's exhaustive-deps rule
    // happy without changing observable behavior; the value is used
    // as part of the scroll decision indirectly (new frame → reset).
    void state.frameIdx;
    el.scrollTop = el.scrollHeight;
  }, [state.frameIdx, state.revealedLines]);

  return (
    <div className={`w-full ${className ?? ""}`}>
      <div className="overflow-hidden rounded-xl border border-neutral-300 bg-neutral-50 shadow-2xl dark:border-neutral-800 dark:bg-neutral-950">
        {/* macOS window chrome */}
        <div className="flex items-center gap-2 border-b border-neutral-300 bg-neutral-200 px-4 py-3 dark:border-neutral-800 dark:bg-neutral-900">
          <span className="size-3 rounded-full bg-red-500" />
          <span className="size-3 rounded-full bg-yellow-500" />
          <span className="size-3 rounded-full bg-green-500" />
          <span className="ml-3 select-none text-xs text-neutral-500 dark:text-neutral-400">
            portwing — zsh
          </span>
          <div className="ml-auto flex items-center gap-2 text-xs text-neutral-500 dark:text-neutral-400">
            <button
              type="button"
              onClick={() => setState((s) => ({ ...s, playing: !s.playing }))}
              className="rounded border border-neutral-400 px-2 py-0.5 hover:border-violet-500 hover:text-violet-600 dark:border-neutral-700 dark:hover:text-violet-400"
            >
              {state.playing ? "pause" : "play"}
            </button>
            <button
              type="button"
              onClick={reset}
              className="rounded border border-neutral-400 px-2 py-0.5 hover:border-violet-500 hover:text-violet-600 dark:border-neutral-700 dark:hover:text-violet-400"
            >
              restart
            </button>
            {[1, 1.5, 2].map((speed) => (
              <button
                key={speed}
                type="button"
                onClick={() => setState((s) => ({ ...s, speed }))}
                className={`rounded border px-2 py-0.5 ${
                  state.speed === speed
                    ? "border-violet-500 text-violet-600 dark:text-violet-400"
                    : "border-neutral-400 hover:border-violet-500 dark:border-neutral-700"
                }`}
              >
                {speed}×
              </button>
            ))}
          </div>
        </div>

        {/* Terminal body. Content flows top-down from the comment
            line, and the effect auto-scrolls the body so the newest
            revealed line is always pinned at the bottom edge when
            the content overflows. Scrollbar hidden via arbitrary CSS
            to keep the terminal chrome clean. */}
        <div
          ref={bodyRef}
          className="h-[34rem] overflow-y-auto px-5 py-4 font-mono text-sm leading-[1.6] text-neutral-800 [scrollbar-width:none] dark:text-neutral-200 [&::-webkit-scrollbar]:hidden"
        >
          {currentFrame && (
            <div key={state.frameIdx}>
              {currentFrame.comment && (
                <div className={`${tok.dim} ${tok.yellow}`}># {currentFrame.comment}</div>
              )}
              <div>
                <span className={tok.green}>$</span>{" "}
                <span className={tok.bold}>{typedCommand}</span>
                {showCursor && state.typedChars < currentFrame.command.length && (
                  <span className="inline-block h-[1.1em] w-[0.55em] translate-y-[0.15em] bg-neutral-800 dark:bg-neutral-200" />
                )}
              </div>
              {visibleLines.map((line) => {
                if (line.kind === "blank") {
                  return <div key={line.id}>&nbsp;</div>;
                }
                if (line.kind === "colorart") {
                  return (
                    // The real CLI banner art (internal/banner/portwing.ans),
                    // rendered to a PNG by scripts/gen-bird-png.mjs — the exact pixels
                    // `portwing` prints. Shown crisp/pixelated so it matches the
                    // terminal (a <pre> of half-blocks gets sub-pixel scanlines + the
                    // wrong cell aspect in browsers). `animate-print-down` wipes it in
                    // top-to-bottom so it draws like the terminal is printing it.
                    // biome-ignore lint/performance/noImgElement: tiny static pixel-art asset
                    <img
                      key={line.id}
                      src="/portwing-bird-banner.png"
                      width={50}
                      height={42}
                      aria-hidden="true"
                      alt=""
                      className="animate-print-down mx-auto my-1 block h-auto w-[200px] [image-rendering:pixelated]"
                    />
                  );
                }
                return (
                  <div key={line.id}>
                    <span>{line.content}</span>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
      <p className="mt-3 text-center text-xs text-neutral-500 dark:text-neutral-400">
        A hand-rendered recreation of the real CLI — every frame mirrors what{" "}
        <code className="rounded bg-neutral-200 px-1 dark:bg-neutral-800">portwing</code> actually
        prints. Use the controls above to pause, restart, or change speed.
      </p>
    </div>
  );
}
