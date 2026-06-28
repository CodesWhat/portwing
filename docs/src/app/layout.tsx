import { Analytics } from "@vercel/analytics/next";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { RootProvider } from "fumadocs-ui/provider/next";
import type { Metadata, Viewport } from "next";
import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google";
import { Footer } from "@/components/footer";
import { SiteBackground } from "@/components/site-background";
import { SiteHeader } from "@/components/site-header";
import { BASE_URL, SITE_CONFIG } from "@/lib/site-config";
import { source } from "@/lib/source";
import "./globals.css";

const ibmPlexSans = IBM_Plex_Sans({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
});

const ibmPlexMono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-mono",
});

export const metadata: Metadata = {
  title: {
    default: `${SITE_CONFIG.name} Docs`,
    template: `%s | ${SITE_CONFIG.name} Docs`,
  },
  description: SITE_CONFIG.description,
  metadataBase: new URL(BASE_URL),
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#ffffff" },
    { media: "(prefers-color-scheme: dark)", color: "#0c0a10" },
  ],
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className={`${ibmPlexSans.className} ${ibmPlexMono.variable}`}>
        {/*
         * RootProvider (fumadocs-ui/provider/next) already initialises next-themes
         * with attribute="class". ThemeToggle uses useTheme() from the same
         * next-themes instance — no separate ThemeProvider needed.
         */}
        <RootProvider>
          <div data-bg={SITE_CONFIG.aurora} className="relative min-h-screen">
            <SiteBackground />
            <div className="relative z-10 flex min-h-screen flex-col">
              <SiteHeader />
              <main id="main-content" className="flex-1">
                {/*
                 * DocsLayout owns the sidebar. The built-in fumadocs nav header,
                 * theme switch, and search toggle are disabled so the SiteHeader
                 * above is the sole chrome — no double-header.
                 */}
                <DocsLayout
                  tree={source.pageTree}
                  nav={{ enabled: false }}
                  themeSwitch={{ enabled: false }}
                  searchToggle={{ enabled: false }}
                >
                  {children}
                </DocsLayout>
              </main>
              <Footer />
            </div>
          </div>
        </RootProvider>
        <Analytics />
      </body>
    </html>
  );
}
