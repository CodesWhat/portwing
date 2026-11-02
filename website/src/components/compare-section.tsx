// SPDX-License-Identifier: MIT

import Link from "next/link";
import { CellMark, MarkLegend } from "@/components/compare-marks";
import { Ic } from "@/components/icons";
import { Section } from "@/components/section";
import { TEASER_ROWS, TEASER_TOOLS } from "@/lib/comparison-matrix";

// Home page teaser: the four direct remote-agent peers. Full matrix lives at /compare.

export function CompareSection() {
  return (
    <Section
      id="compare"
      headingId="cmp-h"
      eyebrow="Why Portwing"
      title="How we compare"
      subtitle="A sourced snapshot of the four closest remote-agent peers. Partial marks usually mean the capability exists at the controller layer or is optional."
      variant="c"
    >
      <div className="scroll" tabIndex={0} role="region" aria-label="Comparison table">
        <table className="cmp">
          <thead>
            <tr>
              <th scope="col">Feature</th>
              {TEASER_TOOLS.map((tool, i) => (
                <th key={tool.name} scope="col" className={i === 0 ? "me" : undefined}>
                  {tool.name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {TEASER_ROWS.map((row) => (
              <tr key={row.key}>
                <th scope="row">{row.label}</th>
                {TEASER_TOOLS.map((tool, i) => (
                  <td key={tool.name} className={i === 0 ? "me" : undefined}>
                    <CellMark value={tool.cells[row.key]} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        <MarkLegend />
      </div>
      <div className="more">
        <Link className="btn" href="/compare">
          View all comparisons <Ic name="arrow-right" />
        </Link>
      </div>
    </Section>
  );
}
