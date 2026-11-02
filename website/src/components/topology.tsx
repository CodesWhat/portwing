// SPDX-License-Identifier: MIT

"use client";

import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { Ic } from "@/components/icons";

const STEPS: { key: string; node: React.ReactNode }[] = [
  {
    key: "hello",
    node: (
      <>
        agent → controller: <b>hello</b>
      </>
    ),
  },
  {
    key: "welcome",
    node: (
      <>
        controller → agent: <b>welcome</b>
      </>
    ),
  },
  { key: "cs", node: <b>dd:component_sync</b> },
  { key: "ct", node: <b>dd:container_sync</b> },
];

/**
 * Three-layer topology strip plus the edge handshake, which plays once when it scrolls into
 * view. `progress` counts finished steps; the step at that index is the active one. Reduced
 * motion shows every step finished.
 */
export function Topology() {
  const [progress, setProgress] = useState(STEPS.length);
  const [reduce, setReduce] = useState(false);
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);
  const root = useRef<HTMLDivElement>(null);

  const clear = useCallback(() => {
    for (const t of timers.current) clearTimeout(t);
    timers.current = [];
  }, []);

  const play = useCallback(() => {
    clear();
    setProgress(-1);
    STEPS.forEach((_, i) => {
      timers.current.push(setTimeout(() => setProgress(i), 500 + i * 750));
    });
    timers.current.push(setTimeout(() => setProgress(STEPS.length), 500 + STEPS.length * 750));
  }, [clear]);

  useEffect(() => {
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    setReduce(reduced);
    if (reduced) return;
    const node = root.current;
    if (!node || !("IntersectionObserver" in window)) return;
    let seen = false;
    const observer = new IntersectionObserver(
      (entries, o) => {
        if (!seen && entries[0].isIntersecting) {
          seen = true;
          play();
          o.disconnect();
        }
      },
      { threshold: 0.4 },
    );
    observer.observe(node);
    return () => {
      observer.disconnect();
      clear();
    };
  }, [play, clear]);

  const replay = () => {
    if (reduce) setProgress(STEPS.length);
    else play();
  };

  return (
    <div className="topo" id="topology" role="group" aria-label="Topology" ref={root}>
      <h3>
        <span>Topology · three layers</span>
        <span className="grow" />
        <button type="button" className="btn sm" id="pw-hs-replay" onClick={replay}>
          <Ic name="rewind" />
          Replay handshake
        </button>
      </h3>
      <ol className="flow">
        <li>
          <a className="node-c" href="https://getdrydock.com">
            <img
              src="/brand/whale-68.webp"
              alt=""
              width={34}
              height={23}
              loading="lazy"
              decoding="async"
            />
            <span>
              <b>Drydock</b>
              <small>controller + UI</small>
            </span>
          </a>
          <span className="arr" aria-hidden="true">
            <small>HTTPS + SSE</small>→
          </span>
        </li>
        <li>
          <a className="node-c" href="https://portwing.codeswhat.com">
            <img
              src="/brand/pigeon-64.webp"
              alt=""
              width={34}
              height={34}
              loading="lazy"
              decoding="async"
            />
            <span>
              <b>Portwing</b>
              <small>agent · Ed25519</small>
            </span>
          </a>
          <span className="arr" aria-hidden="true">
            <small>filtered socket</small>→
          </span>
        </li>
        <li>
          <a className="node-c" href="https://getsockguard.com">
            <img
              src="/brand/dog-64.webp"
              alt=""
              width={34}
              height={34}
              loading="lazy"
              decoding="async"
            />
            <span>
              <b>Sockguard</b>
              <small>socket filter</small>
            </span>
          </a>
          <span className="arr" aria-hidden="true">
            →
          </span>
        </li>
        <li>
          <a className="node-c" href="https://docs.docker.com/reference/api/engine/">
            <Ic name="boat" />
            <span>
              <b>Docker</b>
              <small>Engine API</small>
            </span>
          </a>
        </li>
      </ol>
      <div className="hs">
        <p className="dir">
          Edge mode, as it plays out once: the agent dials out over the <code>portwing/1.0</code>{" "}
          WebSocket, so there is no inbound control port.
        </p>
        <ol id="pw-hs" aria-label="Edge handshake">
          {STEPS.map((step, i) => (
            <Fragment key={step.key}>
              {i > 0 ? (
                <li className="sep" aria-hidden="true">
                  →
                </li>
              ) : null}
              <li
                data-k={step.key}
                data-done={String(progress > i)}
                data-on={String(progress === i)}
              >
                {step.node}
              </li>
            </Fragment>
          ))}
        </ol>
      </div>
    </div>
  );
}
