// SPDX-License-Identifier: MIT

import Link from "next/link";
import { CellMark, MarkLegend } from "@/components/compare-marks";
import { Ic } from "@/components/icons";
import { FEATURES, TOOLS } from "@/lib/comparison-matrix";
import { SITE_CONFIG } from "@/lib/site-config";

// Full comparison matrix for /compare. Each competitor row links to its dedicated page.

export function CompareMatrix() {
  const peers = TOOLS.length - 1;
  return (
    <div className="mx">
      <div
        className="scroll"
        tabIndex={0}
        role="region"
        aria-label={`${SITE_CONFIG.name} comparison matrix. Scroll sideways to see every column.`}
      >
        <table className="cmp mxt">
          <caption className="sr">
            {SITE_CONFIG.name} compared with {peers} alternatives across {FEATURES.length} features
          </caption>
          <thead>
            <tr>
              <th scope="col">Tool</th>
              {FEATURES.map((feature) => (
                <th key={feature.key} scope="col">
                  {feature.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {TOOLS.map((tool) => (
              <tr key={tool.name} className={tool.slug ? undefined : "me-r"}>
                <th scope="row">
                  {tool.slug ? (
                    <Link href={`/compare/${tool.slug}`}>
                      {tool.name}
                      <Ic name="arrow-up-right" />
                    </Link>
                  ) : (
                    <span className="selfn">{tool.name}</span>
                  )}
                </th>
                {FEATURES.map((feature) => (
                  <td key={feature.key}>
                    <CellMark value={tool.cells[feature.key]} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <MarkLegend extra="· Click a tool for the full breakdown" />
    </div>
  );
}
