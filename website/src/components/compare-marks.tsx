// SPDX-License-Identifier: MIT

import { Ic } from "@/components/icons";
import type { CellValue } from "@/lib/comparison-matrix";

const MARKS: Record<CellValue, { cls: string; icon: string; label: string }> = {
  yes: { cls: "yes", icon: "check", label: "Yes" },
  partial: { cls: "part", icon: "minus", label: "Partial" },
  no: { cls: "no", icon: "x", label: "No" },
  undocumented: { cls: "nd", icon: "question", label: "Not documented" },
};

export function CellMark({ value }: { value: CellValue }) {
  const mark = MARKS[value];
  return (
    <span className={mark.cls}>
      <Ic name={mark.icon} />
      <span className="sr">{mark.label}</span>
    </span>
  );
}

export function MarkLegend({ extra }: { extra?: string }) {
  return (
    <div className="leg" role="group" aria-label="Legend">
      {(Object.keys(MARKS) as CellValue[]).map((value) => (
        <span key={value} className={MARKS[value].cls}>
          <Ic name={MARKS[value].icon} />
          {MARKS[value].label}
        </span>
      ))}
      {extra ? <span>{extra}</span> : null}
    </div>
  );
}
