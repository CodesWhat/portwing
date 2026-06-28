/**
 * Portwing site config — edit ONLY this file to reskin.
 * Product content (hero copy, feature lists, comparison data) lives in the
 * per-section files under src/app/data/.
 */

const githubOwner = "CodesWhat";
const githubRepo = "portwing";

// Aurora palette options: "ember" | "ocean" | "violet" | "forest" | "mono"
export type AuroraPalette = "ember" | "ocean" | "violet" | "forest" | "mono";

export const SITE_CONFIG = {
  /** Brand name shown in the header, footer, and metadata. */
  name: "Portwing",
  /** Current release version shown in the hero badge. */
  version: "0.5.0",
  /** Short product tagline used in page titles and OG metadata. */
  tagline: "Security-first remote Docker agent",
  /** Default meta / OpenGraph / Twitter description. */
  description:
    "A lightweight Go agent that exposes Docker control over a hardened WebSocket tunnel, secured with mutual Ed25519 auth and a default-deny socket filter. Part of the CodesWhat stack: Drydock orchestrates, Portwing is the agent, Sockguard filters the socket.",
  /** Production domain (no protocol, no trailing slash). */
  domain: "getportwing.dev",
  /** GitHub owner/org. */
  githubOwner,
  /** GitHub repository name. */
  githubRepo,
  /** Twitter/X handle for the twitter:creator card. */
  twitterCreator: "@codeswhat",
  /** Twitter/X profile URL (used in JSON-LD sameAs). */
  twitterUrl: "https://x.com/codeswhat",
  /** Logo asset in /public. */
  logo: "/portwing.png",
  /** Whether the logo inverts in dark mode (adds `dark:invert`). */
  logoInvertOnDark: true,
  /** Default OpenGraph / Twitter share image in /public.
   *  NOTE: no og-image.png in public/ yet — falling back to /portwing.png. */
  ogImage: "/portwing.png",
  /** OpenGraph locale. */
  locale: "en_US",
  /** Live demo URL — empty, no demo for Portwing. */
  demoUrl: "",
  /** GHCR image path used in quick-start snippets. */
  dockerImage: "ghcr.io/codeswhat/portwing",
  /** License link shown in the footer. */
  licenseUrl: "https://www.gnu.org/licenses/agpl-3.0.html",
  /** Aurora background palette — violet matches the pigeon's slate palette. */
  aurora: "violet" as AuroraPalette,
  /** Prefix for localStorage keys. */
  storagePrefix: "pw",
} as const;

export type SiteConfig = typeof SITE_CONFIG;

/** "owner/repo" slug — used in shields.io / OpenSSF scorecard badge URLs. */
export const REPO_SLUG = `${SITE_CONFIG.githubOwner}/${SITE_CONFIG.githubRepo}`;
/** Canonical GitHub repository URL. */
export const GITHUB_URL = `https://github.com/${REPO_SLUG}`;
/** GitHub releases page. */
export const GITHUB_RELEASES_URL = `${GITHUB_URL}/releases`;
/** GHCR package page (portwing ships to GHCR, not Docker Hub). */
export const DOCKER_HUB_URL = `https://github.com/orgs/CodesWhat/packages/container/package/portwing`;

/**
 * Site base URL. Prefers NEXT_PUBLIC_SITE_URL (Vercel/preview deploys),
 * falls back to the configured production domain.
 */
export const BASE_URL = process.env.NEXT_PUBLIC_SITE_URL || `https://${SITE_CONFIG.domain}`;
/** Live demo URL — empty for Portwing. */
export const DEMO_URL = "";
