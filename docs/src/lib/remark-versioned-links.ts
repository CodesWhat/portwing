import { archiveVersionOf } from "./docs-versions.ts";

// An archived page is a byte copy of the current docs, so it still says
// [Auth](/authentication). Inside vX.Y/ that has to land on /vX.Y/authentication
// or a reader of old docs is dropped into the current ones. Rewriting at build
// time (not in the copy) is what keeps the archive identical to its release tag.

interface EstreeNode {
  type: string;
  value?: unknown;
  raw?: string;
  expression?: EstreeNode;
  body?: EstreeNode[];
  expressions?: EstreeNode[];
  quasis?: { value: { raw: string; cooked?: string | null } }[];
}

interface MdastNode {
  type: string;
  url?: string;
  name?: string;
  value?: unknown;
  data?: { estree?: EstreeNode | null };
  attributes?: MdastNode[];
  children?: MdastNode[];
}

// Last path segment with a file extension: a static asset, not a docs page.
const ASSET_PATH = /\.[A-Za-z0-9]+$/u;

/**
 * Scope one root-relative target to a version. Returns the input unchanged for
 * anything that is not an unversioned docs page path: external and protocol
 * links, anchors-only, relative paths, `//host`, assets, targets already
 * under a vX.Y/ segment, and targets that already carry the /docs basePath.
 */
export function scopeLink(url: string, version: string): string {
  if (!url.startsWith("/") || url.startsWith("//")) return url;
  const end = url.search(/[?#]/u);
  const path = end === -1 ? url : url.slice(0, end);
  const rest = end === -1 ? "" : url.slice(end);
  // Already carries the site basePath: the router would add /docs again.
  if (path === "/docs" || path.startsWith("/docs/")) return url;
  if (ASSET_PATH.test(path)) return url;
  if (/^\/v\d+\.\d+(?:\/|$)/u.test(path)) return url;
  return `/${version}${path === "/" ? "" : path}${rest}`;
}

// The expression of `href={...}` when it is a single string literal or a
// template literal with no interpolation. Anything dynamic returns undefined.
function staticExpression(
  attribute: MdastNode,
): { read: string; write: (next: string) => void } | undefined {
  const value = attribute.value as { type?: string; value?: string; data?: MdastNode["data"] };
  if (value?.type !== "mdxJsxAttributeValueExpression" || typeof value.value !== "string") {
    return undefined;
  }
  const source = value.value;
  const literal = value.data?.estree?.body?.[0]?.expression;
  if (literal) {
    if (value.data?.estree?.body?.length !== 1) return undefined;
    if (literal.type === "Literal" && typeof literal.value === "string") {
      return {
        read: literal.value,
        write(next) {
          literal.value = next;
          literal.raw = JSON.stringify(next);
          value.value = JSON.stringify(next);
        },
      };
    }
    if (
      literal.type === "TemplateLiteral" &&
      literal.expressions?.length === 0 &&
      literal.quasis?.length === 1 &&
      typeof literal.quasis[0].value.cooked === "string"
    ) {
      const quasi = literal.quasis[0];
      return {
        read: quasi.value.cooked as string,
        write(next) {
          quasi.value.raw = next;
          quasi.value.cooked = next;
          value.value = `\`${next}\``;
        },
      };
    }
    return undefined;
  }
  // No estree (parser without acorn): accept only the plain literal shapes.
  const quoted = /^\s*(["'])([^"'\\\n]*)\1\s*$/u.exec(source);
  if (quoted) {
    return {
      read: quoted[2],
      write(next) {
        value.value = JSON.stringify(next);
      },
    };
  }
  const template = /^\s*`([^`\\$]*)`\s*$/u.exec(source);
  if (template) {
    return {
      read: template[1],
      write(next) {
        value.value = `\`${next}\``;
      },
    };
  }
  return undefined;
}

function visit(node: MdastNode, version: string): void {
  if ((node.type === "link" || node.type === "definition") && typeof node.url === "string") {
    node.url = scopeLink(node.url, version);
  } else if (
    (node.type === "mdxJsxFlowElement" || node.type === "mdxJsxTextElement") &&
    node.attributes &&
    // A native <a> gets no basePath from the router, so a scoped root-relative
    // href would 404 there just as the unscoped one does. Content tests forbid
    // them; the plugin leaves them alone.
    node.name !== "a"
  ) {
    for (const attribute of node.attributes) {
      if (attribute.type !== "mdxJsxAttribute" || attribute.name !== "href") continue;
      if (typeof attribute.value === "string") {
        attribute.value = scopeLink(attribute.value, version);
        continue;
      }
      const expression = staticExpression(attribute);
      if (expression) {
        const scoped = scopeLink(expression.read, version);
        if (scoped !== expression.read) expression.write(scoped);
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
