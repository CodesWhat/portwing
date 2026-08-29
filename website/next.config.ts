import path from "node:path";
import { fileURLToPath } from "node:url";
import type { NextConfig } from "next";

const workspaceRoot = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");

const nextConfig: NextConfig = {
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
  // Next embeds the build ID in an inline RSC bootstrap script. A stable ID
  // makes that script hash reproducible so the checked-in route CSP can stay
  // strict. Exported assets retain their own content-hashed filenames.
  generateBuildId: async () => "portwing-website-static",
  images: { unoptimized: true },
  turbopack: {
    root: workspaceRoot,
  },
};

export default nextConfig;
