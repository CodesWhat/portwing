// SPDX-License-Identifier: MIT

import type { ReactNode } from "react";

// Token classes (y-c comment, y-k key, y-v value) are styled in suite.css.

function highlightValue(value: string): ReactNode {
  const hash = value.search(/\s#/);
  const body = hash >= 0 ? value.slice(0, hash) : value;
  const comment = hash >= 0 ? value.slice(hash) : "";
  return (
    <>
      {body ? <span className="y-v">{body}</span> : null}
      {comment ? <span className="y-c">{comment}</span> : null}
    </>
  );
}

/** Minimal YAML coloring: comments, keys, quoted or scalar values. Not a parser. */
export function HighlightedYaml({ code }: { code: string }) {
  return (
    <>
      {code.split("\n").map((line, index) => {
        const key = `${index}:${line}`;
        const suffix = index < code.split("\n").length - 1 ? "\n" : "";
        if (/^\s*#/.test(line)) {
          return (
            <span key={key}>
              <span className="y-c">{line}</span>
              {suffix}
            </span>
          );
        }
        const pair = line.match(/^(\s*)([A-Za-z_][\w-]*)(:)(\s*)(.*)$/);
        if (pair) {
          const [, indent, name, colon, gap, rest] = pair;
          return (
            <span key={key}>
              {indent}
              <span className="y-k">{name}</span>
              {colon}
              {gap}
              {rest ? highlightValue(rest) : null}
              {suffix}
            </span>
          );
        }
        const item = line.match(/^(\s*- )(["'].*)$/);
        if (item) {
          return (
            <span key={key}>
              {item[1]}
              {highlightValue(item[2])}
              {suffix}
            </span>
          );
        }
        return (
          <span key={key}>
            {line}
            {suffix}
          </span>
        );
      })}
    </>
  );
}
