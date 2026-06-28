import { GITHUB_URL, REPO_SLUG, SITE_CONFIG } from "@/lib/site-config";

// Portwing quality + distribution badges.
// Quality row: shields.io pills — machine-generated, verifiable trust.
// No static star/download counts yet (early project); live GitHub shields handle that.

type Badge = { href: string; src: string; alt: string };

const quality: Badge[] = [
  {
    href: `${GITHUB_URL}/blob/main/LICENSE`,
    src: "https://img.shields.io/badge/license-AGPL--3.0-C9A227",
    alt: "License AGPL-3.0",
  },
  {
    href: `${GITHUB_URL}/actions/workflows/ci.yml`,
    src: `https://github.com/${REPO_SLUG}/actions/workflows/ci.yml/badge.svg?branch=main`,
    alt: "CI",
  },
  {
    href: `https://securityscorecards.dev/viewer/?uri=github.com/${REPO_SLUG}`,
    src: `https://img.shields.io/ossf-scorecard/github.com/${REPO_SLUG}?label=openssf+scorecard&style=flat`,
    alt: "OpenSSF Scorecard",
  },
  {
    href: "https://goreportcard.com/report/github.com/codeswhat/portwing",
    src: "https://goreportcard.com/badge/github.com/codeswhat/portwing",
    alt: "Go Report Card",
  },
  {
    href: "https://pkg.go.dev/github.com/codeswhat/portwing",
    src: "https://pkg.go.dev/badge/github.com/codeswhat/portwing.svg",
    alt: "Go Reference",
  },
];

const distribution: Badge[] = [
  {
    href: `https://github.com/orgs/CodesWhat/packages/container/package/portwing`,
    src: `https://img.shields.io/badge/GHCR-${encodeURIComponent(SITE_CONFIG.dockerImage)}-2ea44f?logo=github&logoColor=white`,
    alt: "GHCR image",
  },
  {
    href: `https://github.com/orgs/CodesWhat/packages/container/package/portwing`,
    src: "https://img.shields.io/badge/platforms-amd64%20%7C%20arm64%20%7C%20arm%2Fv7-informational?logo=linux&logoColor=white",
    alt: "Multi-arch: amd64 | arm64 | arm/v7",
  },
  {
    href: `https://github.com/orgs/CodesWhat/packages/container/package/portwing`,
    src: "https://img.shields.io/badge/image%20size-~10%20MB-informational?logo=docker&logoColor=white",
    alt: "Image size ~10 MB",
  },
  {
    href: `${GITHUB_URL}/stargazers`,
    src: `https://img.shields.io/github/stars/${REPO_SLUG}?style=flat`,
    alt: "GitHub stars",
  },
];

function BadgeRow({ badges }: { badges: Badge[] }) {
  return (
    <div className="flex flex-wrap items-center justify-center gap-2">
      {badges.map((b) => (
        <a key={b.alt} href={b.href} target="_blank" rel="noopener noreferrer">
          {/* biome-ignore lint/performance/noImgElement: external badge */}
          <img src={b.src} alt={b.alt} loading="lazy" className="h-5 w-auto" />
        </a>
      ))}
    </div>
  );
}

export function GitHubBadges() {
  return (
    <div className="flex flex-col items-center gap-3">
      <BadgeRow badges={quality} />
      <BadgeRow badges={distribution} />
    </div>
  );
}
