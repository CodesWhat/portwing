// SPDX-License-Identifier: MIT

import type { Metadata } from "next";
import Link from "next/link";
import { Footer } from "@/components/footer";
import { Ic } from "@/components/icons";
import { SiteHeader } from "@/components/site-header";
import "./not-found.css";

// Next adds the noindex robots tag to not-found pages itself. The share-card fields override
// the root layout's so a shared dead link doesn't present itself as the homepage.
export const metadata: Metadata = {
  title: "Page not found",
  openGraph: { title: "Page not found | Portwing", siteName: "Portwing", type: "website" },
  twitter: { card: "summary", title: "Page not found | Portwing" },
};

export default function NotFound() {
  return (
    <>
      <SiteHeader />
      <main id="main-content">
        <div className="nf-wrap" id="nf">
          <section className="nf" aria-labelledby="nf-h">
            <img
              className="nf-m"
              src="/brand/pigeon-256.webp"
              alt="Portwing mascot, a slate and violet toy pigeon"
              width={128}
              height={128}
            />
            <p className="nf-c micro">Error 404 · Portwing</p>
            <h1 id="nf-h">404</h1>
            <p className="nf-p">
              The page isn't here. The path may be mistyped, or the page moved. Fly back home, or
              look through the docs.
            </p>
            <div className="nf-l">
              <Link className="btn primary" href="/">
                <Ic name="arrow-right" />
                Back to Portwing
              </Link>
              <a className="btn" href="/docs">
                <Ic name="book-open" />
                Docs
              </a>
              <Link className="btn" href="/compare">
                <Ic name="arrows-clockwise" />
                Compare
              </Link>
            </div>
          </section>
        </div>
      </main>
      <Footer />
    </>
  );
}
