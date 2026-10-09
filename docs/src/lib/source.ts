import type { Page } from "fumadocs-core/source";
import { loader } from "fumadocs-core/source";
import { docs } from "../../.source/server";
import { slugsForPath } from "./docs-versions";

export const source = loader(docs.toFumadocsSource(), {
  baseUrl: "/",
  // content/docs/current/ serves at the site root of /docs, so existing URLs stay put.
  slugs: (file) => slugsForPath(file.path),
});

type DocsPageData = (typeof docs.docs)[number];

export function getDocsPage(slugs?: string[]) {
  // Page<Type, Data>: the first generic is the slug type, not the data type.
  // Fumadocs' loader still widens .data back to base PageData without the cast.
  return source.getPage(slugs) as Page<string | undefined, DocsPageData> | undefined;
}
