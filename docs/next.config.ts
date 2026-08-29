import { createMDX } from "fumadocs-mdx/next";

const withMDX = createMDX();

// The docs app is a separate Next.js workspace mounted at /docs inside the
// marketing site. `output: "export"` produces the static HTML that the
// website's `build:docs-content` script copies into `website/public/docs/`.
// `basePath: "/docs"` prefixes every internal link and asset URL so navigation
// keeps working once the website serves the export at portwing.codeswhat.com/docs/...
export default withMDX({
  transpilePackages: ["@codeswhat/public-analytics"],
  output: "export",
  // Baked once at build time so the exported HTML and the client bundle carry
  // the same literal. Calling new Date().getFullYear() in the footer component
  // instead runs it a second time during hydration, so a visitor whose local
  // clock has crossed into January while the export still says the build year
  // renders different text than the server did (React hydration error #418).
  env: {
    NEXT_PUBLIC_COPYRIGHT_YEAR: String(new Date().getFullYear()),
  },
  // Keep the exported inline bootstrap deterministic for the website's
  // route-specific CSP hash generation. Static assets remain content-hashed.
  generateBuildId: async () => "portwing-docs-static",
  basePath: "/docs",
  images: {
    unoptimized: true,
  },
});
