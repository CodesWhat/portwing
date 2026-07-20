import path from "node:path";
import { fileURLToPath } from "node:url";
import type { NextConfig } from "next";

const workspaceRoot = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");

const nextConfig: NextConfig = {
  output: "export",
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
