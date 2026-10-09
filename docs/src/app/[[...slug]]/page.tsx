import { Callout } from "fumadocs-ui/components/callout";
import defaultMdxComponents from "fumadocs-ui/mdx";
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/page";
import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { archiveVersionOf } from "@/lib/docs-versions";
import { BASE_URL } from "@/lib/site-config";
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

export default async function Page(props: { params: Promise<{ slug?: string[] }> }) {
  const params = await props.params;
  const page = getDocsPage(params.slug);
  if (!page) notFound();

  const MDX = page.data.body;
  const version = archiveVersionOf(page.path);
  const counterpart = version ? currentSlugs(page.slugs) : [];
  const hasCounterpart = version !== undefined && getDocsPage(counterpart) !== undefined;

  return (
    <DocsPage toc={page.data.toc} full={page.data.full}>
      {version && (
        <Callout type="warn" title={`Archived documentation for ${version}`}>
          This page documents Portwing {version}, which is no longer supported. It is kept for
          reference and is not updated.{" "}
          <Link href={hasCounterpart ? hrefFor(counterpart) : "/"}>
            {hasCounterpart ? "Read this page in the current docs" : "Go to the current docs"}
          </Link>
          .
        </Callout>
      )}
      <DocsTitle>{page.data.title}</DocsTitle>
      <DocsDescription>{page.data.description}</DocsDescription>
      <DocsBody>
        <MDX components={defaultMdxComponents} />
      </DocsBody>
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
    // may still follow their links on to the current docs.
    return {
      title: `${page.data.title} (${version})`,
      description: page.data.description,
      robots: { index: false, follow: true },
    };
  }

  return {
    title: page.data.title,
    description: page.data.description,
    alternates: {
      canonical: new URL(`/docs${hrefFor(page.slugs).replace(/^\/$/u, "")}`, BASE_URL).toString(),
    },
  };
}
