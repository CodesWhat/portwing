"use client";

import { Search } from "lucide-react";
import { useState } from "react";
import { type FeatureCategory, features } from "@/app/data/features";

// ── Data ─────────────────────────────────────────────────────────────────────

const categoryOrder: FeatureCategory[] = ["security", "control", "operations"];

const categoryLabels: Record<FeatureCategory, { label: string; color: string; border: string }> = {
  security: {
    label: "Security",
    color: "text-rose-600 dark:text-rose-400",
    border: "border-rose-500/30",
  },
  control: {
    label: "Control",
    color: "text-indigo-600 dark:text-indigo-400",
    border: "border-indigo-500/30",
  },
  operations: {
    label: "Operations",
    color: "text-violet-600 dark:text-violet-400",
    border: "border-violet-500/30",
  },
};

// ── Component ─────────────────────────────────────────────────────────────────

export function FeaturesBrowser() {
  const [featureQuery, setFeatureQuery] = useState("");

  const q = featureQuery.toLowerCase().trim();
  const filtered = features.filter(
    (f) => q === "" || f.title.toLowerCase().includes(q) || f.description.toLowerCase().includes(q),
  );
  const groups = categoryOrder
    .map((cat) => ({
      cat,
      ...categoryLabels[cat],
      items: filtered.filter((f) => f.category === cat),
    }))
    .filter((g) => g.items.length > 0);

  return (
    <div className="mx-auto max-w-2xl">
      {/* Command-palette panel */}
      <div className="overflow-hidden rounded-2xl border border-neutral-200 bg-white/50 shadow-xl shadow-black/5 backdrop-blur-sm dark:border-neutral-800 dark:bg-neutral-900/50 dark:shadow-black/20">
        {/* Search row */}
        <div className="flex items-center gap-3 border-b border-neutral-200 px-4 py-3 dark:border-neutral-800">
          <Search className="h-4 w-4 shrink-0 text-neutral-400 dark:text-neutral-500" />
          <input
            type="text"
            value={featureQuery}
            onChange={(e) => setFeatureQuery(e.target.value)}
            placeholder="Search capabilities…"
            aria-label="Search capabilities"
            className="flex-1 bg-transparent text-sm text-neutral-700 placeholder-neutral-400 outline-none dark:text-neutral-300 dark:placeholder-neutral-600"
            autoComplete="off"
            spellCheck={false}
          />
          <kbd className="shrink-0 rounded border border-neutral-200 bg-neutral-100 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-neutral-400 dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-500">
            ⌘K
          </kbd>
        </div>

        {/* Results */}
        <div className="max-h-[560px] overflow-y-auto py-2">
          {groups.length === 0 && (
            <p className="px-4 py-8 text-center text-sm text-neutral-400 dark:text-neutral-600">
              No capabilities match &ldquo;{featureQuery}&rdquo;
            </p>
          )}
          {groups.map((group) => (
            <div key={group.cat}>
              <div className="px-4 pb-1 pt-3">
                <span
                  className={`font-mono text-[10px] font-semibold uppercase tracking-widest ${group.color}`}
                >
                  {group.label}
                </span>
              </div>
              {group.items.map((feature) => (
                <div
                  key={feature.title}
                  className="group flex cursor-default items-center gap-3 px-3 py-2.5 hover:bg-neutral-100/70 dark:hover:bg-neutral-800/70"
                >
                  <div
                    className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${feature.bg}`}
                  >
                    <feature.icon size={15} className={feature.color} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-semibold text-neutral-800 dark:text-neutral-200">
                      {feature.title}
                    </p>
                    <p className="truncate text-xs text-neutral-500 dark:text-neutral-400">
                      {feature.description}
                    </p>
                  </div>
                  <span className="shrink-0 font-mono text-xs text-neutral-300 opacity-0 transition-opacity group-hover:opacity-100 dark:text-neutral-600">
                    ↵
                  </span>
                </div>
              ))}
            </div>
          ))}
        </div>

        {/* Footer bar */}
        <div className="flex items-center gap-4 border-t border-neutral-200 px-4 py-2.5 dark:border-neutral-800">
          <span className="font-mono text-[10px] text-neutral-400 dark:text-neutral-600">
            {filtered.length} of {features.length} capabilities
          </span>
          <div className="ml-auto flex items-center gap-3">
            <span className="flex items-center gap-1 font-mono text-[10px] text-neutral-400 dark:text-neutral-600">
              <kbd className="rounded border border-neutral-200 bg-neutral-100 px-1 py-px font-mono text-[9px] dark:border-neutral-700 dark:bg-neutral-800">
                ↑↓
              </kbd>{" "}
              navigate
            </span>
            <span className="flex items-center gap-1 font-mono text-[10px] text-neutral-400 dark:text-neutral-600">
              <kbd className="rounded border border-neutral-200 bg-neutral-100 px-1 py-px font-mono text-[9px] dark:border-neutral-700 dark:bg-neutral-800">
                esc
              </kbd>{" "}
              close
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
