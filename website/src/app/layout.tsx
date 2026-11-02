// SPDX-License-Identifier: MIT

import type { Metadata, Viewport } from "next";
import { AnalyticsRuntime } from "@/components/analytics-runtime";
import { IconSprite } from "@/components/icons";
import { BASE_URL, SITE_CONFIG } from "@/lib/site-config";
import "./skin-portwing.css";
import "./suite.css";
import "./site.css";

// Bump this whenever the favicon/app icons change so browsers re-fetch them.
const ICON_VERSION = "2";

export const metadata: Metadata = {
  title: {
    default: `${SITE_CONFIG.name} — ${SITE_CONFIG.tagline}`,
    template: `%s | ${SITE_CONFIG.name}`,
  },
  description: SITE_CONFIG.description,
  metadataBase: new URL(BASE_URL),
  openGraph: {
    title: `${SITE_CONFIG.name} — ${SITE_CONFIG.tagline}`,
    description: SITE_CONFIG.description,
    url: BASE_URL,
    siteName: SITE_CONFIG.name,
    locale: SITE_CONFIG.locale,
    type: "website",
    images: [
      {
        url: SITE_CONFIG.ogImage,
        width: 1200,
        height: 630,
        alt: `${SITE_CONFIG.name} — ${SITE_CONFIG.tagline}`,
      },
    ],
  },
  twitter: {
    card: "summary_large_image",
    title: `${SITE_CONFIG.name} — ${SITE_CONFIG.tagline}`,
    description: SITE_CONFIG.description,
    creator: SITE_CONFIG.twitterCreator,
    images: [SITE_CONFIG.ogImage],
  },
  manifest: "/site.webmanifest",
  icons: {
    icon: [
      { url: `/favicon.ico?v=${ICON_VERSION}`, sizes: "any" },
      { url: `/favicon-96x96.png?v=${ICON_VERSION}`, sizes: "96x96", type: "image/png" },
    ],
    apple: [{ url: `/apple-touch-icon.png?v=${ICON_VERSION}`, sizes: "180x180" }],
  },
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f1f4f9" },
    { media: "(prefers-color-scheme: dark)", color: "#12151c" },
  ],
};

// Runs before first paint so a saved or system light preference never flashes dark. The key
// matches next-themes, which the docs app uses, so the choice carries across /docs.
const THEME_INIT =
  "(function(){var m=null;try{m=localStorage.getItem('theme');}catch(e){}if(m!=='light'&&m!=='dark'){m=window.matchMedia&&window.matchMedia('(prefers-color-scheme: light)').matches?'light':'dark';}document.documentElement.setAttribute('data-mode',m);})();";

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
      <head>
        <meta name="apple-mobile-web-app-title" content={SITE_CONFIG.name} />
        {/* biome-ignore lint/security/noDangerouslySetInnerHtml: intentional inline theme bootstrap, hashed into the route CSP */}
        <script dangerouslySetInnerHTML={{ __html: THEME_INIT }} />
      </head>
      <body>
        <a className="skip" href="#main-content">
          Skip to content
        </a>
        <IconSprite />
        {children}
        <AnalyticsRuntime />
      </body>
    </html>
  );
}
