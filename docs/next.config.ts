import { createMDX } from "fumadocs-mdx/next";

const withMDX = createMDX();

// The docs app is a separate Next.js workspace mounted at /docs inside the
// marketing site. `output: "export"` produces the static HTML that the
// website's `build:docs-content` script copies into `website/public/docs/`.
// `basePath: "/docs"` prefixes every internal link and asset URL so navigation
// keeps working once the website serves the export at getlookout.dev/docs/...
export default withMDX({
  output: "export",
  basePath: "/docs",
  images: {
    unoptimized: true,
  },
});
