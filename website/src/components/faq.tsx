// SPDX-License-Identifier: MIT

import { faqItems } from "@/app/data/faq";
import { Section } from "@/components/section";

export function FAQ() {
  return (
    <Section
      id="faq"
      headingId="faq-h"
      eyebrow="FAQ"
      title="Frequently asked questions"
      subtitle="Common questions about how Portwing works and how to deploy it securely."
      split
    >
      <div className="faq">
        {faqItems.map((item) => (
          <details className="fq" name="faq" key={item.question}>
            <summary>
              <span className="q">{item.question}</span>
              <span className="pm" aria-hidden="true" />
            </summary>
            <div className="a">
              <p>{item.answer}</p>
            </div>
          </details>
        ))}
      </div>
    </Section>
  );
}
