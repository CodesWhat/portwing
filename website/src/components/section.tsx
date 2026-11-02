// SPDX-License-Identifier: MIT

import type { ReactNode } from "react";

type Props = {
  id: string;
  /** Id of the h2, also the aria-labelledby target. */
  headingId: string;
  eyebrow: string;
  title: string;
  subtitle: string;
  /** Header variant from suite.css: "b" compact, "c" centered; omit for the split layout. */
  variant?: "b" | "c";
  /** Split layout: header on the left, content in `.main` on the right. */
  split?: boolean;
  strikeEyebrow?: boolean;
  children: ReactNode;
};

export function Section({
  id,
  headingId,
  eyebrow,
  title,
  subtitle,
  variant,
  split = false,
  strikeEyebrow = false,
  children,
}: Props) {
  const header = (
    <div className="sec-h">
      <div className={strikeEyebrow ? "eyebrow micro strike" : "eyebrow micro"}>{eyebrow}</div>
      <h2 className="title" id={headingId}>
        {title}
      </h2>
      <p className="sub">{subtitle}</p>
    </div>
  );
  return (
    <section id={id} data-sv={variant} aria-labelledby={headingId}>
      <div className="wrap">
        {split ? (
          <div className="split">
            {header}
            <div className="main">{children}</div>
          </div>
        ) : (
          <>
            {header}
            {children}
          </>
        )}
      </div>
    </section>
  );
}
