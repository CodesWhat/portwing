import { Check, CircleQuestionMark, Minus, X } from "lucide-react";

export type CellValue = "yes" | "partial" | "no" | "undocumented";

export function ComparisonCellIcon({ value }: { value: CellValue }) {
  if (value === "yes") {
    return (
      <>
        <Check className="mx-auto h-4 w-4 text-violet-500" aria-hidden="true" />
        <span className="sr-only">Yes</span>
      </>
    );
  }
  if (value === "partial") {
    return (
      <>
        <Minus className="mx-auto h-4 w-4 text-fuchsia-400" aria-hidden="true" />
        <span className="sr-only">Partial</span>
      </>
    );
  }
  if (value === "undocumented") {
    return (
      <>
        <CircleQuestionMark
          className="mx-auto h-4 w-4 text-neutral-400 dark:text-neutral-500"
          aria-hidden="true"
        />
        <span className="sr-only">Not documented</span>
      </>
    );
  }
  return (
    <>
      <X className="mx-auto h-4 w-4 text-neutral-400 dark:text-neutral-600" aria-hidden="true" />
      <span className="sr-only">No</span>
    </>
  );
}
