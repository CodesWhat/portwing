// SPDX-License-Identifier: MIT

"use client";

import { useEffect, useRef } from "react";
import {
  TERM_AUTOPLAY,
  TERM_SCENES,
  type TermEvent,
  type TermScene,
} from "@/components/terminal-scenes";
import { SITE_CONFIG } from "@/lib/site-config";

// Recorded terminal player. Autoplays the startup scenes once with typed commands and instant
// output lines; each prompt button replays one scene. Reduced motion renders the final state
// with no typing.

const BUTTONS: { scene: string; label: string }[] = [
  { scene: "keygen", label: "keygen" },
  { scene: "start", label: "agent" },
  { scene: "signed", label: "signed" },
  { scene: "path", label: "bad path" },
  { scene: "replay", label: "replay it" },
  { scene: "skew", label: "skew" },
  { scene: "unknown", label: "bad key" },
];

const ART = /[▀-▟]/;

function esc(s: string) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function withVersion(s: string) {
  return s.replaceAll("{{version}}", SITE_CONFIG.version);
}

function highlight(raw: string) {
  const s = raw;
  let h = esc(s);
  h = h.replace(
    /&lt;redacted[^&]*&gt;|&lt;hostname&gt;/g,
    (m) => `<span class="redact">${m}</span>`,
  );
  if (/^\s*\{/.test(s)) {
    h = h.replace(/"([A-Za-z_][\w-]*)":/g, '<span class="jk">"$1"</span>:');
    h = h.replace(
      /"(would_deny|deny|denied|WARN|ERROR|unauthorized|timestamp-skew|replay|unknown-key|invalid-signature)"/g,
      (_m, v: string) => `<span class="${v === "WARN" ? "warn" : "bad"}">"${v}"</span>`,
    );
    h = h.replace(/"(allowed|allow|INFO)"/g, '<span class="ok">"$1"</span>');
  } else if (/^(&lt; |HTTP)/.test(h)) {
    h = h
      .replace(/(40[13] [A-Za-z ]+)/, '<span class="bad">$1</span>')
      .replace(/(200 OK)/, '<span class="ok">$1</span>')
      .replace(/(X-Portwing-Reason: )([\w-]+)/, '$1<span class="bad">$2</span>');
  } else if (/^\s*Decision:/.test(s)) {
    h = h
      .replace(/(allow)\s*$/, '<span class="ok">$1</span>')
      .replace(/(deny)\s*$/, '<span class="bad">$1</span>');
  } else if (/^\s*[✓✗]/.test(s)) {
    h = h
      .replace(/^(\s*)(✓)/, '$1<span class="ok">$2</span>')
      .replace(/^(\s*)(✗)/, '$1<span class="bad">$2</span>');
  }
  return h;
}

function lineEl(ev: TermEvent) {
  const d = document.createElement("div");
  if (ev.k === "cmd") {
    d.className = `tl cmd${ev.p === "client" ? " client" : ""}`;
    d.innerHTML = '<span class="ps" aria-hidden="true">$</span><span class="cx"></span>';
    return d;
  }
  const text = withVersion(ev.s);
  d.className = `tl ${ev.k === "note" ? "note" : ART.test(text) ? "art" : "out"}`;
  d.innerHTML = ev.k === "note" ? esc(text) : highlight(text) || "&nbsp;";
  return d;
}

const sceneById = (id: string): TermScene | undefined => TERM_SCENES.find((s) => s.id === id);

export function Terminal() {
  const rootRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const titleRef = useRef<HTMLElement>(null);
  const liveRef = useRef<HTMLParagraphElement>(null);
  const cmdsRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const root = rootRef.current;
    const body = bodyRef.current;
    const cmds = cmdsRef.current;
    if (!root || !body || !cmds) return;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const btns = [...cmds.querySelectorAll<HTMLButtonElement>(".tcmd")];
    let token = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let started = false;

    const mark = (id: string) => {
      for (const b of btns) b.setAttribute("aria-pressed", String(b.dataset.scene === id));
    };
    const bottom = () => {
      body.scrollTop = body.scrollHeight;
    };
    const finish = () => {
      root.dataset.state = "done";
      body.querySelector(".caret")?.remove();
    };
    const renderInstant = (scenes: TermScene[], clear: boolean) => {
      token++;
      clearTimeout(timer);
      if (clear) body.textContent = "";
      for (const sc of scenes) {
        for (const ev of sc.events) {
          const d = lineEl(ev);
          if (ev.k === "cmd") {
            const cx = d.querySelector(".cx");
            if (cx) cx.textContent = withVersion(ev.s);
          }
          body.appendChild(d);
        }
      }
      bottom();
      finish();
    };
    const play = (scenes: TermScene[], clear: boolean) => {
      if (reduce) {
        renderInstant(scenes, clear);
        return;
      }
      const my = ++token;
      clearTimeout(timer);
      if (clear) body.textContent = "";
      root.dataset.state = "playing";
      const queue = scenes.flatMap((sc) => sc.events);
      let i = 0;
      const caret = document.createElement("span");
      caret.className = "caret";
      caret.setAttribute("aria-hidden", "true");
      const step = () => {
        if (my !== token) return;
        if (i >= queue.length) {
          finish();
          return;
        }
        const ev = queue[i++];
        const d = lineEl(ev);
        body.appendChild(d);
        if (ev.k === "cmd") {
          const ct = d.querySelector(".cx");
          if (!ct) return;
          const s = withVersion(ev.s);
          const speed = ev.p === "client" ? 7 : 22;
          let n = 0;
          const tn = document.createTextNode("");
          ct.appendChild(caret);
          ct.insertBefore(tn, caret);
          const typeNext = () => {
            if (my !== token) return;
            if (n >= s.length) {
              timer = setTimeout(step, 220);
              return;
            }
            const k = 1 + (Math.random() < 0.25 ? 1 : 0);
            n = Math.min(s.length, n + k);
            tn.nodeValue = s.slice(0, n);
            bottom();
            timer = setTimeout(typeNext, speed + Math.random() * speed);
          };
          typeNext();
        } else {
          bottom();
          const big = ART.test(ev.s);
          timer = setTimeout(step, big ? 22 : ev.s.startsWith("{") ? 70 : 34);
        }
      };
      step();
    };
    const setTitle = (t: string) => {
      if (titleRef.current) titleRef.current.textContent = t;
    };

    const handlers = btns.map((b) => {
      const onClick = () => {
        const sc = sceneById(b.dataset.scene ?? "");
        if (!sc) return;
        mark(sc.id);
        setTitle(`portwing ${SITE_CONFIG.version} / ${sc.title}`);
        if (liveRef.current) liveRef.current.textContent = `Playing ${sc.title}`;
        play([sc], true);
      };
      b.addEventListener("click", onClick);
      return [b, onClick] as const;
    });
    const first = TERM_AUTOPLAY.map(sceneById).filter((s): s is TermScene => Boolean(s));
    mark(first[first.length - 1].id);
    const start = () => {
      if (started) return;
      started = true;
      play(first, true);
    };
    let observer: IntersectionObserver | undefined;
    if (reduce) renderInstant(first, true);
    else if ("IntersectionObserver" in window) {
      observer = new IntersectionObserver(
        (entries, o) => {
          if (entries[0].isIntersecting) {
            o.disconnect();
            start();
          }
        },
        { threshold: 0.2 },
      );
      observer.observe(root);
    } else start();

    return () => {
      token++;
      clearTimeout(timer);
      observer?.disconnect();
      for (const [b, onClick] of handlers) b.removeEventListener("click", onClick);
    };
  }, []);

  return (
    <div
      className="term"
      id="term"
      ref={rootRef}
      role="region"
      aria-label="Recorded terminal session"
      data-state="idle"
    >
      <div className="term-bar">
        <span className="dots" aria-hidden="true">
          <i />
          <i />
          <i />
        </span>
        <b id="term-title" ref={titleRef}>
          portwing {SITE_CONFIG.version} · Ed25519 auth
        </b>
      </div>
      <div
        className="term-body"
        id="term-body"
        ref={bodyRef}
        // biome-ignore lint/a11y/noNoninteractiveTabindex: scrollable log region must be keyboard reachable
        tabIndex={0}
        role="log"
        aria-label="Terminal output, scrollable"
        aria-live="off"
      />
      <div className="term-cmds" ref={cmdsRef} role="group" aria-label="Replay a recorded scene">
        {BUTTONS.map(({ scene, label }) => (
          <button
            key={scene}
            type="button"
            className="tcmd"
            data-scene={scene}
            aria-pressed="false"
          >
            <span className="ps" aria-hidden="true">
              $
            </span>
            <span>{label}</span>
          </button>
        ))}
      </div>
      <p className="term-cap">Recorded from portwing v1.0.0 · local stub</p>
      <details className="term-more">
        <summary>Provenance and redactions</summary>
        <div>
          Real output from the binary built from the v1.0.0 tag (commit db6c0b1), recorded
          2026-10-29, standard mode, against a local stub on a unix socket instead of a Docker
          daemon. Requests are signed by <code>dswsign</code>, a small Go program that calls
          portwing's own <code>internal/auth</code> (CanonicalMessage, BodyHashHex, NewNonce), the
          same calls the repo's <code>signRequest</code> test helper makes. Redacted: the private
          key body in the keygen output, and the machine hostname in the agent's info reply. The
          code paths shown are unchanged through v{SITE_CONFIG.version}; only the version strings
          were updated.
        </div>
      </details>
      <p className="sr" id="term-live" ref={liveRef} aria-live="polite" />
    </div>
  );
}
