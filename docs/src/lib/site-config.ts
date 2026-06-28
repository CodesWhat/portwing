/**
 * Portwing site config — edit ONLY this file to reskin.
 * Product content (hero copy, feature lists, comparison data) lives in the
 * per-section files under src/app/data/.
 */

const githubOwner = "CodesWhat";
const githubRepo = "portwing";

// Aurora palette options: "ember" | "ocean" | "violet" | "forest" | "mono"
type AuroraPalette = "ember" | "ocean" | "violet" | "forest" | "mono";

export const SITE_CONFIG = {
  /** Brand name shown in the header, footer, and metadata. */
  name: "Portwing",
  /**
   * Default meta / OpenGraph / Twitter description.
   * Standard mode: HTTP/SSE. Edge mode: outbound WebSocket (for NAT/firewalled hosts).
   * Auth: Ed25519 per-client (or token/TOKEN_HASH). sockguard is an optional sibling.
   */
  description:
    "Lightweight Go agent that gives Drydock a secure foothold on every Docker host. Exposes Docker control via HTTP/SSE (standard mode) or an outbound WebSocket tunnel (edge mode for NAT/firewalled hosts), with Ed25519 per-client auth and a tamper-evident audit log. Part of the CodesWhat stack: Drydock orchestrates, Portwing is the agent, sockguard filters the socket.",
  /** Production domain (no protocol, no trailing slash). */
  domain: "getportwing.dev",
  /** GitHub owner/org. */
  githubOwner,
  /** GitHub repository name. */
  githubRepo,
  /** Logo asset in /public. */
  logo: "/portwing.png",
  /** Whether the logo inverts in dark mode (adds `dark:invert`). */
  logoInvertOnDark: true,
  /** License link shown in the footer. */
  licenseUrl: "https://www.gnu.org/licenses/agpl-3.0.html",
  /** Aurora background palette — violet matches the pigeon's slate palette. */
  aurora: "violet" as AuroraPalette,
} as const;

export type SiteConfig = typeof SITE_CONFIG;

/** "owner/repo" slug — used to build GitHub URLs. */
const REPO_SLUG = `${SITE_CONFIG.githubOwner}/${SITE_CONFIG.githubRepo}`;
/** Canonical GitHub repository URL. */
export const GITHUB_URL = `https://github.com/${REPO_SLUG}`;
/** GitHub releases page. */
export const GITHUB_RELEASES_URL = `${GITHUB_URL}/releases`;
/** CodesWhat org GitHub URL. */
export const GITHUB_ORG_URL = `https://github.com/${SITE_CONFIG.githubOwner}`;

/**
 * Site base URL. Prefers NEXT_PUBLIC_SITE_URL (Vercel/preview deploys),
 * falls back to the configured production domain.
 */
export const BASE_URL = process.env.NEXT_PUBLIC_SITE_URL || `https://${SITE_CONFIG.domain}`;
