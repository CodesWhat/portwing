// SPDX-License-Identifier: MIT

"use client";

import { useEffect, useRef, useState } from "react";
import { type FeatureCategory, features } from "@/app/data/features";
import { Ic } from "@/components/icons";

const GROUPS: { id: FeatureCategory; label: string; tone: string }[] = [
  { id: "security", label: "Security", tone: "t-danger" },
  { id: "control", label: "Control", tone: "t-alt" },
  { id: "operations", label: "Operations", tone: "t-warning" },
];

const searchText = (feature: (typeof features)[number]) =>
  `${feature.title} ${feature.description} ${feature.category}`.toLowerCase();

/** Searchable capability list. Filters as you type; arrow keys move the highlight. */
export function FeaturePalette() {
  const [query, setQuery] = useState("");
  const [sel, setSel] = useState(-1);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLUListElement>(null);

  // Cmd/Ctrl+K focuses the search, unless the user is already typing in some other field.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") return;
      const target = event.target as HTMLElement | null;
      const typing =
        Boolean(target?.isContentEditable) || Boolean(target?.closest("input, textarea, select"));
      if (typing && target !== input.current) return;
      event.preventDefault();
      input.current?.focus();
      input.current?.select();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const needle = query.trim().toLowerCase();
  const visible = features.filter((feature) => !needle || searchText(feature).includes(needle));

  const move = (next: number) => {
    setSel(next);
    list.current
      ?.querySelectorAll("li.it:not([hidden])")
      [next]?.scrollIntoView({ block: "nearest" });
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      move(Math.min(visible.length - 1, sel + 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      move(Math.max(0, sel - 1));
    } else if (event.key === "Escape") {
      setQuery("");
      setSel(-1);
      input.current?.blur();
    }
  };

  return (
    <div className="pal" role="search">
      <div className="pal-in">
        <Ic name="magnifying-glass" />
        <input
          ref={input}
          id="q"
          type="search"
          placeholder="Search capabilities…"
          aria-label="Search capabilities"
          autoComplete="off"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setSel(-1);
          }}
          onKeyDown={onKeyDown}
        />
        <span aria-hidden="true">
          <kbd>⌘K</kbd>
        </span>
      </div>
      <ul id="plist" aria-label="Capabilities" ref={list}>
        {GROUPS.map((group) => {
          const items = features.filter((feature) => feature.category === group.id);
          const shown = items.filter((feature) => visible.includes(feature));
          return [
            <li
              key={`${group.id}-h`}
              className="gh micro"
              role="presentation"
              hidden={!shown.length}
            >
              {group.label}
            </li>,
            ...items.map((feature) => {
              const hidden = !visible.includes(feature);
              const selected = !hidden && visible[sel] === feature;
              return (
                <li
                  key={feature.title}
                  className={`it ${group.tone}${selected ? " sel" : ""}`}
                  hidden={hidden}
                >
                  <span className="tn">
                    <Ic name={feature.icon} />
                  </span>
                  <span>
                    <span className="hd2">
                      <b>{feature.title}</b>
                      <span className={`chip ${group.tone}`}>{group.id}</span>
                    </span>
                    <span className="d">{feature.description}</span>
                  </span>
                </li>
              );
            }),
          ];
        })}
      </ul>
      {visible.length === 0 ? (
        <div className="empty" id="pempty">
          No capabilities match "{query}"
        </div>
      ) : null}
      <div className="pal-f">
        <span id="pcount" aria-live="polite">
          {visible.length} of {features.length} capabilities
        </span>
        <span aria-hidden="true">
          <kbd>↑↓</kbd> navigate &nbsp; <kbd>esc</kbd> close
        </span>
      </div>
    </div>
  );
}
