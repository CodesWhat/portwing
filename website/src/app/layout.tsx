import { Analytics } from "@vercel/analytics/next";
import type { Metadata, Viewport } from "next";
import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google";
import { ThemeProvider } from "@/components/theme-provider";
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

const description =
  "A security-first remote Docker agent. Control your containers from anywhere, safely — over a default-deny socket, with signed policy, Ed25519 auth, and a tamper-evident audit log. The remote agent for Drydock.";

export const metadata: Metadata = {
  title: "Lookout — Security-first remote Docker agent",
  description,
  metadataBase: new URL("https://getlookout.dev"),
  icons: {
    icon: "/lookout.png",
    apple: "/lookout.png",
  },
  openGraph: {
    title: "Lookout — Security-first remote Docker agent",
    description,
    url: "https://getlookout.dev",
    siteName: "Lookout",
    locale: "en_US",
    type: "website",
  },
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#ffffff" },
    { media: "(prefers-color-scheme: dark)", color: "#0c0a10" },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className={`${ibmPlexSans.className} ${ibmPlexMono.variable}`}>
        <ThemeProvider>{children}</ThemeProvider>
        <Analytics />
      </body>
    </html>
  );
}
