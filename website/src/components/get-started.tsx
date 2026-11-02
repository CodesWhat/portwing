// SPDX-License-Identifier: MIT

"use client";

import { type CtaId, captureCta } from "@codeswhat/public-analytics";
import { usePathname } from "next/navigation";
import { useRef, useState } from "react";
import { HighlightedYaml } from "@/components/code-highlight";
import { Ic } from "@/components/icons";
import { Section } from "@/components/section";
import { TrackedLink } from "@/components/tracked-link";
import { SITE_CONFIG } from "@/lib/site-config";

type Tab = "quick" | "secure" | "native";

const TABS: { id: Tab; label: string; icon: string }[] = [
  { id: "quick", label: "Quick", icon: "lightning" },
  { id: "secure", label: "Secure", icon: "shield-check" },
  { id: "native", label: "Native", icon: "package" },
];

const INSTALL_CTA: Record<Tab, CtaId> = {
  quick: "install_quick",
  secure: "install_secure",
  native: "install_native",
};

// Hardened standard-mode compose from docs/getting-started.mdx.
// Runs read_only, drops all caps, and delivers the token as a
// Docker secret instead of an inline environment variable.
const dockerCompose = `# Portwing — standard mode, hardened defaults.
# The image runs as the non-root portwing user (UID 65532). Generate a token
# it can read, and export the numeric group ID of your Docker socket:
#   openssl rand -hex 32 > portwing_token.txt
#   sudo chown 65532:65532 portwing_token.txt && sudo chmod 0400 portwing_token.txt
#   export DOCKER_SOCK_GID=$(stat -c '%g' /var/run/docker.sock)
#
# This plaintext example publishes only on host loopback. For remote access,
# configure Portwing TLS before changing this bind, or keep the plaintext
# listener private behind a TLS-terminating reverse proxy.

services:
  portwing:
    image: ${SITE_CONFIG.dockerImage}:${SITE_CONFIG.version}
    restart: unless-stopped
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    tmpfs:
      - /tmp
    user: "65532:65532"  # image default; explicit so it survives image overrides
    group_add:
      - "\${DOCKER_SOCK_GID:?set to the GID of /var/run/docker.sock (see header)}"
    ports:
      - "127.0.0.1:3000:3000"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - portwing-stacks:/data/stacks
    environment:
      - TOKEN_FILE=/run/secrets/portwing_token
    secrets:
      - portwing_token

secrets:
  portwing_token:
    file: ./portwing_token.txt

volumes:
  portwing-stacks:`;

export function GetStarted() {
  const [tab, setTab] = useState<Tab>("quick");
  const pathname = usePathname();
  const buttons = useRef<Record<Tab, HTMLButtonElement | null>>({
    quick: null,
    secure: null,
    native: null,
  });

  const selectTab = (next: Tab, focus = false) => {
    setTab(next);
    captureCta(pathname, INSTALL_CTA[next], "get_started");
    if (focus) buttons.current[next]?.focus();
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    const index = TABS.findIndex((t) => t.id === tab);
    const at = (i: number) => TABS[(i + TABS.length) % TABS.length].id;
    if (event.key === "ArrowRight") selectTab(at(index + 1), true);
    else if (event.key === "ArrowLeft") selectTab(at(index - 1), true);
    else if (event.key === "Home") selectTab(TABS[0].id, true);
    else if (event.key === "End") selectTab(TABS[TABS.length - 1].id, true);
    else return;
    event.preventDefault();
  };

  return (
    <Section
      id="get-started"
      headingId="gs-h"
      eyebrow="Get running"
      title="Get started in minutes"
      subtitle="Add Portwing to your compose file and point Drydock at it — one agent per host."
      variant="b"
      split
    >
      <div className="tabs" role="tablist" aria-label="Install method" onKeyDown={onKeyDown}>
        {TABS.map(({ id, label, icon }) => (
          <button
            key={id}
            ref={(node) => {
              buttons.current[id] = node;
            }}
            role="tab"
            type="button"
            id={`tab-${id}`}
            aria-selected={tab === id}
            aria-controls={`panel-${id}`}
            tabIndex={tab === id ? 0 : -1}
            onClick={() => selectTab(id)}
          >
            <Ic name={icon} />
            {label}
          </button>
        ))}
      </div>

      <div id="panel-quick" role="tabpanel" aria-labelledby="tab-quick" hidden={tab !== "quick"}>
        <div className="code">
          <div className="lab">
            <Ic name="terminal-window" />
            Quick start · docker run
          </div>
          <pre>
            <code>
              <span className="y-c">$</span> <span className="y-v">docker run</span>
              {` -d \\
  --name portwing \\
  -v /var/run/docker.sock:/var/run/docker.sock \\
  -p 3000:3000 \\
  -e TOKEN=$(openssl rand -hex 24) \\
  `}
              <span className="y-v">
                {SITE_CONFIG.dockerImage}:{SITE_CONFIG.version}
              </span>
            </code>
          </pre>
        </div>
        <p className="note t-warning">
          <span className="chip">
            <Ic name="warning" />
            Heads up
          </span>
          TOKEN is visible in docker inspect — fine for a local try, not for production.
        </p>
      </div>

      <div id="panel-secure" role="tabpanel" aria-labelledby="tab-secure" hidden={tab !== "secure"}>
        <div className="code">
          <div className="lab">
            <Ic name="terminal-window" />
            docker-compose.standard.yml
          </div>
          <pre>
            <code>
              <HighlightedYaml code={dockerCompose} />
            </code>
          </pre>
        </div>
        <p className="note t-success">
          <span className="chip">
            <Ic name="shield-check" />
            Secret
          </span>
          Token delivered as a Docker secret — never in env vars or inspect output.{" "}
          <TrackedLink
            href="/docs/getting-started"
            ctaId="docs_getting_started"
            placement="get_started"
          >
            Full getting-started docs →
          </TrackedLink>
        </p>
      </div>

      <div id="panel-native" role="tabpanel" aria-labelledby="tab-native" hidden={tab !== "native"}>
        <div className="code">
          <div className="lab">
            <Ic name="package" />
            Native packages
          </div>
          <pre>
            <code>
              <span className="y-c"># macOS (latest stable release)</span>
              {`
brew install --cask codeswhat/tap/portwing

`}
              <span className="y-c"># Debian / Ubuntu</span>
              {`
sudo apt install ./portwing_1.0.2_linux_amd64.deb

`}
              <span className="y-c"># Fedora / RHEL</span>
              {`
sudo rpm --install ./portwing_1.0.2_linux_amd64.rpm`}
            </code>
          </pre>
        </div>
        <p className="note t-success">
          <span className="chip">
            <Ic name="shield-check" />
            Verified
          </span>
          Stable packages are checksummed, keyless-signed, and install-tested.{" "}
          <TrackedLink href="/docs/installation" ctaId="docs_installation" placement="get_started">
            Install and verify →
          </TrackedLink>
        </p>
      </div>
    </Section>
  );
}
