import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { RankingActions, RankingProposalForm } from "@/components/admin/discovery/discovery-admin";
import { Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readRanking, WEIGHT_FIELDS, type Weights } from "@/lib/discovery/admin";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Trending ranking", robots: { index: false, follow: false, nocache: true } };

const weightFacts = (w: Weights): Array<[string, string]> => WEIGHT_FIELDS.map((f) => [f.label, String(w[f.key])]);

/**
 * Trending ranking policies (`discovery.view`; proposing and deciding need their own permissions with step-up). Weights
 * are a closed, bounded set; a submitted version is hashed and immutable; the checker is never the proposer.
 */
export default async function RankingPage() {
  await requireStaff();
  const r = readRanking((await serverGetOr404<unknown>("/api/v1/admin/discovery/ranking", "/admin/discovery/ranking")).data);
  return (
    <AccountPage title="Trending ranking" intro="score = (view weight × ln(1 + unique visitors) + share weight × ln(1 + unique sharers)) × (0.25 + 0.75 × freshness), where freshness halves every half-life. Money is never an input.">
      <Section id="in-force" title="In force">
        <p className="text-ink-700">{r.inForceSource === "APPROVED_POLICY" ? `Approved policy version ${r.inForceVersion}.` : "Engineering default: no policy has been approved yet."}</p>
        <Facts items={weightFacts(r.inForce)} />
      </Section>
      <Section id="propose" title="Propose a new version">
        <div className="rounded-card border border-line bg-surface p-5">
          <RankingProposalForm current={r.inForce} />
        </div>
      </Section>
      <Section id="versions" title="Versions">
        {r.policies.length === 0 ? (
          <p className="text-ink-700">No version has been proposed.</p>
        ) : (
          <TableShell caption="Ranking policy versions" head={["Version", "Status", "Weights", "Reason", "Proposed", "Actions"]} minWidthClass="min-w-[56rem]">
            {r.policies.map((p) => (
              <tr key={p.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3">{p.version}</td>
                <td className="p-3">
                  <Badge tone={p.status === "ACTIVE" ? "brand" : p.status === "SUBMITTED" ? "gold" : "neutral"}>{humanise(p.status)}</Badge>
                </td>
                <td className="p-3 text-sm">{WEIGHT_FIELDS.map((f) => `${f.key}=${p.weights[f.key]}`).join(", ")}</td>
                <td className="p-3 text-sm break-words">
                  {p.reason}
                  {p.decisionNote ? <span className="block text-ink-600">Decision: {p.decisionNote}</span> : null}
                </td>
                <td className="p-3 text-sm">
                  <When at={p.proposedAt} />
                </td>
                {/* always mounted: the confirmation survives the refresh that moves the version out of SUBMITTED */}
                <td className="p-3">
                  <RankingActions id={p.id} status={p.status} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
