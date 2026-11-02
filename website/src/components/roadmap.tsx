// SPDX-License-Identifier: MIT

import { type Milestone, roadmap } from "@/app/data/roadmap";
import { Ic } from "@/components/icons";
import { Section } from "@/components/section";
import { SITE_CONFIG } from "@/lib/site-config";

// Roadmap = git-log style timeline, newest first. Released = filled node, HEAD = the last
// released milestone. Each row previews two items; the rest sit in a "+N more" disclosure.
// Older releases fold into one disclosure that continues the same rail.

const PREVIEW_ITEMS = 2;
const VISIBLE_RELEASED_BEFORE_HEAD = 4;

const newestFirst = [...roadmap].reverse();
const headIndex = newestFirst.findIndex((m) => m.status === "released");

function Row({ milestone, head, first }: { milestone: Milestone; head: boolean; first: boolean }) {
  const preview = milestone.items.slice(0, PREVIEW_ITEMS);
  const rest = milestone.items.slice(PREVIEW_ITEMS);
  const planned = milestone.status !== "released";
  const classes = ["row", first ? "first" : "", planned ? "plannedrow" : ""].filter(Boolean);
  return (
    <li className={classes.join(" ")}>
      <div className="rail">
        <i className={head ? "node head" : planned ? "node" : "node rel"} />
      </div>
      <div className="body">
        <div className="hd">
          <span className="v">{milestone.version}</span>
          {head ? (
            <span className="chip nocase t-primary">HEAD -&gt; v{SITE_CONFIG.version}</span>
          ) : null}
          <span className="tt">
            {milestone.emoji} {milestone.title}
          </span>
        </div>
        {preview.map((item) => (
          <span className="it" key={item}>
            {item}
          </span>
        ))}
        {rest.length > 0 ? (
          <details>
            <summary data-n={rest.length} />
            {rest.map((item) => (
              <span className="it" key={item}>
                {item}
              </span>
            ))}
          </details>
        ) : null}
      </div>
    </li>
  );
}

export function Roadmap() {
  const shownCount = headIndex + 1 + VISIBLE_RELEASED_BEFORE_HEAD;
  const shown = newestFirst.slice(0, shownCount);
  const older = newestFirst.slice(shownCount);
  return (
    <Section
      id="roadmap"
      headingId="rm-h"
      eyebrow="On the horizon"
      title="Roadmap"
      subtitle="Where we've been and where we're headed. Direction, not commitments."
      split
    >
      <div className="legend" role="group" aria-label="Legend">
        <span>
          <i className="node rel" />
          Released
        </span>
        <span>
          <i className="node head" />
          Current (HEAD)
        </span>
      </div>
      <div className="log">
        <div className="logh">
          <Ic name="git-branch" />
          <b>roadmap</b>
          <span>git log --graph</span>
        </div>
        <ol className="rows">
          {shown.map((milestone, i) => (
            <Row
              key={milestone.version}
              milestone={milestone}
              head={i === headIndex}
              first={i === 0}
            />
          ))}
          {older.length > 0 ? (
            <li className="rfold">
              <details className="older">
                <summary data-n={older.length}>
                  <span className="rail" aria-hidden="true" />
                </summary>
                <ol className="rows sub">
                  {older.map((milestone) => (
                    <Row key={milestone.version} milestone={milestone} head={false} first={false} />
                  ))}
                  <li className="row root">
                    <div className="rail">
                      <i className="node rel" style={{ opacity: 0.5 }} />
                    </div>
                    <div className="body">└ (initial commit)</div>
                  </li>
                </ol>
              </details>
            </li>
          ) : null}
        </ol>
      </div>
    </Section>
  );
}
