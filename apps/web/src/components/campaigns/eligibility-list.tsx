import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import { reasonGuidance, type FixContext } from "@/lib/campaigns/eligibility";
import type { EligibilityReason } from "@/lib/campaigns/types";

/**
 * Eligibility checklist: each reason the API gave, in plain language, with a link to where it can be fixed.
 * The API's own message is not shown (codes only), so wording stays consistent and nothing internal leaks.
 */
/** `fixes={false}` (staff views) hides the owner-facing fix links. */
export function EligibilityList({ reasons, context, allowed, allowedText, id, fixes = true }: { reasons: EligibilityReason[]; context: FixContext; allowed: boolean; allowedText: string; id?: string; fixes?: boolean }) {
  if (allowed && reasons.length === 0) {
    return (
      <p id={id} className="flex flex-wrap items-center gap-2 text-ink-700">
        <Badge tone="brand">Ready</Badge>
        {allowedText}
      </p>
    );
  }
  return (
    <ul id={id} className="space-y-3" aria-label="Things to do first">
      {reasons.map((reason, i) => {
        const g = reasonGuidance(reason, context);
        return (
          <li key={`${reason.code}-${reason.field ?? ""}-${i}`} className="rounded-xl border border-line bg-surface p-4">
            <p className="flex flex-wrap items-center gap-2 font-semibold text-ink-900">
              <Badge tone="gold">To do</Badge>
              {g.title}
            </p>
            <p className="mt-1 text-ink-700">{g.description}</p>
            {fixes && g.fix ? (
              <Link href={g.fix.href} className="mt-2 inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
                {g.fix.label}
              </Link>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
