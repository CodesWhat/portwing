// SPDX-License-Identifier: MIT

import { Section } from "@/components/section";
import { TrackedLink } from "@/components/tracked-link";
import { GITHUB_ORG_URL } from "@/lib/site-config";

export function Ecosystem() {
  return (
    <Section
      id="ecosystem"
      headingId="eco-h"
      eyebrow="Ecosystem"
      title="Part of the CodesWhat stack"
      subtitle="Portwing is one piece of a small, focused toolkit — each tool does one job, and they compose."
      variant="b"
    >
      <div className="eco">
        <a className="ec" href="https://getdrydock.com">
          <img
            className="wh"
            src="/brand/whale-264.webp"
            alt=""
            width={132}
            height={88}
            loading="lazy"
            decoding="async"
          />
          <h3>Drydock</h3>
          <p>Container update manager — orchestrates across your hosts</p>
        </a>
        <div>
          <img
            src="/brand/pigeon-192.webp"
            alt=""
            width={96}
            height={96}
            loading="lazy"
            decoding="async"
          />
          <h3>Portwing</h3>
          <p>Security-first remote Docker agent</p>
          <span className="chip t-primary">📍 You're here</span>
        </div>
        <a className="ec" href="https://getsockguard.com">
          <img
            src="/brand/dog-192.webp"
            alt=""
            width={96}
            height={96}
            loading="lazy"
            decoding="async"
          />
          <h3>Sockguard</h3>
          <p>Default-deny Docker socket firewall</p>
        </a>
      </div>
      <div className="project">
        <TrackedLink
          className="pill"
          href={GITHUB_ORG_URL}
          target="_blank"
          rel="noopener noreferrer"
          ctaId="github_org"
          placement="footer"
        >
          <img
            src="/brand/codeswhat-52.webp"
            alt=""
            width={26}
            height={26}
            loading="lazy"
            decoding="async"
          />
          A CodesWhat project
        </TrackedLink>
      </div>
    </Section>
  );
}
