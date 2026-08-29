import type { CtaId } from "@codeswhat/public-analytics";
import { ArrowUpRight, BookOpen } from "lucide-react";
import Image from "next/image";
import { DiscordIcon } from "@/components/discord-icon";
import { GithubIcon } from "@/components/github-icon";
import { TrackedLink } from "@/components/tracked-link";
import { iconButtonCn, navLinkCn } from "@/lib/class-names";
import { GITHUB_ORG_URL, GITHUB_RELEASES_URL, GITHUB_URL, SITE_CONFIG } from "@/lib/site-config";

const CODESWHAT = GITHUB_ORG_URL;
const YEAR = process.env.NEXT_PUBLIC_COPYRIGHT_YEAR;
const BLURB =
  "Security-first remote Docker agent. Lightweight Go binary, Ed25519 auth, sockguard-ready socket filter, structured audit log.";

type FooterLink = { label: string; href: string; external?: boolean; ctaId?: CtaId };

const productLinks: FooterLink[] = [
  { label: "Documentation", href: "/docs", ctaId: "docs_root" },
  { label: "Releases", href: GITHUB_RELEASES_URL, external: true },
];

const projectLinks: FooterLink[] = [
  { label: "GitHub", href: GITHUB_URL, external: true, ctaId: "github_repository" },
  { label: "License", href: SITE_CONFIG.licenseUrl, external: true },
];

function FooterLinkEl({ link, className }: { link: FooterLink; className?: string }) {
  if (link.ctaId) {
    return (
      <TrackedLink
        href={link.href}
        target={link.external ? "_blank" : undefined}
        rel={link.external ? "noopener noreferrer" : undefined}
        className={className ?? navLinkCn}
        ctaId={link.ctaId}
        placement="footer"
      >
        {link.label}
      </TrackedLink>
    );
  }
  if (link.external) {
    return (
      <a
        href={link.href}
        target="_blank"
        rel="noopener noreferrer"
        className={className ?? navLinkCn}
      >
        {link.label}
      </a>
    );
  }
  return (
    <a href={link.href} className={className ?? navLinkCn}>
      {link.label}
    </a>
  );
}

function LinkColumn({ heading, links }: { heading: string; links: FooterLink[] }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs font-semibold uppercase tracking-widest text-neutral-400 dark:text-neutral-600">
        {heading}
      </p>
      {links.map((l) => (
        <FooterLinkEl key={l.label} link={l} />
      ))}
    </div>
  );
}

function SocialIcons() {
  return (
    <div className="-ml-2 flex items-center gap-1">
      <TrackedLink
        href={GITHUB_URL}
        target="_blank"
        rel="noopener noreferrer"
        className={iconButtonCn}
        aria-label="GitHub"
        ctaId="github_repository"
        placement="footer"
      >
        <GithubIcon className="h-5 w-5" />
      </TrackedLink>
      <TrackedLink
        href={SITE_CONFIG.discordUrl}
        target="_blank"
        rel="noopener noreferrer"
        className={iconButtonCn}
        aria-label="Discord community"
        ctaId="community_discord"
        placement="footer"
      >
        <DiscordIcon className="h-5 w-5" />
      </TrackedLink>
      <TrackedLink
        href="/docs"
        className={iconButtonCn}
        aria-label="Documentation"
        ctaId="docs_root"
        placement="footer"
      >
        <BookOpen className="h-5 w-5" />
      </TrackedLink>
    </div>
  );
}

function Coin({ size }: { size: number }) {
  return (
    <Image
      src="/codeswhat-logo.png"
      alt="CodesWhat"
      width={size}
      height={size}
      className="rounded-full ring-1 ring-black/10 dark:ring-white/15 dark:invert"
    />
  );
}

function CodesWhatPill() {
  return (
    <TrackedLink
      href={CODESWHAT}
      target="_blank"
      rel="noopener noreferrer"
      className="group inline-flex items-center gap-2.5 rounded-full border border-neutral-200 bg-white/50 py-1.5 pr-3.5 pl-1.5 backdrop-blur-sm transition-colors hover:border-neutral-300 hover:bg-white/80 dark:border-neutral-800 dark:bg-neutral-900/50 dark:hover:border-neutral-700 dark:hover:bg-neutral-900/80"
      ctaId="github_org"
      placement="footer"
    >
      <Coin size={26} />
      <span className="text-xs text-neutral-500 dark:text-neutral-400">
        A <span className="font-semibold text-neutral-700 dark:text-neutral-200">CodesWhat</span>{" "}
        project
      </span>
      <ArrowUpRight className="h-3.5 w-3.5 text-neutral-400 transition-transform group-hover:-translate-y-0.5 group-hover:translate-x-0.5" />
    </TrackedLink>
  );
}

function LicenseLine({ className }: { className?: string }) {
  return (
    <p className={`text-xs text-neutral-500 dark:text-neutral-400 ${className ?? ""}`}>
      &copy; {YEAR} CodesWhat. Released under the{" "}
      <a
        href={SITE_CONFIG.licenseUrl}
        target="_blank"
        rel="noopener noreferrer"
        className="underline underline-offset-2 hover:text-neutral-900 dark:hover:text-neutral-100"
      >
        AGPL-3.0 License
      </a>
      .
    </p>
  );
}

export function Footer({ maxWidthClassName = "max-w-6xl" }: { maxWidthClassName?: string }) {
  return (
    <footer className="border-t border-border/60">
      <div className={`mx-auto px-4 py-12 ${maxWidthClassName}`}>
        <div className="flex flex-col gap-10 lg:flex-row lg:justify-between">
          {/* Brand */}
          <div className="flex max-w-xs flex-col gap-4">
            <div className="flex items-center gap-3">
              <Image
                src={SITE_CONFIG.logo}
                alt=""
                width={30}
                height={30}
                className={SITE_CONFIG.logoInvertOnDark ? "dark:invert" : undefined}
              />
              <span className="text-base font-semibold text-neutral-900 dark:text-neutral-100">
                {SITE_CONFIG.name}
              </span>
            </div>
            <p className="text-sm leading-relaxed text-neutral-500 dark:text-neutral-400">
              {BLURB}
            </p>
            <SocialIcons />
          </div>

          {/* Links */}
          <div className="flex flex-col items-start gap-10 sm:flex-row sm:gap-16">
            <LinkColumn heading="Product" links={productLinks} />
            <LinkColumn heading="Project" links={projectLinks} />
          </div>
        </div>

        {/* Legal */}
        <div className="mt-12 flex flex-col gap-4 border-t border-neutral-200 pt-6 dark:border-neutral-800 sm:flex-row sm:items-center sm:justify-between">
          <LicenseLine />
          <CodesWhatPill />
        </div>
      </div>
    </footer>
  );
}
