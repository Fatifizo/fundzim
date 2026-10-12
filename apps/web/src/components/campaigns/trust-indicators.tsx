import Link from "next/link";

import { activeTrustIndicators, TRUST_DISCLAIMER } from "@/lib/campaigns/trust";
import type { PublicTrust } from "@/lib/campaigns/types";

/**
 * Trust indicators that are true now (ADR-038 §5), each with what it means, followed by what they do not
 * mean. Nothing is shown as a badge unless the API says it is true.
 */
export function TrustIndicators({ trust }: { trust: PublicTrust }) {
  const items = activeTrustIndicators(trust);
  return (
    <section aria-labelledby="trust-title" className="space-y-3 rounded-card border border-line bg-surface p-5">
      <h2 id="trust-title" className="font-display text-xl font-semibold text-ink-900">
        Checks on this campaign
      </h2>
      {items.length > 0 ? (
        <ul className="space-y-3">
          {items.map((i) => (
            <li key={i.key} className="flex gap-3">
              <svg aria-hidden="true" viewBox="0 0 20 20" className="mt-1 size-5 shrink-0 text-brand-700" fill="currentColor">
                <path d="M10 1.5 3 4.5v5c0 4.2 3 7.6 7 9 4-1.4 7-4.8 7-9v-5l-7-3Zm-1.2 12.1-3.3-3.3 1.4-1.4 1.9 1.9 4.5-4.5 1.4 1.4-5.9 5.9Z" />
              </svg>
              <div>
                <p className="font-semibold text-ink-900">{i.label}</p>
                <p className="text-sm text-ink-700">{i.explanation}</p>
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-ink-700">No verification checks are shown for this campaign right now.</p>
      )}
      <p className="text-sm text-ink-700">
        {TRUST_DISCLAIMER}{" "}
        <Link href="/safety" className="font-semibold text-brand-700 underline">
          How FundZim checks campaigns
        </Link>
      </p>
    </section>
  );
}
