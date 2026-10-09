import { defineConfig, defineDocs } from "fumadocs-mdx/config";
import { remarkVersionedLinks } from "./src/lib/remark-versioned-links.ts";

export const docs = defineDocs({
  dir: "content/docs",
});

// Global, not on the collection: a collection's own `mdxOptions` is used as-is,
// without the Fumadocs preset, which silently drops syntax highlighting and the
// table of contents. The global option layers this plugin on top of the preset.
// Archived vX.Y/ pages keep their release-time links; this scopes them to the
// archive at build time and does nothing for current/.
export default defineConfig({
  mdxOptions: {
    remarkPlugins: [remarkVersionedLinks],
  },
});
