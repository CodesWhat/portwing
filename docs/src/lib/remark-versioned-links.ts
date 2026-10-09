import { archiveVersionOf } from "./docs-versions.ts";

// An archived page is a byte copy of the current docs, so it still says
// [Auth](/authentication). Inside vX.Y/ that has to land on /vX.Y/authentication
// or a reader of old docs is dropped into the current ones. Rewriting at build
// time (not in the copy) is what keeps the archive identical to its release tag.

interface MdastNode {
  type: string;
  url?: string;
  name?: string;
  value?: unknown;
  attributes?: MdastNode[];
  children?: MdastNode[];
}

// Last path segment with a file extension: a static asset, not a docs page.
const ASSET_PATH = /\.[A-Za-z0-9]+$/u;

/**
 * Scope one root-relative target to a version. Returns the input unchanged for
 * anything that is not an unversioned docs page path: external and protocol
 * links, anchors-only, relative paths, `//host`, assets, and targets already
 * under a vX.Y/ segment.
 */
export function scopeLink(url: string, version: string): string {
  if (!url.startsWith("/") || url.startsWith("//")) return url;
  const end = url.search(/[?#]/u);
  const path = end === -1 ? url : url.slice(0, end);
  const rest = end === -1 ? "" : url.slice(end);
  if (ASSET_PATH.test(path)) return url;
  if (/^\/v\d+\.\d+(?:\/|$)/u.test(path)) return url;
  return `/${version}${path === "/" ? "" : path}${rest}`;
}

function visit(node: MdastNode, version: string): void {
  if ((node.type === "link" || node.type === "definition") && typeof node.url === "string") {
    node.url = scopeLink(node.url, version);
  } else if (
    (node.type === "mdxJsxFlowElement" || node.type === "mdxJsxTextElement") &&
    node.attributes
  ) {
    for (const attribute of node.attributes) {
      if (
        attribute.type === "mdxJsxAttribute" &&
        attribute.name === "href" &&
        typeof attribute.value === "string"
      ) {
        attribute.value = scopeLink(attribute.value, version);
      }
    }
  }
  for (const child of node.children ?? []) visit(child, version);
}

/**
 * Remark plugin for docs/source.config.ts. Pages outside a vX.Y/ directory,
 * including all of current/, are returned untouched.
 */
export function remarkVersionedLinks() {
  return (tree: MdastNode, file: { path?: string }): void => {
    const version = file.path ? archiveVersionOf(file.path) : undefined;
    if (!version) return;
    visit(tree, version);
  };
}
