// SPDX-License-Identifier: MIT

import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { RootProvider } from "fumadocs-ui/provider/next";
import type { Metadata, Viewport } from "next";
import { AnalyticsRuntime } from "@/components/analytics-runtime";
import { Footer } from "@/components/footer";
import { IconSprite } from "@/components/icons";
import { SiteHeader } from "@/components/site-header";
import { ThemeToggle } from "@/components/theme-toggle";
import { versionTabsOption } from "@/lib/docs-versions";
import { BASE_URL, SITE_CONFIG } from "@/lib/site-config";
import { source } from "@/lib/source";
import "./skin-portwing.css";
import "./globals.css";

const SHARE_IMAGE = {
  url: new URL("/og-image.png", BASE_URL).toString(),
  width: 1200,
  height: 630,
  alt: `${SITE_CONFIG.name} Docs`,
};

export const metadata: Metadata = {
  title: {
    default: `${SITE_CONFIG.name} Docs`,
    template: `%s | ${SITE_CONFIG.name} Docs`,
  },
  description: SITE_CONFIG.description,
  metadataBase: new URL(BASE_URL),
  // summary_large_image needs an image, so the card and the image are declared together.
  openGraph: {
    siteName: `${SITE_CONFIG.name} Docs`,
    type: "website",
    images: [SHARE_IMAGE],
  },
  twitter: {
    card: "summary_large_image",
    images: [SHARE_IMAGE.url],
  },
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f1f4f9" },
    { media: "(prefers-color-scheme: dark)", color: "#12151c" },
  ],
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      data-product="portwing"
      data-family="one-dark"
      data-mode="dark"
      suppressHydrationWarning
    >
      <body>
        <a className="skip" href="#main-content">
          Skip to content
        </a>
        <IconSprite />
        {/*
         * RootProvider (fumadocs-ui/provider/next) initialises next-themes. The mode is mirrored
         * onto <html data-mode> for the shared skin and onto the `dark` class for Fumadocs. With
         * no saved choice it follows the system scheme, as website/ does. Search is not part of
         * these docs, so its dialog and hotkey are off.
         */}
        <RootProvider
          theme={{
            attribute: ["class", "data-mode"],
            defaultTheme: "system",
            enableSystem: true,
          }}
          search={{ enabled: false }}
        >
          <SiteHeader />
          <main id="main-content">
            {/*
             * DocsLayout owns the sidebar. The built-in theme switch and search toggle are
             * disabled; the mode toggle lives in the sidebar footer instead.
             */}
            <DocsLayout
              tree={source.pageTree}
              tabs={versionTabsOption(source.pageTree)}
              nav={{
                // Only the sub-header is shown, and only below md: it carries the button that opens
                // the docs sidebar as a drawer. The brand it would repeat is hidden in CSS.
                title: (
                  <>
                    {/* biome-ignore lint/performance/noImgElement: basePath="/docs" makes next/image prepend /docs to root-relative src; this asset lives at the marketing-site origin root, so a plain <img> is required. */}
                    <img src="/brand/pigeon-64.webp" alt="" width={36} height={36} />
                    <span>{SITE_CONFIG.name} docs</span>
                  </>
                ),
                children: <span className="subnav-label">Docs navigation</span>,
              }}
              sidebar={{
                footer: (
                  <div className="sidefoot">
                    <ThemeToggle />
                  </div>
                ),
              }}
              themeSwitch={{ enabled: false }}
              searchToggle={{ enabled: false }}
            >
              {children}
            </DocsLayout>
          </main>
          <Footer />
        </RootProvider>
        <AnalyticsRuntime />
      </body>
    </html>
  );
}
