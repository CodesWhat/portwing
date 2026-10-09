import type { Root } from "fumadocs-core/page-tree";
import { getSlugs } from "fumadocs-core/source";

// The docs tree is content/docs/current/ plus, later, one content/docs/vX.Y/
// directory per archived release. `current` is the live docs and must keep
// serving at /docs/<page>, so its directory segment never reaches the URL.
// Archived versions keep their segment: vX.Y/<page> serves at /docs/vX.Y/<page>.
const CURRENT_DIR = "current";
const ARCHIVE_DIR = /^v\d+\.\d+$/u;
const CONTENT_MARKER = "/content/docs/";

/**
 * The archived version a docs source path belongs to ("v0.9"), or undefined
 * for `current/` and anything else. This is the one place that decides what
 * counts as an archive: the link scoping plugin and the page renderer both ask
 * here. Accepts the loader's page path (`v0.9/authentication.mdx`) or an
 * absolute file path that contains `content/docs/`.
 */
export function archiveVersionOf(path: string): string | undefined {
  const normalized = path.replaceAll("\\", "/");
  const marker = normalized.lastIndexOf(CONTENT_MARKER);
  const relative = marker === -1 ? normalized : normalized.slice(marker + CONTENT_MARKER.length);
  const separator = relative.indexOf("/");
  if (separator === -1) return undefined;
  const dir = relative.slice(0, separator);
  return ARCHIVE_DIR.test(dir) ? dir : undefined;
}

/**
 * For the Fumadocs loader's `slugs` option. A page under `current/` gets the
 * slugs Fumadocs would give it without that directory, so route groups and
 * encoding behave as they do everywhere else. Anything else returns undefined
 * and takes the loader's default, which keeps the `vX.Y` segment.
 *
 * One default is not carried over: a `current/x.mdx` beside a
 * `current/x/index.mdx` fails the build as a duplicate slug instead of
 * serving the second at `/x/index`.
 */
export function slugsForPath(path: string): string[] | undefined {
  const prefix = `${CURRENT_DIR}/`;
  if (!path.startsWith(prefix)) return undefined;
  return getSlugs(path.slice(prefix.length));
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
