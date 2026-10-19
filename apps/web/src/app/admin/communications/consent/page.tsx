import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { inputClasses } from "@/components/forms/text-field";
import { Section, TableShell, When } from "@/components/admin/payments/common";
import { CommsBanner } from "@/components/admin/communications/comms";
import { buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { list, obj } from "@/lib/operations/normalise";

export const metadata: Metadata = { title: "Consent records", robots: { index: false, follow: false, nocache: true } };

interface Rec {
  id: string;
  channel: string;
  purpose: string;
  status: string;
  source: string;
  policy_version: string;
  evidence_ref: string;
  history: Array<{ from_status: string | null; to_status: string; source: string; policy_version: string; occurred_at: string }>;
}

const UUID = /^[0-9a-f-]{36}$/i;

/** Consent records (`communications.consent.view`): counts, and one person's records with history. Staff never record consent. */
export default async function ConsentPage({ searchParams }: PageProps<"/admin/communications/consent">) {
  await requireStaff();
  const p = await searchParams;
  const userID = typeof p.user_id === "string" && UUID.test(p.user_id) ? p.user_id : undefined;
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/communications/consent${userID ? `?user_id=${userID}` : ""}`, "/admin/communications/consent")).data;
  const summary = list<{ channel: string; purpose: string; status: string; count: number }>(data, "summary");
  const records = list<Rec>(data, "records");
  return (
    <AccountPage title="Consent records" intro={`Consent wording version ${String(obj(data).policy_version ?? "")} (engineering placeholder pending legal review).`}>
      <CommsBanner />
      <Section id="summary" title="Counts">
        {summary.length === 0 ? <p className="text-ink-700">No consent recorded.</p> : (
          <TableShell caption="Consent counts" head={["Channel", "Purpose", "Status", "Records"]}>
            {summary.map((s) => (
              <tr key={`${s.channel}-${s.purpose}-${s.status}`} className="border-b border-line last:border-b-0">
                <td className="p-3">{humanise(s.channel)}</td><td className="p-3">{humanise(s.purpose)}</td><td className="p-3">{humanise(s.status)}</td><td className="p-3">{s.count}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <form method="get" action="/admin/communications/consent" aria-label="Look up a person's consent" className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4 sm:flex-row sm:items-end">
        <div className="flex-1 space-y-1">
          <label htmlFor="consent-user" className="block font-semibold text-ink-900">Account id</label>
          <input id="consent-user" name="user_id" defaultValue={userID} className={inputClasses} autoComplete="off" />
        </div>
        <button type="submit" className={buttonClasses("primary")}>Look up</button>
      </form>
      {userID ? (
        <Section id="records" title="Records">
          {records.length === 0 ? <p className="text-ink-700">No consent recorded for this account (NOT_REQUESTED everywhere).</p> : records.map((r) => (
            <div key={r.id} className="space-y-2 rounded-card border border-line bg-surface p-4">
              <p className="font-semibold">{humanise(r.channel)} · {humanise(r.purpose)}: {humanise(r.status)}</p>
              <p className="text-sm text-ink-600">Source {humanise(r.source)} · wording {r.policy_version} · evidence {r.evidence_ref}</p>
              <ul className="list-disc pl-6 text-sm">
                {r.history.map((h, i) => <li key={i}>{h.from_status ? humanise(h.from_status) : "—"} → {humanise(h.to_status)} (<When at={h.occurred_at} />)</li>)}
              </ul>
            </div>
          ))}
        </Section>
      ) : null}
    </AccountPage>
  );
}
