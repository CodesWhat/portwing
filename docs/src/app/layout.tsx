import { Analytics } from "@vercel/analytics/next";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { RootProvider } from "fumadocs-ui/provider/next";
import type { Metadata, Viewport } from "next";
import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google";
import Image from "next/image";
import { source } from "@/lib/source";
import logo from "../../public/portwing.png";
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
  title: "Portwing Docs",
  description: "Documentation for Portwing, the security-first remote Docker agent.",
  metadataBase: new URL(process.env.NEXT_PUBLIC_SITE_URL || "https://getportwing.dev"),
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
        <RootProvider>
          <DocsLayout
            tree={source.pageTree}
            nav={{
              title: (
                <span className="flex items-center gap-2">
                  <Image src={logo} alt="Portwing" width={28} height={28} priority />
                  <span className="font-semibold tracking-tight">Portwing</span>
                </span>
              ),
              url: "/",
            }}
            links={[
              {
                text: "GitHub",
                url: "https://github.com/CodesWhat/portwing",
                external: true,
              },
            ]}
          >
            {children}
          </DocsLayout>
        </RootProvider>
        <Analytics />
      </body>
    </html>
  );
}
