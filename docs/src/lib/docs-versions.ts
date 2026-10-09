import type { Root } from "fumadocs-core/page-tree";

// The docs tree is content/docs/current/ plus, later, one content/docs/vX.Y/
// directory per archived release. `current` is the live docs and must keep
// serving at /docs/<page>, so its directory segment never reaches the URL.
// Archived versions keep their segment: vX.Y/<page> serves at /docs/vX.Y/<page>.
export const CURRENT_DIR = "current";

export const VERSION_DIR_PATTERN = /^v\d+\.\d+$/;

/**
 * Slugs for a content file path relative to content/docs, for the Fumadocs
 * loader's `slugs` option: drops the leading `current` segment and the page
 * extension, and maps `index` to the directory itself.
 */
export function slugsForPath(path: string): string[] {
  const segments = path.split("/").filter((segment) => segment.length > 0);
  const last = segments.pop();
  if (last === undefined) return [];
  const name = last.replace(/\.[^.]+$/, "");
  if (segments[0] === CURRENT_DIR) segments.shift();
  if (name !== "index") segments.push(name);
  return segments.map((segment) => encodeURI(segment));
}

/**
 * The version switcher is the sidebar tab dropdown, one tab per root folder.
 * With a single version it would be a dropdown with nothing to switch to, so
 * pass `false` to DocsLayout until a second root exists.
 */
export function versionTabsOption(tree: Root): undefined | false {
  const roots = tree.children.filter((node) => node.type === "folder" && node.root);
  return roots.length > 1 ? undefined : false;
}
