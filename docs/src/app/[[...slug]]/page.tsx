// SPDX-License-Identifier: MIT

import { Callout } from "fumadocs-ui/components/callout";
import defaultMdxComponents from "fumadocs-ui/mdx";
import { DocsBody, DocsDescription, DocsPage, DocsTitle, EditOnGitHub } from "fumadocs-ui/page";
import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { archiveVersionOf, CURRENT_DOCS_VERSION } from "@/lib/docs-versions";
import { BASE_URL, GITHUB_URL, SITE_CONFIG } from "@/lib/site-config";
import { getDocsPage, source } from "@/lib/source";

// The same page in the current docs, as the slug list the loader knows it by.
// An archived page's slugs lead with the version segment, so dropping it gives
// the slug the page has under current/ (or none, for the version's index).
function currentSlugs(slugs: string[]): string[] {
  return slugs.slice(1);
}

function hrefFor(slugs: string[]): string {
  return slugs.length === 0 ? "/" : `/${slugs.join("/")}`;
}

function canonicalFor(slugs: string[]): string {
  return new URL(`/docs${hrefFor(slugs).replace(/^\/$/u, "")}`, BASE_URL).toString();
}

export default async function Page(props: { params: Promise<{ slug?: string[] }> }) {
  const params = await props.params;
  const page = getDocsPage(params.slug);
  if (!page) notFound();

  const MDX = page.data.body;
  const version = archiveVersionOf(page.path);
  const counterpart = version ? currentSlugs(page.slugs) : [];
  const hasCounterpart = version !== undefined && getDocsPage(counterpart) !== undefined;

  return (
    <DocsPage toc={page.data.toc} full={page.data.full} breadcrumb={{ enabled: true }}>
      {version && (
        <Callout type="warn" title={`Archived documentation for ${version}`}>
          This page is a frozen reference for Portwing {version} and is not updated. Check the{" "}
          <Link href="https://github.com/CodesWhat/portwing/blob/main/SECURITY.md">
            security policy
          </Link>{" "}
          for release support.{" "}
          <Link href={hasCounterpart ? hrefFor(counterpart) : "/"}>
            {hasCounterpart ? "Read this page in the current docs" : "Go to the current docs"}
          </Link>
          .
        </Callout>
      )}
      <div className="ph">
        <DocsTitle>{page.data.title}</DocsTitle>
        <span className="chip nocase">{version ?? CURRENT_DOCS_VERSION}</span>
      </div>
      <DocsDescription>{page.data.description}</DocsDescription>
      <DocsBody>
        <MDX components={defaultMdxComponents} />
      </DocsBody>
      {!version && (
        <EditOnGitHub
          href={`${GITHUB_URL}/edit/main/docs/content/docs/${page.path}`}
          target="_blank"
          rel="noopener noreferrer"
        />
      )}
    </DocsPage>
  );
}

export function generateStaticParams() {
  return source.generateParams();
}

export async function generateMetadata(props: {
  params: Promise<{ slug?: string[] }>;
}): Promise<Metadata> {
  const params = await props.params;
  const page = getDocsPage(params.slug);
  if (!page) notFound();

  const version = archiveVersionOf(page.path);
  if (version) {
    // Archives are frozen reference copies: out of search results, but crawlers
    // may still follow their links on to the current docs. The canonical points at
    // the same page in the current docs, or at the docs root when it no longer exists.
    const counterpart = currentSlugs(page.slugs);
    const target = getDocsPage(counterpart) ? counterpart : [];
    return {
      title: `${page.data.title} (${version})`,
      description: page.data.description,
      robots: { index: false, follow: true },
      alternates: { canonical: canonicalFor(target) },
    };
  }

  // The landing page's own title is "Introduction", which says nothing in a tab or a result.
  const title =
    page.slugs.length === 0
      ? { absolute: `${SITE_CONFIG.name} Docs: security-first remote Docker agent` }
      : page.data.title;

  return {
    title,
    description: page.data.description,
    alternates: { canonical: canonicalFor(page.slugs) },
  };
}
